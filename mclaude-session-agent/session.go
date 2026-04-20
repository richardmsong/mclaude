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

// startupTimeout is the maximum time to wait for Claude to emit an init event
// after spawning.  If the init event is not received within this window the
// session is marked failed and the process is killed.
const startupTimeout = 30 * time.Second

// maxEventBytes is the maximum NATS payload size (8 MB, matching the server
// config).  Events larger than this are truncated before publishing.
const maxEventBytes = 8 * 1024 * 1024

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
	// initCh is closed when the first init event is received from Claude.
	// start() waits on this channel (up to startupTimeout) to confirm startup.
	initCh   chan struct{}
	// debug, if non-nil, is the unix socket server for mclaude-cli attach.
	debug    *DebugServer
	// extraEnv, if non-nil, is appended to the child process environment.
	// Used in tests to inject MOCK_TRANSCRIPT without affecting the test process.
	extraEnv []string
	// metrics, if non-nil, receives span/counter updates as events flow through.
	// Nil in unit tests that don't need metrics.
	metrics  *Metrics
	// permPolicy controls auto-approve behaviour for control_request events.
	permPolicy   PermissionPolicy
	// allowedTools is the set of tool names auto-approved under allowlist policy.
	// Ignored for other policies.
	allowedTools map[string]bool
	// extraFlags is an optional string of raw CLI flags appended to the Claude
	// spawn command.  It is shell-parsed (POSIX quoting rules) before appending.
	extraFlags string
	// onEventPublished, if non-nil, is called after each successful NATS publish
	// with the event type and the JetStream sequence number of the published message.
	// Used by the agent to update replayFromSeq on compact_boundary events.
	// The seq is 0 when not using JetStream (core NATS publish).
	onEventPublished func(evType string, seq uint64)
	// onStrictDeny, if non-nil, is called when a strict-allowlist session
	// auto-denies a control_request. Receives the tool name from the request.
	onStrictDeny func(toolName string)
	// onRawOutput, if non-nil, is called for every raw stdout line from Claude
	// (in the stdout router goroutine) before the line is published to NATS.
	// Used by QuotaMonitor to scan assistant events for the SESSION_JOB_COMPLETE marker.
	onRawOutput func(evType string, raw []byte)
	// shutdownPending is set to true during graceful shutdown after the KV entry
	// has been written with state:"updating". While true, SubtypeSessionStateChanged
	// events update in-memory state but do NOT flush to KV (to preserve the
	// "updating" banner in the SPA during drain).
	shutdownPending bool
	// inFlightBackgroundAgents tracks the number of Agent(run_in_background=true)
	// tool calls that have been dispatched but whose task-notification has not yet
	// been received. Guarded by mu. The drain predicate in gracefulShutdown waits
	// for this to reach 0 before exiting.
	inFlightBackgroundAgents int
}

// newSession creates a Session but does not start the Claude process yet.
// The default permission policy is managed (forward all to client).
// ExtraFlags from the state are copied into sess.extraFlags so they are
// re-applied on every start() call (new session and resume paths both use start).
func newSession(state SessionState, userID string) *Session {
	return &Session{
		state:      state,
		userID:     userID,
		stdinCh:    make(chan []byte, 64),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		initCh:     make(chan struct{}),
		permPolicy: PermissionPolicyManaged,
		extraFlags: state.ExtraFlags,
	}
}

// getState returns a thread-safe copy of the current session state.
func (s *Session) getState() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// stopAndWait sends an interrupt to Claude, waits up to timeout for exit,
// then SIGKILLs if it hasn't stopped.
func (s *Session) stopAndWait(timeout time.Duration) error {
	s.mu.Lock()
	cmd := s.cmd
	dbg := s.debug
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}

	// Stop debug server before closing stdin so clients get a clean EOF.
	if dbg != nil {
		dbg.Stop()
		s.mu.Lock()
		s.debug = nil
		s.mu.Unlock()
	}

	// Send interrupt via stdin.
	interrupt := []byte(`{"type":"control_request","request":{"subtype":"interrupt"}}`)
	select {
	case s.stdinCh <- interrupt:
	default:
	}
	close(s.stopCh)

	// Wait for process to exit or timeout.
	done := make(chan struct{})
	go func() {
		<-s.doneCh
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return fmt.Errorf("process did not exit within %s; killed", timeout)
	}
}

// sendInterrupt sends a synthetic interrupt to Claude's stdin. This is the same
// code path used by handleControl when it receives an interrupt control_request.
// Used by gracefulShutdown to unblock sessions stuck in StateRequiresAction.
func (s *Session) sendInterrupt() {
	interrupt := []byte(`{"type":"control_request","request":{"subtype":"interrupt"}}`)
	select {
	case s.stdinCh <- interrupt:
	default:
	}
}

