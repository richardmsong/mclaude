package events

import (
	"encoding/json"
	"strings"
)

// ConversationModel is the accumulated, renderable conversation.
type ConversationModel struct {
	Turns      []*Turn
	State      string   // idle | running | requires_action | restarting | failed
	Model      string
	Skills     []string
	TotalUsage Usage
}

// Turn is one conversational turn (user or assistant).
type Turn struct {
	Type   string // "user" | "assistant"
	Blocks []Block
}

// Block is a renderable unit within a turn.
type Block interface {
	BlockType() string
}

// TextBlock is finalised assistant or user text.
type TextBlock struct{ Text string }

func (b *TextBlock) BlockType() string { return "text" }

// StreamingTextBlock accumulates in-flight token deltas.
type StreamingTextBlock struct {
	Chunks   []string
	Complete bool
}

func (b *StreamingTextBlock) BlockType() string { return "streaming_text" }

// Full returns the concatenated text of all chunks.
func (b *StreamingTextBlock) Full() string { return strings.Join(b.Chunks, "") }

// ToolUseBlock represents one tool invocation.
type ToolUseBlock struct {
	ID      string
	Name    string
	Input   json.RawMessage
	Elapsed float64
	Result  *ToolResultBlock
}

func (b *ToolUseBlock) BlockType() string { return "tool_use" }

// ToolResultBlock is the result attached to a ToolUseBlock.
type ToolResultBlock struct {
	ToolUseID string
	Content   string
	IsError   bool
}

func (b *ToolResultBlock) BlockType() string { return "tool_result" }

// ControlRequestBlock is a pending permission.
type ControlRequestBlock struct {
	RequestID string
	ToolName  string
	ToolInput json.RawMessage
	Status    string // "pending" | "approved" | "denied"
}

func (b *ControlRequestBlock) BlockType() string { return "control_request" }

// Accumulator processes a stream of events into a ConversationModel.
type Accumulator struct {
	Model           ConversationModel
	streaming       *StreamingTextBlock
	pendingTools    map[string]*ToolUseBlock
	pendingControls map[string]*ControlRequestBlock
}

// NewAccumulator returns a fresh Accumulator.
func NewAccumulator() *Accumulator {
	return &Accumulator{
		pendingTools:    make(map[string]*ToolUseBlock),
		pendingControls: make(map[string]*ControlRequestBlock),
	}
}

// Feed processes one event.
func (a *Accumulator) Feed(evt *Event) {
	switch evt.Type {
	case "system":
		a.handleSystem(evt)
	case "stream_event":
		a.handleStreamEvent(evt)
	case "assistant":
		a.handleAssistant(evt)
	case "user":
		a.handleUser(evt)
	case "control_request":
		a.handleControlRequest(evt)
	case "tool_progress":
		a.handleToolProgress(evt)
	case "result":
		a.handleResult(evt)
	}
}

func (a *Accumulator) handleSystem(evt *Event) {
	switch evt.Subtype {
	case "session_state_changed":
		a.Model.State = evt.State
	case "init":
		a.Model.Model = evt.Model
		a.Model.Skills = evt.Skills
	case "compact_boundary":
		a.Model.Turns = nil
		a.streaming = nil
	}
}

func (a *Accumulator) handleStreamEvent(evt *Event) {
	if !evt.IsStreamText() {
		return
	}
	if a.streaming == nil {
		a.streaming = &StreamingTextBlock{}
		// Attach to current assistant turn or start one.
		n := len(a.Model.Turns)
		if n == 0 || a.Model.Turns[n-1].Type != "assistant" {
			a.Model.Turns = append(a.Model.Turns, &Turn{Type: "assistant"})
		}
		last := a.Model.Turns[len(a.Model.Turns)-1]
		last.Blocks = append(last.Blocks, a.streaming)
	}
	a.streaming.Chunks = append(a.streaming.Chunks, evt.TextDelta())
}

func (a *Accumulator) handleAssistant(evt *Event) {
	// Finalise any streaming block.
	if a.streaming != nil {
		a.streaming.Complete = true
		a.streaming = nil
	}

	t := &Turn{Type: "assistant"}
	for _, block := range evt.Content {
		switch block.Type {
		case "text":
			t.Blocks = append(t.Blocks, &TextBlock{Text: block.Text})
		case "tool_use":
			tb := &ToolUseBlock{ID: block.ID, Name: block.Name, Input: block.Input}
			t.Blocks = append(t.Blocks, tb)
			a.pendingTools[block.ID] = tb
		// "thinking" blocks are skipped in the CLI renderer.
		}
	}
	if evt.Usage != nil {
		a.Model.TotalUsage.InputTokens += evt.Usage.InputTokens
		a.Model.TotalUsage.OutputTokens += evt.Usage.OutputTokens
	}
	a.Model.Turns = append(a.Model.Turns, t)
}

func (a *Accumulator) handleUser(evt *Event) {
	if evt.Message == nil {
		return
	}
	// Array of content blocks (tool results)?
	var blocks []ContentBlock
	if err := json.Unmarshal(evt.Message.Content, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "tool_result" {
				if tb, ok := a.pendingTools[b.ToolUseID]; ok {
					tb.Result = &ToolResultBlock{
						ToolUseID: b.ToolUseID,
						Content:   b.Content,
						IsError:   b.IsError,
					}
					delete(a.pendingTools, b.ToolUseID)
				}
			}
		}
		return
	}
	// Plain-text user message.
	var text string
	if err := json.Unmarshal(evt.Message.Content, &text); err == nil {
		t := &Turn{Type: "user", Blocks: []Block{&TextBlock{Text: text}}}
		a.Model.Turns = append(a.Model.Turns, t)
	}
}

func (a *Accumulator) handleControlRequest(evt *Event) {
	if !evt.IsPermissionRequest() {
		return
	}
	cb := &ControlRequestBlock{
		RequestID: evt.RequestID,
		ToolName:  evt.Request.ToolName,
		ToolInput: evt.Request.ToolInput,
		Status:    "pending",
	}
	a.pendingControls[evt.RequestID] = cb
	// Attach to the last assistant turn.
	if n := len(a.Model.Turns); n > 0 {
		a.Model.Turns[n-1].Blocks = append(a.Model.Turns[n-1].Blocks, cb)
	}
}

func (a *Accumulator) handleToolProgress(evt *Event) {
	if tb, ok := a.pendingTools[evt.ToolUseID]; ok {
		tb.Elapsed = evt.ElapsedTime
	}
}

func (a *Accumulator) handleResult(evt *Event) {
	if evt.Usage != nil {
		a.Model.TotalUsage.InputTokens += evt.Usage.InputTokens
		a.Model.TotalUsage.OutputTokens += evt.Usage.OutputTokens
	}
}

// PendingControl returns the first pending ControlRequestBlock, or nil.
func (a *Accumulator) PendingControl() *ControlRequestBlock {
	for _, cb := range a.pendingControls {
		if cb.Status == "pending" {
			return cb
		}
	}
	return nil
}

// ResolveControl marks a control request as approved or denied.
func (a *Accumulator) ResolveControl(requestID string, approved bool) {
	if cb, ok := a.pendingControls[requestID]; ok {
		if approved {
			cb.Status = "approved"
		} else {
			cb.Status = "denied"
		}
	}
}
