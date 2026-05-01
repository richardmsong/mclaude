package main

import (
	"encoding/json"
	"time"

	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"

	"mclaude-session-agent/internal/drivers"
)

// PermissionPolicy controls how the session agent responds to control_request
// events (tool-use permission prompts).
//
//   - PermissionPolicyManaged         — forward to NATS client (default)
//   - PermissionPolicyAuto            — auto-approve all tools without forwarding
//   - PermissionPolicyAllowlist       — auto-approve listed tools; forward the rest
//   - PermissionPolicyStrictAllowlist — auto-approve listed tools; auto-deny everything else
type PermissionPolicy string

const (
	PermissionPolicyManaged         PermissionPolicy = "managed"
	PermissionPolicyAuto            PermissionPolicy = "auto"
	PermissionPolicyAllowlist       PermissionPolicy = "allowlist"
	PermissionPolicyStrictAllowlist PermissionPolicy = "strict-allowlist"
)

// SessionState is the materialised view of a session stored in the
// mclaude-sessions-{uslug} NATS KV bucket (per-user bucket per ADR-0054).
//
// The `status` JSON field (Go: State) was renamed from `state` per ADR-0044
// to reflect the coarser lifecycle state tracked at the KV level. The full
// status enum includes quota-managed states: pending, running, paused,
// requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error.
type SessionState struct {
	ID          string            `json:"id"`
	Slug        string            `json:"slug,omitempty"`        // session slug (sslug) per ADR-0024
	UserSlug    string            `json:"userSlug,omitempty"`    // user slug (uslug) per ADR-0024
	HostSlug    string            `json:"hostSlug,omitempty"`    // host slug (hslug) per ADR-0035
	ProjectSlug string            `json:"projectSlug,omitempty"` // project slug (pslug) per ADR-0024
	ProjectID   string            `json:"projectId"`
	Branch      string            `json:"branch"`
	Worktree    string            `json:"worktree"`
	CWD         string            `json:"cwd"`
	Name        string            `json:"name"`
	// State is the session lifecycle status, stored as `status` in KV JSON
	// (renamed from `state` per ADR-0044). Full enum: pending, running, paused,
	// requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error.
	State       string            `json:"status"`
	StateSince  time.Time         `json:"stateSince"`
	CreatedAt   time.Time         `json:"createdAt"`
	// Backend is the CLI backend type (e.g. "claude_code", "droid"). Set on session create.
	Backend      string           `json:"backend,omitempty"`
	Model        string                 `json:"model"`
	// Capabilities holds the CLICapabilities boolean feature flags for the backend (ADR-0005).
	// Populated from the driver's Capabilities() on init event.
	// The SPA reads these to determine backend features (hasThinking, hasEventStream, etc.)
	// without requiring event replay. JSON key is "capabilities" per spec-state-schema.md.
	Capabilities drivers.CLICapabilities `json:"capabilities"`
	// Tools, Skills, Agents are top-level arrays populated from the init event.
	// Promoted from the old nested capabilities.{tools,skills,agents} struct per spec-state-schema.md.
	Tools  []string `json:"tools,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Agents []string `json:"agents,omitempty"`
	PendingControls map[string]any `json:"pendingControls"`
	Usage        UsageStats       `json:"usage"`
	ReplayFromSeq uint64          `json:"replayFromSeq,omitempty"`
	JoinWorktree bool             `json:"joinWorktree"`
	ExtraFlags   string           `json:"extraFlags,omitempty"`

	// Quota-managed session fields (ADR-0044). Zero values are omitted so
	// interactive sessions remain backward-compatible.
	SoftThreshold         int        `json:"softThreshold,omitempty"`
	HardHeadroomTokens    int        `json:"hardHeadroomTokens,omitempty"`
	AutoContinue          bool       `json:"autoContinue,omitempty"`
	BranchSlug            string     `json:"branchSlug,omitempty"`
	PausedVia             string     `json:"pausedVia,omitempty"`
	ClaudeSessionID       string     `json:"claudeSessionId,omitempty"`
	FailedTool            string     `json:"failedTool,omitempty"`
	ResumeAt              *time.Time `json:"resumeAt,omitempty"`
}

// UsageStats accumulates token usage across all turns in a session.
type UsageStats struct {
	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CacheReadTokens  int     `json:"cacheReadTokens"`
	CacheWriteTokens int     `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd"`
}

// sessionKVKey returns the NATS KV key for a session per ADR-0054.
// Format: hosts.{hslug}.projects.{pslug}.sessions.{sslug}
// The user slug is NOT part of the key — it is encoded in the per-user bucket name.
func sessionKVKey(hostSlug slug.HostSlug, projectSlug slug.ProjectSlug, sessionSlug slug.SessionSlug) string {
	return subj.SessionsKVKey(hostSlug, projectSlug, sessionSlug)
}

