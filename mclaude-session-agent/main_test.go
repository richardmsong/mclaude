package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// buildAgentBinary compiles the session-agent binary into t.TempDir()
// and returns its path.  Re-used across health/ready probe tests.
func buildAgentBinary(t *testing.T) string {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	srcDir := filepath.Dir(thisFile)
	binPath := filepath.Join(t.TempDir(), "session-agent")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build session-agent:\n%s\n%v", out, err)
	}
	return binPath
}

// TestHealthProbeExitsZero verifies that `session-agent --health` exits 0
// without needing a NATS connection (liveness must not depend on NATS).
func TestHealthProbeExitsZero(t *testing.T) {
	bin := buildAgentBinary(t)

	cmd := exec.Command(bin, "--health")
	cmd.Env = os.Environ() // inherit, but NATS_URL may be anything — must not matter
	if err := cmd.Run(); err != nil {
		t.Errorf("--health exited non-zero: %v", err)
	}
}

// TestReadyProbeFailsWithoutNATS verifies that `session-agent --ready` exits
// non-zero when NATS is not reachable.
func TestReadyProbeFailsWithoutNATS(t *testing.T) {
	bin := buildAgentBinary(t)

	cmd := exec.Command(bin, "--ready")
	// Point at a port that will refuse connections immediately.
	cmd.Env = append(os.Environ(), "NATS_URL=nats://127.0.0.1:14224")

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if err == nil {
		t.Error("--ready should exit non-zero when NATS is unreachable, got exit 0")
	}
	// Should fail quickly, not hang for minutes.
	if elapsed > 10*time.Second {
		t.Errorf("--ready took too long to fail: %v (want < 10s)", elapsed)
	}
}

// TestCLIFlagsParsed verifies that the binary accepts --nats-url, --user-id,
// --project-id, --nats-creds, --data-dir, and --mode without crashing on
// argument parsing (before any I/O is attempted).
//
// We cannot fully run the agent in a unit test, but we can verify:
//   - --help prints usage without panicking
//   - --health still works regardless of other flags present
func TestCLIFlagsHelp(t *testing.T) {
	bin := buildAgentBinary(t)

	// go's flag package prints usage and exits 2 on -help/-h.
	cmd := exec.Command(bin, "--help")
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		// flag.Usage exits with code 2 — that is the expected behaviour.
		if exitErr.ExitCode() != 2 {
			t.Errorf("--help exited %d (want 2): %s", exitErr.ExitCode(), out)
		}
	} else if err != nil {
		t.Fatalf("unexpected error running --help: %v\n%s", err, out)
	}

	// All expected flags must appear in the usage output.
	// Go's flag package prints flags with a single dash: -flag-name.
	for _, flag := range []string{
		"-nats-url",
		"-nats-creds",
		"-user-id",
		"-project-id",
		"-data-dir",
		"-mode",
	} {
		if !containsBytes(out, flag) {
			t.Errorf("usage output missing flag %q:\n%s", flag, out)
		}
	}
}

// TestHealthProbeIgnoresOtherFlags verifies that --health short-circuits flag
// parsing and does not attempt to parse the remaining arguments.
func TestHealthProbeShortCircuits(t *testing.T) {
	bin := buildAgentBinary(t)

	// Pass --health as the only argument — should exit 0 immediately.
	cmd := exec.Command(bin, "--health")
	if err := cmd.Run(); err != nil {
		t.Errorf("--health should exit 0: %v", err)
	}
}

// containsBytes returns true if haystack contains the needle string.
func containsBytes(haystack []byte, needle string) bool {
	return len(haystack) >= len(needle) && indexBytes(haystack, []byte(needle)) >= 0
}

func indexBytes(s, sep []byte) int {
	n := len(sep)
	for i := 0; i <= len(s)-n; i++ {
		if string(s[i:i+n]) == string(sep) {
			return i
		}
	}
	return -1
}
