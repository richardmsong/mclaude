package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// QuotaMonitor watches the mclaude.users.{uslug}.quota subject for quota status
// updates and gracefully stops a session when the 5h utilization threshold is
// exceeded. One goroutine per session; created in handleCreate when the
// sessions.create request includes softThreshold > 0 (ADR-0044).
//
// Two-tier quota enforcement:
//  1. Soft threshold: injects "MCLAUDE_STOP: quota_soft" via sessions.input (NATS)
//     and waits for Claude to end its turn naturally.
//  2. Hard budget: after soft stop is injected, counts output tokens via onRawOutput.
//     When outputTokensSinceSoftMark >= hardHeadroomTokens, sends a control_request
//     interrupt directly to sess.stdinCh.
//
// Turn-end detection uses the turnEndedCh channel, which is signalled by
// onRawOutput on EventTypeResult. handleTurnEnd() has priority over
// handleSubprocessExit() via a nested non-blocking select.
type QuotaMonitor struct {
	// Identity
	sessionID   string
	sessionSlug string
	userSlug    string
	hostSlug    string
	projectSlug string

	// Config
	softThreshold      int
	hardHeadroomTokens int
	autoContinue       bool
	prompt             string // initial prompt; held until quota allows delivery
	resumePrompt       string

	// Infrastructure
	nc      *nats.Conn
	session *Session

	// Callbacks
	publishLifec func(sessionID, evType string, extra map[string]string)
	writeKV      func(state SessionState) error

	// Channels
	permDeniedCh chan string         // receives toolName on strict-allowlist deny
	quotaCh      chan *nats.Msg      // receives quota status updates from NATS
	quotaSub     *nats.Subscription  // subscription to mclaude.users.{uslug}.quota
	stopCh       chan struct{}        // closed to exit the monitor goroutine
	turnEndedCh  chan struct{}        // 1-buffered; signalled by onRawOutput on result event

	// State (accessed only from the monitor goroutine, so no mutex needed)
	lastU5                    int
	lastR5                    time.Time
	stopReason                string // "" | "quota_soft" | "quota_hard" | "permDenied"
	outputTokensAtSoftMark    int    // cumulative outputTokens snapshot when soft marker injected
	outputTokensSinceSoftMark int    // token estimate since soft mark
	terminalEventPublished    bool   // true once handleTurnEnd or handleSubprocessExit published
}

// newQuotaMonitor creates a QuotaMonitor, subscribes to quota updates,
// starts the monitor goroutine, and returns.
func newQuotaMonitor(
	sessionID, sessionSlug, userSlug, hostSlug, projectSlug string,
	softThreshold, hardHeadroomTokens int,
	autoContinue bool,
	prompt, resumePrompt string,
	nc *nats.Conn,
	sess *Session,
	publishLifec func(sessionID, evType string, extra map[string]string),
	writeKV func(state SessionState) error,
) (*QuotaMonitor, error) {
	quotaCh := make(chan *nats.Msg, 16)
	subject := "mclaude.users." + userSlug + ".quota"
	quotaSub, err := nc.ChanSubscribe(subject, quotaCh)
	if err != nil {
		return nil, fmt.Errorf("quota subscribe: %w", err)
	}

	m := &QuotaMonitor{
		sessionID:          sessionID,
		sessionSlug:        sessionSlug,
		userSlug:           userSlug,
		hostSlug:           hostSlug,
		projectSlug:        projectSlug,
		softThreshold:      softThreshold,
		hardHeadroomTokens: hardHeadroomTokens,
		autoContinue:       autoContinue,
		prompt:             prompt,
		resumePrompt:       resumePrompt,
		nc:                 nc,
		session:            sess,
		publishLifec:       publishLifec,
		writeKV:            writeKV,
		permDeniedCh:       make(chan string, 1),
		quotaCh:            quotaCh,
		quotaSub:           quotaSub,
		stopCh:             make(chan struct{}),
		turnEndedCh:        make(chan struct{}, 1),
	}

	go m.run()
	return m, nil
}

// stop requests the monitor goroutine to exit.
// Safe to call even if the goroutine has already exited.
func (m *QuotaMonitor) stop() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
}

// signalPermDenied sends the denied tool name to the monitor goroutine.
// Non-blocking: if the channel is full (stop already in progress), the
// signal is dropped.
func (m *QuotaMonitor) signalPermDenied(toolName string) {
	select {
	case m.permDeniedCh <- toolName:
	default:
		// stop already in progress; drop signal
	}
}

