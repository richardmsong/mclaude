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
		ID:          sessionID,
		Slug:        sessionID,  // use sessionID as slug for tests (already slug-valid)
		UserSlug:    "test-user",
		HostSlug:    "test-host",
		ProjectSlug: "test-proj",
		ProjectID:   "test-proj",
		State:       StateIdle,
		CreatedAt:   time.Now(),
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

	// All events should be on the correct NATS subject per ADR-0054/ADR-0035.
	// Format: mclaude.users.{u}.hosts.{h}.projects.{p}.sessions.{sslug}.events
	for _, m := range msgs {
		want := "mclaude.users.test-user.hosts.test-host.projects.test-proj.sessions.sess-simple.events"
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
// hierarchical NATS subject per ADR-0054/ADR-0035.
// Format: mclaude.users.{u}.hosts.{h}.projects.{p}.sessions.{sslug}.events
func TestEventSubjectFormat(t *testing.T) {
	_, pc, _ := startTestSession(t, "simple_message.jsonl", "sess-subject-check")
	pc.waitForN(2, 5*time.Second)

	msgs := pc.messages()
	if len(msgs) == 0 {
		t.Fatal("no messages published")
	}
	// The new format puts the session slug BEFORE "events" in the subject.
	// Old (wrong): ...projects.{p}.events.{sslug}
	// New (correct): ...projects.{p}.sessions.{sslug}.events
	expectedPrefix := "mclaude.users.test-user.hosts.test-host.projects.test-proj.sessions."
	expectedSuffix := ".events"
	for _, m := range msgs {
		if !strings.HasPrefix(m.subject, expectedPrefix) || !strings.HasSuffix(m.subject, expectedSuffix) {
			t.Errorf("unexpected subject format: %q (want prefix %q and suffix %q)", m.subject, expectedPrefix, expectedSuffix)
		}
	}
}

// TestShellSplit verifies the shellSplit function handles all quoting cases.
func TestShellSplit(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "basic space-separated",
			input: "a b c",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "double-quoted with spaces",
			input: `"foo bar"`,
			want:  []string{"foo bar"},
		},
		{
			name:  "single-quoted with spaces",
			input: `'foo bar'`,
			want:  []string{"foo bar"},
		},
		{
			name:  "mixed flag and quoted value",
			input: `--flag "value with spaces"`,
			want:  []string{"--flag", "value with spaces"},
		},
		{
			name:  "double-quote escape sequences",
			input: `"a\"b\\c"`,
			want:  []string{`a"b\c`},
		},
		{
			name:  "single-quote no escaping",
			input: `'a\"b'`,
			want:  []string{`a\"b`},
		},
		{
			name:  "extra whitespace between tokens",
			input: "  a   b   c  ",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "full extraFlags example",
			input: `--disallowedTools "Edit(src/**)" --model claude-opus-4-7`,
			want:  []string{"--disallowedTools", "Edit(src/**)", "--model", "claude-opus-4-7"},
		},
		{
			name:  "multiple disallowedTools",
			input: `--disallowedTools "Edit(mclaude-web/src/**)" --disallowedTools "Write(mclaude-web/src/**)" --model claude-opus-4-7`,
			want:  []string{"--disallowedTools", "Edit(mclaude-web/src/**)", "--disallowedTools", "Write(mclaude-web/src/**)", "--model", "claude-opus-4-7"},
		},
		{
			name:    "unclosed double quote",
			input:   `--foo "unclosed`,
			wantErr: true,
		},
		{
			name:    "unclosed single quote",
			input:   "--foo 'unclosed",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := shellSplit(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("shellSplit(%q) expected error, got %v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("shellSplit(%q) unexpected error: %v", tc.input, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("shellSplit(%q) = %v (len %d), want %v (len %d)", tc.input, got, len(got), tc.want, len(tc.want))
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("shellSplit(%q)[%d] = %q, want %q", tc.input, i, got[i], w)
				}
			}
		})
	}
}

// TestSpawnArgsExtraFlags verifies that start() shell-parses extraFlags and
// appends the resulting tokens to the Claude spawn args.
// This applies to both new-session and resume paths.
func TestSpawnArgsExtraFlags(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("simple_message.jsonl")

	for _, resume := range []bool{false, true} {
		resume := resume
		name := "new-session"
		if resume {
			name = "resume-session"
		}
		t.Run(name, func(t *testing.T) {
			sessID := "sess-extraflags-" + name
			st := SessionState{
				ID:         sessID,
				ProjectID:  "test-proj",
				State:      StateIdle,
				CreatedAt:  time.Now(),
				ExtraFlags: `--disallowedTools "Edit(src/**)" --model claude-opus-4-7`,
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

			sess.mu.Lock()
			args := sess.cmd.Args
			sess.mu.Unlock()

			// Verify all four parsed tokens appear in the args.
			wantTokens := []string{"--disallowedTools", "Edit(src/**)", "--model", "claude-opus-4-7"}
			for _, want := range wantTokens {
				var found bool
				for _, arg := range args {
					if arg == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected token %q in spawn args: %v", want, args)
				}
			}

			// Verify the tokens appear as a contiguous pair: --disallowedTools Edit(src/**)
			var pairFound bool
			for i, arg := range args {
				if arg == "--disallowedTools" && i+1 < len(args) && args[i+1] == "Edit(src/**)" {
					pairFound = true
					break
				}
			}
			if !pairFound {
				t.Errorf("--disallowedTools Edit(src/**) pair not found in spawn args: %v", args)
			}
		})
	}
}

// TestSpawnArgsExtraFlagsEmpty verifies that no extra tokens are appended when
// extraFlags is empty.
func TestSpawnArgsExtraFlagsEmpty(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:        "sess-noflags",
		ProjectID: "test-proj",
		State:     StateIdle,
		CreatedAt: time.Now(),
		// ExtraFlags deliberately empty
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

	sess.mu.Lock()
	args := sess.cmd.Args
	sess.mu.Unlock()

	// The fixed args for a non-resume session are:
	// [claudePath --print --verbose --output-format stream-json --input-format stream-json
	//  --include-partial-messages --replay-user-messages --session-id <id>]
	// Index 0 is the binary path, so total = 11.
	const wantArgCount = 11
	if len(args) != wantArgCount {
		t.Errorf("expected %d spawn args (no extra flags), got %d: %v", wantArgCount, len(args), args)
	}
}

// TestSpawnArgsMalformedExtraFlags verifies that start() returns an error when
// extraFlags contains unclosed quotes.
func TestSpawnArgsMalformedExtraFlags(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:         "sess-malformed",
		ProjectID:  "test-proj",
		State:      StateIdle,
		CreatedAt:  time.Now(),
		ExtraFlags: "--foo 'unclosed",
	}
	sess := newSession(st, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	pc := &publishCapture{}
	kc := &kvCapture{}

	err := sess.start(mockClaude, false, pc.publish, kc.write)
	if err == nil {
		t.Fatal("expected error from start() with malformed extraFlags, got nil")
	}
	if !strings.Contains(err.Error(), "extraFlags shell parse") {
		t.Errorf("expected 'extraFlags shell parse' in error message, got: %v", err)
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
