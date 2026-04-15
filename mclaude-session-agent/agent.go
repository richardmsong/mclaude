package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	terminals  map[string]*TerminalSession
	nc         *nats.Conn
	js         jetstream.JetStream
	sessKV     jetstream.KeyValue
	projKV     jetstream.KeyValue
	hbKV       jetstream.KeyValue
	userID     string
	projectID  string
	claudePath string
	// dataDir is the root of the project data volume (e.g. /data).
	// Used to compute worktree paths: {dataDir}/worktrees/{branchSlug}.
	// When empty, git worktree operations are skipped (laptop/dev mode without PVC).
	dataDir    string
	log        zerolog.Logger
	metrics    *Metrics
	// subs holds all active core NATS subscriptions (terminal API) so they can
	// be drained on graceful shutdown.
	subs       []*nats.Subscription
	// cmdConsumer and ctlConsumer are the JetStream pull consumers for API subjects.
	cmdConsumer jetstream.Consumer
	ctlConsumer jetstream.Consumer
	// cmdCancel cancels the command consumer fetch loop.
	cmdCancel context.CancelFunc
	// ctlCancel cancels the control consumer fetch loop.
	ctlCancel context.CancelFunc
	// doExit is called at the end of gracefulShutdown. Defaults to os.Exit(0).
	// Overridable in tests to prevent process exit.
	doExit func(code int)
	// pendingUpdatingIDs tracks session IDs that were in "updating" state during
	// recovery. clearUpdatingState() uses this to write idle to KV after consumers
	// are attached, since the in-memory state is already idle.
	pendingUpdatingIDs map[string]bool
}

// NewAgent creates an Agent connected to the given NATS server.
// m may be nil (no-op metrics) — pass NewMetrics(reg) in production.
// dataDir is the project PVC mount point (e.g. "/data"); pass "" to skip git
// worktree operations (dev/laptop mode without a bare repo).
func NewAgent(nc *nats.Conn, userID, projectID, claudePath, dataDir string, log zerolog.Logger, m *Metrics) (*Agent, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	ctx := context.Background()

	// Session agents fail fast if buckets don't exist — bucket creation is
	// owned by the control-plane.  Presence of buckets confirms the
	// control-plane has started successfully.
	sessKV, err := js.KeyValue(ctx, kvBucketSessions)
	if err != nil {
		return nil, fmt.Errorf("sessions KV bucket not found (control-plane not started?): %w", err)
	}
	projKV, err := js.KeyValue(ctx, kvBucketProjects)
	if err != nil {
		return nil, fmt.Errorf("projects KV bucket not found (control-plane not started?): %w", err)
	}
	hbKV, err := js.KeyValue(ctx, kvBucketHeartbeats)
	if err != nil {
		return nil, fmt.Errorf("heartbeats KV bucket not found (control-plane not started?): %w", err)
	}

	// Ensure the MCLAUDE_EVENTS stream exists. CreateOrUpdateStream is
	// idempotent: if the stream already exists with compatible config it is a
	// no-op; if it exists with different config it is updated; if it does not
	// exist it is created. The session-agent is authoritative for this stream.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "MCLAUDE_EVENTS",
		Subjects:  []string{"mclaude.*.*.events.*"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
		Storage:   jetstream.FileStorage,
		Discard:   jetstream.DiscardOld,
	}); err != nil {
		return nil, fmt.Errorf("ensure MCLAUDE_EVENTS stream: %w", err)
	}

	// Ensure the MCLAUDE_API stream exists. This stream captures all session API
	// subjects for at-least-once delivery. Created idempotently by each agent.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "MCLAUDE_API",
		Subjects:  []string{"mclaude.*.*.api.sessions.>"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    1 * time.Hour,
		Storage:   jetstream.FileStorage,
		Discard:   jetstream.DiscardOld,
	}); err != nil {
		return nil, fmt.Errorf("ensure MCLAUDE_API stream: %w", err)
	}

	agent := &Agent{
		sessions:   make(map[string]*Session),
		terminals:  make(map[string]*TerminalSession),
		nc:         nc,
		js:         js,
		sessKV:     sessKV,
		projKV:     projKV,
		hbKV:       hbKV,
		userID:     userID,
		projectID:  projectID,
		claudePath: claudePath,
		dataDir:    dataDir,
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

// Run starts session recovery, JetStream consumers, terminal NATS subscriptions,
// the heartbeat loop, and waits for ctx cancellation before graceful shutdown.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.recoverSessions(); err != nil {
		a.log.Warn().Err(err).Msg("session recovery failed — continuing without recovery")
	}
	if err := a.createJetStreamConsumers(); err != nil {
		return err
	}
	if err := a.subscribeTerminalAPI(); err != nil {
		return err
	}
	if err := a.clearUpdatingState(); err != nil {
		a.log.Warn().Err(err).Msg("clearUpdatingState failed — continuing")
	}
	a.runHeartbeat(ctx)
	<-ctx.Done()
	a.gracefulShutdown()
	return nil
}