// sessionsInputSubject returns the NATS subject for the session's input.
// Format: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input
func (m *QuotaMonitor) sessionsInputSubject() string {
	return "mclaude.users." + m.userSlug +
		".hosts." + m.hostSlug +
		".projects." + m.projectSlug +
		".sessions." + m.sessionSlug + ".input"
}

// publishToSessionsInput publishes a message text to the session's NATS input
// subject using the standard sessions.input JSON envelope (ADR-0044).
// Format: {"id":"<uuid>","ts":<unix_ms>,"type":"message","text":"<text>"}
func (m *QuotaMonitor) publishToSessionsInput(text string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"id":   uuid.NewString(),
		"ts":   time.Now().UnixMilli(),
		"type": "message",
		"text": text,
	})
	subject := m.sessionsInputSubject()
	_ = m.nc.Publish(subject, payload)
}

// sendGracefulStop publishes the MCLAUDE_STOP: quota_soft marker to the
// session's NATS input subject (sessions.{sslug}.input). The session agent's
// input handler picks this up and forwards it to Claude as a user message.
// This replaces the old approach of writing directly to sess.stdinCh.
func (m *QuotaMonitor) sendGracefulStop() {
	m.publishToSessionsInput("MCLAUDE_STOP: quota_soft")
}

// sendInitialPrompt delivers the caller-supplied prompt to the session's NATS
// input subject. Called when quota permits (u5 < softThreshold and pending).
func (m *QuotaMonitor) sendInitialPrompt() {
	m.publishToSessionsInput(m.prompt)
}

// sendResumeNudge delivers the resume nudge message when quota recovers
// and the session was paused.
func (m *QuotaMonitor) sendResumeNudge() {
	text := m.resumePrompt
	if text == "" {
		text = "Resuming — continue where you left off."
	}
	m.publishToSessionsInput(text)
}

// sendHardInterrupt queues an interrupt control_request directly on the
// session's stdin channel (bypassing NATS — interrupts bypass Stop hooks).
// Only called from the monitor goroutine via onRawOutput.
func (m *QuotaMonitor) sendHardInterrupt() {
	interrupt := []byte(`{"type":"control_request","request":{"subtype":"interrupt"}}`)
	select {
	case m.session.stdinCh <- interrupt:
	default:
	}
}

// onRawOutput is called for each raw stdout line from Claude (via
// Session.onRawOutput). Handles:
//  1. Token counting: byte estimate for assistant events; authoritative count
//     from result event usage.output_tokens (ADR-0044).
//  2. Hard budget check: fires interrupt when outputTokensSinceSoftMark >=
//     hardHeadroomTokens (only when stopReason == "quota_soft").
//  3. Turn-end detection: signals turnEndedCh when EventTypeResult arrives.
//
// This method is called from a separate goroutine (the session stdout router).
// It must not block and must not access the monitor's internal state (except
// turnEndedCh, which is goroutine-safe as a buffered channel).
func (m *QuotaMonitor) onRawOutput(evType string, raw []byte) {
	switch evType {
	case EventTypeAssistant:
		// Primary (byte estimate): count tokens during a running assistant turn.
		// Only count when a soft stop has been injected and we're tracking budget.
		if m.stopReason == "quota_soft" || m.stopReason == "quota_hard" {
			estimate := len(raw) / 4
			m.outputTokensSinceSoftMark += estimate
			// Hard budget check (after byte estimate update).
			if m.stopReason == "quota_soft" && m.hardHeadroomTokens > 0 &&
				m.outputTokensSinceSoftMark >= m.hardHeadroomTokens {
				m.stopReason = "quota_hard"
				m.sendHardInterrupt()
			}
		}

	case EventTypeResult:
		// Authoritative token count from result event.
		if m.stopReason == "quota_soft" || m.stopReason == "quota_hard" {
			var r resultEvent
			if err := json.Unmarshal(raw, &r); err == nil && r.Usage.OutputTokens > 0 {
				// Replace estimate with authoritative cumulative count.
				m.outputTokensSinceSoftMark = r.Usage.OutputTokens - m.outputTokensAtSoftMark
				if m.outputTokensSinceSoftMark < 0 {
					m.outputTokensSinceSoftMark = 0
				}
				// Hard budget check (after authoritative update).
				if m.stopReason == "quota_soft" && m.hardHeadroomTokens > 0 &&
					m.outputTokensSinceSoftMark >= m.hardHeadroomTokens {
					m.stopReason = "quota_hard"
					m.sendHardInterrupt()
				}
			}
		}
		// Signal turn-end (non-blocking; if buffer is full, the event is already queued).
		select {
		case m.turnEndedCh <- struct{}{}:
		default:
		}
	}
}

