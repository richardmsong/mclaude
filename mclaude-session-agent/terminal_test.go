package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockTermPubSub implements termPubSub for tests without a real NATS server.
// Subscribers are registered via subscribe(); published data is delivered to
// matching subscribers.  Callers can also call deliver() to inject inbound
// messages (simulating remote publishers sending to the input subject).
type mockTermPubSub struct {
	mu       sync.Mutex
	subs     map[string]func(data []byte)
	onPublish func(subject string, data []byte)
}

func newMockTermPubSub() *mockTermPubSub {
	return &mockTermPubSub{
		subs: make(map[string]func(data []byte)),
	}
}

func (m *mockTermPubSub) transport() termPubSub {
	return termPubSub{
		publish: func(subject string, data []byte) error {
			m.mu.Lock()
			cb := m.onPublish
			m.mu.Unlock()
			if cb != nil {
				cb(subject, data)
			}
			return nil
		},
		subscribe: func(subject string, handler func(data []byte)) (func(), error) {
			m.mu.Lock()
			m.subs[subject] = handler
			m.mu.Unlock()
			return func() {
				m.mu.Lock()
				delete(m.subs, subject)
				m.mu.Unlock()
			}, nil
		},
	}
}

// deliver simulates a remote publisher sending data to the given subject.
func (m *mockTermPubSub) deliver(subject string, data []byte) {
	m.mu.Lock()
	handler := m.subs[subject]
	m.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

// terminalCapture records bytes published to NATS output subjects.
type terminalCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (tc *terminalCapture) write(p []byte) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.buf.Write(p)
}

func (tc *terminalCapture) string() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.buf.String()
}

func (tc *terminalCapture) waitFor(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(tc.string(), substr) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestPTYStartsShell verifies that startTerminal spawns a shell, routes its
// stdout to the output subject, and accepts input via the input subject.
func TestPTYStartsShell(t *testing.T) {
	mock := newMockTermPubSub()
	cap := &terminalCapture{}

	mock.onPublish = func(subject string, data []byte) {
		if strings.HasSuffix(subject, ".output") {
			cap.write(data)
		}
	}

	ts, err := startTerminal("term-1", "/bin/sh", mock.transport(), "user-1", "host-1", "proj-1")
	if err != nil {
		t.Fatalf("startTerminal: %v", err)
	}
	t.Cleanup(ts.stop)

	// Deliver input via the mock (simulates a NATS publisher).
	inputSubject := "mclaude.users.user-1.hosts.host-1.projects.proj-1.api.terminal.term-1.input"
	mock.deliver(inputSubject, []byte("echo hello-pty-test\n"))

	if !cap.waitFor("hello-pty-test", 5*time.Second) {
		t.Errorf("PTY output not received; got: %q", cap.string())
	}
}

// TestPTYResize verifies that resize() sets the PTY window size without error.
func TestPTYResize(t *testing.T) {
	mock := newMockTermPubSub()
	ts, err := startTerminal("term-2", "/bin/sh", mock.transport(), "user-1", "host-1", "proj-1")
	if err != nil {
		t.Fatalf("startTerminal: %v", err)
	}
	t.Cleanup(ts.stop)

	if err := ts.resize(40, 120); err != nil {
		t.Errorf("resize: %v", err)
	}
}

// TestPTYStop verifies that stop() terminates the shell and doneCh closes.
func TestPTYStop(t *testing.T) {
	mock := newMockTermPubSub()
	ts, err := startTerminal("term-3", "/bin/sh", mock.transport(), "user-1", "host-1", "proj-1")
	if err != nil {
		t.Fatalf("startTerminal: %v", err)
	}

	ts.stop()
	select {
	case <-ts.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("PTY session did not stop within 5s")
	}
}

// TestPTYMultipleSessions verifies two independent PTY sessions do not
// interfere (distinct subjects, distinct PTY fds).
func TestPTYMultipleSessions(t *testing.T) {
	mockA := newMockTermPubSub()
	mockB := newMockTermPubSub()

	capA := &terminalCapture{}
	capB := &terminalCapture{}

	mockA.onPublish = func(_ string, data []byte) { capA.write(data) }
	mockB.onPublish = func(_ string, data []byte) { capB.write(data) }

	tsA, err := startTerminal("term-a", "/bin/sh", mockA.transport(), "user-1", "host-1", "proj-1")
	if err != nil {
		t.Fatalf("start tsA: %v", err)
	}
	tsB, err := startTerminal("term-b", "/bin/sh", mockB.transport(), "user-1", "host-1", "proj-1")
	if err != nil {
		t.Fatalf("start tsB: %v", err)
	}
	t.Cleanup(tsA.stop)
	t.Cleanup(tsB.stop)

	mockA.deliver("mclaude.users.user-1.hosts.host-1.projects.proj-1.api.terminal.term-a.input", []byte("echo session-A\n"))
	mockB.deliver("mclaude.users.user-1.hosts.host-1.projects.proj-1.api.terminal.term-b.input", []byte("echo session-B\n"))

	if !capA.waitFor("session-A", 5*time.Second) {
		t.Errorf("term-a output missing; got: %q", capA.string())
	}
	if !capB.waitFor("session-B", 5*time.Second) {
		t.Errorf("term-b output missing; got: %q", capB.string())
	}

	// Cross-contamination check.
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(capA.string(), "session-B") {
		t.Error("term-a received term-b output (cross-contamination)")
	}
	if strings.Contains(capB.string(), "session-A") {
		t.Error("term-b received term-a output (cross-contamination)")
	}
}

// TestPTYOutputSubjectFormat verifies the output subject follows the naming
// convention: mclaude.{userId}.{projectId}.terminal.{termID}.output
func TestPTYOutputSubjectFormat(t *testing.T) {
	mock := newMockTermPubSub()
	var (
		subjectMu       sync.Mutex
		capturedSubject string
	)
	mock.onPublish = func(subject string, _ []byte) {
		subjectMu.Lock()
		capturedSubject = subject
		subjectMu.Unlock()
	}

	ts, err := startTerminal("my-term", "/bin/sh", mock.transport(), "alice", "myhost", "myproject")
	if err != nil {
		t.Fatalf("startTerminal: %v", err)
	}
	t.Cleanup(ts.stop)

	// Trigger output.
	mock.deliver("mclaude.users.alice.hosts.myhost.projects.myproject.api.terminal.my-term.input", []byte("echo x\n"))
	time.Sleep(500 * time.Millisecond)

	want := "mclaude.users.alice.hosts.myhost.projects.myproject.api.terminal.my-term.output"
	subjectMu.Lock()
	got := capturedSubject
	subjectMu.Unlock()
	if got != want {
		t.Errorf("output subject: got %q, want %q", got, want)
	}
}
