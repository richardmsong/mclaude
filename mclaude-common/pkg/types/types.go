// Package types provides shared Go struct types for NATS KV bucket payloads
// and lifecycle event constants used across all mclaude components.
//
// These types define the canonical wire format for data stored in the
// mclaude-sessions, mclaude-projects, mclaude-hosts, and mclaude-job-queue
// NATS KV buckets, as well as lifecycle events published on
// mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}.
//
// See docs/spec-state-schema.md for the full schema reference.
package types

import "time"

// ---------------------------------------------------------------------------
// Lifecycle event type constants
// ---------------------------------------------------------------------------

// LifecycleEventType is the string value of the "type" field in lifecycle
// event JSON payloads.
type LifecycleEventType string

const (
	LifecycleSessionCreated          LifecycleEventType = "session_created"
	LifecycleSessionStopped          LifecycleEventType = "session_stopped"
	LifecycleSessionRestarting       LifecycleEventType = "session_restarting"
	LifecycleSessionResumed          LifecycleEventType = "session_resumed"
	LifecycleSessionFailed           LifecycleEventType = "session_failed"
	LifecycleSessionUpgrading        LifecycleEventType = "session_upgrading"
	LifecycleSessionJobPaused        LifecycleEventType = "session_job_paused"
	LifecycleSessionJobComplete      LifecycleEventType = "session_job_complete"
	LifecycleSessionJobCancelled     LifecycleEventType = "session_job_cancelled"
	LifecycleSessionJobFailed        LifecycleEventType = "session_job_failed"
	LifecycleSessionPermissionDenied LifecycleEventType = "session_permission_denied"
	// LifecycleSessionQuotaInterrupted is the legacy event name emitted by
	// the current QuotaMonitor code instead of session_job_paused.
	// Retained for backward-compatibility until the ADR-0034 migration.
	LifecycleSessionQuotaInterrupted LifecycleEventType = "session_quota_interrupted"
)

// ---------------------------------------------------------------------------
// Lifecycle event envelope
// ---------------------------------------------------------------------------

// LifecycleEvent is the envelope for all lifecycle event payloads published
// on mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}.
//
// Common fields (Type, SessionID, TS) are always present. Per-event optional
// fields are populated only for the event types that carry them.
type LifecycleEvent struct {
	Type      LifecycleEventType `json:"type"`
	SessionID string             `json:"sessionId"`
	TS        time.Time          `json:"ts"`

	// session_created
	Branch string `json:"branch,omitempty"`

	// session_stopped
	ExitCode *int `json:"exitCode,omitempty"`

	// session_failed, session_job_failed
	Error string `json:"error,omitempty"`

	// session_permission_denied
	Tool string `json:"tool,omitempty"`

	// session_permission_denied, session_job_complete, session_job_cancelled,
	// session_job_failed, session_job_paused
	JobID string `json:"jobId,omitempty"`

	// session_job_complete
	PRUrl string `json:"prUrl,omitempty"`

	// session_job_paused
	PausedVia                  string `json:"pausedVia,omitempty"`
	U5                         *int   `json:"u5,omitempty"`
	R5                         string `json:"r5,omitempty"`
	OutputTokensSinceSoftMark  *int   `json:"outputTokensSinceSoftMark,omitempty"`
}

// ---------------------------------------------------------------------------
// mclaude-sessions KV value
// ---------------------------------------------------------------------------

// SessionState is the materialised view of a session stored in the
// mclaude-sessions NATS KV bucket.
// Key format: {uslug}.{hslug}.{pslug}.{sslug}
type SessionState struct {
	ID              string            `json:"id"`
	Slug            string            `json:"slug,omitempty"`
	UserSlug        string            `json:"userSlug,omitempty"`
	HostSlug        string            `json:"hostSlug,omitempty"`
	ProjectSlug     string            `json:"projectSlug,omitempty"`
	ProjectID       string            `json:"projectId"`
	Branch          string            `json:"branch"`
	Worktree        string            `json:"worktree"`
	CWD             string            `json:"cwd"`
	Name            string            `json:"name"`
	State           string            `json:"state"`
	StateSince      time.Time         `json:"stateSince"`
	CreatedAt       time.Time         `json:"createdAt"`
	Model           string            `json:"model"`
	ExtraFlags      string            `json:"extraFlags,omitempty"`
	Capabilities    Capabilities      `json:"capabilities"`
	PendingControls map[string]any    `json:"pendingControls"`
	Usage           UsageStats        `json:"usage"`
	ReplayFromSeq   uint64           `json:"replayFromSeq,omitempty"`
	JoinWorktree    bool              `json:"joinWorktree"`
}

