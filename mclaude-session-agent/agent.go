package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"
)

const (
	heartbeatInterval    = 30 * time.Second
	sessionDeleteTimeout = 10 * time.Second
	kvBucketSessions     = "mclaude-sessions"
	kvBucketProjects     = "mclaude-projects"
	kvBucketHeartbeats   = "mclaude-heartbeats"
)

// Agent manages all sessions for a single (userId, projectId) pair and owns
// the NATS subscriptions for the project API subjects.
type Agent struct {
	mu         sync.RWMutex
	sessions   map[string]*Session
	nc         *nats.Conn
	js         jetstream.JetStream
	sessKV     jetstream.KeyValue
	projKV     jetstream.KeyValue
	hbKV       jetstream.KeyValue
	userID     string
	projectID  string
	claudePath string
	log        zerolog.Logger
	metrics    *Metrics
}

// NewAgent creates an Agent connected to the given NATS server.
// m may be nil (no-op metrics) — pass NewMetrics(reg) in production.
func NewAgent(nc *nats.Conn, userID, projectID, claudePath string, log zerolog.Logger, m *Metrics) (*Agent, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	ctx := context.Background()

	sessKV, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: kvBucketSessions, History: 1})
	if err != nil {
		return nil, fmt.Errorf("sessions KV: %w", err)
	}
	projKV, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: kvBucketProjects, History: 1})
	if err != nil {
		return nil, fmt.Errorf("projects KV: %w", err)
	}
	hbKV, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: kvBucketHeartbeats, History: 1})
	if err != nil {
		return nil, fmt.Errorf("heartbeats KV: %w", err)
	}

	agent := &Agent{
		sessions:   make(map[string]*Session),
		nc:         nc,
		js:         js,
		sessKV:     sessKV,
		projKV:     projKV,
		hbKV:       hbKV,
		userID:     userID,
		projectID:  projectID,
		claudePath: claudePath,
		log:        log,
		metrics:    m,
	}

	// Wire NATS reconnect counter.
	nc.SetReconnectHandler(func(_ *nats.Conn) {
		log.Warn().Str("component", "session-agent").Msg("NATS reconnected")
		if m != nil {
			m.NATSReconnect()
		}
	})

	return agent, nil
}

// Run starts session recovery, NATS subscriptions, and the heartbeat loop.
// Blocks until ctx is cancelled, then performs graceful shutdown.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.recoverSessions(); err != nil {
		a.log.Warn().Err(err).Msg("session recovery failed — continuing without recovery")
	}
	if err := a.subscribeAPI(); err != nil {
		return err
	}
	a.runHeartbeat(ctx)
	<-ctx.Done()
	a.gracefulShutdown()
	return nil
}

// recoverSessions reads all existing sessions for this project from NATS KV
// and resumes each with --resume {sessionId}.
func (a *Agent) recoverSessions() error {
	ctx := context.Background()
	watcher, err := a.sessKV.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("KV watchAll: %w", err)
	}
	defer watcher.Stop()

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	for entry := range watcher.Updates() {
		if entry == nil {
			break // nil signals end of initial values
		}
		if entry.Operation() != jetstream.KeyValuePut {
			continue
		}
		var st SessionState
		if err := json.Unmarshal(entry.Value(), &st); err != nil {
			continue
		}
		if st.ProjectID != a.projectID || st.ID == "" {
			continue
		}
		// Clear transient state before resuming.
		clearPendingControlsForResume(&st)
		if wErr := a.writeSessionKV(st); wErr != nil {
			a.log.Warn().Err(wErr).Str("sessionId", st.ID).Msg("failed to clear pending controls")
		}
		sess := newSession(st, a.userID)
		sess.metrics = a.metrics
		if sErr := sess.start(a.claudePath, true, publish, a.writeSessionKV); sErr != nil {
			a.log.Warn().Err(sErr).Str("sessionId", st.ID).Msg("failed to resume session on startup")
			continue
		}
		a.mu.Lock()
		a.sessions[st.ID] = sess
		a.mu.Unlock()
		a.publishLifecycle(st.ID, "session_resumed")
		a.log.Info().Str("sessionId", st.ID).Msg("session resumed after startup")
		if a.metrics != nil {
			a.metrics.SessionOpened()
		}
	}
	return nil
}

