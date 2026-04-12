package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Session manages a single Claude Code child process and its NATS routing.
type Session struct {
	mu       sync.Mutex
	state    SessionState
	userID   string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdinCh  chan []byte
	stopCh   chan struct{}
	doneCh   chan struct{}
	// extraEnv, if non-nil, is appended to the child process environment.
	// Used in tests to inject MOCK_TRANSCRIPT without affecting the test process.
	extraEnv []string
	// metrics, if non-nil, receives span/counter updates as events flow through.
	// Nil in unit tests that don't need metrics.
	metrics  *Metrics
}

// newSession creates a Session but does not start the Claude process yet.
func newSession(state SessionState, userID string) *Session {
	return &Session{
		state:   state,
		userID:  userID,
		stdinCh: make(chan []byte, 64),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// start spawns the Claude Code process and begins routing events.
func (s *Session) start(claudePath string, publish func(subject string, data []byte) error, writeKV func(state SessionState) error) error {
	args := []string{
		"--print", "--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--include-partial-messages",
		"--session-id", s.state.ID,
	}
	if s.state.CWD != "" {
		args = append(args, "-w", s.state.CWD)
	}

	cmd := exec.Command(claudePath, args...)
	if len(s.extraEnv) > 0 {
		cmd.Env = append(append([]string{}, os.Environ()...), s.extraEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.mu.Unlock()

	// Emit a point-in-time span for the Claude process spawn.
	_, spawnSpan := ClaudeSpawnSpan(context.Background(), s.state.ID, false)
	if err := cmd.Start(); err != nil {
		spawnSpan.End()
		return fmt.Errorf("start claude: %w", err)
	}
	spawnSpan.End()

	// Stdin serializer: drains stdinCh to the pipe sequentially so NDJSON
	// lines never interleave.
	go func() {
		for msg := range s.stdinCh {
			_, _ = stdin.Write(msg)
			_, _ = stdin.Write([]byte("\n"))
		}
	}()

	// Stdout router: reads stream-json lines and publishes to NATS.
	go func() {
		defer close(s.doneCh)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

		eventSubject := fmt.Sprintf("mclaude.%s.%s.events.%s",
			s.userID,
			s.state.ProjectID,
			s.state.ID)

		for scanner.Scan() {
			line := scanner.Bytes()
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)

			evType, _ := parseEventType(lineCopy)

			// Trace the NATS publish and count the event type.
			_, pubSpan := NATSPublishSpan(context.Background(), eventSubject)
			_ = publish(eventSubject, lineCopy)
			pubSpan.End()

			if s.metrics != nil && evType != "" {
				s.metrics.EventPublished(evType)
			}

			s.handleSideEffect(lineCopy, writeKV)
		}
	}()

	return nil
}

// handleSideEffect inspects specific event types and updates local state/KV.
func (s *Session) handleSideEffect(line []byte, writeKV func(state SessionState) error) {
	evType, subtype := parseEventType(line)
	switch evType {
	case EventTypeSystem:
		switch subtype {
		case SubtypeInit:
			var init initEvent
			if err := json.Unmarshal(line, &init); err == nil {
				s.mu.Lock()
				s.state.Model = init.Model
				s.state.Capabilities = Capabilities{Tools: init.Tools}
				s.mu.Unlock()
				s.flushKV(writeKV)
			}
		case SubtypeSessionStateChanged:
			var ev stateChangedEvent
			if err := json.Unmarshal(line, &ev); err == nil {
				s.mu.Lock()
				s.state.State = ev.State
				s.state.StateSince = time.Now()
				s.mu.Unlock()
				s.flushKV(writeKV)
			}
		}
	case EventTypeControlRequest:
		var cr controlRequestEvent
		if err := json.Unmarshal(line, &cr); err == nil {
			s.mu.Lock()
			if s.state.PendingControls == nil {
				s.state.PendingControls = make(map[string]any)
			}
			s.state.PendingControls[cr.RequestID] = cr.Request
			s.mu.Unlock()
			s.flushKV(writeKV)
		}
	case EventTypeResult:
		var r resultEvent
		if err := json.Unmarshal(line, &r); err == nil {
			s.mu.Lock()
			s.state.Usage.InputTokens += r.Usage.InputTokens
			s.state.Usage.OutputTokens += r.Usage.OutputTokens
			s.state.Usage.CostUSD += r.TotalCostUSD
			s.mu.Unlock()
			s.flushKV(writeKV)
		}
	case EventTypeCompactBoundary:
		// replayFromSeq will be updated by the agent with the JetStream seq.
	}
}

func (s *Session) flushKV(writeKV func(state SessionState) error) {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	_ = writeKV(st)
}

// sendInput queues a stream-json input line for delivery to Claude's stdin.
func (s *Session) sendInput(data []byte) {
	s.stdinCh <- data
}

// clearPendingControl removes a control request after it has been answered.
func (s *Session) clearPendingControl(requestID string, writeKV func(state SessionState) error) {
	s.mu.Lock()
	delete(s.state.PendingControls, requestID)
	s.mu.Unlock()
	s.flushKV(writeKV)
}

// stop interrupts and waits for the Claude process to exit.
func (s *Session) stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	interrupt := []byte(`{"type":"control_request","request":{"subtype":"interrupt"}}`)
	select {
	case s.stdinCh <- interrupt:
	default:
	}
	close(s.stopCh)
}

// waitDone blocks until the Claude process exits.
func (s *Session) waitDone() {
	<-s.doneCh
}