// recoverSessions reads all existing sessions for this project from NATS KV
// and resumes each with --resume {sessionId}.
// Sessions in "updating" state are resumed but their KV entry is NOT updated yet
// (the "updating" banner stays visible in the UI until clearUpdatingState() runs).
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

		wasUpdating := st.State == StateUpdating

		// Clear transient state before resuming.
		clearPendingControlsForResume(&st)

		// Write the cleared state to KV — but only if the session was NOT in
		// "updating" state. For "updating" sessions we keep the KV entry as-is
		// so the UI banner remains visible. clearUpdatingState() will write
		// state:"idle" later, after consumers are attached and the agent is ready.
		if wasUpdating {
			if a.pendingUpdatingIDs == nil {
				a.pendingUpdatingIDs = make(map[string]bool)
			}
			a.pendingUpdatingIDs[st.ID] = true
		}
		if !wasUpdating {
			if wErr := a.writeSessionKV(st); wErr != nil {
				a.log.Warn().Err(wErr).Str("sessionId", st.ID).Msg("failed to clear pending controls")
			}
		}

		sess := newSession(st, a.userID)
		sess.metrics = a.metrics

		// Start debug unix socket for mclaude-cli attach.
		sessID := st.ID
		dbg := NewDebugServer(sessID,
			func(data []byte) { sess.sendInput(data) },
			func() { a.publishLifecycle(sessID, "debug_attached") },
			func() { a.publishLifecycle(sessID, "debug_detached") },
		)
		if err := dbg.Start(); err != nil {
			a.log.Warn().Err(err).Str("sessionId", sessID).Msg("debug socket start failed on recovery (non-fatal)")
		} else {
			sess.mu.Lock()
			sess.debug = dbg
			sess.mu.Unlock()
		}

		if sErr := sess.start(a.claudePath, true, publish, a.writeSessionKV); sErr != nil {
			dbg.Stop()
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

// createJetStreamConsumers creates (or attaches to existing) durable pull consumers
// for the command and control API subjects, then starts their fetch goroutines.
func (a *Agent) createJetStreamConsumers() error {
	ctx := context.Background()
	stream, err := a.js.Stream(ctx, "MCLAUDE_API")
	if err != nil {
		return fmt.Errorf("MCLAUDE_API stream lookup: %w", err)
	}

	cmdName := fmt.Sprintf("sa-cmd-%s-%s", a.userID, a.projectID)
	ctlName := fmt.Sprintf("sa-ctl-%s-%s", a.userID, a.projectID)
	prefix := fmt.Sprintf("mclaude.%s.%s.api.sessions.", a.userID, a.projectID)

	cmdCons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:        cmdName,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        60 * time.Second,
		MaxDeliver:     5,
		FilterSubjects: []string{
			prefix + "create",
			prefix + "delete",
			prefix + "input",
			prefix + "restart",
		},
	})
	if err != nil {
		return fmt.Errorf("create cmd consumer: %w", err)
	}

	ctlCons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:        ctlName,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        60 * time.Second,
		MaxDeliver:     5,
		FilterSubjects: []string{prefix + "control"},
	})
	if err != nil {
		return fmt.Errorf("create ctl consumer: %w", err)
	}

	a.cmdConsumer = cmdCons
	a.ctlConsumer = ctlCons

	cmdCtx, cmdCancel := context.WithCancel(context.Background())
	ctlCtx, ctlCancel := context.WithCancel(context.Background())
	a.cmdCancel = cmdCancel
	a.ctlCancel = ctlCancel

	go a.runConsumer(cmdCtx, cmdCons, a.dispatchCmd)
	go a.runConsumer(ctlCtx, ctlCons, a.dispatchCtl)

	return nil
}

