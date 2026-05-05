package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

const debugSocketFmt = "/tmp/mclaude-session-%s.sock"

// DebugServer listens on a unix socket and bridges mclaude-cli debug clients
// to a running Session.
//
// Each session agent exposes one DebugServer per session at
// /tmp/mclaude-session-{sessionId}.sock.
//
// Protocol over the socket (NDJSON):
//
//	Server → client: stream-json events forwarded from Claude Code stdout
//	Client → server: stream-json messages forwarded to Claude Code stdin
type DebugServer struct {
	mu         sync.Mutex
	sessionID  string
	socketPath string
	listener   net.Listener
	// clients holds all currently-connected debug clients.
	clients map[net.Conn]struct{}
	// sendInput queues a line for delivery to Claude stdin (same channel as NATS).
	sendInput func([]byte)
	// onAttach / onDetach are called when a debug client connects / disconnects.
	onAttach func()
	onDetach func()
}

// NewDebugServer creates a DebugServer for the given session.
// sendInput is the callback to deliver a message to Claude's stdin.
// onAttach / onDetach are called when clients connect / disconnect.
func NewDebugServer(sessionID string, sendInput func([]byte), onAttach, onDetach func()) *DebugServer {
	return &DebugServer{
		sessionID:  sessionID,
		socketPath: fmt.Sprintf(debugSocketFmt, sessionID),
		clients:    make(map[net.Conn]struct{}),
		sendInput:  sendInput,
		onAttach:   onAttach,
		onDetach:   onDetach,
	}
}

// Start begins listening on the unix socket.
// Returns immediately — accepts run in a background goroutine.
func (s *DebugServer) Start() error {
	// Remove stale socket file if it exists.
	_ = os.Remove(s.socketPath)

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("debug socket listen %s: %w", s.socketPath, err)
	}
	s.listener = l

	go s.acceptLoop()
	return nil
}

// Stop closes the listener and all connected clients.
func (s *DebugServer) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	for conn := range s.clients {
		conn.Close()
	}
	s.mu.Unlock()
	_ = os.Remove(s.socketPath)
}

// Broadcast sends a stream-json line to all connected debug clients.
// Called by the session's stdout goroutine for every published event.
func (s *DebugServer) Broadcast(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dead []net.Conn
	for conn := range s.clients {
		if _, err := conn.Write(append(line, '\n')); err != nil {
			dead = append(dead, conn)
		}
	}
	for _, conn := range dead {
		conn.Close()
		delete(s.clients, conn)
	}
}

// acceptLoop accepts incoming connections until the listener is closed.
func (s *DebugServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener closed — normal on shutdown.
			return
		}
		s.mu.Lock()
		s.clients[conn] = struct{}{}
		s.mu.Unlock()

		if s.onAttach != nil {
			s.onAttach()
		}

		go s.handleClient(conn)
	}
}

// handleClient reads NDJSON messages from a debug client and forwards them
// to Claude's stdin.
func (s *DebugServer) handleClient(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
		if s.onDetach != nil {
			s.onDetach()
		}
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20) // 1MB max per line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		s.sendInput(cp)
	}
	// EOF or error — normal on client disconnect.
	if err := scanner.Err(); err != nil && err != io.EOF {
		// Non-fatal: client may have disconnected abruptly.
		_ = err
	}
}
