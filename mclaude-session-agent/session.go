package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

// pendingShell represents a Bash(run_in_background=true) tool_use that has been
// observed in an assistant message but whose tool_result (containing the
// backgroundTaskId) has not yet arrived. Phase 1 of two-phase shell tracking.
type pendingShell struct {
	ToolUseId string
	Command   string
	StartedAt time.Time
}

// inFlightShell represents a fully promoted background shell: the tool_result
// has been observed and the taskId + outputFilePath are known. Phase 2 of
// two-phase shell tracking. Used during graceful shutdown to publish synthetic
// <task-notification status=killed> XML messages.
type inFlightShell struct {
	ToolUseId      string
	TaskId         string
	Command        string
	OutputFilePath string
	StartedAt      time.Time
}

// backgroundTaskIdRe extracts the Claude Code internal random task ID from
// a tool_result text like "Command was manually backgrounded with ID: b3f7x2a9".
var backgroundTaskIdRe = regexp.MustCompile(`Command was manually backgrounded with ID: (\S+)`)

// sanitizePath replaces every character that is NOT [a-zA-Z0-9] with '-'.
// Matches Claude Code's TypeScript sanitizePath (sessionStoragePortable.ts).
func sanitizePath(p string) string {
	result := []byte(p)
	for i, c := range result {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			result[i] = '-'
		}
	}
	return string(result)
}

// shellOutputPath constructs the absolute path to a background shell's output file
// on the PVC. Formula: {tmpDir}/claude-{uid}/{sanitizedCwd}/{sessionId}/tasks/{taskId}.output
func shellOutputPath(tmpDir, sanitizedCwd, sessionId, taskId string) string {
	uid := os.Getuid()
	return filepath.Join(tmpDir, fmt.Sprintf("claude-%d", uid),
		sanitizedCwd, sessionId, "tasks", taskId+".output")
}

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
	// stopping is set to true when the session is being intentionally stopped
	// (delete, restart, or graceful shutdown). The crash watcher goroutine
	// checks this flag to distinguish intentional stops from unexpected crashes.
	stopping bool
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
	// pendingShells tracks Bash(run_in_background=true) tool_use blocks observed
	// in assistant messages. Keyed by toolUseId. Entries are promoted to
	// inFlightShells when the matching tool_result arrives with a backgroundTaskId.
	// Guarded by mu. Phase 1 of two-phase shell tracking.
	pendingShells map[string]pendingShell
	// inFlightShells tracks fully promoted background shells with known taskIds
	// and output paths. Keyed by toolUseId. Used during graceful shutdown to
	// publish synthetic <task-notification status=killed> XML messages.
	// Guarded by mu. Phase 2 of two-phase shell tracking.
	inFlightShells map[string]*inFlightShell
}

