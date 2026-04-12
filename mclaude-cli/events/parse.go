package events

import (
	"encoding/json"
	"fmt"
)

// Parse parses a raw JSON line from the unix socket into an Event.
func Parse(data []byte) (*Event, error) {
	var evt Event
	if err := json.Unmarshal(data, &evt); err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}
	return &evt, nil
}

// IsStreamText reports whether this event carries a text delta.
func (e *Event) IsStreamText() bool {
	return e.Type == "stream_event" &&
		e.StreamEvt != nil &&
		e.StreamEvt.Type == "content_block_delta" &&
		e.StreamEvt.Delta != nil &&
		e.StreamEvt.Delta.Type == "text_delta"
}

// TextDelta returns the text delta from a stream_event, or empty string.
func (e *Event) TextDelta() string {
	if !e.IsStreamText() {
		return ""
	}
	return e.StreamEvt.Delta.Text
}

// IsPermissionRequest reports whether this is a can_use_tool control_request.
func (e *Event) IsPermissionRequest() bool {
	return e.Type == "control_request" &&
		e.Request != nil &&
		e.Request.Subtype == "can_use_tool"
}

// ToolResultBlocks returns tool_result content blocks from a user message.
// Returns nil if the message content is not an array of blocks.
func (e *Event) ToolResultBlocks() []ContentBlock {
	if e.Type != "user" || e.Message == nil {
		return nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(e.Message.Content, &blocks); err != nil {
		return nil
	}
	var results []ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results = append(results, b)
		}
	}
	return results
}
