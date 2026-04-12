package events_test

import (
	"testing"

	"mclaude-cli/events"
)

func TestParseSystemInit(t *testing.T) {
	raw := `{"type":"system","subtype":"init","skills":["commit"],"tools":["Bash","Read"],"model":"claude-opus-4-6"}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if evt.Type != "system" {
		t.Errorf("Type = %q; want %q", evt.Type, "system")
	}
	if evt.Subtype != "init" {
		t.Errorf("Subtype = %q; want %q", evt.Subtype, "init")
	}
	if evt.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q; want %q", evt.Model, "claude-opus-4-6")
	}
	if len(evt.Skills) != 1 || evt.Skills[0] != "commit" {
		t.Errorf("Skills = %v; want [commit]", evt.Skills)
	}
	if len(evt.Tools) != 2 {
		t.Errorf("Tools = %v; want 2 entries", evt.Tools)
	}
}

func TestParseSystemStateChanged(t *testing.T) {
	raw := `{"type":"system","subtype":"session_state_changed","state":"running"}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if evt.State != "running" {
		t.Errorf("State = %q; want %q", evt.State, "running")
	}
}

func TestParseStreamEvent(t *testing.T) {
	raw := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !evt.IsStreamText() {
		t.Fatal("IsStreamText = false; want true")
	}
	if evt.TextDelta() != "Hello" {
		t.Errorf("TextDelta = %q; want %q", evt.TextDelta(), "Hello")
	}
}

func TestParseStreamEventNonText(t *testing.T) {
	// A stream_event that isn't a text_delta should not be IsStreamText.
	raw := `{"type":"stream_event","event":{"type":"content_block_start","index":0}}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if evt.IsStreamText() {
		t.Error("IsStreamText = true; want false for non-text_delta")
	}
	if evt.TextDelta() != "" {
		t.Errorf("TextDelta = %q; want empty for non-text event", evt.TextDelta())
	}
}

func TestParseAssistantMessage(t *testing.T) {
	raw := `{"type":"assistant","content":[{"type":"text","text":"Hi"},{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}],"model":"claude-opus-4-6","usage":{"input_tokens":5,"output_tokens":3}}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(evt.Content) != 2 {
		t.Fatalf("Content length = %d; want 2", len(evt.Content))
	}
	if evt.Content[0].Type != "text" || evt.Content[0].Text != "Hi" {
		t.Errorf("Content[0] = %+v; want text 'Hi'", evt.Content[0])
	}
	if evt.Content[1].Type != "tool_use" || evt.Content[1].Name != "Bash" {
		t.Errorf("Content[1] = %+v; want tool_use Bash", evt.Content[1])
	}
	if evt.Usage == nil || evt.Usage.InputTokens != 5 {
		t.Errorf("Usage = %+v; want InputTokens=5", evt.Usage)
	}
}

func TestParseControlRequest(t *testing.T) {
	raw := `{"type":"control_request","request_id":"cr_1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !evt.IsPermissionRequest() {
		t.Fatal("IsPermissionRequest = false; want true")
	}
	if evt.RequestID != "cr_1" {
		t.Errorf("RequestID = %q; want %q", evt.RequestID, "cr_1")
	}
	if evt.Request.ToolName != "Bash" {
		t.Errorf("ToolName = %q; want %q", evt.Request.ToolName, "Bash")
	}
}

func TestParseControlRequestNonPermission(t *testing.T) {
	raw := `{"type":"control_request","request":{"subtype":"interrupt"}}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if evt.IsPermissionRequest() {
		t.Error("IsPermissionRequest = true; want false for interrupt")
	}
}

func TestParseToolProgress(t *testing.T) {
	raw := `{"type":"tool_progress","tool_use_id":"tu_1","tool_name":"Bash","elapsed_time_seconds":3.5}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if evt.ToolUseID != "tu_1" {
		t.Errorf("ToolUseID = %q; want %q", evt.ToolUseID, "tu_1")
	}
	if evt.ElapsedTime != 3.5 {
		t.Errorf("ElapsedTime = %v; want 3.5", evt.ElapsedTime)
	}
}

func TestParseResult(t *testing.T) {
	raw := `{"type":"result","subtype":"success","usage":{"input_tokens":20,"output_tokens":10},"duration_ms":1200}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if evt.Subtype != "success" {
		t.Errorf("Subtype = %q; want %q", evt.Subtype, "success")
	}
	if evt.DurationMS != 1200 {
		t.Errorf("DurationMS = %d; want 1200", evt.DurationMS)
	}
}

func TestParseToolResultBlocks(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"output here","is_error":false}]}}`
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	blocks := evt.ToolResultBlocks()
	if len(blocks) != 1 {
		t.Fatalf("ToolResultBlocks length = %d; want 1", len(blocks))
	}
	if blocks[0].ToolUseID != "tu_1" {
		t.Errorf("ToolUseID = %q; want %q", blocks[0].ToolUseID, "tu_1")
	}
	if blocks[0].Content != "output here" {
		t.Errorf("Content = %q; want %q", blocks[0].Content, "output here")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := events.Parse([]byte(`{not valid json`))
	if err == nil {
		t.Error("expected error for invalid JSON; got nil")
	}
}
