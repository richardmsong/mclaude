package main

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDebugServerStartStop verifies the server starts, accepts connections,
// and cleans up its socket file on Stop.
func TestDebugServerStartStop(t *testing.T) {
	var (
		attached atomic.Int64
		detached atomic.Int64
	)

	dbg := NewDebugServer("dbg-test-1",
		func([]byte) {},
		func() { attached.Add(1) },
		func() { detached.Add(1) },
	)

	if err := dbg.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Socket file must exist.
	socketPath := dbg.socketPath
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix socket: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let acceptLoop register the client

	if attached.Load() != 1 {
		t.Errorf("onAttach called %d times, want 1", attached.Load())
	}

	conn.Close()
	time.Sleep(50 * time.Millisecond) // let handleClient detect close

	dbg.Stop()
	time.Sleep(50 * time.Millisecond)

	if detached.Load() != 1 {
		t.Errorf("onDetach called %d times, want 1", detached.Load())
	}
}

// TestDebugServerBroadcast verifies that Broadcast() sends a line to every
// connected client.
func TestDebugServerBroadcast(t *testing.T) {
	dbg := NewDebugServer("dbg-test-broadcast",
		func([]byte) {},
		nil, nil,
	)
	if err := dbg.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer dbg.Stop()

	// Connect two clients.
	connA, err := net.Dial("unix", dbg.socketPath)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.Close()

	connB, err := net.Dial("unix", dbg.socketPath)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.Close()

	time.Sleep(50 * time.Millisecond) // let connections be accepted

	// Broadcast an event line.
	event := []byte(`{"type":"system","subtype":"init","session_id":"s1"}`)
	dbg.Broadcast(event)

	// Both clients should receive the line.
	for _, conn := range []net.Conn{connA, connB} {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			t.Errorf("client did not receive broadcast: %v", scanner.Err())
			continue
		}
		if !strings.Contains(scanner.Text(), "init") {
			t.Errorf("unexpected line: %q", scanner.Text())
		}
	}
}

// TestDebugServerClientToStdin verifies that messages written by a debug
// client are forwarded to the sendInput callback (→ Claude stdin).
func TestDebugServerClientToStdin(t *testing.T) {
	var (
		mu       sync.Mutex
		received [][]byte
	)
	dbg := NewDebugServer("dbg-test-stdin",
		func(data []byte) {
			mu.Lock()
			cp := make([]byte, len(data))
			copy(cp, data)
			received = append(received, cp)
			mu.Unlock()
		},
		nil, nil,
	)
	if err := dbg.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer dbg.Stop()

	conn, err := net.Dial("unix", dbg.socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	// Write two messages from the client.
	msg1 := `{"type":"user","message":{"role":"user","content":"hello"}}`
	msg2 := `{"type":"control_request","request":{"subtype":"interrupt"}}`
	if _, err := conn.Write([]byte(msg1 + "\n")); err != nil {
		t.Fatalf("write msg1: %v", err)
	}
	if _, err := conn.Write([]byte(msg2 + "\n")); err != nil {
		t.Fatalf("write msg2: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	got := len(received)
	mu.Unlock()
	if got != 2 {
		t.Errorf("sendInput called %d times, want 2", got)
	}
}

// TestDebugServerMultipleClients verifies attach/detach counts with multiple
// concurrent clients.
func TestDebugServerMultipleClients(t *testing.T) {
	var attached, detached atomic.Int64

	dbg := NewDebugServer("dbg-test-multi",
		func([]byte) {},
		func() { attached.Add(1) },
		func() { detached.Add(1) },
	)
	if err := dbg.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer dbg.Stop()

	const n = 3
	conns := make([]net.Conn, n)
	for i := range conns {
		c, err := net.Dial("unix", dbg.socketPath)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns[i] = c
	}
	time.Sleep(100 * time.Millisecond)

	if attached.Load() != int64(n) {
		t.Errorf("attached: got %d, want %d", attached.Load(), n)
	}

	for _, c := range conns {
		c.Close()
	}
	time.Sleep(100 * time.Millisecond)

	if detached.Load() != int64(n) {
		t.Errorf("detached: got %d, want %d", detached.Load(), n)
	}
}
