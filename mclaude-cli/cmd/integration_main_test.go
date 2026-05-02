//go:build integration

// Package cmd_test — integration_main_test.go
//
// TestMain for CLI integration tests. Creates an ephemeral test user via the
// admin API, acquires NATS credentials via the device-code flow, and stores
// them in cmd/cli-integration/.test-creds.json for individual test functions.
//
// Preconditions:
//   - ADMIN_URL must be set; if unset, writes {skipped:true} and exits.
//   - MCLAUDE_TEST_HOST_SLUG must refer to a pre-registered host (default: dev-local).
//
// See ADR-0065 and docs/mclaude-cli/spec-cli.md §Smoke Tests.
package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mclaude-cli/cmd"
	clicontext "mclaude-cli/context"
)

// integrationCreds holds the credentials read from .test-creds.json after TestMain
// completes the device-code flow. Test functions reference these globals.
var (
	intJWT      string
	intNKeySeed string
	intUserSlug string
	intUserID   string
	intHSlug    string
	intAdminURL string
	intServerURL string
	// intProjectSlug is set by TestIntegration_Import_HappyPath and deleted in teardown.
	intProjectSlug string
)

// testCredsPath returns the path to .test-creds.json relative to the test binary.
func testCredsPath() string {
	return filepath.Join("cli-integration", ".test-creds.json")
}

