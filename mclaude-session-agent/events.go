package main

import "encoding/json"

// Event types emitted by Claude Code on stdout (stream-json protocol).
const (
	EventTypeSystem         = "system"
	EventTypeAssistant      = "assistant"
	EventTypeUser           = "user"
	EventTypeStreamEvent    = "stream_event"
	EventTypeControlRequest = "control_request"
	EventTypeToolProgress   = "tool_progress"
	EventTypeResult         = "result"
	EventTypeCompactBoundary = "compact_boundary"
)

// System event subtypes.
const (
	SubtypeInit               = "init"
	SubtypeSessionStateChanged = "session_state_changed"
	SubtypeCompactBoundary    = "compact_boundary"
)

// Session states from session_state_changed events.
const (
	StateIdle            = "idle"
	StateRunning         = "running"
	StateRequiresAction  = "requires_action"
	StateUpdating        = "updating" // pod is draining for a graceful upgrade
	StateRestarting      = "restarting"
	StateFailed          = "failed"
	StatePlanMode        = "plan_mode"
	StateWaitingForInput = "waiting_for_input"
	StateUnknown         = "unknown"
)

// eventHeader holds the minimum fields needed to dispatch an event.
type eventHeader struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
}

// parseEventType extracts the type (and subtype for system events) from a raw
// stream-json line without fully unmarshaling it.
func parseEventType(line []byte) (evType, subtype string) {
	var h eventHeader
	if err := json.Unmarshal(line, &h); err != nil {
		return "", ""
	}
	return h.Type, h.Subtype
}

// initEvent is the first event emitted by Claude Code on startup.
type initEvent struct {
	Type        string   `json:"type"`
	Subtype     string   `json:"subtype"`
	SessionID   string   `json:"session_id"`
	Skills      []string `json:"skills"`
	Tools       []string `json:"tools"`
	Agents      []string `json:"agents"`
	MCPServers  []any    `json:"mcp_servers"`
	Model       string   `json:"model"`
	PermMode    string   `json:"permissionMode"`
}

// stateChangedEvent carries a session state transition.
type stateChangedEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

// controlRequestEvent is emitted when Claude Code needs permission to use a tool.
type controlRequestEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// resultEvent is the final event of a Claude turn.
type resultEvent struct {
	Type        string      `json:"type"`
	Subtype     string      `json:"subtype"`
	SessionID   string      `json:"session_id"`
	TotalCostUSD float64    `json:"total_cost_usd"`
	Usage       resultUsage `json:"usage"`
	IsError     bool        `json:"is_error"`
	DurationMs  int64       `json:"duration_ms"`
	NumTurns    int         `json:"num_turns"`
}

type resultUsage struct {
	InputTokens               int `json:"input_tokens"`
	OutputTokens              int `json:"output_tokens"`
	CacheCreationInputTokens  int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens      int `json:"cache_read_input_tokens"`
	ServerToolUseInputTokens  int `json:"server_tool_use_input_tokens"`
	ServerToolUseOutputTokens int `json:"server_tool_use_output_tokens"`
}

// compactBoundaryEvent is emitted when Claude Code compacts the context.
type compactBoundaryEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}