// gracefulShutdown stops all active sessions on SIGTERM/cancel.
func (a *Agent) gracefulShutdown() {
	a.mu.RLock()
	ids := make([]string, 0, len(a.sessions))
	for id := range a.sessions {
		ids = append(ids, id)
	}
	a.mu.RUnlock()

	for _, id := range ids {
		a.mu.RLock()
		sess, ok := a.sessions[id]
		a.mu.RUnlock()
		if !ok {
			continue
		}
		if err := sess.stopAndWait(sessionDeleteTimeout); err != nil {
			a.log.Warn().Err(err).Str("sessionId", id).Msg("session did not stop cleanly")
		}
		a.publishLifecycle(id, "session_stopped")
	}
}

// subscribeAPI sets up NATS subscriptions for session CRUD and I/O.
func (a *Agent) subscribeAPI() error {
	prefix := fmt.Sprintf("mclaude.%s.%s.api.sessions.", a.userID, a.projectID)

	type entry struct {
		subject string
		handler nats.MsgHandler
	}
	entries := []entry{
		{prefix + "create", a.handleCreate},
		{prefix + "delete", a.handleDelete},
		{prefix + "input", a.handleInput},
		{prefix + "control", a.handleControl},
		{prefix + "restart", a.handleRestart},
	}

	for _, e := range entries {
		if _, err := a.nc.Subscribe(e.subject, e.handler); err != nil {
			return fmt.Errorf("subscribe %s: %w", e.subject, err)
		}
	}
	return nil
}

// handleCreate processes a sessions.create request/reply.
// Payload: {name, branch, cwd, joinWorktree}
// Reply:   {id} or {error}
func (a *Agent) handleCreate(msg *nats.Msg) {
	var req struct {
		Name         string `json:"name"`
		Branch       string `json:"branch"`
		CWD          string `json:"cwd"`
		JoinWorktree bool   `json:"joinWorktree"`
	}
	if len(msg.Data) > 0 {
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			a.reply(msg, nil, "invalid request: "+err.Error())
			return
		}
	}
	if req.Branch == "" {
		req.Branch = "main"
	}

	branchSlug := SlugifyBranch(req.Branch)
	sessionID := uuid.NewString()

	// Check for worktree collision.
	a.mu.RLock()
	collision := a.worktreeInUse(branchSlug)
	a.mu.RUnlock()

	if collision && !req.JoinWorktree {
		a.reply(msg, nil, "worktree already in use for branch "+req.Branch)
		return
	}

	cwd := "/data/worktrees/" + branchSlug
	if req.CWD != "" {
		cwd = "/data/worktrees/" + branchSlug + "/" + req.CWD
	}

	now := time.Now().UTC()
	state := SessionState{
		ID:              sessionID,
		ProjectID:       a.projectID,
		Branch:          req.Branch,
		Worktree:        branchSlug,
		CWD:             cwd,
		Name:            req.Name,
		State:           StateIdle,
		StateSince:      now,
		CreatedAt:       now,
		JoinWorktree:    req.JoinWorktree,
		PendingControls: make(map[string]any),
	}

	if err := a.writeSessionKV(state); err != nil {
		a.log.Error().Err(err).Str("sessionId", sessionID).Msg("failed to write initial session KV")
		a.reply(msg, nil, "KV write failed: "+err.Error())
		return
	}

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	sess := newSession(state, a.userID)
	sess.metrics = a.metrics
	if err := sess.start(a.claudePath, false, publish, a.writeSessionKV); err != nil {
		a.log.Error().Err(err).Str("sessionId", sessionID).Msg("failed to start claude")
		a.reply(msg, nil, "start claude: "+err.Error())
		return
	}

	a.mu.Lock()
	a.sessions[sessionID] = sess
	a.mu.Unlock()

	a.publishLifecycle(sessionID, "session_created")

	a.log.Info().
		Str("component", "session-agent").
		Str("userId", a.userID).
		Str("projectId", a.projectID).
		Str("sessionId", sessionID).
		Msg("session created")

	if a.metrics != nil {
		a.metrics.SessionOpened()
	}

	a.reply(msg, map[string]string{"id": sessionID}, "")
}