// testCreds is the JSON schema for cli-integration/.test-creds.json.
type testCreds struct {
	JWT      string `json:"jwt"`
	NKeySeed string `json:"nkeySeed"`
	UserSlug string `json:"userSlug"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// adminUserRequest is the POST /admin/users request body.
type adminUserRequest struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// adminUserResponse is the POST /admin/users response body.
// ADR-0065: response returns {id, email, name, slug}.
type adminUserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
}

// TestMain creates the ephemeral test user, acquires NATS credentials via
// the device-code flow, and stores them in cli-integration/.test-creds.json.
// Teardown deletes the test project (if any) and the test user.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// Ensure the cli-integration directory exists.
	if err := os.MkdirAll("cli-integration", 0700); err != nil {
		fmt.Fprintf(os.Stderr, "integration: create cli-integration dir: %v\n", err)
		return 1
	}

	adminURL := os.Getenv("ADMIN_URL")
	if adminURL == "" {
		// Skip: write {skipped:true} and return without failure.
		writeSkipped()
		fmt.Fprintln(os.Stderr, "integration: ADMIN_URL not set — skipping integration tests")
		return 0
	}

	serverURL := os.Getenv("MCLAUDE_TEST_SERVER_URL")
	if serverURL == "" {
		serverURL = clicontext.DefaultServerURL
	}

	hslug := os.Getenv("MCLAUDE_TEST_HOST_SLUG")
	if hslug == "" {
		hslug = "dev-local"
	}

	adminToken := os.Getenv("ADMIN_TOKEN")

	// Create ephemeral test user.
	ts := time.Now().UnixMilli()
	userID := fmt.Sprintf("cli-test-%d", ts)
	userEmail := fmt.Sprintf("cli-test-%d@integration.test", ts)
	userPassword := fmt.Sprintf("Pwd%d!X", ts)
	userName := fmt.Sprintf("CLI Test User %d", ts)

	user, err := createTestUser(adminURL, adminToken, adminUserRequest{
		ID:       userID,
		Email:    userEmail,
		Name:     userName,
		Password: userPassword,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: create test user: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "integration: created test user slug=%s\n", user.Slug)

	// Acquire NATS credentials via device-code flow.
	// Two concurrent goroutines: RunLogin polls; a helper submits the code via form POST.
	credsPath := testCredsPath()

	type loginResult struct {
		result *cmd.LoginResult
		err    error
	}
	loginCh := make(chan loginResult, 1)

	// Temporary auth path so RunLogin writes to our test-creds location.
	authPath := credsPath

	var userCode string
	var codeMu sync.Mutex
	codeReady := make(chan struct{})

	// Acquire NATS credentials via device-code flow.
	// Goroutine 1 runs RunLogin (which POSTs /api/auth/device-code and polls).
	// Goroutine 2 reads the user code from RunLogin's output and submits the
	// form POST to /api/auth/device-code/verify.
	//
	// RunLogin writes the user code to its io.Writer output as:
	//   "Then enter the code: XXXXXX"
	// We capture that by piping RunLogin's output through an io.Pipe.

	pr, pw := io.Pipe()

	go func() {
		flags := cmd.LoginFlags{
			ServerURL: serverURL,
			AuthPath:  authPath,
			// Use a temp context path so we don't pollute real context.
			ContextPath: filepath.Join("cli-integration", ".test-context.json"),
		}
		result, err := cmd.RunLogin(flags, pw)
		pw.Close()
		loginCh <- loginResult{result: result, err: err}
	}()

	// Goroutine 2: parse RunLogin output to get the userCode, then submit the form.
	verifyErrCh := make(chan error, 1)
	go func() {
		// Read from pr line by line to find the user code.
		buf := make([]byte, 4096)
		accumulated := ""
		for {
			n, readErr := pr.Read(buf)
			if n > 0 {
				accumulated += string(buf[:n])
				// Look for "enter the code: XXXXXX"
				code := extractUserCode(accumulated)
				if code != "" {
					codeMu.Lock()
					userCode = code
					codeMu.Unlock()
					close(codeReady)
					// Drain remaining output.
					go io.Copy(io.Discard, pr)
					break
				}
			}
			if readErr != nil {
				verifyErrCh <- fmt.Errorf("read login output: %w", readErr)
				return
			}
		}

		// Code is already populated at this point; read it under lock.
		codeMu.Lock()
		uc := userCode
		codeMu.Unlock()

		if uc == "" {
			verifyErrCh <- fmt.Errorf("could not extract user code from RunLogin output")
			return
		}

		// Submit the device-code verify form via form POST.
		// POST /api/auth/device-code/verify with fields user_code, email, password.
		err := submitDeviceCodeVerify(serverURL, uc, userEmail, userPassword)
		verifyErrCh <- err
	}()

	// Wait for RunLogin to complete (or fail).
	lr := <-loginCh

	// Wait for the verify goroutine (may have already finished).
	select {
	case verifyErr := <-verifyErrCh:
		if verifyErr != nil {
			fmt.Fprintf(os.Stderr, "integration: device-code verify: %v\n", verifyErr)
			_ = deleteTestUser(adminURL, adminToken, user.ID)
			return 1
		}
	case <-time.After(5 * time.Second):
		// Verify goroutine may have completed before we checked — that's fine.
	}

	if lr.err != nil {
		fmt.Fprintf(os.Stderr, "integration: RunLogin: %v\n", lr.err)
		_ = deleteTestUser(adminURL, adminToken, user.ID)
		return 1
	}

	// Read back the credentials from the file RunLogin wrote.
	credsData, err := os.ReadFile(credsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: read test-creds: %v\n", err)
		_ = deleteTestUser(adminURL, adminToken, user.ID)
		return 1
	}
	var creds testCreds
	if err := json.Unmarshal(credsData, &creds); err != nil {
		fmt.Fprintf(os.Stderr, "integration: parse test-creds: %v\n", err)
		_ = deleteTestUser(adminURL, adminToken, user.ID)
		return 1
	}

	// Populate globals.
	intJWT = creds.JWT
	intNKeySeed = creds.NKeySeed
	intUserSlug = creds.UserSlug
	intUserID = user.ID
	intHSlug = hslug
	intAdminURL = adminURL
	intServerURL = serverURL

	// codeReady is already closed by the verify goroutine (which broke out of the loop).
	// We don't need to wait on it here since we already waited on loginCh and verifyErrCh.
	_ = codeReady

	fmt.Fprintf(os.Stderr, "integration: TestMain setup complete; userSlug=%s hslug=%s\n", intUserSlug, intHSlug)

	// Run tests.
	code := m.Run()

	// Teardown: delete test project (if any) then delete test user.
	if intProjectSlug != "" {
		if err := deleteTestProject(adminURL, adminToken, user.ID, intProjectSlug); err != nil {
			fmt.Fprintf(os.Stderr, "integration: teardown delete project: %v\n", err)
		}
	}
	if err := deleteTestUser(adminURL, adminToken, user.ID); err != nil {
		fmt.Fprintf(os.Stderr, "integration: teardown delete user: %v\n", err)
	}

	return code
}

// writeSkipped writes {skipped:true} to the test-creds file and returns.
func writeSkipped() {
	_ = os.MkdirAll("cli-integration", 0700)
	data, _ := json.Marshal(testCreds{Skipped: true})
	_ = os.WriteFile(testCredsPath(), data, 0600)
}

// extractUserCode parses the RunLogin output to find the user code.
// RunLogin prints: "Then enter the code: XXXXXX"
func extractUserCode(output string) string {
	const prefix = "Then enter the code: "
	idx := findSubstring(output, prefix)
	if idx < 0 {
		return ""
	}
	rest := output[idx+len(prefix):]
	// Code ends at newline or end of string.
	end := len(rest)
	for i, c := range rest {
		if c == '\n' || c == '\r' || c == ' ' {
			end = i
			break
		}
	}
	code := rest[:end]
	if len(code) == 0 {
		return ""
	}
	return code
}

func findSubstring(s, sub string) int {
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// submitDeviceCodeVerify completes the device-code authorization via form POST.
// POST /api/auth/device-code/verify with form fields: user_code, email, password.
func submitDeviceCodeVerify(serverURL, userCode, email, password string) error {
	formData := url.Values{
		"user_code": {userCode},
		"email":     {email},
		"password":  {password},
	}
	resp, err := http.PostForm(serverURL+"/api/auth/device-code/verify", formData)
	if err != nil {
		return fmt.Errorf("POST /api/auth/device-code/verify: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST /api/auth/device-code/verify returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// createTestUser creates an ephemeral test user via POST /admin/users.
func createTestUser(adminURL, adminToken string, req adminUserRequest) (*adminUserResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, adminURL+"/admin/users", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if adminToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+adminToken)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("POST /admin/users: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST /admin/users returned %d: %s", resp.StatusCode, respBody)
	}
	var user adminUserResponse
	if err := json.Unmarshal(respBody, &user); err != nil {
		return nil, fmt.Errorf("parse /admin/users response: %w", err)
	}
	if user.Slug == "" {
		return nil, fmt.Errorf("/admin/users response missing slug field; body: %s", respBody)
	}
	return &user, nil
}

// deleteTestUser deletes the test user via DELETE /admin/users/{id}.
func deleteTestUser(adminURL, adminToken, userID string) error {
	reqURL := fmt.Sprintf("%s/admin/users/%s", adminURL, userID)
	httpReq, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	if adminToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+adminToken)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("DELETE /admin/users/%s: %w", userID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("DELETE /admin/users/%s returned %d: %s", userID, resp.StatusCode, body)
	}
	return nil
}

// deleteTestProject deletes the test project via DELETE /admin/users/{uid}/projects/{pslug}.
func deleteTestProject(adminURL, adminToken, userID, projectSlug string) error {
	reqURL := fmt.Sprintf("%s/admin/users/%s/projects/%s", adminURL, userID, projectSlug)
	httpReq, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	if adminToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+adminToken)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("DELETE project %s: %w", projectSlug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("DELETE project %s returned %d: %s", projectSlug, resp.StatusCode, body)
	}
	return nil
}