// addPendingControl adds a control request to the session's pending map.
func addPendingControl(st *SessionState, requestID string, payload json.RawMessage) {
	if st.PendingControls == nil {
		st.PendingControls = make(map[string]any)
	}
	st.PendingControls[requestID] = payload
}

// removePendingControl removes a control request from the pending map.
func removePendingControl(st *SessionState, requestID string) {
	delete(st.PendingControls, requestID)
}

// clearPendingControlsForResume resets transient state that becomes stale
// after a crash or ungraceful shutdown.  Called before restarting with --resume.
func clearPendingControlsForResume(st *SessionState) {
	st.PendingControls = make(map[string]any)
	st.State = StateRestarting
}

// accumulateUsage adds token counts and cost from a result event to the session.
func accumulateUsage(st *SessionState, usage resultUsage, costUSD float64) {
	st.Usage.InputTokens += usage.InputTokens
	st.Usage.OutputTokens += usage.OutputTokens
	st.Usage.CacheReadTokens += usage.CacheReadInputTokens
	st.Usage.CacheWriteTokens += usage.CacheCreationInputTokens
	st.Usage.CostUSD += costUSD
}

// ProjectState is the materialised view of a project in mclaude-projects KV.
type ProjectState struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	GitURL       string    `json:"gitUrl"`
	Status       string    `json:"status"`
	SessionCount int       `json:"sessionCount"`
	Worktrees    []string  `json:"worktrees"`
	CreatedAt    time.Time `json:"createdAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`
}

// QuotaStatus holds the API quota utilisation data published to mclaude.{userId}.quota.
type QuotaStatus struct {
	HasData bool      `json:"hasData"`
	U5      int       `json:"u5"` // 5h utilization %
	R5      time.Time `json:"r5"` // 5h reset time
	U7      int       `json:"u7"` // 7-day utilization %
	R7      time.Time `json:"r7"` // 7-day reset time
	TS      time.Time `json:"ts"`  // timestamp of this fetch
}

// QuotaMonitorConfig is sent in sessions.create to activate quota monitoring
// for a scheduled job session.
type QuotaMonitorConfig struct {
	Threshold    int    `json:"threshold"`    // % 5h utilization; 0 = disabled
	Priority     int    `json:"priority"`     // 1-10; affects preemption order in dispatcher
	JobID        string `json:"jobId"`        // KV key suffix for the job entry ({userId}/{jobId})
	AutoContinue bool   `json:"autoContinue"` // if true, dispatcher re-queues at 5h reset time
}

// JobEntry is the value stored in the mclaude-job-queue KV bucket.
// Key: {uslug}.{jobId} per ADR-0024.
// ADR-0034 target schema adds: Prompt, Title, BranchSlug, ResumePrompt,
// SoftThreshold, HardHeadroomTokens, PermPolicy, AllowedTools, ClaudeSessionID, PausedVia.
type JobEntry struct {
	ID                 string     `json:"id"`
	UserID             string     `json:"userId"`
	UserSlug           string     `json:"userSlug,omitempty"`           // user slug (uslug) per ADR-0024
	HostSlug           string     `json:"hostSlug,omitempty"`           // host slug (hslug) per ADR-0035
	ProjectID          string     `json:"projectId"`
	ProjectSlug        string     `json:"projectSlug,omitempty"`        // project slug (pslug) per ADR-0024
	SessionID          string     `json:"sessionId"`
	SessionSlug        string     `json:"sessionSlug,omitempty"`        // session slug (sslug) per ADR-0024
	SpecPath           string     `json:"specPath"`
	Priority           int        `json:"priority"`
	Threshold          int        `json:"threshold"`
	AutoContinue       bool       `json:"autoContinue"`
	Status             string     `json:"status"`
	Branch             string     `json:"branch"`
	PRUrl              string     `json:"prUrl"`
	FailedTool         string     `json:"failedTool"`
	Error              string     `json:"error"`
	RetryCount         int        `json:"retryCount"`
	ResumeAt           *time.Time `json:"resumeAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	StartedAt          *time.Time `json:"startedAt"`
	CompletedAt        *time.Time `json:"completedAt"`
	// ADR-0034 fields:
	Prompt             string   `json:"prompt,omitempty"`
	Title              string   `json:"title,omitempty"`
	BranchSlug         string   `json:"branchSlug,omitempty"`
	ResumePrompt       string   `json:"resumePrompt,omitempty"`
	SoftThreshold      int      `json:"softThreshold,omitempty"`
	HardHeadroomTokens int      `json:"hardHeadroomTokens,omitempty"`
	PermPolicy         string   `json:"permPolicy,omitempty"`
	AllowedTools       []string `json:"allowedTools,omitempty"`
	ClaudeSessionID    string   `json:"claudeSessionId,omitempty"`
	PausedVia          string   `json:"pausedVia,omitempty"`
}
