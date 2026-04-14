package main

import (
	"encoding/json"
	"testing"
	"time"
)

// TestKVKeyConstruction verifies the key format used for NATS KV lookups.
func TestKVKeyConstruction(t *testing.T) {
	cases := []struct {
		userID    string
		projectID string
		sessionID string
		want      string
	}{
		{"user-1", "proj-1", "sess-1", "user-1.proj-1.sess-1"},
		{"alice", "myproject", "abc-123-def", "alice.myproject.abc-123-def"},
	}
	for _, tc := range cases {
		got := sessionKVKey(tc.userID, tc.projectID, tc.sessionID)
		if got != tc.want {
			t.Errorf("sessionKVKey(%q,%q,%q) = %q, want %q",
				tc.userID, tc.projectID, tc.sessionID, got, tc.want)
		}
	}
}

func TestHeartbeatKVKey(t *testing.T) {
	got := heartbeatKVKey("user-1", "proj-1")
	want := "user-1.proj-1"
	if got != want {
		t.Errorf("heartbeatKVKey = %q, want %q", got, want)
	}
}

// TestPendingControlsOps verifies add/remove operations on PendingControls.
func TestPendingControlsOps(t *testing.T) {
	st := SessionState{
		PendingControls: make(map[string]any),
	}

	// Add two pending controls.
	payload1 := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Bash"}`)
	payload2 := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Read"}`)
	addPendingControl(&st, "cr_01", payload1)
	addPendingControl(&st, "cr_02", payload2)

	if len(st.PendingControls) != 2 {
		t.Fatalf("expected 2 pending controls, got %d", len(st.PendingControls))
	}

	// Remove one.
	removePendingControl(&st, "cr_01")
	if len(st.PendingControls) != 1 {
		t.Fatalf("expected 1 pending control after remove, got %d", len(st.PendingControls))
	}
	if _, ok := st.PendingControls["cr_02"]; !ok {
		t.Error("cr_02 should still be pending")
	}

	// Remove same key twice is idempotent.
	removePendingControl(&st, "cr_01")
	if len(st.PendingControls) != 1 {
		t.Errorf("double-remove changed count: got %d", len(st.PendingControls))
	}

	// Remove last.
	removePendingControl(&st, "cr_02")
	if len(st.PendingControls) != 0 {
		t.Errorf("expected empty map, got %d entries", len(st.PendingControls))
	}
}

// TestPendingControlsNilMapSafe verifies we don't panic on nil map.
func TestPendingControlsNilMapSafe(t *testing.T) {
	st := SessionState{} // PendingControls is nil

	// addPendingControl should initialise the map.
	addPendingControl(&st, "cr_01", json.RawMessage(`{}`))
	if st.PendingControls == nil {
		t.Error("expected map to be initialised")
	}
	if len(st.PendingControls) != 1 {
		t.Errorf("expected 1 entry, got %d", len(st.PendingControls))
	}
}

// TestSessionStateSerialization verifies round-trip JSON for NATS KV.
func TestSessionStateSerialization(t *testing.T) {
	st := SessionState{
		ID:        "abc-123",
		ProjectID: "proj-1",
		Branch:    "feature/auth",
		Worktree:  "feature-auth",
		State:     StateIdle,
		StateSince: time.Now().UTC().Truncate(time.Second),
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
		Model:     "claude-sonnet-4-6",
		Capabilities: Capabilities{
			Skills: []string{"commit", "review-pr"},
			Tools:  []string{"Bash", "Read"},
			Agents: []string{"general-purpose"},
		},
		PendingControls: make(map[string]any),
		Usage: UsageStats{
			InputTokens:  100,
			OutputTokens: 50,
			CostUSD:      0.001,
		},
		ReplayFromSeq: 42,
	}

	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SessionState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != st.ID {
		t.Errorf("ID: got %q, want %q", got.ID, st.ID)
	}
	if got.Branch != st.Branch {
		t.Errorf("Branch: got %q, want %q", got.Branch, st.Branch)
	}
	if got.ReplayFromSeq != st.ReplayFromSeq {
		t.Errorf("ReplayFromSeq: got %d, want %d", got.ReplayFromSeq, st.ReplayFromSeq)
	}
	if len(got.Capabilities.Skills) != 2 {
		t.Errorf("Skills: got %v", got.Capabilities.Skills)
	}
}

// TestUsageAccumulation verifies the accumulation of usage stats across turns.
func TestUsageAccumulation(t *testing.T) {
	st := &SessionState{}

	accumulateUsage(st, resultUsage{InputTokens: 100, OutputTokens: 50}, 0.001)
	accumulateUsage(st, resultUsage{InputTokens: 200, OutputTokens: 80}, 0.002)

	if st.Usage.InputTokens != 300 {
		t.Errorf("InputTokens: got %d, want 300", st.Usage.InputTokens)
	}
	if st.Usage.OutputTokens != 130 {
		t.Errorf("OutputTokens: got %d, want 130", st.Usage.OutputTokens)
	}
	if st.Usage.CostUSD < 0.0029 || st.Usage.CostUSD > 0.0031 {
		t.Errorf("CostUSD: got %f, want ~0.003", st.Usage.CostUSD)
	}
}