// Capabilities lists the skills, tools, and agents available in a session.
// Populated from the Claude Code init event and refreshed on reload_plugins.
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

// ---------------------------------------------------------------------------
// mclaude-projects KV value
// ---------------------------------------------------------------------------

// ProjectState is the materialised view of a project stored in the
// mclaude-projects NATS KV bucket.
// Key format: {userId}.{projectId} (UUID-based; migration to slug-based deferred per ADR-0050)
type ProjectState struct {
	ID            string    `json:"id"`
	Slug          string    `json:"slug"`
	UserSlug      string    `json:"userSlug"`
	HostSlug      string    `json:"hostSlug"`
	Name          string    `json:"name"`
	GitURL        string    `json:"gitUrl"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	GitIdentityID string    `json:"gitIdentityId,omitempty"`
}

// ---------------------------------------------------------------------------
// mclaude-hosts KV value
// ---------------------------------------------------------------------------

// HostState is the materialised view of a host stored in the mclaude-hosts
// NATS KV bucket.
// Key format: {uslug}.{hslug}
type HostState struct {
	Slug       string    `json:"slug"`
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	Online     bool      `json:"online"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// ---------------------------------------------------------------------------
// Quota status (core NATS pub/sub payload)
// ---------------------------------------------------------------------------

// QuotaStatus holds the API quota utilisation data published to
// mclaude.users.{uslug}.quota (core NATS, not JetStream).
type QuotaStatus struct {
	HasData bool      `json:"hasData"`
	U5      int       `json:"u5"`
	R5      time.Time `json:"r5"`
	U7      int       `json:"u7"`
	R7      time.Time `json:"r7"`
	TS      time.Time `json:"ts"`
}

// ---------------------------------------------------------------------------
// mclaude-job-queue KV value (ADR-0034 target schema)
// ---------------------------------------------------------------------------

// JobEntry is the value stored in the mclaude-job-queue KV bucket.
// Key format: {uslug}.{jobId}
//
// This struct reflects the ADR-0034 target schema. The current session-agent
// code still carries some ADR-0009 fields (specPath, threshold, prUrl) and
// lacks several ADR-0034 additions. See spec-state-schema.md for details.
type JobEntry struct {
	ID               string     `json:"id"`
	UserID           string     `json:"userId"`
	UserSlug         string     `json:"userSlug,omitempty"`
	HostSlug         string     `json:"hostSlug,omitempty"`
	ProjectID        string     `json:"projectId"`
	ProjectSlug      string     `json:"projectSlug,omitempty"`
	SessionID        string     `json:"sessionId"`
	SessionSlug      string     `json:"sessionSlug,omitempty"`
	ClaudeSessionID  string     `json:"claudeSessionID,omitempty"`
	Prompt           string     `json:"prompt,omitempty"`
	Title            string     `json:"title,omitempty"`
	BranchSlug       string     `json:"branchSlug,omitempty"`
	ResumePrompt     string     `json:"resumePrompt,omitempty"`
	Priority         int        `json:"priority"`
	SoftThreshold    int        `json:"softThreshold,omitempty"`
	HardHeadroomTokens int     `json:"hardHeadroomTokens,omitempty"`
	AutoContinue     bool       `json:"autoContinue"`
	PermPolicy       string     `json:"permPolicy,omitempty"`
	AllowedTools     []string   `json:"allowedTools,omitempty"`
	Status           string     `json:"status"`
	PausedVia        string     `json:"pausedVia,omitempty"`
	Branch           string     `json:"branch"`
	FailedTool       string     `json:"failedTool,omitempty"`
	Error            string     `json:"error,omitempty"`
	RetryCount       int        `json:"retryCount"`
	ResumeAt         *time.Time `json:"resumeAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
}