// handleTurnEnd is called when a result event is received (turn ended).
// Inspects stopReason to distinguish pause vs completion.
// Sets terminalEventPublished = true so handleSubprocessExit knows not to publish
// a session_job_failed event.
func (m *QuotaMonitor) handleTurnEnd() {
	m.terminalEventPublished = true

	switch m.stopReason {
	case "quota_soft":
		// Soft-paused: publish session_job_paused with pausedVia=quota_soft.
		extra := map[string]string{
			"pausedVia": "quota_soft",
			"r5":        m.lastR5.UTC().Format(time.RFC3339),
		}
		m.publishLifec(m.sessionID, "session_job_paused", extra)
		// Update session KV → status: paused.
		m.session.mu.Lock()
		m.session.state.State = StatusPaused
		m.session.state.PausedVia = "quota_soft"
		m.session.state.StateSince = time.Now().UTC()
		if m.autoContinue && !m.lastR5.IsZero() {
			resumeAt := m.lastR5
			m.session.state.ResumeAt = &resumeAt
		}
		st := m.session.state
		m.session.mu.Unlock()
		if m.writeKV != nil {
			_ = m.writeKV(st)
		}
		// Reset for next turn.
		m.stopReason = ""
		m.outputTokensSinceSoftMark = 0

	case "quota_hard":
		// Hard-paused: publish session_job_paused with pausedVia=quota_hard.
		extra := map[string]string{
			"pausedVia":                 "quota_hard",
			"r5":                        m.lastR5.UTC().Format(time.RFC3339),
			"outputTokensSinceSoftMark": fmt.Sprintf("%d", m.outputTokensSinceSoftMark),
		}
		m.publishLifec(m.sessionID, "session_job_paused", extra)
		// Update session KV → status: paused.
		m.session.mu.Lock()
		m.session.state.State = StatusPaused
		m.session.state.PausedVia = "quota_hard"
		m.session.state.StateSince = time.Now().UTC()
		if m.autoContinue && !m.lastR5.IsZero() {
			resumeAt := m.lastR5
			m.session.state.ResumeAt = &resumeAt
		}
		st := m.session.state
		m.session.mu.Unlock()
		if m.writeKV != nil {
			_ = m.writeKV(st)
		}
		// Reset for next turn.
		m.stopReason = ""
		m.outputTokensSinceSoftMark = 0

	case "":
		// Natural completion: Claude ended its turn without a platform-injected stop.
		m.publishLifec(m.sessionID, "session_job_complete", map[string]string{
			"sessionId": m.sessionID,
		})
		// Update session KV → status: completed.
		m.session.mu.Lock()
		m.session.state.State = StatusCompleted
		m.session.state.StateSince = time.Now().UTC()
		st := m.session.state
		m.session.mu.Unlock()
		if m.writeKV != nil {
			_ = m.writeKV(st)
		}

	case "permDenied":
		// session_permission_denied was already published by onStrictDeny.
		// KV → needs_spec_fix was set there too. Nothing more to publish here.

	default:
		// Unknown stopReason; treat as completion.
		m.publishLifec(m.sessionID, "session_job_complete", map[string]string{
			"sessionId": m.sessionID,
		})
	}
}

// handleSubprocessExit is called when session.doneCh closes (subprocess exited).
// If terminalEventPublished is true, the exit was expected (after completion or
// cancellation). Otherwise, publish session_job_failed.
func (m *QuotaMonitor) handleSubprocessExit() {
	if m.terminalEventPublished {
		// Expected cleanup after natural completion, quota pause, or cancel.
		return
	}
	// Subprocess exited without a turn-end signal → unexpected failure.
	m.publishLifec(m.sessionID, "session_job_failed", map[string]string{
		"error": "subprocess exited without turn-end signal",
	})
	// Update session KV → status: failed.
	m.session.mu.Lock()
	m.session.state.State = StateFailed
	m.session.state.StateSince = time.Now().UTC()
	st := m.session.state
	m.session.mu.Unlock()
	if m.writeKV != nil {
		_ = m.writeKV(st)
	}
}