// runConsumer runs a pull-based JetStream fetch loop, dispatching each message.
// Stops when ctx is cancelled.
func (a *Agent) runConsumer(ctx context.Context, cons jetstream.Consumer, dispatch func(jetstream.Msg)) {
	backoff := 100 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := cons.Fetch(10, jetstream.FetchMaxWait(5*time.Second))
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log.Warn().Err(err).Msg("JetStream fetch error — backing off")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 5*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 100 * time.Millisecond
		for msg := range msgs.Messages() {
			dispatch(msg)
			if err := msg.Ack(); err != nil {
				a.log.Warn().Err(err).Str("subject", msg.Subject()).Msg("JetStream ack failed")
			}
		}
	}
}

// jsToNatsMsg wraps a jetstream.Msg into a *nats.Msg for handler compatibility.
// The wrapped msg has .Data, .Subject, and .Header populated.
// .Reply is empty — handlers must not call msg.Respond() (reply() is a no-op
// when msg.Reply == "").
func jsToNatsMsg(jm jetstream.Msg) *nats.Msg {
	return &nats.Msg{
		Subject: jm.Subject(),
		Data:    jm.Data(),
		Header:  jm.Headers(),
	}
}

// dispatchCmd routes a command consumer message to the appropriate handler.
func (a *Agent) dispatchCmd(jm jetstream.Msg) {
	msg := jsToNatsMsg(jm)
	switch {
	case strings.HasSuffix(jm.Subject(), ".create"):
		a.handleCreate(msg)
	case strings.HasSuffix(jm.Subject(), ".delete"):
		a.handleDelete(msg)
	case strings.HasSuffix(jm.Subject(), ".input"):
		a.handleInput(msg)
	case strings.HasSuffix(jm.Subject(), ".restart"):
		a.handleRestart(msg)
	default:
		a.log.Warn().Str("subject", jm.Subject()).Msg("dispatchCmd: unrecognised subject")
	}
}

// dispatchCtl routes a control consumer message to the handleControl handler.
func (a *Agent) dispatchCtl(jm jetstream.Msg) {
	a.handleControl(jsToNatsMsg(jm))
}

// clearUpdatingState writes state:"idle" to KV for all sessions currently
// in "updating" state. Called after JetStream consumers are attached and the
// agent is ready to process queued messages.
func (a *Agent) clearUpdatingState() error {
	a.mu.RLock()
	sessions := make([]*Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.RUnlock()

	for _, sess := range sessions {
		st := sess.getState()
		if a.pendingUpdatingIDs[st.ID] {
			st.State = StateIdle
			st.StateSince = time.Now().UTC()
			if err := a.writeSessionKV(st); err != nil {
				a.log.Warn().Err(err).Str("sessionId", st.ID).Msg("clearUpdatingState: KV write failed")
			}
			delete(a.pendingUpdatingIDs, st.ID)
		}
	}
	return nil
}

// gracefulShutdown implements the spec shutdown sequence for SIGTERM:
//
//  1. Write state:"updating" to session KV for all sessions.
//  2. Cancel command consumer context (stops cmd fetch loop; messages queue in JetStream).
//  3. Drain core NATS subscriptions (terminal API).
//  4. Keep control consumer running.
//  5. Poll 1s: wait for all sessions to be idle or updating.
//  6. Cancel control consumer context.
//  7. Publish lifecycle "session_upgrading" for each session.
//  8. Exit(0).
func (a *Agent) gracefulShutdown() {
	// Step 1: write state:"updating" for all sessions.
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
		st := sess.getState()
		st.State = StateUpdating
		st.StateSince = time.Now().UTC()
		sess.mu.Lock()
		sess.state.State = StateUpdating
		sess.state.StateSince = st.StateSince
		sess.mu.Unlock()
		if err := a.writeSessionKV(st); err != nil {
			a.log.Warn().Err(err).Str("sessionId", id).Msg("gracefulShutdown: failed to write updating state")
		}
	}

	// Step 2: stop the command consumer (new work queues in JetStream).
	if a.cmdCancel != nil {
		a.cmdCancel()
	}

	// Step 3: drain core NATS subscriptions (terminal API).
	a.mu.RLock()
	subs := make([]*nats.Subscription, len(a.subs))
	copy(subs, a.subs)
	a.mu.RUnlock()

	for _, sub := range subs {
		if err := sub.Drain(); err != nil {
			a.log.Warn().Err(err).Str("subject", sub.Subject).Msg("subscription drain failed")
		}
	}

	// Step 4 & 5: keep control consumer running; poll until all sessions idle/updating.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.mu.RLock()
		allDone := true
		for _, sess := range a.sessions {
			st := sess.getState()
			if st.State != StateIdle && st.State != StateUpdating {
				allDone = false
				break
			}
		}
		a.mu.RUnlock()
		if allDone {
			break
		}
	}

	// Step 6: stop the control consumer.
	if a.ctlCancel != nil {
		a.ctlCancel()
	}

	// Step 7: publish lifecycle "session_upgrading" for each session.
	for _, id := range ids {
		a.publishLifecycle(id, "session_upgrading")
	}

	// Step 8: exit.
	exitFn := a.doExit
	if exitFn == nil {
		exitFn = os.Exit
	}
	exitFn(0)
}

