package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// QuotaMonitor watches the mclaude.{userId}.quota subject for quota status
// updates and gracefully stops a session when the 5h utilization threshold is
// exceeded. One goroutine per session; created in handleCreate when the
// sessions.create request includes a QuotaMonitor config.
type QuotaMonitor struct {
	sessionID    string
	userID       string
	projectID    string
	branch       string // git branch (e.g. "schedule/spa-abc12345")
	cfg          QuotaMonitorConfig
	nc           *nats.Conn
	session      *Session
	publishLifec func(sessionID, evType string, extra map[string]string)
	permDeniedCh chan string          // receives toolName on strict-allowlist deny
	quotaCh      chan *nats.Msg       // receives quota status updates from NATS
	quotaSub     *nats.Subscription  // subscription to mclaude.{userID}.quota
	stopCh       chan struct{}        // closed to exit the monitor goroutine
	lastU5                   int       // last 5h utilization %
	lastR5                   time.Time // last 5h reset time
	outputTokensAtSoftMark   int       // outputTokens snapshot when soft threshold was reached
	completionPR             string    // set by onRawOutput when SESSION_JOB_COMPLETE detected
	stopTimeout              time.Duration // timeout before hard interrupt; 0 = use default (30 min)
}

// newQuotaMonitor creates a QuotaMonitor, subscribes to quota updates,
// starts the monitor goroutine, and returns. Called from handleCreate.
// userSlug is used (not UUID) so the subscription subject matches the slug-based
// subject the daemon publishes to (spec: GAP-SA-K11).
func newQuotaMonitor(
	sessionID, userSlug, projectID, branch string,
	cfg QuotaMonitorConfig,
	nc *nats.Conn,
	sess *Session,
	publishLifec func(sessionID, evType string, extra map[string]string),
) (*QuotaMonitor, error) {
	quotaCh := make(chan *nats.Msg, 16)
	subject := "mclaude.users." + userSlug + ".quota"
	quotaSub, err := nc.ChanSubscribe(subject, quotaCh)
	if err != nil {
		return nil, fmt.Errorf("quota subscribe: %w", err)
	}

	m := &QuotaMonitor{
		sessionID:    sessionID,
		userID:       userSlug,
		projectID:    projectID,
		branch:       branch,
		cfg:          cfg,
		nc:           nc,
		session:      sess,
		publishLifec: publishLifec,
		permDeniedCh: make(chan string, 1),
		quotaCh:      quotaCh,
		quotaSub:     quotaSub,
		stopCh:       make(chan struct{}),
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

// onRawOutput is called for each raw stdout line from Claude (via
// Session.onRawOutput). Scans assistant events for the SESSION_JOB_COMPLETE
// marker and records the PR URL.
func (m *QuotaMonitor) onRawOutput(evType string, raw []byte) {
	if evType != EventTypeAssistant {
		return
	}
	const marker = "SESSION_JOB_COMPLETE:"
	idx := bytes.Index(raw, []byte(marker))
	if idx == -1 {
		return
	}
	// Extract the PR URL: everything after the marker until whitespace or end
	// of string value. Take up to 200 bytes to be safe.
	rest := raw[idx+len(marker):]
	end := bytes.IndexAny(rest, " \t\n\r\"}")
	if end == -1 {
		end = len(rest)
	}
	if end > 200 {
		end = 200
	}
	m.completionPR = string(rest[:end])
}

// sendGracefulStop queues the quota threshold message on the session's
// stdin channel. This tells Claude to finish its current task and stop.
func (m *QuotaMonitor) sendGracefulStop() {
	msg, _ := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": "QUOTA_THRESHOLD_REACHED: The 5-hour API quota threshold has been reached. Please finish your current task and commit all changes, run --audit-only to generate a gap report and output the full results, then stop. Do not start any new tasks.",
		},
	})
	select {
	case m.session.stdinCh <- msg:
	default:
	}
}

// sendHardInterrupt queues an interrupt control_request on the session's
// stdin channel. Called after the graceful stop timeout expires.
func (m *QuotaMonitor) sendHardInterrupt() {
	interrupt := []byte(`{"type":"control_request","request":{"subtype":"interrupt"}}`)
	select {
	case m.session.stdinCh <- interrupt:
	default:
	}
}

// publishExitLifecycle determines the exit reason and publishes the
// appropriate lifecycle event. Called when session.doneCh is closed.
func (m *QuotaMonitor) publishExitLifecycle(stopReason string) {
	switch {
	case m.completionPR != "":
		m.publishLifec(m.sessionID, "session_job_complete", map[string]string{
			"jobId":  m.cfg.JobID,
			"branch": m.branch,
		})
	case stopReason == "quota":
		// Compute actual output tokens since the soft mark was set.
		currentOutputTokens := m.session.getState().Usage.OutputTokens
		tokensSinceSoftMark := currentOutputTokens - m.outputTokensAtSoftMark
		if tokensSinceSoftMark < 0 {
			tokensSinceSoftMark = 0
		}
		m.publishLifec(m.sessionID, "session_job_paused", map[string]string{
			"jobId":                    m.cfg.JobID,
			"pausedVia":               "quota_threshold",
			"r5":                      m.lastR5.UTC().Format(time.RFC3339),
			"outputTokensSinceSoftMark": fmt.Sprintf("%d", tokensSinceSoftMark),
		})
	case stopReason == "permDenied":
		// session_permission_denied was already published synchronously by
		// onStrictDeny. Publishing a second event would overwrite the
		// needs_spec_fix status in KV.
	default:
		m.publishLifec(m.sessionID, "session_job_failed", map[string]string{
			"jobId": m.cfg.JobID,
			"error": "session exited without completion marker",
		})
	}
}

// run is the main monitor goroutine. It selects on quota updates,
// permission-denied signals, and the session done channel.
func (m *QuotaMonitor) run() {
	defer m.quotaSub.Unsubscribe() //nolint:errcheck

	stopReason := ""
	var stopTimer <-chan time.Time

	for {
		select {
		case <-m.stopCh:
			return

		case toolName := <-m.permDeniedCh:
			if stopReason == "" {
				stopReason = "permDenied"
				_ = toolName // already published by onStrictDeny
				m.sendGracefulStop()
				d := m.stopTimeout
				if d == 0 {
					d = 30 * time.Minute
				}
				stopTimer = time.After(d)
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
			if qs.HasData && m.cfg.Threshold > 0 && qs.U5 >= m.cfg.Threshold && stopReason == "" {
				stopReason = "quota"
				m.outputTokensAtSoftMark = m.session.getState().Usage.OutputTokens
				m.sendGracefulStop()
				d := m.stopTimeout
				if d == 0 {
					d = 30 * time.Minute
				}
				stopTimer = time.After(d)
			}

		case <-stopTimer:
			m.sendHardInterrupt()
			// Reset the timer to nil so we don't fire again.
			stopTimer = nil

		case <-m.session.doneCh:
			m.publishExitLifecycle(stopReason)
			close(m.stopCh)
			return
		}
	}
}
