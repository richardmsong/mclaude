package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	testutil "mclaude-session-agent/testutil"
)

// publishCapture collects published (subject, data) pairs for assertions.
type publishCapture struct {
	mu   sync.Mutex
	msgs []capturedMsg
}

type capturedMsg struct {
	subject string
	data    []byte
}

func (pc *publishCapture) publish(subject string, data []byte) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	pc.msgs = append(pc.msgs, capturedMsg{subject: subject, data: cp})
	return nil
}

func (pc *publishCapture) messages() []capturedMsg {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	result := make([]capturedMsg, len(pc.msgs))
	copy(result, pc.msgs)
	return result
}

func (pc *publishCapture) waitForN(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(pc.messages()) >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// kvCapture collects SessionState writes for assertions.
type kvCapture struct {
	mu     sync.Mutex
	states []SessionState
}

func (kc *kvCapture) write(state SessionState) error {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	kc.states = append(kc.states, state)
	return nil
}

func (kc *kvCapture) all() []SessionState {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	result := make([]SessionState, len(kc.states))
	copy(result, kc.states)
	return result
}

func (kc *kvCapture) waitFor(pred func([]SessionState) bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(kc.all()) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// startTestSession builds mock-claude, creates and starts a Session with
// the given transcript, and returns captures + the session.
func startTestSession(t *testing.T, transcriptName, sessionID string) (*Session, *publishCapture, *kvCapture) {
	t.Helper()

	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath(transcriptName)

	st := SessionState{
		ID:        sessionID,
		ProjectID: "test-proj",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}

	sess := newSession(st, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	pc := &publishCapture{}
	kc := &kvCapture{}

	if err := sess.start(mockClaude, false, pc.publish, kc.write); err != nil {
		t.Fatalf("session.start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	return sess, pc, kc
}

// TestSimpleMessageLifecycle drives the simple_message transcript and verifies
// events are published and KV state transitions happen correctly.
func TestSimpleMessageLifecycle(t *testing.T) {
	sess, pc, kc := startTestSession(t, "simple_message.jsonl", "sess-simple")

	// Startup turn: init + idle state should appear immediately.
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("startup events not received (got %d)", len(pc.messages()))
	}

	// Verify init event was published.
	msgs := pc.messages()
	var foundInit bool
	for _, m := range msgs {
		var ev map[string]any
		if err := json.Unmarshal(m.data, &ev); err == nil {
			if ev["type"] == "system" && ev["subtype"] == "init" {
				foundInit = true
			}
		}
	}
	if !foundInit {
		t.Error("init event not published")
	}

	// KV should reflect idle state after init.
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.State == StateIdle && s.Model != "" {
				return true
			}
		}
		return false
	}, 5*time.Second) {
		t.Error("KV never reached idle state with model set")
	}

	// Send user message to trigger the next turn.
	userMsg := []byte(`{"type":"user","message":{"role":"user","content":"hello"}}`)
	sess.sendInput(userMsg)

	// Wait for result event (end of turn).
	if !pc.waitForN(12, 10*time.Second) {
		t.Logf("published %d messages so far", len(pc.messages()))
	}

	msgs = pc.messages()
	var foundResult bool
	for _, m := range msgs {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeResult {
			foundResult = true
		}
	}
	if !foundResult {
		t.Error("result event not published")
	}

	// KV usage should be non-zero after result.
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.Usage.InputTokens > 0 {
				return true
			}
		}
		return false
	}, 5*time.Second) {
		t.Error("KV usage never incremented")
	}

	// All events should be on the correct NATS subject.
	for _, m := range msgs {
		want := "mclaude.test-user.test-proj.events.sess-simple"
		if m.subject != want {
			t.Errorf("event on wrong subject: got %q, want %q", m.subject, want)
			break
		}
	}
}

