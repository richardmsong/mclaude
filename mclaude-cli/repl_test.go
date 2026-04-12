// Component tests for the REPL.  Each test spins up a MockServer, injects
// canned events, feeds controlled user input, and asserts the rendered output
// and the messages sent back to the server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"mclaude-cli/repl"
	"mclaude-cli/testutil"
)

// transcriptPath returns the absolute path to a testutil transcript file.
func transcriptPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testutil", "transcripts", name)
}

// syncBuffer is a thread-safe writer suitable for concurrent use as repl.Output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForOutput polls out until expected appears or timeout elapses.
// Returns true if found.
func waitForOutput(tb testing.TB, out *syncBuffer, expected string, timeout time.Duration) bool {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), expected) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// runSimpleREPL connects to srv, runs the REPL with inputLines fed from a
// strings.Reader, and returns (output, error).  Suitable for tests where all
// input can be determined upfront and no timing synchronisation is needed.
func runSimpleREPL(t *testing.T, srv *testutil.MockServer, inputLines []string) (string, error) {
	t.Helper()
	conn, err := net.Dial("unix", srv.Path)
	if err != nil {
		t.Fatalf("dial mock server: %v", err)
	}
	var out bytes.Buffer
	cfg := repl.Config{
		SessionID: "test-session",
		Input:     strings.NewReader(strings.Join(inputLines, "\n") + "\n"),
		Output:    &out,
		Log:       zerolog.Nop(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = repl.Run(ctx, conn, cfg)
	return out.String(), err
}

// ── Simple message ────────────────────────────────────────────────────────────

func TestReplSimpleMessage(t *testing.T) {
	srv := testutil.NewMockServerFromFile(t, transcriptPath("simple_message.jsonl"))
	go srv.ServeOne(0)

	out, err := runSimpleREPL(t, srv, []string{"hello"})
	if err != nil {
		t.Fatalf("REPL returned error: %v", err)
	}
	srv.Wait()

	if !strings.Contains(out, "test-session") {
		t.Errorf("output = %q; want session ID in header", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("output = %q; want 'Hello there!' from transcript", out)
	}

	received := srv.Received()
	if len(received) == 0 {
		t.Fatal("server received no messages; want at least one user message")
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(received[0], &msg); err != nil {
		t.Fatalf("unmarshal received[0]: %v", err)
	}
	if msg["type"] != "user" {
		t.Errorf("received[0].type = %q; want user", msg["type"])
	}
}

// ── Streaming text ────────────────────────────────────────────────────────────

func TestReplStreamingTextAccumulated(t *testing.T) {
	srv := testutil.NewMockServerFromFile(t, transcriptPath("streaming.jsonl"))
	go srv.ServeOne(0)

	out, _ := runSimpleREPL(t, srv, []string{"analyze"})
	srv.Wait()

	if !strings.Contains(out, "I'll") {
		t.Errorf("output = %q; want first streaming chunk", out)
	}
	if !strings.Contains(out, "analyze") {
		t.Errorf("output = %q; want second streaming chunk", out)
	}
	if !strings.Contains(out, "code") {
		t.Errorf("output = %q; want third streaming chunk", out)
	}
}

// ── Permission prompt (approve) ───────────────────────────────────────────────

// TestReplToolUsePermissionApprove uses io.Pipe so we can withhold "y" until
// the permission prompt has actually appeared in the output — avoiding the
// race between the event goroutine storing the pending permission and the input
// goroutine reading "y".
func TestReplToolUsePermissionApprove(t *testing.T) {
	srv := testutil.NewMockServerFromFile(t, transcriptPath("tool_use.jsonl"))
	go srv.ServeOne(2 * time.Millisecond) // small inter-event delay

	conn, err := net.Dial("unix", srv.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	pr, pw := io.Pipe()
	var out syncBuffer
	cfg := repl.Config{
		SessionID: "test-session",
		Input:     pr,
		Output:    &out,
		Log:       zerolog.Nop(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replDone := make(chan error, 1)
	go func() { replDone <- repl.Run(ctx, conn, cfg) }()

	// Send initial user message.
	fmt.Fprintln(pw, "run tests")

	// Wait until the permission prompt is rendered before sending "y".
	if !waitForOutput(t, &out, "(y/n)", 3*time.Second) {
		t.Fatal("permission prompt did not appear within timeout")
	}
	fmt.Fprintln(pw, "y")
	pw.Close()

	if err := <-replDone; err != nil {
		t.Fatalf("REPL error: %v", err)
	}
	srv.Wait()

	outStr := out.String()
	if !strings.Contains(outStr, "Allow") {
		t.Errorf("output = %q; want 'Allow ...' permission prompt", outStr)
	}
	if !strings.Contains(outStr, "tool_use") {
		t.Errorf("output = %q; want [tool_use: ...] rendered", outStr)
	}
	if !strings.Contains(outStr, "tool_result") {
		t.Errorf("output = %q; want [tool_result] in output", outStr)
	}

	received := srv.Received()
	if len(received) < 2 {
		t.Fatalf("server received %d messages; want ≥2 (user msg + control_response)", len(received))
	}

	var ctrlResp map[string]interface{}
	if err := json.Unmarshal(received[1], &ctrlResp); err != nil {
		t.Fatalf("unmarshal control_response: %v", err)
	}
	if ctrlResp["type"] != "control_response" {
		t.Errorf("received[1].type = %q; want control_response", ctrlResp["type"])
	}
	outer, _ := ctrlResp["response"].(map[string]interface{})
	if outer == nil {
		t.Fatalf("control_response.response is nil")
	}
	inner, _ := outer["response"].(map[string]interface{})
	if inner == nil {
		t.Fatalf("control_response.response.response is nil")
	}
	if inner["behavior"] != "allow" {
		t.Errorf("behavior = %q; want allow", inner["behavior"])
	}
}

// ── Permission prompt (deny) ──────────────────────────────────────────────────

func TestReplToolUsePermissionDeny(t *testing.T) {
	srv := testutil.NewMockServerFromFile(t, transcriptPath("tool_use.jsonl"))
	go srv.ServeOne(2 * time.Millisecond)

	conn, err := net.Dial("unix", srv.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	pr, pw := io.Pipe()
	var out syncBuffer
	cfg := repl.Config{
		SessionID: "test-session",
		Input:     pr,
		Output:    &out,
		Log:       zerolog.Nop(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replDone := make(chan error, 1)
	go func() { replDone <- repl.Run(ctx, conn, cfg) }()

	fmt.Fprintln(pw, "run tests")

	if !waitForOutput(t, &out, "(y/n)", 3*time.Second) {
		t.Fatal("permission prompt did not appear within timeout")
	}
	fmt.Fprintln(pw, "n")
	pw.Close()

	if err := <-replDone; err != nil {
		t.Fatalf("REPL error: %v", err)
	}
	srv.Wait()

	received := srv.Received()
	if len(received) < 2 {
		t.Fatalf("server received %d messages; want ≥2", len(received))
	}

	var ctrlResp map[string]interface{}
	if err := json.Unmarshal(received[1], &ctrlResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	outer, _ := ctrlResp["response"].(map[string]interface{})
	if outer == nil {
		t.Fatalf("control_response.response is nil")
	}
	inner, _ := outer["response"].(map[string]interface{})
	if inner == nil {
		t.Fatalf("control_response.response.response is nil")
	}
	if inner["behavior"] != "deny" {
		t.Errorf("behavior = %q; want deny", inner["behavior"])
	}
}

// ── User message format ───────────────────────────────────────────────────────

func TestReplUserMessageFormat(t *testing.T) {
	// Server sends no events — just verifies the message the CLI sends.
	srv := testutil.NewMockServer(t, [][]byte{})
	go srv.ServeOne(0)

	runSimpleREPL(t, srv, []string{"fix the bug"})
	srv.Wait()

	received := srv.Received()
	if len(received) == 0 {
		t.Fatal("server received no messages")
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(received[0], &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg["type"] != "user" {
		t.Errorf("type = %q; want user", msg["type"])
	}
	body, ok := msg["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("message field missing or wrong type")
	}
	if body["role"] != "user" {
		t.Errorf("role = %q; want user", body["role"])
	}
	if body["content"] != "fix the bug" {
		t.Errorf("content = %q; want %q", body["content"], "fix the bug")
	}
}

// ── Blank lines ignored ───────────────────────────────────────────────────────

func TestReplEmptyInputIgnored(t *testing.T) {
	srv := testutil.NewMockServer(t, [][]byte{})
	go srv.ServeOne(0)

	runSimpleREPL(t, srv, []string{"", "  ", "hello"})
	srv.Wait()

	received := srv.Received()
	if len(received) != 1 {
		t.Errorf("received %d messages; want 1 (blank lines must be ignored)", len(received))
	}
}

// ── State indicator rendered ──────────────────────────────────────────────────

func TestReplStateRendered(t *testing.T) {
	srv := testutil.NewMockServerFromFile(t, transcriptPath("simple_message.jsonl"))
	go srv.ServeOne(0)

	out, _ := runSimpleREPL(t, srv, []string{"hi"})
	srv.Wait()

	if !strings.Contains(out, "state:") {
		t.Errorf("output = %q; want [state: ...] marker", out)
	}
}
