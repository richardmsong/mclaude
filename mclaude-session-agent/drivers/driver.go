// Package drivers provides the CLIDriver interface and supporting types for
// the pluggable CLI backend architecture (ADR-0005). Each CLI backend
// (Claude Code, Factory Droid, Devin ACP, etc.) implements CLIDriver to own
// its native protocol while emitting canonical driver events.
package drivers

import (
	"context"
	"io"
	"os/exec"
)

// CLIBackend identifies a CLI backend type.
type CLIBackend string

const (
	BackendClaudeCode      CLIBackend = "claude_code"
	BackendDroid           CLIBackend = "droid"
	BackendDevinACP        CLIBackend = "devin_acp"
	BackendGemini          CLIBackend = "gemini"
	BackendGenericTerminal CLIBackend = "generic_terminal"
)

// CLICapabilities contains boolean feature flags for the backend.
// These are published in the init event and stored in session KV so the SPA
// can toggle per-backend UI features without requiring event replay.
type CLICapabilities struct {
	HasThinking      bool              `json:"hasThinking"`
	HasSubagents     bool              `json:"hasSubagents"`
	HasSkills        bool              `json:"hasSkills"`
	HasPlanMode      bool              `json:"hasPlanMode"`
	HasMissions      bool              `json:"hasMissions"`
	HasEventStream   bool              `json:"hasEventStream"`
	HasSessionResume bool              `json:"hasSessionResume"`
	ThinkingLabel    string            `json:"thinkingLabel,omitempty"`
	ModelOptions     []string          `json:"modelOptions,omitempty"`
	PermissionModes  []string          `json:"permissionModes,omitempty"`
	ToolIcons        map[string]string `json:"toolIcons,omitempty"`
}

// Process represents a running CLI backend process.
type Process struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	PID    int
}

// LaunchOptions holds parameters for spawning or resuming a CLI process.
type LaunchOptions struct {
	// SessionID is the unique identifier for the Claude Code session.
	// Passed to the CLI via --session-id flag (new) or --resume (resume).
	SessionID string
	// CWD is the working directory for the CLI process.
	CWD string
	// Model is the model to use (optional; driver-specific handling).
	Model string
	// SystemPrompt is an optional system prompt override.
	SystemPrompt string
	// PermissionMode controls how the CLI handles tool permission prompts.
	// Claude Code: "auto" | "managed" | "allowlist"
	PermissionMode string
	// ExtraFlags is a raw string of additional CLI flags (shell-parsed before use).
	ExtraFlags string
	// ExtraEnv is a list of additional environment variables (KEY=VALUE format).
	ExtraEnv []string
}

// UserMessage represents a user input message to send to the CLI backend.
type UserMessage struct {
	// Text is the message content.
	Text string
	// Attachments are resolved binary attachments (downloaded from S3 by the agent).
	Attachments []ResolvedAttachment
}

// ResolvedAttachment holds binary data for an attachment already downloaded from S3.
type ResolvedAttachment struct {
	Filename string
	MimeType string
	Data     []byte
}

// SessionConfig holds mutable session configuration that can be updated at runtime.
type SessionConfig struct {
	Model          *string `json:"model,omitempty"`
	PermissionMode *string `json:"permissionMode,omitempty"`
	SystemPrompt   *string `json:"systemPrompt,omitempty"`
}

// DriverEvent is emitted by a CLIDriver.ReadEvents() goroutine for each event
// from the CLI backend. It carries the native-protocol raw bytes (for NATS
// publish and backward-compatible side-effect processing) plus the native
// event type string (for routing in the session event loop).
//
// The raw bytes are the driver's native-format event (e.g., stream-json NDJSON
// for ClaudeCodeDriver). They are published as-is to NATS for backward
// compatibility with the SPA. A future migration phase will switch NATS
// publish to a canonical JSON format per ADR-0005 Phase 6.
type DriverEvent struct {
	// NativeType is the driver-native event type string.
	// For ClaudeCodeDriver, this is the stream-json "type" field value (e.g., "assistant", "result").
	// Used by the session agent's handleSideEffect and onRawOutput callbacks.
	NativeType string
	// Raw is the original bytes emitted by the driver for this event.
	// Published to NATS as the session event payload. For debug broadcast.
	Raw []byte
}

// CLIDriver owns the full lifecycle of a CLI backend process. It speaks the
// backend's native protocol and emits DriverEvents for the session agent's
// event loop.
type CLIDriver interface {
	// Backend returns the backend type identifier.
	Backend() CLIBackend

	// DisplayName returns the human-readable name of the backend.
	DisplayName() string

	// Capabilities returns the feature flags for this backend.
	Capabilities() CLICapabilities

	// Launch spawns a new CLI process and returns its handle.
	// The caller must subsequently call ReadEvents to drain stdout.
	Launch(ctx context.Context, opts LaunchOptions) (*Process, error)

	// Resume spawns a CLI process in resume mode (e.g., --resume <sessionID>).
	Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error)

	// SendMessage writes a user message to the process stdin in the backend's
	// native format.
	SendMessage(proc *Process, msg UserMessage) error

	// SendPermissionResponse writes a permission response to the process stdin.
	// allow=true approves the pending permission request, allow=false denies it.
	SendPermissionResponse(proc *Process, requestID string, allow bool) error

	// UpdateConfig sends a runtime configuration update to the backend.
	UpdateConfig(proc *Process, cfg SessionConfig) error

	// Interrupt sends a graceful interrupt signal to the running process.
	// For Claude Code, this writes a control_request interrupt to stdin.
	Interrupt(proc *Process) error

	// ReadEvents blocks, reading stdout from the process and emitting DriverEvents
	// on the out channel until the process exits. Returns nil on clean exit.
	// The caller closes the channel after ReadEvents returns.
	ReadEvents(proc *Process, out chan<- DriverEvent) error
}