// TestToolUseLifecycle drives the tool_use transcript:
// init → idle → user message → control_request → control_response → result.
func TestToolUseLifecycle(t *testing.T) {
	sess, pc, kc := startTestSession(t, "tool_use.jsonl", "sess-tool")

	// Wait for startup (init + idle).
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("startup not received")
	}

	// Send user message.
	userMsg := []byte(`{"type":"user","message":{"role":"user","content":"run echo hello"}}`)
	sess.sendInput(userMsg)

	// Wait for control_request (requires_action state).
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.State == StateRequiresAction {
				return true
			}
		}
		return false
	}, 10*time.Second) {
		t.Fatal("session never entered requires_action state")
	}

	// Verify control_request was published.
	var foundCR bool
	for _, m := range pc.messages() {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeControlRequest {
			foundCR = true
		}
	}
	if !foundCR {
		t.Error("control_request not published")
	}

	// Verify KV has a pending control.
	states := kc.all()
	var foundPending bool
	for _, s := range states {
		if len(s.PendingControls) > 0 {
			foundPending = true
			break
		}
	}
	if !foundPending {
		t.Error("no pending controls in KV")
	}

	// Send control_response (approve the tool use).
	ctrlResp := []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"cr_01","response":{"behavior":"allow"}}}`)
	sess.sendInput(ctrlResp)

	// Wait for result event.
	if !pc.waitForN(20, 10*time.Second) {
		t.Logf("waiting for result, have %d msgs", len(pc.messages()))
	}

	var foundResult bool
	for _, m := range pc.messages() {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeResult {
			foundResult = true
		}
	}
	if !foundResult {
		t.Error("result event not published after tool use")
	}
}

// TestParallelToolsLifecycle drives the parallel_tools transcript:
// two simultaneous control_requests must both appear in KV.
func TestParallelToolsLifecycle(t *testing.T) {
	sess, pc, kc := startTestSession(t, "parallel_tools.jsonl", "sess-parallel")

	// Startup.
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("startup not received")
	}

	// Trigger first turn.
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"check two things"}}`))

	// Wait for two control_requests in published events.
	if !pc.waitForN(6, 10*time.Second) {
		t.Logf("have %d msgs", len(pc.messages()))
	}

	var crCount int
	for _, m := range pc.messages() {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeControlRequest {
			crCount++
		}
	}
	if crCount < 2 {
		t.Errorf("expected 2 control_requests, got %d", crCount)
	}

	// KV should show requires_action and pending controls.
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.State == StateRequiresAction {
				return true
			}
		}
		return false
	}, 5*time.Second) {
		t.Error("session never entered requires_action")
	}

	// Send approval and verify result.
	sess.sendInput([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"cr_01","response":{"behavior":"allow"}}}`))

	// Wait for result.
	if !pc.waitForN(15, 10*time.Second) {
		t.Logf("waiting for result, have %d", len(pc.messages()))
	}

	var foundResult bool
	for _, m := range pc.messages() {
		evType, _ := parseEventType(m.data)
		if evType == EventTypeResult {
			foundResult = true
		}
	}
	if !foundResult {
		t.Error("result not published after parallel tools")
	}
}

// TestCompactionBoundary drives the compaction.jsonl transcript and verifies
// compact_boundary events are published.
func TestCompactionBoundary(t *testing.T) {
	sess, pc, _ := startTestSession(t, "compaction.jsonl", "sess-compact")

	// Startup.
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("startup not received")
	}

	// First turn.
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"first message"}}`))

	// Compaction turn (session sends empty stdin to trigger).
	time.Sleep(200 * time.Millisecond)
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"compact"}}`))

	// Post-compaction turn.
	time.Sleep(200 * time.Millisecond)
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"after compact"}}`))

	// Wait for compact_boundary event AND two result events.
	hasCompact := func(msgs []capturedMsg) bool {
		for _, m := range msgs {
			evType, _ := parseEventType(m.data)
			if evType == EventTypeCompactBoundary {
				return true
			}
		}
		return false
	}
	hasTwoResults := func(msgs []capturedMsg) bool {
		var n int
		for _, m := range msgs {
			evType, _ := parseEventType(m.data)
			if evType == EventTypeResult {
				n++
			}
		}
		return n >= 2
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		msgs := pc.messages()
		if hasCompact(msgs) && hasTwoResults(msgs) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !hasCompact(pc.messages()) {
		t.Error("compact_boundary event not published")
	}
	if !hasTwoResults(pc.messages()) {
		t.Errorf("expected at least 2 result events, got fewer (total msgs: %d)", len(pc.messages()))
	}
}

// TestEventSubjectFormat verifies all events are published on the correct
// hierarchical NATS subject.
func TestEventSubjectFormat(t *testing.T) {
	_, pc, _ := startTestSession(t, "simple_message.jsonl", "sess-subject-check")
	pc.waitForN(2, 5*time.Second)

	msgs := pc.messages()
	if len(msgs) == 0 {
		t.Fatal("no messages published")
	}
	for _, m := range msgs {
		if !strings.HasPrefix(m.subject, "mclaude.test-user.test-proj.events.") {
			t.Errorf("unexpected subject prefix: %q", m.subject)
		}
	}
}

// TestSpawnArgsIncludeReplayUserMessages verifies that start() passes
// --replay-user-messages to the Claude process for both new and resume sessions.
// The spec (plan-replay-user-messages.md) requires this flag to enable Claude
// to echo user messages on stdout so they flow through the events stream.
func TestSpawnArgsIncludeReplayUserMessages(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("simple_message.jsonl")

	for _, resume := range []bool{false, true} {
		resume := resume
		name := "new-session"
		if resume {
			name = "resume-session"
		}
		t.Run(name, func(t *testing.T) {
			sessID := "sess-args-check-" + name
			st := SessionState{
				ID:        sessID,
				ProjectID: "test-proj",
				State:     StateIdle,
				CreatedAt: time.Now(),
			}
			sess := newSession(st, "test-user")
			sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

			pc := &publishCapture{}
			kc := &kvCapture{}

			if err := sess.start(mockClaude, resume, pc.publish, kc.write); err != nil {
				t.Fatalf("session.start: %v", err)
			}
			t.Cleanup(func() {
				sess.stop()
				sess.waitDone()
			})

			// Read the cmd.Args that were passed to the process.
			sess.mu.Lock()
			args := sess.cmd.Args
			sess.mu.Unlock()

			// --replay-user-messages must be present.
			var found bool
			for _, arg := range args {
				if arg == "--replay-user-messages" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("--replay-user-messages not found in spawn args: %v", args)
			}

			// --include-partial-messages must also be present.
			var foundPartial bool
			for _, arg := range args {
				if arg == "--include-partial-messages" {
					foundPartial = true
					break
				}
			}
			if !foundPartial {
				t.Errorf("--include-partial-messages not found in spawn args: %v", args)
			}

			// Verify the session/resume flag is correct.
			if resume {
				var foundResume bool
				for i, arg := range args {
					if arg == "--resume" && i+1 < len(args) && args[i+1] == sessID {
						foundResume = true
						break
					}
				}
				if !foundResume {
					t.Errorf("--resume %s not found in resume spawn args: %v", sessID, args)
				}
			} else {
				var foundSessionID bool
				for i, arg := range args {
					if arg == "--session-id" && i+1 < len(args) && args[i+1] == sessID {
						foundSessionID = true
						break
					}
				}
				if !foundSessionID {
					t.Errorf("--session-id %s not found in new-session spawn args: %v", sessID, args)
				}
			}
		})
	}
}
