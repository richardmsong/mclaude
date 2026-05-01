package events_test

import (
	"testing"

	"mclaude-cli/events"
)

// mustParse is a test helper that fails the test on parse error.
func mustParse(t *testing.T, raw string) *events.Event {
	t.Helper()
	evt, err := events.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return evt
}

func TestAccumulatorSystemInit(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"system","subtype":"init","model":"claude-opus-4-6","skills":["commit"]}`))
	if acc.Model.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q; want %q", acc.Model.Model, "claude-opus-4-6")
	}
	if len(acc.Model.Skills) != 1 || acc.Model.Skills[0] != "commit" {
		t.Errorf("Skills = %v; want [commit]", acc.Model.Skills)
	}
}

func TestAccumulatorStateChanged(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"system","subtype":"session_state_changed","state":"running"}`))
	if acc.Model.State != "running" {
		t.Errorf("State = %q; want running", acc.Model.State)
	}
	acc.Feed(mustParse(t, `{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	if acc.Model.State != "idle" {
		t.Errorf("State = %q; want idle after second update", acc.Model.State)
	}
}

func TestAccumulatorStreamingText(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}}`))
	acc.Feed(mustParse(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}}`))

	if len(acc.Model.Turns) != 1 {
		t.Fatalf("Turns = %d; want 1", len(acc.Model.Turns))
	}
	turn := acc.Model.Turns[0]
	if turn.Type != "assistant" {
		t.Errorf("Turn type = %q; want assistant", turn.Type)
	}
	if len(turn.Blocks) != 1 {
		t.Fatalf("Blocks = %d; want 1", len(turn.Blocks))
	}
	sb, ok := turn.Blocks[0].(*events.StreamingTextBlock)
	if !ok {
		t.Fatalf("Block type = %T; want *StreamingTextBlock", turn.Blocks[0])
	}
	if sb.Full() != "Hello" {
		t.Errorf("Full() = %q; want %q", sb.Full(), "Hello")
	}
}

func TestAccumulatorAssistantFinalises(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}}`))
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"Hi"}],"model":"claude-opus-4-6","usage":{"input_tokens":5,"output_tokens":2}}`))

	// After assistant event there should be two turns: one streaming (from stream_event)
	// and one from the assistant message.
	if len(acc.Model.Turns) != 2 {
		t.Fatalf("Turns = %d; want 2", len(acc.Model.Turns))
	}
	streamTurn := acc.Model.Turns[0]
	sb, ok := streamTurn.Blocks[0].(*events.StreamingTextBlock)
	if !ok {
		t.Fatalf("First block = %T; want *StreamingTextBlock", streamTurn.Blocks[0])
	}
	if !sb.Complete {
		t.Error("StreamingTextBlock.Complete = false; want true after assistant event")
	}

	// Usage accumulated.
	if acc.Model.TotalUsage.InputTokens != 5 {
		t.Errorf("TotalUsage.InputTokens = %d; want 5", acc.Model.TotalUsage.InputTokens)
	}
}

func TestAccumulatorToolUseAndResult(t *testing.T) {
	acc := events.NewAccumulator()
	// Assistant proposes a tool use.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}`))
	// Tool result arrives in a user message.
	acc.Feed(mustParse(t, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"file.go"}]}}`))

	if len(acc.Model.Turns) != 1 {
		t.Fatalf("Turns = %d; want 1 (tool result attaches to tool_use block, not new turn)", len(acc.Model.Turns))
	}
	tb, ok := acc.Model.Turns[0].Blocks[0].(*events.ToolUseBlock)
	if !ok {
		t.Fatalf("Block = %T; want *ToolUseBlock", acc.Model.Turns[0].Blocks[0])
	}
	if tb.Name != "Bash" {
		t.Errorf("Name = %q; want Bash", tb.Name)
	}
	if tb.Result == nil {
		t.Fatal("Result = nil; want ToolResultBlock attached")
	}
	if tb.Result.Content != "file.go" {
		t.Errorf("Result.Content = %q; want %q", tb.Result.Content, "file.go")
	}
}

func TestAccumulatorControlRequest(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"rm -rf /"}}]}`))
	acc.Feed(mustParse(t, `{"type":"control_request","request_id":"cr_1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}}`))

	cr := acc.PendingControl()
	if cr == nil {
		t.Fatal("PendingControl() = nil; want a ControlRequestBlock")
	}
	if cr.ToolName != "Bash" {
		t.Errorf("ToolName = %q; want Bash", cr.ToolName)
	}
	if cr.Status != "pending" {
		t.Errorf("Status = %q; want pending", cr.Status)
	}

	// Approve and verify status update.
	acc.ResolveControl("cr_1", true)
	if cr.Status != "approved" {
		t.Errorf("Status = %q; want approved after ResolveControl", cr.Status)
	}
	if acc.PendingControl() != nil {
		t.Error("PendingControl() non-nil after resolution; want nil")
	}
}

