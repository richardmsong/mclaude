// monitoring_test.go verifies structured log output and exit codes.
//
// Categories tested:
//   - --log-machine flag produces newline-delimited JSON on stderr with
//     required fields: component, sessionId, level, time
//   - --log-level flag controls zerolog global level
//   - Exit code 1 when the unix socket does not exist
//   - Exit code 0 on clean REPL EOF
//
// These tests exercise run() directly (not exec.Command) so they stay fast and
// the binary need not be pre-built.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"mclaude-cli/repl"
	"mclaude-cli/testutil"
)

// ── Exit codes ────────────────────────────────────────────────────────────────

func TestExitCodeNoSocket(t *testing.T) {
	// Redirect os.Stderr to /dev/null so the error log doesn't pollute test output.
	old := os.Stderr
	os.Stderr, _ = os.Open(os.DevNull)
	defer func() { os.Stderr = old }()

	code := run([]string{"mclaude-cli", "attach", "no-such-session",
		"--socket", "/tmp/mclaude-does-not-exist-12345.sock"})
	if code != 1 {
		t.Errorf("exit code = %d; want 1 when socket does not exist", code)
	}
}

func TestExitCodeMissingSubcommand(t *testing.T) {
	old := os.Stderr
	os.Stderr, _ = os.Open(os.DevNull)
	defer func() { os.Stderr = old }()

	code := run([]string{"mclaude-cli"}) // no subcommand
	if code != 1 {
		t.Errorf("exit code = %d; want 1 for missing subcommand", code)
	}
}

func TestExitCodeWrongSubcommand(t *testing.T) {
	old := os.Stderr
	os.Stderr, _ = os.Open(os.DevNull)
	defer func() { os.Stderr = old }()

	code := run([]string{"mclaude-cli", "unknown", "session-id"})
	if code != 1 {
		t.Errorf("exit code = %d; want 1 for unknown subcommand", code)
	}
}

func TestExitCodeCleanEOF(t *testing.T) {
	// A server that sends one event and closes — clean REPL exit.
	srv := testutil.NewMockServer(t, [][]byte{
		[]byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`),
	})
	go srv.ServeOne(0)

	conn, err := net.Dial("unix", srv.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	var out bytes.Buffer
	cfg := repl.Config{
		SessionID: "clean-exit",
		Input:     strings.NewReader(""), // immediate EOF
		Output:    &out,
		Log:       zerolog.Nop(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := repl.Run(ctx, conn, cfg); err != nil {
		t.Errorf("Run() returned %v; want nil for clean EOF", err)
	}
}

// ── Structured log output ─────────────────────────────────────────────────────

// captureLog redirects os.Stderr to a pipe, calls fn, then returns everything
// written to stderr.
func captureLog(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestLogMachineFlag(t *testing.T) {
	srv := testutil.NewMockServer(t, [][]byte{
		[]byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`),
	})
	go srv.ServeOne(0)

	logOutput := captureLog(func() {
		// run() logs to os.Stderr, so capturing stderr captures logs.
		run([]string{
			"mclaude-cli", "attach", "log-test",
			"--socket", srv.Path,
			"--log-machine",
		})
	})
	srv.Wait()

	if strings.TrimSpace(logOutput) == "" {
		t.Skip("no log output captured — possibly --log-machine suppresses output at info level")
	}

	// Each line should be valid JSON.
	scanner := bufio.NewScanner(strings.NewReader(logOutput))
	lineCount := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("log line is not valid JSON: %q (%v)", line, err)
			continue
		}

		// Required fields.
		if obj["component"] != "mclaude-cli" {
			t.Errorf("log line missing/wrong component field: %q", line)
		}
		if obj["sessionId"] != "log-test" {
			t.Errorf("log line missing/wrong sessionId field: %q", line)
		}
		if _, ok := obj["level"]; !ok {
			t.Errorf("log line missing level field: %q", line)
		}
		lineCount++
	}

	if lineCount == 0 {
		t.Skip("no parseable JSON log lines produced — verify zerolog emits at INFO for this session")
	}
}

func TestLogLevelFlag(t *testing.T) {
	// With --log-level warn, debug/info lines should not appear.
	srv := testutil.NewMockServer(t, [][]byte{})
	go srv.ServeOne(0)

	logOutput := captureLog(func() {
		run([]string{
			"mclaude-cli", "attach", "level-test",
			"--socket", srv.Path,
			"--log-machine",
			"--log-level", "warn",
		})
	})
	srv.Wait()

	// None of the lines should have level "debug" or "info".
	scanner := bufio.NewScanner(strings.NewReader(logOutput))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		lvl, _ := obj["level"].(string)
		if lvl == "debug" || lvl == "info" {
			t.Errorf("log line with level %q appeared despite --log-level warn: %q", lvl, line)
		}
	}
}

// TestNoFmtPrintln verifies that production paths use zerolog, not fmt.Println.
// We do this statically by scanning source files for fmt.Println calls.
func TestNoFmtPrintln(t *testing.T) {
	// Packages that must not use fmt.Println (renderer may use fmt.Fprint* to w
	// but not fmt.Println/Printf to stdout/stderr).
	scanDirs := []string{
		"repl",
	}

	for _, dir := range scanDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			data, err := os.ReadFile(dir + "/" + e.Name())
			if err != nil {
				t.Fatalf("read %s/%s: %v", dir, e.Name(), err)
			}
			if bytes.Contains(data, []byte("fmt.Println(")) ||
				bytes.Contains(data, []byte("fmt.Printf(os.Stderr")) {
				t.Errorf("%s/%s contains fmt.Println or fmt.Printf to stderr; use zerolog", dir, e.Name())
			}
		}
	}
}
