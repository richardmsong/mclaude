package main

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	testutil "mclaude-session-agent/testutil"
)

// TestCrashMidTool verifies behaviour when Claude exits without emitting a
// result event (mid-tool crash scenario).
//
// Expected behaviour:
//   - Events up to the crash are published (assistant + control_request)
//   - doneCh is closed when the process exits
//   - KV retains the pendingControls written before the crash
//   - No result event is published
func TestCrashMidTool(t *testing.T) {
	sess, pc, kc := startTestSession(t, "crash_mid_tool.jsonl", "sess-crash")

	// Startup turn.
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("startup events not received (got %d)", len(pc.messages()))
	}

	// Send user message to trigger the turn that contains __crash__.
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"do the thing"}}`))

	// Wait for Claude process to exit (doneCh closes).
	select {
	case <-sess.doneCh:
		// process exited as expected
	case <-time.After(10 * time.Second):
		t.Fatal("session process did not exit within timeout")
	}

	msgs := pc.messages()

	// control_request should have been published before the crash.
	var foundCR bool
	for _, m := range msgs {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeControlRequest {
			foundCR = true
		}
	}
	if !foundCR {
		t.Error("control_request was not published before crash")
	}

	// No result event should have been published.
	for _, m := range msgs {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeResult {
			t.Error("result event published despite crash — expected none")
		}
	}

	// KV should have pending controls written (from before the crash).
	states := kc.all()
	var maxPending int
	for _, s := range states {
		if len(s.PendingControls) > maxPending {
			maxPending = len(s.PendingControls)
		}
	}
	if maxPending == 0 {
		t.Error("KV never recorded pending controls before crash")
	}
}

// TestResumeClearsPendingControls verifies that when a session is resumed after
// a crash, the stale pendingControls from the previous run are cleared from the
// SessionState before the resumed session starts processing.
func TestResumeClearsPendingControls(t *testing.T) {
	// Step 1: Set up a session state that has stale pending controls
	// (simulating what KV would look like after a crash).
	staleSt := SessionState{
		ID:        "sess-resume",
		ProjectID: "test-proj",
		State:     StateRequiresAction,
		CreatedAt: time.Now(),
		PendingControls: map[string]any{
			"stale-cr-01": json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Bash"}`),
		},
	}

	// Step 2: Simulate recovery — clear pending controls before restart.
	clearPendingControlsForResume(&staleSt)

	if len(staleSt.PendingControls) != 0 {
		t.Errorf("expected pending controls cleared, got %d", len(staleSt.PendingControls))
	}
	if staleSt.State != StateIdle {
		t.Errorf("expected state reset to idle, got %q", staleSt.State)
	}

	// Step 3: Start a resumed session with the cleared state.
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("session_resume.jsonl")

	sess := newSession(staleSt, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	pc := &publishCapture{}
	kc := &kvCapture{}

	if err := sess.start(mockClaude, pc.publish, kc.write); err != nil {
		t.Fatalf("session.start (resume): %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	// Startup turn emitted.
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("resume startup not received")
	}

	// Send user message.
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"continue"}}`))

	// Wait for result.
	if !pc.waitForN(8, 10*time.Second) {
		t.Logf("have %d msgs", len(pc.messages()))
	}

	var foundResult bool
	for _, m := range pc.messages() {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeResult {
			foundResult = true
		}
	}
	if !foundResult {
		t.Error("resumed session did not produce result")
	}
}

// TestPublishErrorTolerance verifies that transient publish errors do not
// crash the session — events keep being read from Claude stdout.
func TestPublishErrorTolerance(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:        "sess-publish-err",
		ProjectID: "test-proj",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	// Publish function that fails every other call.
	var callN atomic.Int64
	var publishErrors int64
	publish := func(subject string, data []byte) error {
		n := callN.Add(1)
		if n%2 == 0 {
			atomic.AddInt64(&publishErrors, 1)
			return errors.New("simulated NATS disconnect")
		}
		return nil
	}

	var kvWrites int64
	writeKV := func(state SessionState) error {
		atomic.AddInt64(&kvWrites, 1)
		return nil
	}

	if err := sess.start(mockClaude, publish, writeKV); err != nil {
		t.Fatalf("session.start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	// Startup (some events published, some errored).
	time.Sleep(500 * time.Millisecond)

	// Send user message.
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}`))

	// Wait for process to finish.
	select {
	case <-sess.doneCh:
	case <-time.After(15 * time.Second):
		t.Fatal("session did not finish")
	}

	// The session must have survived despite publish errors.
	if atomic.LoadInt64(&publishErrors) == 0 {
		t.Error("expected some publish errors to occur")
	}
	// KV writes should still have happened (they use a separate callback).
	if atomic.LoadInt64(&kvWrites) == 0 {
		t.Error("expected some KV writes despite publish errors")
	}
}

// TestGracefulStop verifies that session.stop() causes the process to exit
// cleanly within the timeout period.
func TestGracefulStop(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:        "sess-graceful",
		ProjectID: "test-proj",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	var mu sync.Mutex
	var published []capturedMsg

	publish := func(subject string, data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		mu.Lock()
		published = append(published, capturedMsg{subject: subject, data: cp})
		mu.Unlock()
		return nil
	}

	if err := sess.start(mockClaude, publish, func(SessionState) error { return nil }); err != nil {
		t.Fatalf("session.start: %v", err)
	}

	// Wait for startup.
	time.Sleep(200 * time.Millisecond)

	// Stop the session.
	start := time.Now()
	sess.stop()

	select {
	case <-sess.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not stop within 5s after stop()")
	}

	elapsed := time.Since(start)
	t.Logf("session stopped in %v", elapsed)
}
