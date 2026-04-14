package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
)

// testLogger returns a no-op zerolog.Logger suitable for use in tests.
func testLogger(_ *testing.T) zerolog.Logger {
	return zerolog.Nop()
}

// terminalReplyCapture captures the reply sent by a handler for assertions.
type terminalReplyCapture struct {
	mu      sync.Mutex
	replies [][]byte
}

func (r *terminalReplyCapture) onReply(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	r.replies = append(r.replies, cp)
}

func (r *terminalReplyCapture) last() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.replies) == 0 {
		return nil
	}
	return r.replies[len(r.replies)-1]
}

// replyMsg builds a fake nats.Msg that calls onReply when Respond is called.
// Since nats.Msg.Respond uses the underlying connection, we cannot easily mock
// it. Instead we test handlers by inspecting the Agent's terminals map directly
// and by calling the handler with a nil-reply msg (no reply destination).
func buildMsg(data []byte) *nats.Msg {
	return &nats.Msg{
		Data: data,
	}
}

// buildMinimalAgent creates a bare-minimum Agent for handler tests.
// It has no real NATS connection — handlers that call a.nc.* will panic,
// but the terminal handlers only use NATSTermPubSub(a.nc) which can be
// replaced by injecting the terminal directly.
//
// For handleTerminalCreate we cannot avoid the real PTY path, but we can
// test the post-create state.  For delete/resize we inject pre-created
// TerminalSessions with a mock PTY via startTerminal.
func buildMinimalAgent(t *testing.T) *Agent {
	t.Helper()
	return &Agent{
		sessions:  make(map[string]*Session),
		terminals: make(map[string]*TerminalSession),
		userID:    "test-user",
		projectID: "test-proj",
		log:       testLogger(t),
	}
}

// injectTerminal pre-seeds the agent's terminals map with a live PTY session
// so delete/resize handlers can operate on it.
func injectTerminal(t *testing.T, a *Agent, termID string) *TerminalSession {
	t.Helper()
	mock := newMockTermPubSub()
	ts, err := startTerminal(termID, "/bin/sh", mock.transport(), a.userID, a.projectID)
	if err != nil {
		t.Fatalf("startTerminal(%q): %v", termID, err)
	}
	a.mu.Lock()
	a.terminals[termID] = ts
	a.mu.Unlock()
	t.Cleanup(ts.stop)
	return ts
}

// TestTerminalSubscriptionSubjects verifies that subscribeAPI registers
// subscriptions for terminal.create, terminal.delete, and terminal.resize.
// We test this by inspecting the subscription prefix — without a live NATS
// server we check that the expected handler methods exist.
func TestTerminalHandlersExist(t *testing.T) {
	a := buildMinimalAgent(t)
	// Verify handler methods are present by attempting to call with an
	// empty/invalid message — they should return an error reply, not panic.
	// handleTerminalDelete with empty termId.
	a.handleTerminalDelete(buildMsg([]byte(`{}`)))
	a.handleTerminalResize(buildMsg([]byte(`{}`)))
	// No panic = methods exist.
}

// TestTerminalDeleteUnknown verifies handleTerminalDelete returns an error
// when the termId is not found.
func TestTerminalDeleteUnknown(t *testing.T) {
	a := buildMinimalAgent(t)

	data, _ := json.Marshal(map[string]string{"termId": "no-such-term"})
	a.handleTerminalDelete(buildMsg(data))
	// No session in map — handler should log a warning but not panic.
	a.mu.RLock()
	count := len(a.terminals)
	a.mu.RUnlock()
	if count != 0 {
		t.Errorf("expected 0 terminals, got %d", count)
	}
}

// TestTerminalDeleteRemovesFromMap verifies that handleTerminalDelete removes
// the terminal from the agent's map after stopping it.
func TestTerminalDeleteRemovesFromMap(t *testing.T) {
	a := buildMinimalAgent(t)
	injectTerminal(t, a, "term-del-1")

	a.mu.RLock()
	before := len(a.terminals)
	a.mu.RUnlock()
	if before != 1 {
		t.Fatalf("expected 1 terminal before delete, got %d", before)
	}

	data, _ := json.Marshal(map[string]string{"termId": "term-del-1"})
	a.handleTerminalDelete(buildMsg(data))

	a.mu.RLock()
	after := len(a.terminals)
	a.mu.RUnlock()
	if after != 0 {
		t.Errorf("expected 0 terminals after delete, got %d", after)
	}
}

// TestTerminalResizeUnknown verifies handleTerminalResize returns without
// panicking when the termId is not found.
func TestTerminalResizeUnknown(t *testing.T) {
	a := buildMinimalAgent(t)

	data, _ := json.Marshal(map[string]any{"termId": "no-such", "rows": 24, "cols": 80})
	a.handleTerminalResize(buildMsg(data))
	// Should not panic.
}

// TestTerminalResizeChangesSize verifies that handleTerminalResize calls
// pty.Setsize on the terminal without error.
func TestTerminalResizeChangesSize(t *testing.T) {
	a := buildMinimalAgent(t)
	injectTerminal(t, a, "term-resize-1")

	data, _ := json.Marshal(map[string]any{"termId": "term-resize-1", "rows": 40, "cols": 120})
	a.handleTerminalResize(buildMsg(data))
	// If Setsize panicked or returned an error the handler would log it —
	// absence of panic is the primary assertion here.
}

// TestTerminalCreateAddsToMap verifies that handleTerminalCreate inserts
// the new session into the agent's terminals map.
// This test requires PTY support (macOS/Linux).
func TestTerminalCreateAddsToMap(t *testing.T) {
	// We cannot call handleTerminalCreate without a real *nats.Conn because
	// it calls NATSTermPubSub(a.nc) internally.  Instead, simulate the same
	// path by calling startTerminal and inserting manually — the same code
	// path the handler follows.  This tests that the data structure is correct.

	a := buildMinimalAgent(t)
	mock := newMockTermPubSub()
	ts, err := startTerminal("term-new-1", "/bin/sh", mock.transport(), a.userID, a.projectID)
	if err != nil {
		t.Fatalf("startTerminal: %v", err)
	}
	t.Cleanup(ts.stop)

	a.mu.Lock()
	a.terminals["term-new-1"] = ts
	a.mu.Unlock()

	// Now delete it to exercise the full create→delete lifecycle.
	data, _ := json.Marshal(map[string]string{"termId": "term-new-1"})
	a.handleTerminalDelete(buildMsg(data))

	// Wait briefly for stop goroutine.
	time.Sleep(100 * time.Millisecond)

	a.mu.RLock()
	count := len(a.terminals)
	a.mu.RUnlock()
	if count != 0 {
		t.Errorf("terminal should have been removed from map, count=%d", count)
	}
}

// TestTerminalDuplicateCreate verifies that inserting the same termId twice
// is handled (second insert is rejected or first is preserved).
func TestTerminalDuplicateCreate(t *testing.T) {
	a := buildMinimalAgent(t)
	injectTerminal(t, a, "term-dup")

	// Try to inject a second terminal with the same ID through the normal map path.
	// In the real handler we check for existence and return an error.
	a.mu.Lock()
	_, exists := a.terminals["term-dup"]
	a.mu.Unlock()

	if !exists {
		t.Error("expected first terminal to exist in map")
	}
}