// handleDelete processes a sessions.delete request/reply.
// Payload: {sessionId}
// Reply:   {} or {error}
func (a *Agent) handleDelete(msg *nats.Msg) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.SessionID == "" {
		a.reply(msg, nil, "invalid request: missing sessionId")
		return
	}

	a.mu.Lock()
	sess, ok := a.sessions[req.SessionID]
	if ok {
		delete(a.sessions, req.SessionID)
	}
	a.mu.Unlock()

	if !ok {
		a.reply(msg, nil, "session not found: "+req.SessionID)
		return
	}

	if err := sess.stopAndWait(sessionDeleteTimeout); err != nil {
		a.log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("session did not stop cleanly")
	}

	// Delete from KV.
	key := sessionKVKey(a.userID, a.projectID, req.SessionID)
	_ = a.sessKV.Delete(context.Background(), key)

	a.publishLifecycle(req.SessionID, "session_stopped")

	a.log.Info().
		Str("sessionId", req.SessionID).
		Msg("session deleted")

	if a.metrics != nil {
		a.metrics.SessionClosed()
	}

	a.reply(msg, map[string]string{}, "")
}

// handleInput routes a user message to the target session's stdin.
// Payload: raw stream-json user message (must contain session_id field).
// No reply — fire and forget.
func (a *Agent) handleInput(msg *nats.Msg) {
	var header struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(msg.Data, &header); err != nil || header.SessionID == "" {
		a.log.Warn().Msg("sessions.input: missing session_id")
		return
	}

	a.mu.RLock()
	sess, ok := a.sessions[header.SessionID]
	a.mu.RUnlock()

	if !ok {
		a.log.Warn().Str("sessionId", header.SessionID).Msg("sessions.input: session not found")
		return
	}

	sess.sendInput(msg.Data)
}

// handleControl routes a control message (permission response, interrupt, model
// change) to the appropriate session's stdin.
// Payload: {type: "control_response", response: {request_id, ...}} or
//          {type: "control_request", request: {subtype: "interrupt"/"set_model"}}
// No reply — fire and forget.
func (a *Agent) handleControl(msg *nats.Msg) {
	var envelope struct {
		Type     string          `json:"type"`
		Response controlResponse `json:"response"`
	}
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		a.log.Warn().Err(err).Msg("sessions.control: failed to parse envelope")
		return
	}

	switch envelope.Type {
	case "control_response":
		// Route to the session that owns this request_id.
		requestID := envelope.Response.RequestID
		if requestID == "" {
			a.log.Warn().Msg("sessions.control: control_response missing request_id")
			return
		}
		sess := a.sessionForRequest(requestID)
		if sess == nil {
			a.log.Warn().Str("requestId", requestID).Msg("sessions.control: no session owns request_id")
			return
		}
		sess.sendInput(msg.Data)
		sess.clearPendingControl(requestID, a.writeSessionKV)

	default:
		// control_request (interrupt, set_model, etc.) — broadcast to all sessions.
		// In the common case there is exactly one active session per project.
		a.mu.RLock()
		sessions := make([]*Session, 0, len(a.sessions))
		for _, s := range a.sessions {
			sessions = append(sessions, s)
		}
		a.mu.RUnlock()
		for _, s := range sessions {
			s.sendInput(msg.Data)
		}
	}
}