// subscribeTerminalAPI sets up core NATS subscriptions for terminal management.
// Terminal I/O is latency-sensitive and stays on core NATS (ephemeral).
func (a *Agent) subscribeTerminalAPI() error {
	termPrefix := fmt.Sprintf("mclaude.%s.%s.api.terminal.", a.userID, a.projectID)

	type entry struct {
		subject string
		handler nats.MsgHandler
	}
	entries := []entry{
		{termPrefix + "create", a.handleTerminalCreate},
		{termPrefix + "delete", a.handleTerminalDelete},
		{termPrefix + "resize", a.handleTerminalResize},
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range entries {
		sub, err := a.nc.Subscribe(e.subject, e.handler)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", e.subject, err)
		}
		a.subs = append(a.subs, sub)
	}
	return nil
}

// publishAPIError publishes an api_error event to the project-level _api subject.
// Used when a create/delete/restart handler encounters an error.
func (a *Agent) publishAPIError(requestID, operation, errMsg string) {
	subject := fmt.Sprintf("mclaude.%s.%s.events._api", a.userID, a.projectID)
	payload, _ := json.Marshal(map[string]string{
		"type":       "api_error",
		"request_id": requestID,
		"operation":  operation,
		"error":      errMsg,
	})
	_ = a.nc.Publish(subject, payload)
}

// handleCreate processes a sessions.create request.
// Payload: {name, branch, cwd, joinWorktree, requestId}
// Success: session appears in KV (SPA watches KV).
// Error: publish api_error event to mclaude.{userId}.{projectId}.events._api.
func (a *Agent) handleCreate(msg *nats.Msg) {
	var req struct {
		Name         string `json:"name"`
		Branch       string `json:"branch"`
		CWD          string `json:"cwd"`
		JoinWorktree bool   `json:"joinWorktree"`
		RequestID    string `json:"requestId"`
	}
	if len(msg.Data) > 0 {
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			a.reply(msg, nil, "invalid request: "+err.Error())
			a.publishAPIError(req.RequestID, "create", "invalid request: "+err.Error())
			return
		}
	}

	sessionID := uuid.NewString()

	// Resolve the effective data directory (fall back to /data when unset).
	dataDir := a.dataDir
	if dataDir == "" {
		dataDir = "/data"
	}

	repoPath := filepath.Join(dataDir, "repo")

	// Step 1 (spec): Derive branch.
	// If branch is empty, slugify name. If both are empty, use session-{shortId}.
	if req.Branch == "" {
		if req.Name != "" {
			req.Branch = SlugifyBranch(req.Name)
		} else {
			req.Branch = "session-" + sessionID[:8]
		}
	}

	branch := req.Branch
	branchSlug := SlugifyBranch(branch)
	worktreePath := filepath.Join(dataDir, "worktrees", branchSlug)
	cwd := worktreePath
	if req.CWD != "" {
		cwd = filepath.Join(worktreePath, req.CWD)
	}

	// Check for worktree collision (step 4).
	a.mu.RLock()
	collision := a.worktreeInUse(branchSlug)
	a.mu.RUnlock()

	if collision && !req.JoinWorktree {
		// Step 5: error if not joining.
		errMsg := "worktree already in use for branch " + req.Branch
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "create", errMsg)
		return
	}

	// Step 7: create worktree if not joining an existing one.
	if !collision {
		if err := a.gitWorktreeAdd(repoPath, worktreePath, branch); err != nil {
			a.log.Error().Err(err).
				Str("branch", branch).
				Str("worktreePath", worktreePath).
				Msg("git worktree add failed")
			errMsg := "git worktree add: " + err.Error()
			a.reply(msg, nil, errMsg)
			a.publishAPIError(req.RequestID, "create", errMsg)
			return
		}
	}

	now := time.Now().UTC()
	state := SessionState{
		ID:              sessionID,
		ProjectID:       a.projectID,
		Branch:          branch,
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
		errMsg := "KV write failed: " + err.Error()
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "create", errMsg)
		return
	}

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	sess := newSession(state, a.userID)
	sess.metrics = a.metrics

	// Wire the onEventPublished callback so that compact_boundary events update
	// replayFromSeq in KV.  The seq argument is the JetStream sequence number
	// of the published message (0 for core NATS publishes that aren't ack'd).
	// We use a js.Publish override via a separate JetStream publish for the
	// compact_boundary event to get its seq; for other events we use core NATS.
	sessIDForCB := sessionID
	sess.onEventPublished = func(evType string, seq uint64) {
		if evType != EventTypeCompactBoundary {
			return
		}
		// When the agent uses core NATS (seq==0), we can ask JetStream for the
		// last sequence on the events stream for this session subject.
		// This is a best-effort update; failures are non-fatal.
		a.updateReplayFromSeq(sessIDForCB)
	}

	// Start debug unix socket for mclaude-cli attach.
	dbg := NewDebugServer(sessionID,
		func(data []byte) { sess.sendInput(data) },
		func() { a.publishLifecycle(sessionID, "debug_attached") },
		func() { a.publishLifecycle(sessionID, "debug_detached") },
	)
	if err := dbg.Start(); err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessionID).Msg("debug socket start failed (non-fatal)")
		// Non-fatal — CLI attach won't work but sessions still function.
	} else {
		sess.mu.Lock()
		sess.debug = dbg
		sess.mu.Unlock()
	}

	if err := sess.start(a.claudePath, false, publish, a.writeSessionKV); err != nil {
		dbg.Stop()
		a.log.Error().Err(err).Str("sessionId", sessionID).Msg("failed to start claude")
		a.publishLifecycleFailed(sessionID, err.Error())
		errMsg := "start claude: " + err.Error()
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "create", errMsg)
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

