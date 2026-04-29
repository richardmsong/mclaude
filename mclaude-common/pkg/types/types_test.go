package types

import (
	"encoding/json"
	"testing"
	"time"
)

// ptr returns a pointer to the given value (test helper).
func ptr[T any](v T) *T { return &v }

func TestSessionStateRoundTrip(t *testing.T) {
	orig := SessionState{
		ID:          "550e8400-e29b-41d4-a716-446655440000",
		Slug:        "my-session",
		UserSlug:    "alice-gmail",
		HostSlug:    "mbp16",
		ProjectSlug: "myrepo",
		ProjectID:   "660e8400-e29b-41d4-a716-446655440000",
		Branch:      "main",
		Worktree:    "/data/worktrees/main",
		CWD:         "/data/worktrees/main",
		Name:        "My Session",
		State:       "running",
		StateSince:  time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 4, 29, 9, 0, 0, 0, time.UTC),
		Model:       "claude-sonnet-4-20250514",
		ExtraFlags:  "--model claude-sonnet-4-20250514",
		Capabilities: Capabilities{
			Skills: []string{"review"},
			Tools:  []string{"Read", "Write"},
			Agents: []string{"subagent"},
		},
		PendingControls: map[string]any{"req-1": map[string]any{"tool": "Bash"}},
		Usage: UsageStats{
			InputTokens:      1000,
			OutputTokens:     500,
			CacheReadTokens:  200,
			CacheWriteTokens: 100,
			CostUSD:          0.05,
		},
		ReplayFromSeq: 42,
		JoinWorktree:  true,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal SessionState: %v", err)
	}

	var decoded SessionState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal SessionState: %v", err)
	}

	// Spot-check key fields.
	if decoded.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", decoded.ID, orig.ID)
	}
	if decoded.Slug != orig.Slug {
		t.Errorf("Slug: got %q, want %q", decoded.Slug, orig.Slug)
	}
	if decoded.State != orig.State {
		t.Errorf("State: got %q, want %q", decoded.State, orig.State)
	}
	if decoded.Usage.CostUSD != orig.Usage.CostUSD {
		t.Errorf("Usage.CostUSD: got %f, want %f", decoded.Usage.CostUSD, orig.Usage.CostUSD)
	}
	if decoded.ReplayFromSeq != orig.ReplayFromSeq {
		t.Errorf("ReplayFromSeq: got %d, want %d", decoded.ReplayFromSeq, orig.ReplayFromSeq)
	}
	if len(decoded.Capabilities.Tools) != 2 {
		t.Errorf("Capabilities.Tools: got %d items, want 2", len(decoded.Capabilities.Tools))
	}
}

func TestProjectStateRoundTrip(t *testing.T) {
	orig := ProjectState{
		ID:            "770e8400-e29b-41d4-a716-446655440000",
		Slug:          "myrepo",
		UserSlug:      "alice-gmail",
		HostSlug:      "mbp16",
		Name:          "My Repo",
		GitURL:        "https://github.com/alice/myrepo.git",
		Status:        "active",
		CreatedAt:     time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		GitIdentityID: "conn-123",
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal ProjectState: %v", err)
	}

	var decoded ProjectState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal ProjectState: %v", err)
	}

	if decoded.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", decoded.ID, orig.ID)
	}
	if decoded.GitIdentityID != orig.GitIdentityID {
		t.Errorf("GitIdentityID: got %q, want %q", decoded.GitIdentityID, orig.GitIdentityID)
	}
	if decoded.HostSlug != orig.HostSlug {
		t.Errorf("HostSlug: got %q, want %q", decoded.HostSlug, orig.HostSlug)
	}
}

