// Package testutil provides a mock unix socket server for CLI tests.
// The server streams canned JSON events loaded from transcript files,
// then captures whatever the client sends back.
package testutil

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// MockServer is a mock unix socket server that streams canned events then
// reads whatever the client sends.
type MockServer struct {
	// Path is the unix socket path. Use this to connect the CLI under test.
	Path string

	listener net.Listener
	events   [][]byte // pre-loaded JSON lines to send

	mu       sync.Mutex
	received [][]byte // messages received from client
	done     chan struct{}
}

// NewMockServer creates a mock server with the given canned event lines.
// The socket is cleaned up via t.Cleanup.
//
// Note: we use /tmp directly rather than t.TempDir() because macOS enforces
// a 104-byte unix socket path limit and t.TempDir() paths can exceed it for
// tests with long names.
func NewMockServer(t *testing.T, events [][]byte) *MockServer {
	t.Helper()
	// Short, predictable base path to stay well under the 104-byte macOS limit.
	tmpDir, err := os.MkdirTemp("/tmp", "mcl-")
	if err != nil {
		t.Fatalf("mock server mkdirtemp: %v", err)
	}
	sockPath := filepath.Join(tmpDir, "s.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("mock server listen: %v", err)
	}

	s := &MockServer{
		Path:     sockPath,
		listener: l,
		events:   events,
		done:     make(chan struct{}),
	}
	t.Cleanup(func() {
		l.Close()
		os.RemoveAll(tmpDir)
	})
	return s
}

// NewMockServerFromFile creates a mock server whose events are loaded from a
// JSONL transcript file. Lines beginning with '#' and blank lines are skipped.
func NewMockServerFromFile(t *testing.T, transcriptPath string) *MockServer {
	t.Helper()
	f, err := os.Open(transcriptPath)
	if err != nil {
		t.Fatalf("open transcript %s: %v", transcriptPath, err)
	}
	defer f.Close()

	var evts [][]byte
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		evts = append(evts, cp)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan transcript: %v", err)
	}
	return NewMockServer(t, evts)
}

// ServeOne accepts one connection, sends all events (with optional delay between
// each), then reads messages from the client until it disconnects.
// Call in a goroutine; use Wait to block until it finishes.
func (s *MockServer) ServeOne(delay time.Duration) {
	defer close(s.done)
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	for _, evt := range s.events {
		if delay > 0 {
			time.Sleep(delay)
		}
		conn.Write(evt)
		conn.Write([]byte("\n"))
	}

	// Read any messages the client sends after receiving events.
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		cp := make([]byte, len(line))
		copy(cp, line)
		s.mu.Lock()
		s.received = append(s.received, cp)
		s.mu.Unlock()
	}
}

// Received returns all messages received from the client (safe to call
// concurrently with ServeOne).
func (s *MockServer) Received() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([][]byte, len(s.received))
	copy(cp, s.received)
	return cp
}

// Wait blocks until ServeOne returns.
func (s *MockServer) Wait() {
	<-s.done
}
