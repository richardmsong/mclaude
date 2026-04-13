package main

import (
	"encoding/json"
	"fmt"
	"time"
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