func TestAccumulatorControlDeny(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"assistant","content":[]}`))
	acc.Feed(mustParse(t, `{"type":"control_request","request_id":"cr_2","request":{"subtype":"can_use_tool","tool_name":"Edit","tool_input":{}}}`))

	acc.ResolveControl("cr_2", false)
	cr := acc.PendingControl()
	if cr != nil {
		t.Errorf("PendingControl() = %+v; want nil after deny", cr)
	}
}

func TestAccumulatorToolProgress(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{}}]}`))
	acc.Feed(mustParse(t, `{"type":"tool_progress","tool_use_id":"tu_1","tool_name":"Bash","elapsed_time_seconds":5.0}`))

	tb := acc.Model.Turns[0].Blocks[0].(*events.ToolUseBlock)
	if tb.Elapsed != 5.0 {
		t.Errorf("Elapsed = %v; want 5.0", tb.Elapsed)
	}
}

func TestAccumulatorCompactBoundaryResets(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"old content"}]}`))
	if len(acc.Model.Turns) != 1 {
		t.Fatalf("expected 1 turn before compaction")
	}
	acc.Feed(mustParse(t, `{"type":"system","subtype":"compact_boundary","summary":"context compacted"}`))
	if len(acc.Model.Turns) != 0 {
		t.Errorf("Turns = %d; want 0 after compact_boundary", len(acc.Model.Turns))
	}
}

func TestAccumulatorResultUsageAccumulates(t *testing.T) {
	acc := events.NewAccumulator()
	acc.Feed(mustParse(t, `{"type":"result","subtype":"success","usage":{"input_tokens":10,"output_tokens":5}}`))
	acc.Feed(mustParse(t, `{"type":"result","subtype":"success","usage":{"input_tokens":20,"output_tokens":8}}`))
	if acc.Model.TotalUsage.InputTokens != 30 {
		t.Errorf("TotalUsage.InputTokens = %d; want 30", acc.Model.TotalUsage.InputTokens)
	}
	if acc.Model.TotalUsage.OutputTokens != 13 {
		t.Errorf("TotalUsage.OutputTokens = %d; want 13", acc.Model.TotalUsage.OutputTokens)
	}
}

func TestAccumulatorClearEventResetsState(t *testing.T) {
	acc := events.NewAccumulator()
	// Build up some state.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"hello"}]}`))
	if len(acc.Model.Turns) != 1 {
		t.Fatalf("expected 1 turn before clear")
	}
	// Feed a clear event — turns must be wiped, no divider produced.
	acc.Feed(mustParse(t, `{"type":"clear"}`))
	if len(acc.Model.Turns) != 0 {
		t.Errorf("Turns = %d; want 0 after clear", len(acc.Model.Turns))
	}
}

