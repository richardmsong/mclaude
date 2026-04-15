package main

import (
	"encoding/json"
	"fmt"
	"time"
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
// mclaude-sessions NATS KV bucket.
type SessionState struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"projectId"`
	Branch      string            `json:"branch"`
	Worktree    string            `json:"worktree"`
	CWD         string            `json:"cwd"`
	Name        string            `json:"name"`
	State       string            `json:"state"`
	StateSince  time.Time         `json:"stateSince"`
	CreatedAt   time.Time         `json:"createdAt"`
	Model       string            `json:"model"`
	Capabilities Capabilities     `json:"capabilities"`
	PendingControls map[string]any `json:"pendingControls"`
	Usage       UsageStats        `json:"usage"`
	ReplayFromSeq uint64          `json:"replayFromSeq,omitempty"`
	JoinWorktree bool             `json:"joinWorktree"`
}

// Capabilities are populated from the init event and refreshed on reload_plugins.
type Capabilities struct {
	Skills []string `json:"skills"`
	Tools  []string `json:"tools"`
	Agents []string `json:"agents"`
}

// UsageStats accumulates token usage across all turns in a session.
type UsageStats struct {
	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CacheReadTokens  int     `json:"cacheReadTokens"`
	CacheWriteTokens int     `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd"`
}

// sessionKVKey returns the NATS KV key for a session.
// Format: {userId}.{projectId}.{sessionId} — dots are the NATS token separator,
// required for wildcard matching (">" and "*" only work across dot-separated tokens).
func sessionKVKey(userID, projectID, sessionID string) string {
	return fmt.Sprintf("%s.%s.%s", userID, projectID, sessionID)
}

// heartbeatKVKey returns the NATS KV key for a project heartbeat.
// Format: {userId}.{projectId}
func heartbeatKVKey(userID, projectID string) string {
	return fmt.Sprintf("%s.%s", userID, projectID)
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
	st.State = StateIdle
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
// Key: {userId}/{jobId}
type JobEntry struct {
	ID           string     `json:"id"`
	UserID       string     `json:"userId"`
	ProjectID    string     `json:"projectId"`
	SpecPath     string     `json:"specPath"`
	Priority     int        `json:"priority"`
	Threshold    int        `json:"threshold"`
	AutoContinue bool       `json:"autoContinue"`
	Status       string     `json:"status"`
	SessionID    string     `json:"sessionId"`
	Branch       string     `json:"branch"`
	PRUrl        string     `json:"prUrl"`
	FailedTool   string     `json:"failedTool"`
	Error        string     `json:"error"`
	RetryCount   int        `json:"retryCount"`
	ResumeAt     *time.Time `json:"resumeAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	StartedAt    *time.Time `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
}