func TestHostStateRoundTrip(t *testing.T) {
	orig := HostState{
		Slug:       "mbp16",
		Type:       "machine",
		Name:       "alice's MBP",
		Role:       "owner",
		Online:     true,
		LastSeenAt: time.Date(2026, 4, 29, 10, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal HostState: %v", err)
	}

	var decoded HostState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal HostState: %v", err)
	}

	if decoded.Slug != orig.Slug {
		t.Errorf("Slug: got %q, want %q", decoded.Slug, orig.Slug)
	}
	if decoded.Online != orig.Online {
		t.Errorf("Online: got %v, want %v", decoded.Online, orig.Online)
	}
	if decoded.Type != orig.Type {
		t.Errorf("Type: got %q, want %q", decoded.Type, orig.Type)
	}
}

func TestQuotaStatusRoundTrip(t *testing.T) {
	orig := QuotaStatus{
		HasData: true,
		U5:      42,
		R5:      time.Date(2026, 4, 29, 15, 0, 0, 0, time.UTC),
		U7:      15,
		R7:      time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC),
		TS:      time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal QuotaStatus: %v", err)
	}

	var decoded QuotaStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal QuotaStatus: %v", err)
	}

	if decoded.U5 != orig.U5 {
		t.Errorf("U5: got %d, want %d", decoded.U5, orig.U5)
	}
	if decoded.HasData != orig.HasData {
		t.Errorf("HasData: got %v, want %v", decoded.HasData, orig.HasData)
	}
}

func TestJobEntryRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	started := time.Date(2026, 4, 29, 10, 5, 0, 0, time.UTC)
	resumeAt := time.Date(2026, 4, 29, 15, 0, 0, 0, time.UTC)

	orig := JobEntry{
		ID:                 "880e8400-e29b-41d4-a716-446655440000",
		UserID:             "990e8400-e29b-41d4-a716-446655440000",
		UserSlug:           "alice-gmail",
		HostSlug:           "mbp16",
		ProjectID:          "aa0e8400-e29b-41d4-a716-446655440000",
		ProjectSlug:        "myrepo",
		SessionID:          "bb0e8400-e29b-41d4-a716-446655440000",
		SessionSlug:        "fix-bug",
		ClaudeSessionID:    "claude-sess-123",
		Prompt:             "Fix the login bug",
		Title:              "fix-login-bug",
		BranchSlug:         "fix-login-bug",
		ResumePrompt:       "Continue fixing the login bug",
		Priority:           5,
		SoftThreshold:      75,
		HardHeadroomTokens: 50000,
		AutoContinue:       true,
		PermPolicy:         "managed",
		AllowedTools:       []string{"Read", "Write"},
		Status:             "running",
		PausedVia:          "",
		Branch:             "schedule/fix-login-bug",
		RetryCount:         0,
		ResumeAt:           &resumeAt,
		CreatedAt:          now,
		StartedAt:          &started,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal JobEntry: %v", err)
	}

	var decoded JobEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal JobEntry: %v", err)
	}

	if decoded.ID != orig.ID {
		t.Errorf("ID: got %q, want %q", decoded.ID, orig.ID)
	}
	if decoded.ClaudeSessionID != orig.ClaudeSessionID {
		t.Errorf("ClaudeSessionID: got %q, want %q", decoded.ClaudeSessionID, orig.ClaudeSessionID)
	}
	if decoded.SoftThreshold != orig.SoftThreshold {
		t.Errorf("SoftThreshold: got %d, want %d", decoded.SoftThreshold, orig.SoftThreshold)
	}
	if decoded.ResumeAt == nil || !decoded.ResumeAt.Equal(resumeAt) {
		t.Errorf("ResumeAt: got %v, want %v", decoded.ResumeAt, resumeAt)
	}
	if len(decoded.AllowedTools) != 2 {
		t.Errorf("AllowedTools: got %d items, want 2", len(decoded.AllowedTools))
	}
}

func TestLifecycleEventRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		event LifecycleEvent
	}{
		{
			name: "session_created",
			event: LifecycleEvent{
				Type:      LifecycleSessionCreated,
				SessionID: "sess-1",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				Branch:    "main",
			},
		},
		{
			name: "session_stopped",
			event: LifecycleEvent{
				Type:      LifecycleSessionStopped,
				SessionID: "sess-2",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				ExitCode:  ptr(0),
			},
		},
		{
			name: "session_failed",
			event: LifecycleEvent{
				Type:      LifecycleSessionFailed,
				SessionID: "sess-3",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				Error:     "process crashed",
			},
		},
		{
			name: "session_permission_denied",
			event: LifecycleEvent{
				Type:      LifecycleSessionPermissionDenied,
				SessionID: "sess-4",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				Tool:      "Bash",
				JobID:     "job-1",
			},
		},
		{
			name: "session_job_complete",
			event: LifecycleEvent{
				Type:      LifecycleSessionJobComplete,
				SessionID: "sess-5",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				JobID:     "job-2",
				Branch:    "feature/login",
				PRUrl:     "https://github.com/org/repo/pull/42",
			},
		},
		{
			name: "session_job_paused",
			event: LifecycleEvent{
				Type:                      LifecycleSessionJobPaused,
				SessionID:                 "sess-6",
				TS:                        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				JobID:                     "job-3",
				PausedVia:                 "quota_soft",
				U5:                        ptr(76),
				R5:                        "2026-04-29T15:00:00Z",
				OutputTokensSinceSoftMark: ptr(12345),
			},
		},
		{
			name: "session_job_cancelled",
			event: LifecycleEvent{
				Type:      LifecycleSessionJobCancelled,
				SessionID: "sess-7",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				JobID:     "job-4",
			},
		},
		{
			name: "session_job_failed",
			event: LifecycleEvent{
				Type:      LifecycleSessionJobFailed,
				SessionID: "sess-8",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
				JobID:     "job-5",
				Error:     "timeout",
			},
		},
		{
			name: "session_restarting",
			event: LifecycleEvent{
				Type:      LifecycleSessionRestarting,
				SessionID: "sess-9",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "session_resumed",
			event: LifecycleEvent{
				Type:      LifecycleSessionResumed,
				SessionID: "sess-10",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "session_upgrading",
			event: LifecycleEvent{
				Type:      LifecycleSessionUpgrading,
				SessionID: "sess-11",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "session_quota_interrupted",
			event: LifecycleEvent{
				Type:      LifecycleSessionQuotaInterrupted,
				SessionID: "sess-12",
				TS:        time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var decoded LifecycleEvent
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if decoded.Type != tt.event.Type {
				t.Errorf("Type: got %q, want %q", decoded.Type, tt.event.Type)
			}
			if decoded.SessionID != tt.event.SessionID {
				t.Errorf("SessionID: got %q, want %q", decoded.SessionID, tt.event.SessionID)
			}
			if !decoded.TS.Equal(tt.event.TS) {
				t.Errorf("TS: got %v, want %v", decoded.TS, tt.event.TS)
			}
		})
	}
}

func TestLifecycleEventTypeConstants(t *testing.T) {
	// Verify all 12 lifecycle event types have the expected string values.
	expected := map[LifecycleEventType]string{
		LifecycleSessionCreated:          "session_created",
		LifecycleSessionStopped:          "session_stopped",
		LifecycleSessionRestarting:       "session_restarting",
		LifecycleSessionResumed:          "session_resumed",
		LifecycleSessionFailed:           "session_failed",
		LifecycleSessionUpgrading:        "session_upgrading",
		LifecycleSessionJobPaused:        "session_job_paused",
		LifecycleSessionJobComplete:      "session_job_complete",
		LifecycleSessionJobCancelled:     "session_job_cancelled",
		LifecycleSessionJobFailed:        "session_job_failed",
		LifecycleSessionPermissionDenied: "session_permission_denied",
		LifecycleSessionQuotaInterrupted: "session_quota_interrupted",
	}

	for constant, want := range expected {
		if string(constant) != want {
			t.Errorf("constant %q: got %q, want %q", want, string(constant), want)
		}
	}
}
