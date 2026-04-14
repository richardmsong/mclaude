package main

import (
	"encoding/json"
	"testing"
	"time"
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

	if len(found.Capabilities.Skills) != 2 {
		t.Errorf("skills: got %v, want [commit review-pr]", found.Capabilities.Skills)
	}
	if len(found.Capabilities.Tools) != 6 {
		t.Errorf("tools: got %v, want 6 tools", found.Capabilities.Tools)
	}
	if len(found.Capabilities.Agents) != 2 {
		t.Errorf("agents: got %v, want [general-purpose Explore]", found.Capabilities.Agents)
	}

	// Suppress unused sess variable warning.
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