// handleDelete processes a sessions.delete request.
// Payload: {sessionId, requestId}
// Success: session disappears from KV (SPA watches KV).
// Error: publish api_error event.
func (a *Agent) handleDelete(msg *nats.Msg) {
	var req struct {
		SessionID string `json:"sessionId"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.SessionID == "" {
		errMsg := "invalid request: missing sessionId"
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "delete", errMsg)
		return
	}

	a.mu.Lock()
	sess, ok := a.sessions[req.SessionID]
	if ok {
		delete(a.sessions, req.SessionID)
	}
	a.mu.Unlock()

	if !ok {
		errMsg := "session not found: " + req.SessionID
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "delete", errMsg)
		return
	}

	if err := sess.stopAndWait(sessionDeleteTimeout); err != nil {
		a.log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("session did not stop cleanly")
	}

	// Remove git worktree if /data/repo exists and this was the last session
	// using the branch.  Scratch projects (no bare repo) have no worktree to
	// remove; the worktree field will be empty in that case, so the guard on
	// st.Worktree also covers them — but we additionally check that /data/repo
	// exists to be explicit.
	st := sess.getState()
	if st.Worktree != "" {
		effectiveDataDir := a.dataDir
		if effectiveDataDir == "" {
			effectiveDataDir = "/data"
		}
		repoPath := filepath.Join(effectiveDataDir, "repo")
		a.mu.RLock()
		lastUser := !a.worktreeInUse(st.Worktree)
		a.mu.RUnlock()
		if lastUser {
			worktreePath := filepath.Join(effectiveDataDir, "worktrees", st.Worktree)
			if err := a.gitWorktreeRemove(repoPath, worktreePath); err != nil {
				a.log.Warn().Err(err).
					Str("worktree", st.Worktree).
					Msg("git worktree remove failed (non-fatal)")
			}
		}
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
// Payload: stream-json user message with an added session_id routing field.
// The session_id field is stripped before forwarding to Claude's stdin because
// Claude Code's --input-format stream-json does not accept unknown top-level
// fields and would reject or mishandle a message containing session_id.
func (a *Agent) handleInput(msg *nats.Msg) {
	// Parse the payload into a generic map so we can extract session_id and
	// then remove it before forwarding the rest to Claude's stdin.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(msg.Data, &fields); err != nil {
		a.log.Warn().Err(err).Msg("sessions.input: failed to parse payload")
		return
	}

	sessionIDRaw, ok := fields["session_id"]
	if !ok || string(sessionIDRaw) == `""` || string(sessionIDRaw) == "null" {
		a.log.Warn().Msg("sessions.input: missing session_id")
		return
	}

	// Unquote the JSON string value.
	var sessionID string
	if err := json.Unmarshal(sessionIDRaw, &sessionID); err != nil || sessionID == "" {
		a.log.Warn().Msg("sessions.input: session_id not a non-empty string")
		return
	}

	a.mu.RLock()
	sess, ok2 := a.sessions[sessionID]
	a.mu.RUnlock()

	if !ok2 {
		a.log.Warn().Str("sessionId", sessionID).Msg("sessions.input: session not found")
		return
	}

	// Remove the routing field before forwarding to Claude's stdin.
	delete(fields, "session_id")

	cleaned, err := json.Marshal(fields)
	if err != nil {
		a.log.Warn().Err(err).Msg("sessions.input: failed to re-marshal payload without session_id")
		return
	}

	sess.sendInput(cleaned)
}

// handleControl routes a control message (permission response, interrupt, model
// change) to the appropriate session's stdin.
// Payload: {type: "control_response", response: {request_id, ...}} or
//
//	{type: "control_request", request: {subtype: "interrupt"/"set_model"}}
//
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
// Payload: {sessionId, requestId}
// Success: session state transitions through "restarting" in KV.
// Error: publish api_error event.
func (a *Agent) handleRestart(msg *nats.Msg) {
	var req struct {
		SessionID string `json:"sessionId"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.SessionID == "" {
		errMsg := "invalid request: missing sessionId"
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "restart", errMsg)
		return
	}

	a.mu.Lock()
	sess, ok := a.sessions[req.SessionID]
	a.mu.Unlock()

	if !ok {
		errMsg := "session not found: " + req.SessionID
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "restart", errMsg)
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

	// Restart debug unix socket for mclaude-cli attach.
	restartID := req.SessionID
	newDbg := NewDebugServer(restartID,
		func(data []byte) { newSess.sendInput(data) },
		func() { a.publishLifecycle(restartID, "debug_attached") },
		func() { a.publishLifecycle(restartID, "debug_detached") },
	)
	if err := newDbg.Start(); err != nil {
		a.log.Warn().Err(err).Str("sessionId", restartID).Msg("debug socket start failed on restart (non-fatal)")
	} else {
		newSess.mu.Lock()
		newSess.debug = newDbg
		newSess.mu.Unlock()
	}

	if err := newSess.start(a.claudePath, true, publish, a.writeSessionKV); err != nil {
		newDbg.Stop()
		a.log.Error().Err(err).Str("sessionId", req.SessionID).Msg("failed to resume session")
		errMsg := "resume failed: " + err.Error()
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "restart", errMsg)
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

// publishLifecycleFailed publishes a session_failed lifecycle event with an
// error message.  Called when sess.start() returns an error so clients know
// the session will never become active.
func (a *Agent) publishLifecycleFailed(sessionID, errMsg string) {
	subject := fmt.Sprintf("mclaude.%s.%s.lifecycle.%s", a.userID, a.projectID, sessionID)
	payload, _ := json.Marshal(map[string]string{
		"type":      "session_failed",
		"sessionId": sessionID,
		"error":     errMsg,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// updateReplayFromSeq queries JetStream for the last sequence number of the
// events stream and writes it to KV as replayFromSeq for the given session.
// Called after a compact_boundary event is published so that new subscribers
// skip already-compacted history.
func (a *Agent) updateReplayFromSeq(sessionID string) {
	ctx := context.Background()

	// Get the stream handle, then query its current state for the last seq.
	stream, err := a.js.Stream(ctx, "MCLAUDE_EVENTS")
	if err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessionID).Msg("updateReplayFromSeq: Stream lookup failed")
		return
	}
	info, err := stream.Info(ctx)
	if err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessionID).Msg("updateReplayFromSeq: stream.Info failed")
		return
	}

	// Read current state, update replayFromSeq, write back.
	a.mu.RLock()
	sess, ok := a.sessions[sessionID]
	a.mu.RUnlock()
	if !ok {
		return
	}
	lastSeq := info.State.LastSeq
	st := sess.getState()
	st.ReplayFromSeq = lastSeq
	if err := a.writeSessionKV(st); err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessionID).Msg("updateReplayFromSeq: KV write failed")
		return
	}
	// Update in-memory state too.
	sess.mu.Lock()
	sess.state.ReplayFromSeq = lastSeq
	sess.mu.Unlock()
	a.log.Debug().
		Str("sessionId", sessionID).
		Uint64("replayFromSeq", lastSeq).
		Msg("replayFromSeq updated on compact_boundary")
}

