package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	testutil "mclaude-session-agent/testutil"
)

// ---------------------------------------------------------------------------
// ADR-0051, Fix 1: init event sets state to idle in KV
// ---------------------------------------------------------------------------

// TestInitEventSetsStateToIdleInKV verifies that when a session's init event
// is received, the KV entry is updated with state:"idle" — even when the
// session was started in a "failed" or "restarting" state (ADR-0051).
//
// Uses the init_only.jsonl transcript which emits a single init event with no
// following session_state_changed event, isolating the init handler's KV write.
func TestInitEventSetsStateToIdleInKV(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("init_only.jsonl")

	// Start the session in StateFailed, as would happen after a crash/resume.
	st := SessionState{
		ID:          "sess-adr0051-init",
		Slug:        "sess-adr0051-init",
		UserSlug:    "test-user",
		HostSlug:    "test-host",
		ProjectSlug: "test-proj",
		ProjectID:   "test-proj",
		State:       StateFailed,
		CreatedAt:   time.Now(),
	}

	sess := newSession(st, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	pc := &publishCapture{}
	kc := &kvCapture{}

	if err := sess.start(mockClaude, true, pc.publish, kc.write); err != nil {
		t.Fatalf("session.start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		// doneCh may already be closed since the transcript exits immediately.
		select {
		case <-sess.doneCh:
		case <-time.After(2 * time.Second):
		}
	})

	// After the init event, KV must show StateIdle with the model populated.
	// The init_only transcript has no session_state_changed event, so any
	// StateIdle write comes exclusively from the init handler (Fix 1).
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.State == StateIdle && s.Model == "test-model" {
				return true
			}
		}
		return false
	}, 5*time.Second) {
		states := kc.all()
		stateNames := make([]string, 0, len(states))
		for _, s := range states {
			stateNames = append(stateNames, s.State)
		}
		t.Errorf("KV never reached idle+model after init event. "+
			"Session was started in StateFailed. KV states written: %v", stateNames)
	}

	// Verify also that the init event was actually published to NATS.
	if !pc.waitForN(1, 3*time.Second) {
		t.Fatal("no events published to NATS")
	}
	var foundInit bool
	for _, m := range pc.messages() {
		evType, subtype := parseEventType(m.data)
		if evType == EventTypeSystem && subtype == SubtypeInit {
			foundInit = true
		}
	}
	if !foundInit {
		t.Error("init event not published to NATS")
	}
}

// ---------------------------------------------------------------------------
// ADR-0051, Fix 3: flushKV logs warning on writeKV error
// ---------------------------------------------------------------------------

// TestFlushKVLogsOnError verifies that flushKV logs a warn-level message when
// writeKV returns an error, rather than silently discarding it (ADR-0051).
func TestFlushKVLogsOnError(t *testing.T) {
	var buf bytes.Buffer
	testLog := zerolog.New(&buf).Level(zerolog.WarnLevel)

	st := SessionState{
		ID:        "sess-adr0051-flush",
		ProjectID: "test-proj",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "test-user")
	sess.log = testLog

	// writeKV that always fails.
	writeErr := errors.New("NATS: connection refused")
	writeKV := func(_ SessionState) error { return writeErr }

	// flushKV must not panic or return an error; fire-and-forget semantics are preserved.
	sess.flushKV(writeKV)

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected a log entry after writeKV error, got nothing")
	}
	if !strings.Contains(logOutput, `"level":"warn"`) {
		t.Errorf("expected warn level in log output, got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "flushKV") {
		t.Errorf("expected 'flushKV' in log message, got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "NATS: connection refused") {
		t.Errorf("expected error text in log output, got: %q", logOutput)
	}
}

// TestFlushKVSilentOnSuccess verifies that flushKV produces no log output
// when writeKV succeeds (no spurious warnings on the happy path).
func TestFlushKVSilentOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	testLog := zerolog.New(&buf).Level(zerolog.WarnLevel)

	st := SessionState{
		ID:        "sess-adr0051-ok",
		ProjectID: "test-proj",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "test-user")
	sess.log = testLog

	writeKV := func(_ SessionState) error { return nil }
	sess.flushKV(writeKV)

	if buf.Len() > 0 {
		t.Errorf("expected no log output on success, got: %q", buf.String())
	}
}