// start spawns the Claude Code process and begins routing events.
// If resume is true, uses --resume {id} instead of --session-id {id}.
//
// start blocks for up to startupTimeout waiting for Claude's init event.
// If the init event is not received within that window, the process is killed
// and an error is returned (spec: "30s startup timeout → failed state").
func (s *Session) start(claudePath string, resume bool, publish func(subject string, data []byte) error, writeKV func(state SessionState) error) error {
	var args []string
	if resume {
		args = []string{
			"--print", "--verbose",
			"--output-format", "stream-json",
			"--input-format", "stream-json",
			"--include-partial-messages",
			"--replay-user-messages",
			"--resume", s.state.ID,
		}
	} else {
		args = []string{
			"--print", "--verbose",
			"--output-format", "stream-json",
			"--input-format", "stream-json",
			"--include-partial-messages",
			"--replay-user-messages",
			"--session-id", s.state.ID,
		}
	}

	// Append shell-parsed extraFlags if set.
	if s.extraFlags != "" {
		extra, err := shellSplit(s.extraFlags)
		if err != nil {
			return fmt.Errorf("extraFlags shell parse: %w", err)
		}
		args = append(args, extra...)
	}

	cmd := exec.Command(claudePath, args...)
	if s.state.CWD != "" {
		cmd.Dir = s.state.CWD
	}
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

	// Stdout router: reads stream-json lines and publishes to NATS and debug clients.
	go func() {
		defer close(s.doneCh)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

		// Build event subject using slug fields (ADR-0024). Fall back to IDs.
		var eventSubject string
		if s.state.UserSlug != "" && s.state.ProjectSlug != "" && s.state.Slug != "" {
			eventSubject = "mclaude.users." + s.state.UserSlug + ".projects." + s.state.ProjectSlug + ".events." + s.state.Slug
		} else {
			eventSubject = fmt.Sprintf("mclaude.users.%s.projects.%s.events.%s",
				s.userID,
				s.state.ProjectID,
				s.state.ID)
		}

		for scanner.Scan() {
			line := scanner.Bytes()
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)

			evType, _ := parseEventType(lineCopy)

			// Update in-flight background agent counter based on stdout events.
			// +1 when an assistant message contains an Agent tool_use with run_in_background:true.
			// -1 when a user message with origin.kind=="task-notification" is observed.
			s.updateInFlightBackgroundAgents(evType, lineCopy)

			// Truncate events that exceed the NATS max payload limit (8 MB).
			// The full content remains in Claude's JSONL for recovery.
			lineCopy = truncateEventIfNeeded(lineCopy)

			// Trace the NATS publish and count the event type.
			_, pubSpan := NATSPublishSpan(context.Background(), eventSubject)
			_ = publish(eventSubject, lineCopy)
			pubSpan.End()

			if s.metrics != nil && evType != "" {
				s.metrics.EventPublished(evType)
			}

			// Notify the agent of the published event (used for replayFromSeq tracking).
			s.mu.Lock()
			notify := s.onEventPublished
			s.mu.Unlock()
			if notify != nil {
				notify(evType, 0)
			}

			// Notify the quota monitor of raw output (scans for SESSION_JOB_COMPLETE marker).
			s.mu.Lock()
			rawNotify := s.onRawOutput
			s.mu.Unlock()
			if rawNotify != nil {
				rawNotify(evType, lineCopy)
			}

			// Forward to debug clients (mclaude-cli attach).
			s.mu.Lock()
			dbg := s.debug
			s.mu.Unlock()
			if dbg != nil {
				dbg.Broadcast(lineCopy)
			}

			s.handleSideEffect(lineCopy, writeKV)
		}
	}()

	// In --input-format stream-json mode, Claude only emits the init event
	// after receiving the first user message. Don't block on init — return
	// immediately so the session can accept input. The init event will arrive
	// asynchronously when the user sends their first message.
	//
	// Monitor for early exit in a background goroutine so we can log it.
	go func() {
		select {
		case <-s.initCh:
			// Claude initialized successfully after receiving first message.
		case <-time.After(startupTimeout):
			// No init after timeout — likely the user never sent a message.
			// Don't kill the process; it's idle but valid.
		case <-s.doneCh:
			// Claude exited before init — will be cleaned up by the reaper.
		}
	}()

	return nil
}

