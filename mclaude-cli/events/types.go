// Package events contains stream-json event types and parsing logic for
// events emitted by the session agent over the unix socket.
package events

import "encoding/json"

// Event is a parsed stream-json event.  Fields are optional depending on
// the event type — callers should check Type/Subtype before reading them.
type Event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// system: init
	Skills []string `json:"skills,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	Model  string   `json:"model,omitempty"`

	// system: session_state_changed
	State string `json:"state,omitempty"`

	// system: compact_boundary
	Summary string `json:"summary,omitempty"`

	// stream_event
	StreamEvt *StreamEventData `json:"event,omitempty"`

	// assistant
	Content         []ContentBlock `json:"content,omitempty"`
	Usage           *Usage         `json:"usage,omitempty"`
	ParentToolUseID string         `json:"parent_tool_use_id,omitempty"`

	// user
	Message *UserMessage `json:"message,omitempty"`

	// control_request
	RequestID string          `json:"request_id,omitempty"`
	Request   *ControlRequest `json:"request,omitempty"`

	// tool_progress
	ToolUseID   string  `json:"tool_use_id,omitempty"`
	ToolName    string  `json:"tool_name,omitempty"`
	ElapsedTime float64 `json:"elapsed_time_seconds,omitempty"`

	// result
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// StreamEventData is the inner event within a stream_event.
type StreamEventData struct {
	Type  string        `json:"type"`
	Delta *DeltaContent `json:"delta,omitempty"`
}

// DeltaContent holds a streaming text delta.
type DeltaContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ContentBlock is one block within an assistant or user message.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Usage is token usage from an assistant message or result event.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_input_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// UserMessage wraps a user turn.
type UserMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ControlRequest is a can_use_tool permission request.
type ControlRequest struct {
	Subtype   string          `json:"subtype"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
}
