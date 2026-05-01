package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
)

func TestParseEventType(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantType    string
		wantSubtype string
	}{
		{
			name:        "system init",
			line:        `{"type":"system","subtype":"init","session_id":"s1"}`,
			wantType:    "system",
			wantSubtype: "init",
		},
		{
			name:        "session_state_changed",
			line:        `{"type":"system","subtype":"session_state_changed","state":"idle"}`,
			wantType:    "system",
			wantSubtype: "session_state_changed",
		},
		{
			name:        "assistant",
			line:        `{"type":"assistant","session_id":"s1","message":{}}`,
			wantType:    "assistant",
			wantSubtype: "",
		},
		{
			name:        "control_request",
			line:        `{"type":"control_request","request_id":"cr_1","request":{}}`,
			wantType:    "control_request",
			wantSubtype: "",
		},
		{
			name:        "result success",
			line:        `{"type":"result","subtype":"success","is_error":false}`,
			wantType:    "result",
			wantSubtype: "success",
		},
		{
			name:        "stream_event",
			line:        `{"type":"stream_event","event":{"type":"content_block_delta"}}`,
			wantType:    "stream_event",
			wantSubtype: "",
		},
		{
			name:        "compact_boundary",
			line:        `{"type":"compact_boundary","session_id":"s1"}`,
			wantType:    "compact_boundary",
			wantSubtype: "",
		},
		{
			name:        "tool_progress",
			line:        `{"type":"tool_progress","tool_use_id":"tu_1","tool_name":"Bash"}`,
			wantType:    "tool_progress",
			wantSubtype: "",
		},
		{
			name:        "malformed JSON",
			line:        `not json`,
			wantType:    "",
			wantSubtype: "",
		},
		{
			name:        "empty object",
			line:        `{}`,
			wantType:    "",
			wantSubtype: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotSubtype := parseEventType([]byte(tc.line))
			if gotType != tc.wantType {
				t.Errorf("type: got %q, want %q", gotType, tc.wantType)
			}
			if gotSubtype != tc.wantSubtype {
				t.Errorf("subtype: got %q, want %q", gotSubtype, tc.wantSubtype)
			}
		})
	}
}

func TestInitEventParsing(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s1","skills":["commit","review-pr"],"tools":["Bash","Read","Edit"],"agents":["general-purpose","Explore"],"mcp_servers":[],"model":"claude-sonnet-4-6","permissionMode":"managed"}`
	var ev initEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != "system" {
		t.Errorf("type: got %q, want %q", ev.Type, "system")
	}
	if ev.Subtype != "init" {
		t.Errorf("subtype: got %q, want %q", ev.Subtype, "init")
	}
	if ev.Model != "claude-sonnet-4-6" {
		t.Errorf("model: got %q, want %q", ev.Model, "claude-sonnet-4-6")
	}
	if len(ev.Tools) != 3 {
		t.Errorf("tools count: got %d, want 3", len(ev.Tools))
	}
	if len(ev.Skills) != 2 {
		t.Errorf("skills count: got %d, want 2", len(ev.Skills))
	}
	if len(ev.Agents) != 2 {
		t.Errorf("agents count: got %d, want 2", len(ev.Agents))
	}
}

// TestInitEventPopulatesCapabilities verifies that a session's Capabilities
// struct is populated with skills, tools, and agents from the init event.
func TestInitEventPopulatesCapabilities(t *testing.T) {
	sess, _, kc := startTestSession(t, "simple_message.jsonl", "sess-caps")

	// Wait for KV write with model set (init event processed).
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.Model != "" {
				return true
			}
		}
		return false
	}, 5*time.Second) {
		t.Fatal("KV never written after init event")
	}

	// Find the first KV state that has Capabilities set.
	var found *SessionState
	for _, s := range kc.all() {
		s := s
		if s.Model != "" {
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatal("no KV state with model set")
	}

	// Skills, Tools, Agents are top-level fields per spec-state-schema.md.
	if len(found.Skills) != 2 {
		t.Errorf("skills: got %v, want [commit review-pr]", found.Skills)
	}
	if len(found.Tools) != 6 {
		t.Errorf("tools: got %v, want 6 tools", found.Tools)
	}
	if len(found.Agents) != 2 {
		t.Errorf("agents: got %v, want [general-purpose Explore]", found.Agents)
	}

	// Suppress unused sess variable warning.
	_ = sess
}