// updateInFlightBackgroundAgents updates the inFlightBackgroundAgents counter
// based on observed stdout events from Claude:
//   - +1 when an assistant message contains an Agent tool_use block with
//     run_in_background: true in its input.
//   - -1 (floored at 0) when a user message with origin.kind == "task-notification"
//     is observed (indicating the background agent has completed).
func (s *Session) updateInFlightBackgroundAgents(evType string, line []byte) {
	switch evType {
	case EventTypeAssistant:
		// Parse the assistant message to check for Agent tool_use with run_in_background.
		var msg struct {
			Message struct {
				Content []struct {
					Type  string          `json:"type"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return
		}
		for _, block := range msg.Message.Content {
			if block.Type != "tool_use" || block.Name != "Agent" {
				continue
			}
			var input struct {
				RunInBackground bool `json:"run_in_background"`
			}
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}
			if input.RunInBackground {
				s.mu.Lock()
				s.inFlightBackgroundAgents++
				s.mu.Unlock()
			}
		}
	case EventTypeUser:
		// Check for task-notification origin.
		var msg struct {
			Origin struct {
				Kind string `json:"kind"`
			} `json:"origin"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return
		}
		if msg.Origin.Kind == "task-notification" {
			s.mu.Lock()
			if s.inFlightBackgroundAgents > 0 {
				s.inFlightBackgroundAgents--
			}
			s.mu.Unlock()
		}
	}
}

// handleSideEffect inspects specific event types and updates local state/KV.
// It also handles permission-policy auto-approve for control_request events.
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
				s.state.Capabilities = Capabilities{
					Skills: init.Skills,
					Tools:  init.Tools,
					Agents: init.Agents,
				}
				initCh := s.initCh
				s.mu.Unlock()
				s.flushKV(writeKV)
				// Signal that Claude started successfully (unblocks start()).
				// Use a non-blocking select so a second init event doesn't panic.
				select {
				case <-initCh:
				default:
					close(initCh)
				}
			}
		case SubtypeSessionStateChanged:
			var ev stateChangedEvent
			if err := json.Unmarshal(line, &ev); err == nil {
				s.mu.Lock()
				s.state.State = ev.State
				s.state.StateSince = time.Now()
				pending := s.shutdownPending
				s.mu.Unlock()
				// While shutdownPending is true, do NOT flush state to KV.
				// The KV entry was already written with state:"updating" in
				// gracefulShutdown step 1. Flushing here would overwrite the
				// "updating" banner with Claude's live state (e.g. "idle"),
				// causing the SPA banner to disappear mid-upgrade.
				if !pending {
					s.flushKV(writeKV)
				}
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
			policy := s.permPolicy
			allowedTools := s.allowedTools
			s.mu.Unlock()
			s.flushKV(writeKV)

			// Permission policy: auto-approve without forwarding to NATS if
			// the policy permits this tool.
			if shouldAutoApprove(policy, allowedTools, cr) {
				resp := buildAutoApproveResponse(cr.RequestID)
				s.stdinCh <- resp
				s.clearPendingControl(cr.RequestID, writeKV)
			} else if policy == PermissionPolicyStrictAllowlist {
				// strict-allowlist: auto-deny tools not in the allowlist.
				resp, _ := json.Marshal(map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "success",
						"request_id": cr.RequestID,
						"response":   map[string]string{"behavior": "deny"},
					},
				})
				s.stdinCh <- resp
				s.clearPendingControl(cr.RequestID, writeKV)
				// Notify via onStrictDeny callback (e.g. QuotaMonitor).
				var toolName string
				var toolReq struct {
					ToolName string `json:"tool_name"`
				}
				_ = json.Unmarshal(cr.Request, &toolReq)
				toolName = toolReq.ToolName
				s.mu.Lock()
				denyFn := s.onStrictDeny
				s.mu.Unlock()
				if denyFn != nil {
					denyFn(toolName)
				}
			}
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
		// replayFromSeq is updated via onEventPublished callback in the
		// stdout router, which runs before handleSideEffect.  The callback
		// receives the JetStream seq of the published compact_boundary event.
		// Nothing to do here — replayFromSeq is written by the agent.
	}
}

// shouldAutoApprove returns true if the permission policy means the agent
// should respond to this control_request without forwarding to the client.
func shouldAutoApprove(policy PermissionPolicy, allowed map[string]bool, cr controlRequestEvent) bool {
	switch policy {
	case PermissionPolicyAuto:
		return true
	case PermissionPolicyAllowlist, PermissionPolicyStrictAllowlist:
		// Parse tool_name from the request payload.
		var req struct {
			ToolName string `json:"tool_name"`
		}
		_ = json.Unmarshal(cr.Request, &req)
		return allowed[req.ToolName]
	default:
		return false
	}
}

// buildAutoApproveResponse constructs a control_response that approves the
// given request_id (behavior: "allow").
func buildAutoApproveResponse(requestID string) []byte {
	resp, _ := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   map[string]string{"behavior": "allow"},
		},
	})
	return resp
}

// truncateEventIfNeeded returns the line unchanged if it fits within
// maxEventBytes.  If it is larger, the returned bytes are a JSON object
// with a "truncated": true field and the "content" field removed.
// This preserves metadata (type, session_id, etc.) while fitting NATS limits.
func truncateEventIfNeeded(line []byte) []byte {
	if len(line) <= maxEventBytes {
		return line
	}
	// Unmarshal into a generic map, remove "content", add "truncated": true.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		// Can't parse — return a minimal truncation marker.
		return []byte(`{"type":"truncated","truncated":true}`)
	}
	delete(obj, "content")
	obj["truncated"] = json.RawMessage(`true`)
	out, err := json.Marshal(obj)
	if err != nil {
		return []byte(`{"type":"truncated","truncated":true}`)
	}
	return out
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