// newSession creates a Session but does not start the Claude process yet.
// The default permission policy is managed (forward all to client).
// ExtraFlags from the state are copied into sess.extraFlags so they are
// re-applied on every start() call (new session and resume paths both use start).
func newSession(state SessionState, userID string) *Session {
	return &Session{
		state:          state,
		userID:         userID,
		stdinCh:        make(chan []byte, 64),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
		initCh:         make(chan struct{}),
		permPolicy:     PermissionPolicyManaged,
		extraFlags:     state.ExtraFlags,
		pendingShells:  make(map[string]pendingShell),
		inFlightShells: make(map[string]*inFlightShell),
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
	s.stopping = true
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

		// Build event subject using slug fields (ADR-0024 / ADR-0035).
		// ADR-0035 requires .hosts.{hslug}. between user and project segments.
		var eventSubject string
		if s.state.UserSlug != "" && s.state.HostSlug != "" && s.state.ProjectSlug != "" && s.state.Slug != "" {
			eventSubject = "mclaude.users." + s.state.UserSlug + ".hosts." + s.state.HostSlug + ".projects." + s.state.ProjectSlug + ".events." + s.state.Slug
		} else {
			eventSubject = fmt.Sprintf("mclaude.users.%s.hosts.%s.projects.%s.events.%s",
				s.userID,
				s.state.HostSlug,
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

			// Two-phase shell tracking for Bash(run_in_background=true).
			// Phase 1: assistant message with Bash tool_use + run_in_background:true → pendingShells.
			// Phase 2: user message with matching tool_result → promoted to inFlightShells.
			// Removal: user message with origin.kind=="task-notification" → removed from inFlightShells.
			s.updateInFlightShells(evType, lineCopy)

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

	// Monitor startup: if Claude doesn't reach idle within 30s, mark as failed
	// (spec: GAP-SA-K4). Also handles early exit before init.
	go func() {
		select {
		case <-s.initCh:
			// Claude initialized successfully.
		case <-time.After(startupTimeout):
			// No init after timeout — mark session as failed.
			s.mu.Lock()
			s.state.State = StateFailed
			s.state.StateSince = time.Now().UTC()
			s.mu.Unlock()
			s.flushKV(writeKV)
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

// updateInFlightShells implements two-phase shell tracking for
// Bash(run_in_background=true) tool calls:
//
//   - Phase 1 (Pending): On assistant message with a tool_use block where
//     name == "Bash" AND input.run_in_background == true, record a pending entry
//     keyed by tool_use.id with command from input.command.
//
//   - Phase 2 (Promoted): On user message with a tool_result block whose
//     tool_use_id matches a pending entry, extract backgroundTaskId from result
//     text (regex: "Command was manually backgrounded with ID: {id}"), compute
//     outputFilePath, promote to inFlightShells.
//
//   - Removal: On user message with origin.kind == "task-notification"
//     referencing the shell's toolUseId (real task-notification arrived — shell
//     completed naturally).
//
// Shell tracking is disabled when CLAUDE_CODE_TMPDIR is empty.
func (s *Session) updateInFlightShells(evType string, line []byte) {
	tmpDir := os.Getenv("CLAUDE_CODE_TMPDIR")
	if tmpDir == "" {
		return // shell tracking is a K8s-only feature
	}

	switch evType {
	case EventTypeAssistant:
		// Phase 1: look for Bash tool_use with run_in_background:true.
		var msg struct {
			Message struct {
				Content []struct {
					Type  string          `json:"type"`
					ID    string          `json:"id"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return
		}
		for _, block := range msg.Message.Content {
			if block.Type != "tool_use" || block.Name != "Bash" {
				continue
			}
			var input struct {
				RunInBackground bool   `json:"run_in_background"`
				Command         string `json:"command"`
			}
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}
			if input.RunInBackground && block.ID != "" {
				s.mu.Lock()
				s.pendingShells[block.ID] = pendingShell{
					ToolUseId: block.ID,
					Command:   input.Command,
					StartedAt: time.Now(),
				}
				s.mu.Unlock()
			}
		}

	case EventTypeUser:
		// First check for task-notification origin (removal path).
		// Use a separate, forgiving parse that doesn't require content to be an array.
		var originCheck struct {
			Origin struct {
				Kind string `json:"kind"`
			} `json:"origin"`
		}
		if err := json.Unmarshal(line, &originCheck); err != nil {
			return
		}

		// Removal: task-notification origin removes from inFlightShells.
		if originCheck.Origin.Kind == "task-notification" {
			s.removeShellByTaskNotification(line)
			return
		}

		// Phase 2: look for tool_result matching a pending shell.
		var msg struct {
			Message struct {
				Content []struct {
					Type      string `json:"type"`
					ToolUseId string `json:"tool_use_id"`
					Content   string `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return
		}

		// Phase 2: check tool_result blocks for pending shell promotion.
		for _, block := range msg.Message.Content {
			if block.Type != "tool_result" {
				continue
			}
			s.mu.Lock()
			pending, ok := s.pendingShells[block.ToolUseId]
			if !ok {
				s.mu.Unlock()
				continue
			}
			delete(s.pendingShells, block.ToolUseId)
			s.mu.Unlock()

			// Extract backgroundTaskId from the result text.
			matches := backgroundTaskIdRe.FindStringSubmatch(block.Content)
			if len(matches) < 2 {
				continue // not a background shell result; discard pending entry
			}
			taskId := matches[1]

			cwd := s.getState().CWD
			outputPath := shellOutputPath(tmpDir, sanitizePath(cwd), s.getState().ID, taskId)

			s.mu.Lock()
			s.inFlightShells[pending.ToolUseId] = &inFlightShell{
				ToolUseId:      pending.ToolUseId,
				TaskId:         taskId,
				Command:        pending.Command,
				OutputFilePath: outputPath,
				StartedAt:      pending.StartedAt,
			}
			s.mu.Unlock()
		}
	}
}

// removeShellByTaskNotification removes an inFlightShell when a real
// task-notification message arrives, indicating the shell completed naturally.
// It scans the notification content for the tool-use-id to identify which shell.
func (s *Session) removeShellByTaskNotification(line []byte) {
	// The task-notification is a user message containing XML in the content.
	// We need to find the tool-use-id referenced in it.
	// Try to parse tool-use-id from the message content (XML-like extraction).
	var msg struct {
		Message struct {
			Content interface{} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}

	// Content can be a string or an array of content blocks.
	var contentStr string
	switch v := msg.Message.Content.(type) {
	case string:
		contentStr = v
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					contentStr += text
				} else if content, ok := m["content"].(string); ok {
					contentStr += content
				}
			}
		}
	}

	if contentStr == "" {
		// Fallback: remove any inFlightShell whose toolUseId appears in the raw line.
		s.mu.Lock()
		for toolUseId := range s.inFlightShells {
			if len(toolUseId) > 0 && strings.Contains(string(line), toolUseId) {
				delete(s.inFlightShells, toolUseId)
				break
			}
		}
		s.mu.Unlock()
		return
	}

	// Extract <tool-use-id>...</tool-use-id> from the content.
	toolUseIdRe := regexp.MustCompile(`<tool-use-id>([^<]+)</tool-use-id>`)
	matches := toolUseIdRe.FindStringSubmatch(contentStr)
	if len(matches) >= 2 {
		s.mu.Lock()
		delete(s.inFlightShells, matches[1])
		s.mu.Unlock()
		return
	}

	// Fallback: check if any toolUseId appears in the content string.
	s.mu.Lock()
	for toolUseId := range s.inFlightShells {
		if len(toolUseId) > 0 && strings.Contains(contentStr, toolUseId) {
			delete(s.inFlightShells, toolUseId)
			break
		}
	}
	s.mu.Unlock()
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
			accumulateUsage(&s.state, r.Usage, r.TotalCostUSD)
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
	s.stopping = true
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

// exitCode returns the process exit code, or -1 if not available.
func (s *Session) exitCode() int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}