// run is the main monitor goroutine. It selects on quota updates,
// permission-denied signals, turn-end signals, and the session done channel.
// Priority: turnEndedCh is checked before session.doneCh using a nested
// non-blocking select to ensure handleTurnEnd fires before handleSubprocessExit
// on natural completion (where both channels may become ready simultaneously).
func (m *QuotaMonitor) run() {
	defer m.quotaSub.Unsubscribe() //nolint:errcheck

	for {
		// Priority check: non-blockingly drain turnEndedCh before entering
		// the full select. This ensures that if both turnEndedCh and doneCh
		// are ready, we always handle turn-end first.
		select {
		case <-m.turnEndedCh:
			m.handleTurnEnd()
			continue
		default:
		}

		select {
		case <-m.stopCh:
			return

		case toolName := <-m.permDeniedCh:
			if m.stopReason == "" {
				m.stopReason = "permDenied"
				// Update session KV → needs_spec_fix.
				m.session.mu.Lock()
				m.session.state.State = StatusNeedsSpecFix
				m.session.state.FailedTool = toolName
				m.session.state.StateSince = time.Now().UTC()
				st := m.session.state
				m.session.mu.Unlock()
				if m.writeKV != nil {
					_ = m.writeKV(st)
				}
			}

		case msg := <-m.quotaCh:
			if msg == nil {
				continue
			}
			var qs QuotaStatus
			if err := json.Unmarshal(msg.Data, &qs); err != nil {
				continue
			}
			m.lastU5 = qs.U5
			m.lastR5 = qs.R5

			if !qs.HasData {
				// No data: do not trigger stop or start sessions.
				continue
			}

			// Soft threshold breached: inject quota_soft marker.
			if qs.U5 >= m.softThreshold && m.stopReason == "" {
				m.stopReason = "quota_soft"
				// Capture token count at soft mark.
				m.session.mu.Lock()
				m.outputTokensAtSoftMark = m.session.state.Usage.OutputTokens
				m.session.mu.Unlock()
				m.outputTokensSinceSoftMark = 0
				m.sendGracefulStop()

				// If hardHeadroomTokens == 0, fire hard interrupt immediately.
				if m.hardHeadroomTokens == 0 {
					m.stopReason = "quota_hard"
					m.sendHardInterrupt()
				}
				continue
			}

			// Quota available for pending session: send initial prompt.
			m.session.mu.Lock()
			sessionState := m.session.state.State
			m.session.mu.Unlock()
			if sessionState == StatusPending && qs.U5 < m.softThreshold {
				m.sendInitialPrompt()
				m.session.mu.Lock()
				m.session.state.State = StateRunning
				m.session.state.StateSince = time.Now().UTC()
				st := m.session.state
				m.session.mu.Unlock()
				if m.writeKV != nil {
					_ = m.writeKV(st)
				}
				continue
			}

			// Quota recovered for paused session: send resume nudge.
			if sessionState == StatusPaused && qs.U5 < m.softThreshold {
				if m.autoContinue {
					// For autoContinue sessions, check if resumeAt has passed.
					m.session.mu.Lock()
					resumeAt := m.session.state.ResumeAt
					m.session.mu.Unlock()
					if resumeAt != nil && time.Now().Before(*resumeAt) {
						// Not yet time to resume.
						continue
					}
				}
				m.sendResumeNudge()
				m.session.mu.Lock()
				m.session.state.State = StateRunning
				m.session.state.PausedVia = ""
				m.session.state.ResumeAt = nil
				m.session.state.StateSince = time.Now().UTC()
				st := m.session.state
				m.session.mu.Unlock()
				if m.writeKV != nil {
					_ = m.writeKV(st)
				}
			}

		case <-m.turnEndedCh:
			m.handleTurnEnd()

		case <-m.session.doneCh:
			// Priority: check turnEndedCh one more time before handling exit.
			// This covers the race where the result event fires and doneCh
			// closes at nearly the same time.
			select {
			case <-m.turnEndedCh:
				m.handleTurnEnd()
			default:
			}
			m.handleSubprocessExit()
			close(m.stopCh)
			return
		}
	}
}