// TestInitEventPopulatesCapabilities_BoolFlags verifies that the Capabilities field
// is populated with CLICapabilities boolean flags from the driver on init event.
// ADR-0005: the SPA reads these to determine backend features without event replay.
// Per spec-state-schema.md: capabilities holds boolean flags (hasThinking, etc.),
// NOT tools/skills/agents (those are now top-level fields).
func TestInitEventPopulatesDriverCapabilities(t *testing.T) {
	sess, _, kc := startTestSession(t, "simple_message.jsonl", "sess-driver-caps")

	// Wait for KV write with model set (init event processed).
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.Model != "" {
				return true
			}
		}
		return false
	}, 5*time.Second) {
		t.Fatal("KV never written after init event")
	}

	// Find the first KV state that has Capabilities set.
	var found *SessionState
	for _, s := range kc.all() {
		s := s
		if s.Model != "" {
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatal("no KV state with model set")
	}

	// ClaudeCodeDriver capabilities: HasThinking=true, HasEventStream=true, HasSubagents=true.
	// These are now in the top-level Capabilities field (drivers.CLICapabilities).
	if !found.Capabilities.HasThinking {
		t.Error("Capabilities.HasThinking: got false, want true (ClaudeCodeDriver)")
	}
	if !found.Capabilities.HasEventStream {
		t.Error("Capabilities.HasEventStream: got false, want true (ClaudeCodeDriver)")
	}
	if !found.Capabilities.HasSubagents {
		t.Error("Capabilities.HasSubagents: got false, want true (ClaudeCodeDriver)")
	}
	if !found.Capabilities.HasSessionResume {
		t.Error("Capabilities.HasSessionResume: got false, want true (ClaudeCodeDriver)")
	}
	if found.Capabilities.ThinkingLabel != "Thinking" {
		t.Errorf("Capabilities.ThinkingLabel: got %q, want %q", found.Capabilities.ThinkingLabel, "Thinking")
	}

	_ = sess
}

func TestStateChangedEventParsing(t *testing.T) {
	for _, state := range []string{"idle", "running", "requires_action"} {
		line, _ := json.Marshal(map[string]string{
			"type":    "system",
			"subtype": "session_state_changed",
			"state":   state,
		})
		evType, subtype := parseEventType(line)
		if evType != "system" || subtype != "session_state_changed" {
			t.Errorf("state %q: got type=%q subtype=%q", state, evType, subtype)
		}

		var ev stateChangedEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("unmarshal %q: %v", state, err)
		}
		if ev.State != state {
			t.Errorf("state: got %q, want %q", ev.State, state)
		}
	}
}

func TestControlRequestEventParsing(t *testing.T) {
	line := `{"type":"control_request","session_id":"s1","request_id":"cr_01","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_input":{"command":"echo hi"}}}`
	evType, _ := parseEventType([]byte(line))
	if evType != "control_request" {
		t.Errorf("type: got %q, want control_request", evType)
	}

	var ev controlRequestEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.RequestID != "cr_01" {
		t.Errorf("request_id: got %q, want cr_01", ev.RequestID)
	}
	if len(ev.Request) == 0 {
		t.Error("request payload should not be empty")
	}
}