func TestAccumulatorClearResetsStreamingBlock(t *testing.T) {
	acc := events.NewAccumulator()
	// Start a streaming text block.
	acc.Feed(mustParse(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}}`))
	if len(acc.Model.Turns) != 1 {
		t.Fatalf("expected 1 turn with streaming block")
	}
	// Clear must reset the streaming block so the next delta starts fresh.
	acc.Feed(mustParse(t, `{"type":"clear"}`))
	if len(acc.Model.Turns) != 0 {
		t.Errorf("Turns after clear = %d; want 0", len(acc.Model.Turns))
	}
	// A new delta after clear should start a new turn (not append to nil).
	acc.Feed(mustParse(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"fresh"}}}`))
	if len(acc.Model.Turns) != 1 {
		t.Fatalf("expected 1 turn after new delta post-clear")
	}
	sb, ok := acc.Model.Turns[0].Blocks[0].(*events.StreamingTextBlock)
	if !ok {
		t.Fatalf("block = %T; want *StreamingTextBlock", acc.Model.Turns[0].Blocks[0])
	}
	if sb.Full() != "fresh" {
		t.Errorf("Full() = %q; want \"fresh\"", sb.Full())
	}
}

func TestAccumulatorParentToolUseIDNesting(t *testing.T) {
	acc := events.NewAccumulator()

	// Parent turn with a tool_use block.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"tool_use","id":"tu_parent","name":"Agent","input":{}}]}`))
	if len(acc.Model.Turns) != 1 {
		t.Fatalf("Turns = %d; want 1 after parent assistant event", len(acc.Model.Turns))
	}

	// Subagent assistant message carrying parentToolUseId.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"subagent reply"}],"parent_tool_use_id":"tu_parent"}`))

	// Top-level turns must not grow — subagent turn is nested under the parent tool_use block.
	if len(acc.Model.Turns) != 1 {
		t.Errorf("Turns = %d; want 1 (subagent turn must not be appended at top level)", len(acc.Model.Turns))
	}
	parentTurn := acc.Model.Turns[0]
	tb, ok := parentTurn.Blocks[0].(*events.ToolUseBlock)
	if !ok {
		t.Fatalf("first block = %T; want *ToolUseBlock", parentTurn.Blocks[0])
	}
	if len(tb.AgentTurns) != 1 {
		t.Fatalf("AgentTurns = %d; want 1", len(tb.AgentTurns))
	}
	agentTurn := tb.AgentTurns[0]
	if agentTurn.Type != "assistant" {
		t.Errorf("AgentTurn.Type = %q; want assistant", agentTurn.Type)
	}
	if len(agentTurn.Blocks) != 1 {
		t.Fatalf("AgentTurn.Blocks = %d; want 1", len(agentTurn.Blocks))
	}
	txt, ok := agentTurn.Blocks[0].(*events.TextBlock)
	if !ok {
		t.Fatalf("AgentTurn block = %T; want *TextBlock", agentTurn.Blocks[0])
	}
	if txt.Text != "subagent reply" {
		t.Errorf("TextBlock.Text = %q; want \"subagent reply\"", txt.Text)
	}
}

func TestAccumulatorParentToolUseIDMultipleTurns(t *testing.T) {
	acc := events.NewAccumulator()

	// Parent turn.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"tool_use","id":"tu_p","name":"Agent","input":{}}]}`))

	// Two subagent turns nested under the same parent.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"first"}],"parent_tool_use_id":"tu_p"}`))
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"second"}],"parent_tool_use_id":"tu_p"}`))

	if len(acc.Model.Turns) != 1 {
		t.Errorf("Turns = %d; want 1 (all subagent turns nested)", len(acc.Model.Turns))
	}
	tb := acc.Model.Turns[0].Blocks[0].(*events.ToolUseBlock)
	if len(tb.AgentTurns) != 2 {
		t.Errorf("AgentTurns = %d; want 2", len(tb.AgentTurns))
	}
}

func TestAccumulatorParentToolUseIDUnknownParent(t *testing.T) {
	acc := events.NewAccumulator()

	// Subagent message with an unknown parent — falls through to top-level append.
	acc.Feed(mustParse(t, `{"type":"assistant","content":[{"type":"text","text":"orphan"}],"parent_tool_use_id":"tu_unknown"}`))

	// Unknown parent: fall back to top-level append so nothing is silently dropped.
	if len(acc.Model.Turns) != 1 {
		t.Errorf("Turns = %d; want 1 (orphan turn appended at top level)", len(acc.Model.Turns))
	}
}