// handleRestart stops a session and relaunches it with --resume.
// Payload: {sessionId}
// Reply:   {} or {error}
func (a *Agent) handleRestart(msg *nats.Msg) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.SessionID == "" {
		a.reply(msg, nil, "invalid request: missing sessionId")
		return
	}

	a.mu.Lock()
	sess, ok := a.sessions[req.SessionID]
	a.mu.Unlock()

	if !ok {
		a.reply(msg, nil, "session not found: "+req.SessionID)
		return
	}

	a.publishLifecycle(req.SessionID, "session_restarting")

	if err := sess.stopAndWait(sessionDeleteTimeout); err != nil {
		a.log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("session stop before restart did not complete cleanly")
	}

	// Read current state, clear pending controls.
	st := sess.getState()
	clearPendingControlsForResume(&st)
	if err := a.writeSessionKV(st); err != nil {
		a.log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("failed to write KV before restart")
	}

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	// Relaunch with --resume.
	newSess := newSession(st, a.userID)
	newSess.metrics = a.metrics
	if err := newSess.start(a.claudePath, true, publish, a.writeSessionKV); err != nil {
		a.log.Error().Err(err).Str("sessionId", req.SessionID).Msg("failed to resume session")
		a.reply(msg, nil, "resume failed: "+err.Error())
		return
	}

	a.mu.Lock()
	a.sessions[req.SessionID] = newSess
	a.mu.Unlock()

	a.publishLifecycle(req.SessionID, "session_resumed")

	if a.metrics != nil {
		a.metrics.ClaudeRestart()
	}

	a.log.Info().Str("sessionId", req.SessionID).Msg("session restarted")
	a.reply(msg, map[string]string{}, "")
}

// runHeartbeat writes the project heartbeat to NATS KV every 30s.
func (a *Agent) runHeartbeat(ctx context.Context) {
	go func() {
		tick := time.NewTicker(heartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				key := heartbeatKVKey(a.userID, a.projectID)
				val := []byte(fmt.Sprintf(`{"ts":%q}`, time.Now().UTC().Format(time.RFC3339)))
				_, _ = a.hbKV.Put(ctx, key, val)
			}
		}
	}()
}

// writeSessionKV serialises and persists a SessionState to NATS KV.
func (a *Agent) writeSessionKV(state SessionState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	key := sessionKVKey(a.userID, state.ProjectID, state.ID)
	_, span := KVWriteSpan(context.Background(), kvBucketSessions, key)
	_, err = a.sessKV.Put(context.Background(), key, data)
	span.End()
	return err
}

// publishLifecycle publishes a lifecycle event on the project's lifecycle subject.
func (a *Agent) publishLifecycle(sessionID, eventType string) {
	subject := fmt.Sprintf("mclaude.%s.%s.lifecycle.%s", a.userID, a.projectID, sessionID)
	payload, _ := json.Marshal(map[string]string{
		"type":      eventType,
		"sessionId": sessionID,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// reply sends a NATS reply. If errMsg is non-empty, sends {error: errMsg}.
// If data is nil and errMsg is empty, sends {}.
func (a *Agent) reply(msg *nats.Msg, data any, errMsg string) {
	if msg.Reply == "" {
		return
	}
	var b []byte
	if errMsg != "" {
		b, _ = json.Marshal(map[string]string{"error": errMsg})
	} else if data != nil {
		b, _ = json.Marshal(data)
	} else {
		b = []byte("{}")
	}
	_ = msg.Respond(b)
}

// worktreeInUse returns true if any active session uses the given branch slug.
// Caller must hold at least a.mu.RLock().
func (a *Agent) worktreeInUse(slug string) bool {
	for _, s := range a.sessions {
		st := s.getState()
		if st.Worktree == slug {
			return true
		}
	}
	return false
}

// sessionForRequest returns the session that owns the given pending control request_id.
func (a *Agent) sessionForRequest(requestID string) *Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, s := range a.sessions {
		st := s.getState()
		if _, ok := st.PendingControls[requestID]; ok {
			return s
		}
	}
	return nil
}

// controlResponse is the inner object of a control_response message.
type controlResponse struct {
	RequestID string `json:"request_id"`
}