// reply sends a NATS reply. If errMsg is non-empty, sends {error: errMsg}.
// If data is nil and errMsg is empty, sends {}.
// This is a no-op when msg.Reply == "" (JetStream messages have no Reply).
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

// gitWorktreeAdd runs `git -C {repoPath} worktree add {worktreePath} {branch}`.
// Returns nil if the command succeeds.
func (a *Agent) gitWorktreeAdd(repoPath, worktreePath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", worktreePath, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// gitWorktreeRemove runs `git -C {repoPath} worktree remove --force {worktreePath}`.
func (a *Agent) gitWorktreeRemove(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// handleTerminalCreate spawns a PTY shell and bridges it to NATS.
// Payload: {termId, branch, shell}
// Reply:   {id} or {error}
// NATS subjects:
//
//	mclaude.{userId}.{projectId}.terminal.{termId}.output  → PTY stdout (raw bytes)
//	mclaude.{userId}.{projectId}.terminal.{termId}.input   ← PTY stdin (raw bytes)
func (a *Agent) handleTerminalCreate(msg *nats.Msg) {
	var req struct {
		TermID string `json:"termId"`
		Shell  string `json:"shell"`
	}
	if len(msg.Data) > 0 {
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			a.reply(msg, nil, "invalid request: "+err.Error())
			return
		}
	}
	if req.TermID == "" {
		req.TermID = "term-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if req.Shell == "" {
		req.Shell = "/bin/sh"
	}

	a.mu.Lock()
	if _, exists := a.terminals[req.TermID]; exists {
		a.mu.Unlock()
		a.reply(msg, nil, "terminal already exists: "+req.TermID)
		return
	}
	a.mu.Unlock()

	tr := NATSTermPubSub(a.nc)
	ts, err := startTerminal(req.TermID, req.Shell, tr, a.userID, a.projectID)
	if err != nil {
		a.log.Error().Err(err).Str("termId", req.TermID).Msg("failed to start terminal")
		a.reply(msg, nil, "start terminal: "+err.Error())
		return
	}

	a.mu.Lock()
	a.terminals[req.TermID] = ts
	a.mu.Unlock()

	a.log.Info().
		Str("termId", req.TermID).
		Str("shell", req.Shell).
		Msg("terminal created")

	a.reply(msg, map[string]string{"id": req.TermID}, "")
}

// handleTerminalDelete terminates a PTY session.
// Payload: {termId}
// Reply:   {} or {error}
func (a *Agent) handleTerminalDelete(msg *nats.Msg) {
	var req struct {
		TermID string `json:"termId"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.TermID == "" {
		a.reply(msg, nil, "invalid request: missing termId")
		return
	}

	a.mu.Lock()
	ts, ok := a.terminals[req.TermID]
	if ok {
		delete(a.terminals, req.TermID)
	}
	a.mu.Unlock()

	if !ok {
		a.reply(msg, nil, "terminal not found: "+req.TermID)
		return
	}

	ts.stop()
	a.log.Info().Str("termId", req.TermID).Msg("terminal deleted")
	a.reply(msg, map[string]string{}, "")
}

// handleTerminalResize resizes the PTY window for a terminal session.
// Payload: {termId, rows, cols}
// Reply:   {} or {error}
func (a *Agent) handleTerminalResize(msg *nats.Msg) {
	var req struct {
		TermID string `json:"termId"`
		Rows   uint16 `json:"rows"`
		Cols   uint16 `json:"cols"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.TermID == "" {
		a.reply(msg, nil, "invalid request: missing termId")
		return
	}

	a.mu.RLock()
	ts, ok := a.terminals[req.TermID]
	a.mu.RUnlock()

	if !ok {
		a.reply(msg, nil, "terminal not found: "+req.TermID)
		return
	}

	if err := ts.resize(req.Rows, req.Cols); err != nil {
		a.reply(msg, nil, "resize: "+err.Error())
		return
	}

	a.reply(msg, map[string]string{}, "")
}