// TestHandleInputStripsSessionID verifies that handleInput removes the
// session_id routing field from the NATS payload before forwarding to
// Claude's stdin via sendInput.  Claude Code's --input-format stream-json
// expects {"type":"user","message":{...}} without session_id.
func TestHandleInputStripsSessionID(t *testing.T) {
	const sessID = "sess-strip-test"

	// Build a minimal Session with a buffered stdinCh so we can read
	// what handleInput sends without starting the full Claude subprocess.
	sess := &Session{
		state:   SessionState{ID: sessID},
		stdinCh: make(chan []byte, 8),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		initCh:  make(chan struct{}),
	}

	// Build an Agent with the session registered but no real NATS connection.
	a := &Agent{
		sessions:  make(map[string]*Session),
		terminals: make(map[string]*TerminalSession),
		userID:    "u1",
		projectID: "p1",
		log:       zerolog.Nop(),
	}
	a.mu.Lock()
	a.sessions[sessID] = sess
	a.mu.Unlock()

	// Payload as the NATS subject handler receives it — includes session_id and uuid.
	// uuid must be preserved and forwarded to Claude stdin so Claude can echo it
	// back in the --replay-user-messages replay event.
	payload := map[string]any{
		"session_id": sessID,
		"type":       "user",
		"uuid":       "550e8400-e29b-41d4-a716-446655440000",
		"message": map[string]any{
			"role":    "user",
			"content": "hello world",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Invoke handleInput directly (no NATS required).
	a.handleInput(&nats.Msg{Data: data})

	// Read what landed on stdinCh — must arrive quickly since it's buffered.
	var received []byte
	select {
	case received = <-sess.stdinCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for data on stdinCh")
	}

	// session_id must not appear in the forwarded payload.
	var result map[string]json.RawMessage
	if err := json.Unmarshal(received, &result); err != nil {
		t.Fatalf("unmarshal received payload: %v", err)
	}
	if _, has := result["session_id"]; has {
		t.Errorf("session_id must be stripped before forwarding to Claude stdin; got: %s", received)
	}

	// The stream-json fields (type, uuid, message) must be preserved.
	// uuid is round-tripped to Claude stdin so Claude echoes it back in the
	// --replay-user-messages replay event on stdout.
	if string(result["type"]) != `"user"` {
		t.Errorf("type field must be preserved; got %s", result["type"])
	}
	if string(result["uuid"]) != `"550e8400-e29b-41d4-a716-446655440000"` {
		t.Errorf("uuid field must be preserved; got %s", result["uuid"])
	}
	if len(result["message"]) == 0 {
		t.Error("message field must be preserved")
	}

	// Verify the message content is intact.
	var msg map[string]any
	if err := json.Unmarshal(result["message"], &msg); err != nil {
		t.Fatalf("unmarshal message field: %v", err)
	}
	if msg["content"] != "hello world" {
		t.Errorf("message.content: got %v, want 'hello world'", msg["content"])
	}

	// Confirm only one item landed on the channel (no duplicates).
	if len(sess.stdinCh) != 0 {
		t.Errorf("expected exactly 1 item on stdinCh, but %d remain", len(sess.stdinCh)+1)
	}
}

// TestHandleInputMissingSessionID verifies that handleInput logs a warning and
// does not call sendInput when the session_id field is absent or empty.
func TestHandleInputMissingSessionID(t *testing.T) {
	a := &Agent{
		sessions:  make(map[string]*Session),
		terminals: make(map[string]*TerminalSession),
		userID:    "u1",
		projectID: "p1",
		log:       zerolog.Nop(),
	}

	cases := []struct {
		name    string
		payload []byte
	}{
		{"no session_id field", []byte(`{"type":"user","message":{"role":"user","content":"hi"}}`)},
		{"empty session_id", []byte(`{"session_id":"","type":"user","message":{}}`)},
		{"malformed JSON", []byte(`not json`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// If handleInput calls sendInput on a nil session it panics —
			// confirming the absence of a panic is the assertion.
			a.handleInput(&nats.Msg{Data: tc.payload})
		})
	}
}

// TestHandleInputUnknownSession verifies that handleInput does not panic or
// forward data when session_id is valid JSON but no session exists with that ID.
func TestHandleInputUnknownSession(t *testing.T) {
	a := &Agent{
		sessions:  make(map[string]*Session),
		terminals: make(map[string]*TerminalSession),
		userID:    "u1",
		projectID: "p1",
		log:       zerolog.Nop(),
	}

	payload := []byte(`{"session_id":"does-not-exist","type":"user","message":{"role":"user","content":"hi"}}`)
	// Must not panic — the session lookup fails and nothing is sent.
	a.handleInput(&nats.Msg{Data: payload})
}

// TestHandleInputNoManualPublish verifies that handleInput does NOT publish to
// the events stream directly.  With --replay-user-messages, Claude echoes user
// messages on stdout; the stdout scanner publishes them.  handleInput only
// writes to Claude's stdin.  This test confirms that handleInput with a real
// JetStream mock does NOT call Publish — the a.js field is nil here, and we
// verify the function still succeeds (sends to stdin) without panicking.
func TestHandleInputNoManualPublish(t *testing.T) {
	const sessID = "sess-no-publish"

	sess := &Session{
		state:   SessionState{ID: sessID},
		stdinCh: make(chan []byte, 8),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		initCh:  make(chan struct{}),
	}

	// Agent with nil js — if handleInput tries to call a.js.Publish it would panic.
	// The test passes only if handleInput does not touch a.js.
	a := &Agent{
		sessions:  make(map[string]*Session),
		terminals: make(map[string]*TerminalSession),
		userID:    "u1",
		projectID: "p1",
		log:       zerolog.Nop(),
		// js is intentionally nil — accessing it panics, confirming no publish
	}
	a.mu.Lock()
	a.sessions[sessID] = sess
	a.mu.Unlock()

	payload := []byte(`{"session_id":"` + sessID + `","type":"user","uuid":"abc-123","message":{"role":"user","content":"test"}}`)

	// Must not panic (would panic if a.js.Publish were called with a.js == nil).
	a.handleInput(&nats.Msg{Data: payload})

	// Confirm something arrived on stdinCh (the actual forwarding still works).
	select {
	case received := <-sess.stdinCh:
		var result map[string]json.RawMessage
		if err := json.Unmarshal(received, &result); err != nil {
			t.Fatalf("unmarshal stdinCh data: %v", err)
		}
		if _, has := result["session_id"]; has {
			t.Error("session_id must be stripped; still present in forwarded data")
		}
		if string(result["uuid"]) != `"abc-123"` {
			t.Errorf("uuid must be preserved; got %s", result["uuid"])
		}
	case <-time.After(time.Second):
		t.Fatal("no data received on stdinCh")
	}
}
