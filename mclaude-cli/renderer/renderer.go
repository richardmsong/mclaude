// Package renderer prints stream-json events as human-readable terminal text.
// It is stateless — callers drive the render loop and pass events one at a time.
package renderer

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"mclaude-cli/events"
)

// Renderer writes human-readable event text to the configured writer.
type Renderer struct {
	w io.Writer
}

// New returns a Renderer that writes to w.
func New(w io.Writer) *Renderer { return &Renderer{w: w} }

// Render formats and writes one event.
func (r *Renderer) Render(evt *events.Event) {
	switch evt.Type {
	case "system":
		r.renderSystem(evt)
	case "stream_event":
		if evt.IsStreamText() {
			fmt.Fprint(r.w, evt.TextDelta())
		}
	case "assistant":
		r.renderAssistant(evt)
	case "control_request":
		r.renderControlRequest(evt)
	case "tool_progress":
		r.renderToolProgress(evt)
	case "result":
		r.renderResult(evt)
	}
}

// RenderToolResult formats a tool result that was attached to a tool_use block.
func (r *Renderer) RenderToolResult(toolUseID, content string, isError bool) {
	label := "tool_result"
	if isError {
		label = "tool_error"
	}
	lines := strings.SplitN(content, "\n", 6)
	if len(lines) > 5 {
		extra := strings.Count(content, "\n") - 4
		lines = lines[:5]
		lines = append(lines, fmt.Sprintf("[…%d more lines]", extra))
	}
	fmt.Fprintf(r.w, "[%s]\n%s\n", label, strings.Join(lines, "\n"))
}

func (r *Renderer) renderSystem(evt *events.Event) {
	switch evt.Subtype {
	case "session_state_changed":
		fmt.Fprintf(r.w, "[state: %s]\n", evt.State)
	case "init":
		fmt.Fprintf(r.w, "[model: %s]\n", evt.Model)
	case "compact_boundary":
		fmt.Fprintln(r.w, "\n--- context compacted ---")
	}
}

func (r *Renderer) renderAssistant(evt *events.Event) {
	for _, block := range evt.Content {
		switch block.Type {
		case "text":
			// Streaming text was already printed incrementally; emit trailing newline.
			fmt.Fprintln(r.w)
		case "tool_use":
			summary := summarizeInput(block.Input)
			fmt.Fprintf(r.w, "[tool_use: %s %s]\n", block.Name, summary)
		}
	}
}

func (r *Renderer) renderControlRequest(evt *events.Event) {
	if evt.Request == nil || evt.Request.Subtype != "can_use_tool" {
		return
	}
	summary := summarizeInput(evt.Request.ToolInput)
	fmt.Fprintf(r.w, "[Allow %s %s? (y/n)] ", evt.Request.ToolName, summary)
}

func (r *Renderer) renderToolProgress(evt *events.Event) {
	fmt.Fprintf(r.w, "[%s: %.1fs]\n", evt.ToolName, evt.ElapsedTime)
}

func (r *Renderer) renderResult(evt *events.Event) {
	if evt.Usage != nil {
		fmt.Fprintf(r.w, "[tokens: in=%d out=%d]\n",
			evt.Usage.InputTokens, evt.Usage.OutputTokens)
	}
}

// summarizeInput returns a one-line summary of a JSON tool-input object.
// It prefers the first string-valued field, truncated to 40 characters.
func summarizeInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		s := string(raw)
		if len(s) > 60 {
			return s[:60] + "…"
		}
		return s
	}
	for _, v := range m {
		if s, ok := v.(string); ok {
			if len(s) > 40 {
				s = s[:40] + "…"
			}
			return fmt.Sprintf("%q", s)
		}
	}
	s := string(raw)
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
