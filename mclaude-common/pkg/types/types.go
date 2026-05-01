// Package types provides shared Go struct types for NATS KV bucket payloads
// and lifecycle event constants used across all mclaude components.
//
// These types define the canonical wire format for data stored in the
// mclaude-sessions-{uslug}, mclaude-projects-{uslug}, and mclaude-hosts
// NATS KV buckets, as well as lifecycle events published on
// mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.*
// subjects (ADR-0054 consolidated sessions.> hierarchy).
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
// on mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.{eventType}
// (ADR-0054 consolidated sessions.> hierarchy).
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
// mclaude-sessions-{uslug} KV value
// ---------------------------------------------------------------------------

// SessionState is the materialised view of a session stored in the
// mclaude-sessions-{uslug} NATS KV bucket (per-user per ADR-0054).
// Key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug}
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
	ReplayFromSeq   uint64            `json:"replayFromSeq,omitempty"`
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
// mclaude-projects-{uslug} KV value
// ---------------------------------------------------------------------------

// ProjectState is the materialised view of a project stored in the
// mclaude-projects-{uslug} NATS KV bucket (per-user per ADR-0054).
// Key format: hosts.{hslug}.projects.{pslug}
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
	// ImportRef is the import ID (e.g. "imp-001") set by the control-plane
	// when a project is created via `mclaude import`. Cleared by the
	// session-agent after unpacking the import archive. Null when not an
	// imported project. See ADR-0053.
	ImportRef string `json:"importRef,omitempty"`
}

// ---------------------------------------------------------------------------
// mclaude-hosts KV value
// ---------------------------------------------------------------------------

// HostState is the materialised view of a host stored in the mclaude-hosts
// NATS KV bucket (shared bucket, per-host read scoping in user JWT per ADR-0054).
// Key format: {hslug} (flat, globally unique per ADR-0054)
type HostState struct {
	Slug       string    `json:"slug"`
	Type       string    `json:"type"`
	Name       string    `json:"name"`
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
// Import and attachment types (ADR-0053)
// Binary data (imports, attachments) flows through S3 with pre-signed URLs.
// No NATS Object Store is used for binary data.
// ---------------------------------------------------------------------------

// ImportRequest is the payload for the import.request NATS request/reply
// (CLI → CP). CP responds with a pre-signed S3 URL for the import archive.
type ImportRequest struct {
	// Slug is the candidate project slug for the imported project.
	Slug string `json:"slug"`
	// SizeBytes is the size of the import archive in bytes.
	SizeBytes int64 `json:"sizeBytes"`
}

// ImportMetadata is the contents of metadata.json inside an import archive.
// Describes the provenance of the imported workspace.
type ImportMetadata struct {
	// CWD is the working directory of the imported workspace.
	CWD string `json:"cwd"`
	// GitRemote is the git remote URL of the imported repository (if any).
	GitRemote string `json:"gitRemote,omitempty"`
	// GitBranch is the git branch at the time of import (if any).
	GitBranch string `json:"gitBranch,omitempty"`
	// ImportedAt is the timestamp when the import was created.
	ImportedAt time.Time `json:"importedAt"`
	// SessionIds is the list of Claude Code session IDs in the archive.
	SessionIds []string `json:"sessionIds,omitempty"`
	// ClaudeCodeVersion is the version of Claude Code that created the archive.
	ClaudeCodeVersion string `json:"claudeCodeVersion,omitempty"`
}

// AttachmentRef is a lightweight reference to an attachment carried in NATS
// messages. The S3 key is not included — agents resolve it via the
// attachments.download request/reply flow.
type AttachmentRef struct {
	// ID is the opaque attachment ID (e.g. "att-001").
	ID string `json:"id"`
	// Filename is the original filename (preserved for display/download).
	Filename string `json:"filename"`
	// MimeType is the MIME type (e.g. "image/png").
	MimeType string `json:"mimeType"`
	// SizeBytes is the file size in bytes.
	SizeBytes int64 `json:"sizeBytes"`
}

// AttachmentMeta is the full attachment metadata stored in Postgres (the
// attachments table) and returned by the download handler. Internal to the
// control-plane — never sent directly over NATS. Agents use AttachmentRef
// in NATS messages and resolve the full metadata via request/reply.
type AttachmentMeta struct {
	// ID is the opaque attachment ID (e.g. "att-001").
	ID string `json:"id"`
	// S3Key is the full S3 object key (e.g. "alice/laptop-a/myapp/attachments/att-001").
	S3Key string `json:"s3Key"`
	// Filename is the original filename.
	Filename string `json:"filename"`
	// MimeType is the MIME type.
	MimeType string `json:"mimeType"`
	// SizeBytes is the file size in bytes.
	SizeBytes int64 `json:"sizeBytes"`
	// UserSlug is the owning user's slug.
	UserSlug string `json:"userSlug"`
	// HostSlug is the host the project runs on.
	HostSlug string `json:"hostSlug"`
	// ProjectSlug is the project the attachment belongs to.
	ProjectSlug string `json:"projectSlug"`
	// CreatedAt is when the attachment was created.
	CreatedAt time.Time `json:"createdAt"`
}
