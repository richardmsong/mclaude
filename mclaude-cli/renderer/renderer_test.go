package renderer_test

import (
	"bytes"
	"strings"
	"testing"

	"mclaude-cli/events"
	"mclaude-cli/renderer"
)

// mustParse parses raw JSON and fails the test on error.
func mustParse(t *testing.T, raw string) *events.Event {
	t.Helper()
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return evt
}

// render returns the text produced by rendering a single event.
func render(t *testing.T, raw string) string {
	t.Helper()
	var buf bytes.Buffer
	r := renderer.New(&buf)
	r.Render(mustParse(t, raw))
	return buf.String()
}

func TestRenderStreamEvent(t *testing.T) {
	out := render(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}}`)
	if out != "Hello" {
		t.Errorf("output = %q; want %q", out, "Hello")
	}
}

func TestRenderStreamEventNonText(t *testing.T) {
	// Non-text stream events should produce no output.
	out := render(t, `{"type":"stream_event","event":{"type":"content_block_start","index":0}}`)
	if out != "" {
		t.Errorf("output = %q; want empty for non-text stream event", out)
	}
}

func TestRenderAssistantText(t *testing.T) {
	// The renderer emits a newline after the text block (streaming already printed it).
	out := render(t, `{"type":"assistant","content":[{"type":"text","text":"Done."}]}`)
	if !strings.Contains(out, "\n") {
		t.Errorf("output = %q; want trailing newline after text block", out)
	}
}

func TestRenderAssistantToolUse(t *testing.T) {
	out := render(t, `{"type":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"npm test"}}]}`)
	if !strings.Contains(out, "tool_use") {
		t.Errorf("output = %q; want [tool_use: ...]", out)
	}
	if !strings.Contains(out, "Bash") {
		t.Errorf("output = %q; want Bash in tool_use line", out)
	}
	if !strings.Contains(out, "npm test") {
		t.Errorf("output = %q; want command summary in output", out)
	}
}

func TestRenderControlRequestPermission(t *testing.T) {
	out := render(t, `{"type":"control_request","request_id":"cr_1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_input":{"command":"ls"}}}`)
	if !strings.Contains(out, "Allow") {
		t.Errorf("output = %q; want 'Allow ...' permission prompt", out)
	}
	if !strings.Contains(out, "Bash") {
		t.Errorf("output = %q; want tool name in permission prompt", out)
	}
	if !strings.Contains(out, "(y/n)") {
		t.Errorf("output = %q; want '(y/n)' in permission prompt", out)
	}
}

func TestRenderControlRequestInterruptNoOutput(t *testing.T) {
	// Non-permission control_requests should produce no output.
	out := render(t, `{"type":"control_request","request":{"subtype":"interrupt"}}`)
	if out != "" {
		t.Errorf("output = %q; want empty for interrupt control_request", out)
	}
}

func TestRenderSystemStateChanged(t *testing.T) {
	out := render(t, `{"type":"system","subtype":"session_state_changed","state":"running"}`)
	if !strings.Contains(out, "running") {
		t.Errorf("output = %q; want state in output", out)
	}
}

func TestRenderSystemInit(t *testing.T) {
	out := render(t, `{"type":"system","subtype":"init","model":"claude-opus-4-6"}`)
	if !strings.Contains(out, "claude-opus-4-6") {
		t.Errorf("output = %q; want model name in output", out)
	}
}

func TestRenderCompactBoundary(t *testing.T) {
	out := render(t, `{"type":"system","subtype":"compact_boundary"}`)
	if !strings.Contains(out, "compact") {
		t.Errorf("output = %q; want compact marker", out)
	}
}

func TestRenderToolProgress(t *testing.T) {
	out := render(t, `{"type":"tool_progress","tool_use_id":"tu_1","tool_name":"Bash","elapsed_time_seconds":7.5}`)
	if !strings.Contains(out, "7.5") {
		t.Errorf("output = %q; want elapsed time", out)
	}
	if !strings.Contains(out, "Bash") {
		t.Errorf("output = %q; want tool name", out)
	}
}

func TestRenderResult(t *testing.T) {
	out := render(t, `{"type":"result","subtype":"success","usage":{"input_tokens":100,"output_tokens":50}}`)
	if !strings.Contains(out, "100") {
		t.Errorf("output = %q; want input token count", out)
	}
	if !strings.Contains(out, "50") {
		t.Errorf("output = %q; want output token count", out)
	}
}

func TestRenderToolResultShort(t *testing.T) {
	var buf bytes.Buffer
	r := renderer.New(&buf)
	r.RenderToolResult("tu_1", "output line", false)
	out := buf.String()
	if !strings.Contains(out, "tool_result") {
		t.Errorf("output = %q; want 'tool_result' label", out)
	}
	if !strings.Contains(out, "output line") {
		t.Errorf("output = %q; want content", out)
	}
}

func TestRenderToolResultError(t *testing.T) {
	var buf bytes.Buffer
	r := renderer.New(&buf)
	r.RenderToolResult("tu_1", "error message", true)
	out := buf.String()
	if !strings.Contains(out, "tool_error") {
		t.Errorf("output = %q; want 'tool_error' label for error result", out)
	}
}

func TestRenderToolResultTruncated(t *testing.T) {
	// Results with >5 lines should be truncated.
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7"
	var buf bytes.Buffer
	r := renderer.New(&buf)
	r.RenderToolResult("tu_1", content, false)
	out := buf.String()
	if !strings.Contains(out, "more lines") {
		t.Errorf("output = %q; want truncation indicator for long output", out)
	}
}

func TestRenderStreamingMultipleDeltas(t *testing.T) {
	// Verify incremental streaming output is emitted immediately per delta.
	var buf bytes.Buffer
	r := renderer.New(&buf)
	for _, chunk := range []string{"foo", " bar", " baz"} {
		raw := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + chunk + `"}}}`
		r.Render(mustParse(t, raw))
	}
	out := buf.String()
	if out != "foo bar baz" {
		t.Errorf("output = %q; want %q", out, "foo bar baz")
	}
}
