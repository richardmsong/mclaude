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

	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"

	"mclaude-session-agent/internal/drivers"
)

const (
	sessionDeleteTimeout = 10 * time.Second
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
	// hostsKV provides read-only access to the shared mclaude-hosts KV bucket.
	// Used to read the host's own key (`{hslug}`) for host configuration.
	// Access is read-only per ADR-0054; CP is the sole writer (sourced from $SYS events).
	// Per ADR-0054: "Agent must use raw JetStream API (direct-get + consumer-create)
	// for the hosts bucket, not the high-level KV client which requires STREAM.INFO."
	// Therefore, hostsKV is nil when the agent JWT lacks STREAM.INFO for KV_mclaude-hosts.
	// The agent accesses the hosts bucket via direct-get using hostsKVRaw when hostsKV is nil.
	hostsKV    jetstream.KeyValue
	hostSlug    slug.HostSlug
	userID      string
	userSlug    slug.UserSlug
	projectID   string
	projectSlug slug.ProjectSlug
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
	// cmdMsgs and ctlMsgs are MessagesContext handles for the ordered push consumers.
	// cmdMsgs is stopped early in graceful shutdown (step 2) so new commands queue
	// in JetStream for the replacement pod.  ctlMsgs stays running until step 7 so
	// interrupts and permission responses keep working during draining.
	cmdMsgs     jetstream.MessagesContext
	ctlMsgs     jetstream.MessagesContext
	// sessKVBucket is the name of the per-user sessions KV bucket (ADR-0054).
	// Stored so KVWriteSpan and error messages can reference the bucket name.
	sessKVBucket string
	// doExit is called at the end of gracefulShutdown. Defaults to os.Exit(0).
	// Overridable in tests to prevent process exit.
	doExit func(code int)
	// writeSessionKVFn, if non-nil, overrides writeSessionKV. Used in tests that
	// exercise gracefulShutdown without a real NATS connection.
	writeSessionKVFn func(state SessionState) error
	// pendingUpdatingIDs tracks session IDs that were in "updating" state during
	// recovery. clearUpdatingState() uses this to write idle to KV after consumers
	// are attached, since the in-memory state is already idle.
	pendingUpdatingIDs map[string]bool
	// credMgr refreshes git credentials before git operations (nil in dev/laptop mode).
	credMgr *CredentialManager
	// gitIdentityID is the GIT_IDENTITY_ID for credential identity selection.
	gitIdentityID string
	// quotaPublisherMu guards quotaPublisherCancel.
	quotaPublisherMu sync.Mutex
	// quotaPublisherCancel, if non-nil, cancels the running quota publisher goroutine.
	// Set when the agent is designated as the quota publisher (ADR-0044).
	quotaPublisherCancel context.CancelFunc
	// driverRegistry maps CLIBackend enum values to driver instances (ADR-0005).
	// Session create requests specify the backend; the registry looks up the driver.
	// Populated in NewAgent with the ClaudeCodeDriver and any other registered drivers.
	driverRegistry *drivers.DriverRegistry
}

// NewAgent creates an Agent connected to the given NATS server.
// m may be nil (no-op metrics) — pass NewMetrics(reg) in production.
// dataDir is the project PVC mount point (e.g. "/data"); pass "" to skip git
// worktree operations (dev/laptop mode without a bare repo).
func NewAgent(nc *nats.Conn, userID string, userSlug slug.UserSlug, hostSlug slug.HostSlug, projectID string, projectSlug slug.ProjectSlug, claudePath, dataDir string, log zerolog.Logger, m *Metrics, credMgr *CredentialManager, gitIdentityID string) (*Agent, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	ctx := context.Background()

	// Per ADR-0054, KV buckets are per-user. The control-plane creates them on
	// user registration. Session agents fail fast if buckets don't exist —
	// their presence confirms the control-plane has started successfully.
	sessKVBucket := "mclaude-sessions-" + string(userSlug)
	sessKV, err := js.KeyValue(ctx, sessKVBucket)
	if err != nil {
		return nil, fmt.Errorf("sessions KV bucket %q not found (control-plane not started?): %w", sessKVBucket, err)
	}
	projKVBucket := "mclaude-projects-" + string(userSlug)
	projKV, err := js.KeyValue(ctx, projKVBucket)
	if err != nil {
		return nil, fmt.Errorf("projects KV bucket %q not found (control-plane not started?): %w", projKVBucket, err)
	}

	// Per ADR-0054, agents access the shared mclaude-hosts bucket in read-only mode.
	// The agent JWT may lack $JS.API.STREAM.INFO.KV_mclaude-hosts permission (to prevent
	// host enumeration via SubjectFilter). If the high-level KV client fails, we skip the
	// hosts bucket — the agent can still function without it (host config is optional).
	hostsKV, hostsKVErr := js.KeyValue(ctx, "mclaude-hosts")
	if hostsKVErr != nil {
		// Non-fatal: JWT may not include STREAM.INFO for the hosts bucket.
		// Use nil to indicate unavailability; code that reads host config
		// checks for nil before using hostsKV.
		log.Warn().Err(hostsKVErr).Msg("hosts KV bucket unavailable (non-fatal — host config reads disabled)")
		hostsKV = nil
	}

	// Per ADR-0054, the agent does NOT create streams. The control-plane creates
	// the consolidated per-user MCLAUDE_SESSIONS_{uslug} stream on user registration.
	// The agent only creates ordered push consumers on the pre-existing stream.

	agent := &Agent{
		sessions:      make(map[string]*Session),
		terminals:     make(map[string]*TerminalSession),
		nc:            nc,
		js:            js,
		sessKV:        sessKV,
		projKV:        projKV,
		hostsKV:       hostsKV,
		sessKVBucket:  sessKVBucket,
		hostSlug:      hostSlug,
		userID:        userID,
		userSlug:      userSlug,
		projectID:     projectID,
		projectSlug:   projectSlug,
		claudePath:    claudePath,
		dataDir:       dataDir,
		log:           log,
		metrics:       m,
		credMgr:       credMgr,
		gitIdentityID: gitIdentityID,
	}

	// Initialize the DriverRegistry and register all supported CLI drivers (ADR-0005).
	// DroidDriver, DevinACPDriver, and GenericTerminalDriver are stub implementations
	// that return "not yet implemented" from Launch/Resume. Full protocol implementations
	// will be added in ADR-0005 Phases 4-7.
	reg := drivers.NewDriverRegistry()
	reg.Register(drivers.NewClaudeCodeDriver(claudePath))
	reg.Register(drivers.NewDroidDriver())
	reg.Register(drivers.NewDevinACPDriver())
	reg.Register(drivers.NewGenericTerminalDriver())
	agent.driverRegistry = reg

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
// the manage API subscriptions, the fsnotify watcher, and waits for ctx cancellation
// before graceful shutdown.
func (a *Agent) Run(ctx context.Context) error {
	// Check for pending session import BEFORE recovery (ADR-0053).
	// If importRef is set in project KV, download and unpack before starting sessions.
	if err := a.checkImport(ctx); err != nil {
		a.log.Warn().Err(err).Msg("import check failed — continuing without import")
	}

	if err := a.recoverSessions(); err != nil {
		a.log.Warn().Err(err).Msg("session recovery failed — continuing without recovery")
	}
	if err := a.createJetStreamConsumers(); err != nil {
		return err
	}
	if err := a.subscribeTerminalAPI(); err != nil {
		return err
	}
	if err := a.subscribeManageAPI(); err != nil {
		return err
	}
	if err := a.clearUpdatingState(); err != nil {
		a.log.Warn().Err(err).Msg("clearUpdatingState failed — continuing")
	}

	// Start fsnotify watcher on session data directory (ADR-0053).
	// Discovers new JSONL session files from imports or manual placement.
	go a.watchSessionDataDir(ctx)

	// Start daily JSONL cleanup goroutine.
	// Deletes JSONL files older than 90 days from the session data directory.
	go a.runJSONLCleanup(ctx)

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
	// Per ADR-0054, watch only this project's session keys (filtered by host+project prefix).
	// The per-user bucket (mclaude-sessions-{uslug}) contains all users' sessions;
	// filtering to this project avoids processing unrelated sessions on recovery.
	projectKeyPrefix := "hosts." + string(a.hostSlug) + ".projects." + string(a.projectSlug) + ".sessions.>"
	watcher, err := a.sessKV.Watch(ctx, projectKeyPrefix)
	if err != nil {
		return fmt.Errorf("KV watch(%s): %w", projectKeyPrefix, err)
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
		sess.log = a.log

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

		// Publish session_restarting before starting resume (spec: GAP-SA-N5).
		a.publishLifecycle(st.ID, "session_restarting")

		if sErr := sess.start(a.claudePath, true, publish, a.writeSessionKV); sErr != nil {
			dbg.Stop()
			a.log.Warn().Err(sErr).Str("sessionId", st.ID).Msg("failed to resume session on startup")
			continue
		}
		a.mu.Lock()
		a.sessions[st.ID] = sess
		a.mu.Unlock()
		// ADR-0051: start a crash watcher for recovered sessions, matching
		// what handleCreate does for new sessions. Without this, a recovered
		// session whose Claude process crashes won't auto-restart.
		go a.watchSessionCrash(st.ID, sess)
		a.publishLifecycle(st.ID, "session_resumed")
		a.log.Info().Str("sessionId", st.ID).Msg("session resumed after startup")
		if a.metrics != nil {
			a.metrics.SessionOpened()
		}
	}
	return nil
}

// createJetStreamConsumers creates ordered push consumers on the per-user
// MCLAUDE_SESSIONS_{uslug} stream (ADR-0054). Two consumers are created:
//   - cmdMsgs: sessions.create + sessions.*.{input,delete,config} — stopped early in
//     graceful shutdown so new commands queue for the replacement pod.
//   - ctlMsgs: sessions.*.control.> — stopped late in graceful shutdown so
//     interrupts and permission responses keep working while sessions drain.
func (a *Agent) createJetStreamConsumers() error {
	ctx := context.Background()
	streamName := "MCLAUDE_SESSIONS_" + string(a.userSlug)

	// Build the project-scoped subject prefix.
	projBase := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + "."

	// Command consumer: sessions.create + per-session command subjects.
	cmdCons, err := a.js.OrderedConsumer(ctx, streamName, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{
			projBase + "sessions.create",
			projBase + "sessions.*.input",
			projBase + "sessions.*.delete",
			projBase + "sessions.*.config",
		},
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("create cmd consumer on %s: %w", streamName, err)
	}

	// Control consumer: per-session control subjects (interrupt, restart).
	ctlCons, err := a.js.OrderedConsumer(ctx, streamName, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{
			projBase + "sessions.*.control.>",
		},
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("create ctl consumer on %s: %w", streamName, err)
	}

	cmdMsgs, err := cmdCons.Messages()
	if err != nil {
		return fmt.Errorf("cmd consumer Messages: %w", err)
	}
	ctlMsgs, err := ctlCons.Messages()
	if err != nil {
		return fmt.Errorf("ctl consumer Messages: %w", err)
	}

	a.cmdMsgs = cmdMsgs
	a.ctlMsgs = ctlMsgs

	go a.runConsumer(cmdMsgs, a.dispatchCmd)
	go a.runConsumer(ctlMsgs, a.dispatchCtl)

	return nil
}

// runConsumer iterates a MessagesContext sequentially, dispatching each message.
// Stops when the MessagesContext is stopped (Stop() is called externally).
func (a *Agent) runConsumer(msgs jetstream.MessagesContext, dispatch func(jetstream.Msg)) {
	for {
		msg, err := msgs.Next()
		if err != nil {
			// MessagesContext was stopped — exit goroutine cleanly.
			return
		}
		dispatch(msg)
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
// Routing is by subject suffix within the ADR-0054 sessions.> hierarchy.
func (a *Agent) dispatchCmd(jm jetstream.Msg) {
	msg := jsToNatsMsg(jm)
	s := jm.Subject()
	switch {
	case strings.HasSuffix(s, ".sessions.create"):
		a.handleCreate(msg)
	case strings.HasSuffix(s, ".delete"):
		a.handleDelete(msg)
	case strings.HasSuffix(s, ".input"):
		a.handleInput(msg)
	case strings.HasSuffix(s, ".config"):
		a.handleConfig(msg)
	default:
		a.log.Warn().Str("subject", s).Msg("dispatchCmd: unrecognised subject")
	}
}

// dispatchCtl routes a control consumer message to the appropriate handler.
// sessions.*.control.restart → handleRestart; all others → handleControl.
func (a *Agent) dispatchCtl(jm jetstream.Msg) {
	msg := jsToNatsMsg(jm)
	s := jm.Subject()
	switch {
	case strings.HasSuffix(s, ".control.restart"):
		a.handleRestart(msg)
	default:
		a.handleControl(msg)
	}
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
//  1. Write state:"updating" to session KV for all sessions (SPA banner).
//     Set sess.shutdownPending = true. Do NOT mutate sess.state.State in memory —
//     it must keep tracking Claude's live state for the drain predicate.
//  2. Cancel command consumer context (stops cmd fetch loop; messages queue in JetStream).
//  3. Drain core NATS subscriptions (terminal API).
//  4. Keep control consumer running (interrupts, permission responses still work).
//  5. Poll 1s: drain predicate — all sessions must satisfy:
//     sess.state.State == StateIdle AND sess.inFlightBackgroundAgents == 0.
//     Sessions stuck in StateRequiresAction are interrupted so permission prompts
//     don't block the upgrade indefinitely.
//  6. Cancel control consumer context.
//  7. Publish lifecycle "session_upgrading" for each session.
//  8. Exit(0).
func (a *Agent) gracefulShutdown() {
	// Step 1: write state:"updating" to KV for all sessions (SPA banner).
	// Set shutdownPending = true. Do NOT mutate in-memory state.State.
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
		// Build the KV payload with state:"updating" from the current live state.
		st := sess.getState()
		st.State = StateUpdating
		st.StateSince = time.Now().UTC()
		// Set shutdownPending so the SubtypeSessionStateChanged handler stops
		// flushing to KV (preserving the "updating" banner).
		sess.mu.Lock()
		sess.shutdownPending = true
		sess.mu.Unlock()
		if err := a.writeSessionKV(st); err != nil {
			a.log.Warn().Err(err).Str("sessionId", id).Msg("gracefulShutdown: failed to write updating state")
		}
	}

	// Step 2: stop the command consumer (new work queues in JetStream for the replacement pod).
	if a.cmdMsgs != nil {
		a.cmdMsgs.Stop()
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

	// Steps 4 & 5: keep control consumer running; poll until all sessions satisfy
	// the drain predicate: state == StateIdle AND inFlightBackgroundAgents == 0.
	// On each tick, interrupt any session in StateRequiresAction so pending
	// permission prompts do not block the upgrade indefinitely.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.mu.RLock()
		sessions := make([]*Session, 0, len(a.sessions))
		for _, s := range a.sessions {
			sessions = append(sessions, s)
		}
		a.mu.RUnlock()

		allDone := true
		for _, sess := range sessions {
			sess.mu.Lock()
			state := sess.state.State
			inFlight := sess.inFlightBackgroundAgents
			sess.mu.Unlock()

			// Auto-interrupt sessions stuck in requires_action so the user's
			// absence doesn't block the upgrade. The turn aborts → idle.
			if state == StateRequiresAction {
				sess.sendInterrupt()
				allDone = false
				continue
			}

			if state != StateIdle || inFlight > 0 {
				allDone = false
			}
		}
		if allDone {
			break
		}
	}

	// Step 6: publish synthetic <task-notification status=killed> for each in-flight
	// background shell. These messages queue in JetStream for the new pod.
	a.publishShellKilledNotifications(ids)

	// Step 7: stop the control consumer.
	if a.ctlMsgs != nil {
		a.ctlMsgs.Stop()
	}

	// Step 8: publish lifecycle "session_upgrading" for each session.
	for _, id := range ids {
		a.publishLifecycle(id, "session_upgrading")
	}

	// Step 9: exit.
	exitFn := a.doExit
	if exitFn == nil {
		exitFn = os.Exit
	}
	exitFn(0)
}

// publishShellKilledNotifications publishes synthetic <task-notification status=killed>
// messages for each in-flight background shell across all sessions. Called during
// graceful shutdown (step 6) after the drain predicate is satisfied and BEFORE
// stopping the ctl consumer. Messages are published to the per-session
// sessions.{sslug}.input subject so they queue in JetStream for the new pod.
func (a *Agent) publishShellKilledNotifications(sessionIDs []string) {
	for _, id := range sessionIDs {
		a.mu.RLock()
		sess, ok := a.sessions[id]
		a.mu.RUnlock()
		if !ok {
			continue
		}

		sess.mu.Lock()
		shells := make([]*inFlightShell, 0, len(sess.inFlightShells))
		for _, sh := range sess.inFlightShells {
			shells = append(shells, sh)
		}
		sess.mu.Unlock()

		// Determine the per-session input subject (ADR-0054).
		st := sess.getState()
		sessSlug := slug.SessionSlug(id)
		if st.Slug != "" {
			sessSlug = slug.SessionSlug(st.Slug)
		}
		inputSubject := subj.UserHostProjectSessionsInput(a.userSlug, a.hostSlug, a.projectSlug, sessSlug)

		for _, sh := range shells {
			xml := fmt.Sprintf(
				`<task-notification><task-id>%s</task-id><tool-use-id>%s</tool-use-id><output-file>%s</output-file><status>killed</status><summary>Shell "%s" was killed during server upgrade</summary></task-notification>`,
				sh.TaskId, sh.ToolUseId, sh.OutputFilePath, sh.Command,
			)

			payload, _ := json.Marshal(map[string]interface{}{
				"session_id": id,
				"type":       "user",
				"message": map[string]string{
					"role":    "user",
					"content": xml,
				},
			})

			if err := a.nc.Publish(inputSubject, payload); err != nil {
				a.log.Warn().Err(err).
					Str("sessionId", id).
					Str("toolUseId", sh.ToolUseId).
					Msg("failed to publish shell-killed notification")
			} else {
				a.log.Info().
					Str("sessionId", id).
					Str("toolUseId", sh.ToolUseId).
					Str("taskId", sh.TaskId).
					Msg("published shell-killed notification")
			}
		}
	}
}

// subscribeManageAPI subscribes to project-scoped management subjects:
//   - manage.designate-quota-publisher — CP designates or de-designates this agent
//     as the quota publisher for the user (ADR-0044).
//
// Core NATS subscriptions (not JetStream) because management signals are ephemeral.
func (a *Agent) subscribeManageAPI() error {
	managePrefix := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + ".manage."

	sub, err := a.nc.Subscribe(managePrefix+"designate-quota-publisher", a.handleDesignateQuotaPublisher)
	if err != nil {
		return fmt.Errorf("subscribe manage.designate-quota-publisher: %w", err)
	}
	a.mu.Lock()
	a.subs = append(a.subs, sub)
	a.mu.Unlock()
	return nil
}

// handleDesignateQuotaPublisher processes a manage.designate-quota-publisher message.
// Payload: {quotaPublisher: bool}
// When true: this agent starts running the quota publisher goroutine.
// When false: this agent stops its quota publisher (another agent has taken over).
// Per ADR-0044, CP sends this on $SYS DISCONNECT of the previously designated agent.
func (a *Agent) handleDesignateQuotaPublisher(msg *nats.Msg) {
	var req struct {
		QuotaPublisher bool `json:"quotaPublisher"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		a.log.Warn().Err(err).Msg("designate-quota-publisher: failed to parse payload")
		return
	}

	a.quotaPublisherMu.Lock()
	defer a.quotaPublisherMu.Unlock()

	if req.QuotaPublisher {
		if a.quotaPublisherCancel != nil {
			// Already running — no-op.
			a.log.Debug().Msg("quota publisher already running; ignoring duplicate designation")
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.quotaPublisherCancel = cancel
		go a.runQuotaPublisher(ctx)
		a.log.Info().Msg("quota publisher started — designated by CP")
	} else {
		if a.quotaPublisherCancel == nil {
			return // not running
		}
		a.quotaPublisherCancel()
		a.quotaPublisherCancel = nil
		a.log.Info().Msg("quota publisher stopped — de-designated by CP")
	}
}

// startQuotaPublisher starts the quota publisher goroutine if not already running.
// Called when CP's agents.register response contains quotaPublisher: true.
// External callers (main.go) use this to honour the initial designation.
func (a *Agent) startQuotaPublisher() {
	a.quotaPublisherMu.Lock()
	defer a.quotaPublisherMu.Unlock()
	if a.quotaPublisherCancel != nil {
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.quotaPublisherCancel = cancel
	go a.runQuotaPublisher(ctx)
	a.log.Info().Msg("quota publisher started — initial designation")
}

// runQuotaPublisher polls the Anthropic OAuth usage API every 60s and publishes
// QuotaStatus to mclaude.users.{uslug}.quota (core NATS, no JetStream retention).
// Only runs on the designated agent per user (ADR-0044).
// Reads OAuth token from ~/.claude/.credentials.json.
func (a *Agent) runQuotaPublisher(ctx context.Context) {
	quotaSubject := subj.UserQuota(a.userSlug)

	publishOnce := func() {
		qs := fetchQuotaStatus("") // empty path = use default ~/.claude/.credentials.json
		data, _ := json.Marshal(qs)
		if err := a.nc.Publish(quotaSubject, data); err != nil {
			a.log.Warn().Err(err).Msg("quota publisher: failed to publish QuotaStatus")
		}
	}

	// Publish immediately on startup.
	publishOnce()

	ticker := time.NewTicker(quotaPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publishOnce()
		}
	}
}

// subscribeTerminalAPI sets up core NATS subscriptions for terminal management.
// Terminal I/O is latency-sensitive and stays on core NATS (ephemeral).
func (a *Agent) subscribeTerminalAPI() error {
	termPrefix := subj.UserHostProjectAPITerminal(a.userSlug, a.hostSlug, a.projectSlug, "")

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

// publishAPIError publishes an api_error event to the project-level sessions._api subject.
// Used when a create/delete/restart handler encounters an error.
func (a *Agent) publishAPIError(requestID, operation, errMsg string) {
	subject := "mclaude.users." + string(a.userSlug) + ".hosts." + string(a.hostSlug) + ".projects." + string(a.projectSlug) + ".sessions._api"
	payload, _ := json.Marshal(map[string]string{
		"type":       "api_error",
		"request_id": requestID,
		"operation":  operation,
		"error":      errMsg,
	})
	_ = a.nc.Publish(subject, payload)
}

// defaultDevHarnessAllowlist is retained as a convenience constant for callers
// that want to reference the canonical tool set. The session-agent no longer
// applies it as a default — callers must pass AllowedTools explicitly.
// Per ADR-0044: if AllowedTools is empty on a strict-allowlist session, the
// agent rejects the create request.
var defaultDevHarnessAllowlist = []string{
	"Read", "Write", "Edit", "Glob", "Grep", "Bash",
	"Agent", "TaskCreate", "TaskUpdate", "TaskGet", "TaskList", "TaskOutput", "TaskStop",
}

// handleCreate processes a sessions.create request.
// Payload: {name, branch, cwd, joinWorktree, requestId, permPolicy, allowedTools,
//           backend, prompt, branchSlug, softThreshold, hardHeadroomTokens,
//           autoContinue, resumePrompt, quotaMonitor (deprecated)}
// Success: session appears in KV (SPA watches KV).
// Error: publish api_error event to mclaude.{userId}.{projectId}.sessions._api.
func (a *Agent) handleCreate(msg *nats.Msg) {
	var req struct {
		Name         string             `json:"name"`
		Branch       string             `json:"branch"`
		CWD          string             `json:"cwd"`
		JoinWorktree bool               `json:"joinWorktree"`
		RequestID    string             `json:"requestId"`
		PermPolicy   string             `json:"permPolicy"`
		AllowedTools []string           `json:"allowedTools"`
		ExtraFlags   string             `json:"extraFlags"`
		Backend      string             `json:"backend"`
		// ADR-0044 top-level quota fields (supersede nested quotaMonitor).
		Prompt             string `json:"prompt"`
		BranchSlug         string `json:"branchSlug"`
		SoftThreshold      int    `json:"softThreshold"`
		HardHeadroomTokens int    `json:"hardHeadroomTokens"`
		AutoContinue       bool   `json:"autoContinue"`
		ResumePrompt       string `json:"resumePrompt"`
		// QuotaMonitor is the deprecated nested form; still accepted for backward-compat.
		QuotaMonitor *QuotaMonitorConfig `json:"quotaMonitor"`
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
	sessionSlug := slug.Slugify(req.Name)
	if sessionSlug == "" {
		sessionSlug = "s-" + sessionID[:8]
	}

	// Determine effective branchSlug: explicit req.BranchSlug, then slugified branch.
	effectiveBranchSlug := req.BranchSlug
	if effectiveBranchSlug == "" {
		effectiveBranchSlug = branchSlug
	}

	// Determine initial lifecycle state: quota-managed sessions start as "pending"
	// (prompt held until quota allows), interactive sessions start as "idle".
	initialState := StateIdle
	if req.SoftThreshold > 0 {
		initialState = StatusPending
	}

	backend := req.Backend
	if backend == "" {
		backend = "claude_code"
	}

	state := SessionState{
		ID:              sessionID,
		Slug:            sessionSlug,
		UserSlug:        string(a.userSlug),
		HostSlug:        string(a.hostSlug),
		ProjectSlug:     string(a.projectSlug),
		ProjectID:       a.projectID,
		Branch:          branch,
		Worktree:        branchSlug,
		CWD:             cwd,
		Name:            req.Name,
		State:           initialState,
		StateSince:      now,
		CreatedAt:       now,
		JoinWorktree:    req.JoinWorktree,
		ExtraFlags:      req.ExtraFlags,
		Backend:         backend,
		PendingControls: make(map[string]any),
		// Quota fields — zero values omitted for interactive sessions.
		SoftThreshold:      req.SoftThreshold,
		HardHeadroomTokens: req.HardHeadroomTokens,
		AutoContinue:       req.AutoContinue,
		BranchSlug:         effectiveBranchSlug,
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
	sess.log = a.log

	// Look up the CLIDriver from the DriverRegistry (ADR-0005).
	// The driver is used by sess.start() to launch/resume the CLI process.
	// Falls back to auto-creating ClaudeCodeDriver(claudePath) if not found.
	if a.driverRegistry != nil {
		if drv, ok := a.driverRegistry.GetOrDefault(drivers.CLIBackend(backend)); ok {
			sess.driver = drv
		}
	}

	// Apply permission policy from request (backward-compatible: absent = managed).
	if req.PermPolicy != "" {
		sess.permPolicy = PermissionPolicy(req.PermPolicy)
	}
	// Build allowedTools set.
	// Per ADR-0044: if allowedTools is empty on a strict-allowlist session, reject.
	// There is no default allowlist — callers must explicitly declare their tool scope.
	toolList := req.AllowedTools
	if len(toolList) == 0 && req.PermPolicy == string(PermissionPolicyStrictAllowlist) {
		errMsg := "strict-allowlist sessions require an explicit allowedTools list"
		a.log.Warn().Str("sessionId", sessionID).Msg(errMsg)
		a.publishLifecycleError(sessionID, errMsg)
		a.reply(msg, nil, errMsg)
		a.publishAPIError(req.RequestID, "create", errMsg)
		return
	}
	if len(toolList) > 0 {
		set := make(map[string]bool, len(toolList))
		for _, t := range toolList {
			set[t] = true
		}
		sess.allowedTools = set
	}
	// extraFlags is already set in the state struct literal above and propagated
	// into sess.extraFlags via newSession(state, ...). No additional assignment needed.

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

	// Wire quota monitor if requested (ADR-0044).
	// Activated by top-level softThreshold > 0. The deprecated nested quotaMonitor
	// field is no longer supported; callers must use top-level quota fields.
	// Must be set before sess.start() so the goroutine can read the fields.
	var monitor *QuotaMonitor
	if req.SoftThreshold > 0 {
		// ADR-0044 top-level quota fields (supersede deprecated nested quotaMonitor shape).
		var monErr error
		monitor, monErr = newQuotaMonitor(
			sessionID, sessionSlug,
			string(a.userSlug), string(a.hostSlug), string(a.projectSlug),
			req.SoftThreshold, req.HardHeadroomTokens,
			req.AutoContinue,
			req.Prompt, req.ResumePrompt,
			a.nc, sess,
			a.publishLifecycleExtra,
			a.writeSessionKV,
		)
		if monErr != nil {
			a.log.Warn().Err(monErr).Str("sessionId", sessionID).Msg("quota monitor setup failed (non-fatal)")
		} else {
			sess.mu.Lock()
			sess.monitor = monitor
			sess.onStrictDeny = func(toolName string) {
				a.publishPermDenied(sessionID, toolName, sessionID)
				monitor.signalPermDenied(toolName)
			}
			sess.onRawOutput = monitor.onRawOutput
			sess.mu.Unlock()
		}
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
		if monitor != nil {
			monitor.stop()
		}
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

	// Start crash watcher goroutine (spec: GAP-SA-K16).
	go a.watchSessionCrash(sessionID, sess)

	a.publishLifecycleWithBranch(sessionID, "session_created", branch)

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

	// Stop QuotaMonitor before stopping the process (ADR-0044).
	sess.mu.Lock()
	monitor := sess.monitor
	sess.mu.Unlock()
	if monitor != nil {
		monitor.stop()
	}

	if err := sess.stopAndWait(sessionDeleteTimeout); err != nil {
		a.log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("session did not stop cleanly")
	}

	st := sess.getState()
	isQuotaManaged := st.SoftThreshold > 0

	// Worktree removal — branches on quota-managed vs interactive sessions.
	// Quota-managed sessions: skip removal if Branch starts with "schedule/"
	// (worktree persists for potential re-use). Interactive sessions: always
	// remove if this was the last session on the branch.
	if st.Worktree != "" {
		effectiveDataDir := a.dataDir
		if effectiveDataDir == "" {
			effectiveDataDir = "/data"
		}
		repoPath := filepath.Join(effectiveDataDir, "repo")
		a.mu.RLock()
		lastUser := !a.worktreeInUse(st.Worktree)
		a.mu.RUnlock()

		// For quota-managed sessions on schedule/ branches, skip worktree removal.
		skipRemoval := isQuotaManaged && strings.HasPrefix(st.Branch, "schedule/")
		if lastUser && !skipRemoval {
			worktreePath := filepath.Join(effectiveDataDir, "worktrees", st.Worktree)
			if err := a.gitWorktreeRemove(repoPath, worktreePath); err != nil {
				a.log.Warn().Err(err).
					Str("worktree", st.Worktree).
					Msg("git worktree remove failed (non-fatal)")
			}
		}
	}

	// KV and lifecycle event differ between interactive and quota-managed sessions.
	delSessSlug := slug.SessionSlug(req.SessionID)
	if st.Slug != "" {
		delSessSlug = slug.SessionSlug(st.Slug)
	}
	key := sessionKVKey(a.hostSlug, a.projectSlug, delSessSlug)

	if isQuotaManaged {
		// Quota-managed: tombstone KV entry (disappears from SPA), emit cancelled event.
		_ = a.sessKV.Delete(context.Background(), key)
		a.publishLifecycle(req.SessionID, "session_job_cancelled")
	} else {
		// Interactive: delete KV entry, emit stopped event.
		_ = a.sessKV.Delete(context.Background(), key)
		a.publishLifecycleWithExitCode(req.SessionID, "session_stopped", sess.exitCode())
	}

	a.log.Info().
		Str("sessionId", req.SessionID).
		Bool("quotaManaged", isQuotaManaged).
		Msg("session deleted")

	if a.metrics != nil {
		a.metrics.SessionClosed()
	}

	a.reply(msg, map[string]string{}, "")
}

// handleInput routes a user message to the target session.
// Per ADR-0054, input is published to sessions.{sslug}.input (per-session subject).
// The session_id UUID field in the payload is still supported for routing
// (backward-compatible with SPA/CLI that include it). If session_id is absent,
// the session slug extracted from the subject is used as a fallback.
//
// Routing is type-based per the spec's "Event Routing / Input routing" section:
//   - type: "message"           → driver.SendMessage(proc, msg)
//   - type: "skill_invoke"      → driver.SendMessage(proc, /skill-name prefixed)
//   - type: "permission_response" / "control_response" → driver.SendPermissionResponse
//   - other (incl. legacy type:"user" stream-json) → strip session_id, sendInput
//
// The session_id field is stripped before forwarding to Claude's stdin because
// Claude Code's --input-format stream-json does not accept unknown top-level fields.
func (a *Agent) handleInput(msg *nats.Msg) {
	// Parse the payload into a generic map so we can extract session_id and type.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(msg.Data, &fields); err != nil {
		a.log.Warn().Err(err).Msg("sessions.input: failed to parse payload")
		return
	}

	var sess *Session

	// Prefer session_id (UUID) from payload for routing (backward-compatible).
	sessionIDRaw, hasSessionID := fields["session_id"]
	if hasSessionID && string(sessionIDRaw) != `""` && string(sessionIDRaw) != "null" {
		var sessionID string
		if err := json.Unmarshal(sessionIDRaw, &sessionID); err == nil && sessionID != "" {
			a.mu.RLock()
			sess = a.sessions[sessionID]
			a.mu.RUnlock()
		}
	}

	// Fallback: extract session slug from the subject.
	// Subject: mclaude.users.{u}.hosts.{h}.projects.{p}.sessions.{sslug}.input
	if sess == nil {
		parts := strings.SplitN(msg.Subject, ".sessions.", 2)
		if len(parts) == 2 {
			sslugAndSuffix := parts[1]
			// sslugAndSuffix is "{sslug}.input" — extract the slug.
			if dotIdx := strings.Index(sslugAndSuffix, "."); dotIdx > 0 {
				sslug := sslugAndSuffix[:dotIdx]
				sess = a.sessionBySlug(sslug)
			}
		}
		if sess == nil {
			a.log.Warn().Str("subject", msg.Subject).Msg("sessions.input: session not found")
			return
		}
	}

	// Extract the type field to determine routing.
	var msgType string
	if typeRaw, ok := fields["type"]; ok {
		_ = json.Unmarshal(typeRaw, &msgType)
	}

	switch msgType {
	case "message":
		// SessionInput envelope: {type:"message", text:"...", attachments:[...]}
		// Route through driver.SendMessage so the driver formats the native protocol.
		var text string
		if textRaw, ok := fields["text"]; ok {
			_ = json.Unmarshal(textRaw, &text)
		}
		// Process attachments: download from S3 to temp files, convert to ResolvedAttachment.
		var attachments []drivers.ResolvedAttachment
		tmpPaths, err := a.processInputAttachments(context.Background(), msg.Data)
		if err != nil {
			a.log.Warn().Err(err).Msg("sessions.input: attachment processing failed")
		}
		for filename, path := range tmpPaths {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				a.log.Warn().Err(readErr).Str("path", path).Msg("failed to read attachment temp file")
				continue
			}
			attachments = append(attachments, drivers.ResolvedAttachment{Filename: filename, Data: data})
			_ = os.Remove(path)
		}
		userMsg := drivers.UserMessage{Text: text, Attachments: attachments}
		sess.sendViaDriver(func(drv drivers.CLIDriver, proc *drivers.Process) error {
			return drv.SendMessage(proc, userMsg)
		})

	case "skill_invoke":
		// Skill invocations are sent as /skill-name prefixed messages.
		var skillName string
		if snRaw, ok := fields["skillName"]; ok {
			_ = json.Unmarshal(snRaw, &skillName)
		}
		var args string
		if argsRaw, ok := fields["args"]; ok {
			_ = json.Unmarshal(argsRaw, &args)
		}
		text := "/" + skillName
		if args != "" {
			text += " " + args
		}
		userMsg := drivers.UserMessage{Text: text}
		sess.sendViaDriver(func(drv drivers.CLIDriver, proc *drivers.Process) error {
			return drv.SendMessage(proc, userMsg)
		})

	case "permission_response", "control_response":
		// Permission response: route through driver.SendPermissionResponse.
		// Also clear the pending control from KV.
		var envelope struct {
			Type     string          `json:"type"`
			Response controlResponse `json:"response"`
			// SessionInput format fields:
			RequestID string `json:"requestId"`
			Allowed   bool   `json:"allowed"`
		}
		_ = json.Unmarshal(msg.Data, &envelope)

		// Support both control_response (old) and permission_response (new) fields.
		requestID := envelope.Response.RequestID
		allowed := true // default allow for control_response
		if envelope.RequestID != "" {
			requestID = envelope.RequestID
			allowed = envelope.Allowed
		}

		if requestID != "" {
			sess.clearPendingControl(requestID, a.writeSessionKV)
		}
		rID := requestID
		all := allowed
		sess.sendViaDriver(func(drv drivers.CLIDriver, proc *drivers.Process) error {
			return drv.SendPermissionResponse(proc, rID, all)
		})

	default:
		// Legacy / passthrough path: includes type:"user" (old stream-json format) and
		// any other unrecognised type. Strip session_id, forward raw bytes to stdinCh.
		// This maintains backward compatibility with SPAs/CLIs that send stream-json
		// directly on the input subject.
		//
		// Also handles old-style control_response (type:"control_response") that arrived
		// before permission_response routing was added.
		if msgType == "control_response" {
			var envelope struct {
				Type     string          `json:"type"`
				Response controlResponse `json:"response"`
			}
			if err := json.Unmarshal(msg.Data, &envelope); err == nil {
				if envelope.Response.RequestID != "" {
					sess.clearPendingControl(envelope.Response.RequestID, a.writeSessionKV)
				}
			}
		}

		delete(fields, "session_id")
		cleaned, err := json.Marshal(fields)
		if err != nil {
			a.log.Warn().Err(err).Msg("sessions.input: failed to re-marshal payload without session_id")
			return
		}
		sess.sendInput(cleaned)
	}
}

// handleControl routes a control message (permission response, interrupt, model
// change) to the appropriate session.
// Payload: {type: "control_response", response: {request_id, ...}} or
//
//	{type: "control_request", request: {subtype: "interrupt"/"set_model"}}
//
// Interrupts from sessions.{sslug}.control.interrupt are routed to
// driver.Interrupt(proc) per the spec "Event Routing / Input routing" section.
// No reply — fire and forget.
func (a *Agent) handleControl(msg *nats.Msg) {
	var envelope struct {
		Type    string          `json:"type"`
		Response controlResponse `json:"response"`
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
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
		sess.clearPendingControl(requestID, a.writeSessionKV)
		rID := requestID
		sess.sendViaDriver(func(drv drivers.CLIDriver, proc *drivers.Process) error {
			return drv.SendPermissionResponse(proc, rID, true)
		})

	case "control_request":
		// Interrupts from sessions.{sslug}.control.interrupt → driver.Interrupt(proc).
		// Other control_requests (set_model, etc.) → broadcast raw to all sessions.
		if envelope.Request.Subtype == "interrupt" {
			a.mu.RLock()
			sessions := make([]*Session, 0, len(a.sessions))
			for _, s := range a.sessions {
				sessions = append(sessions, s)
			}
			a.mu.RUnlock()
			for _, s := range sessions {
				s.sendViaDriver(func(drv drivers.CLIDriver, proc *drivers.Process) error {
					return drv.Interrupt(proc)
				})
			}
		} else {
			// Non-interrupt control_requests (e.g. set_model) — broadcast raw.
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

	default:
		// Unknown type — broadcast raw to all sessions for forward compatibility.
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

// handleConfig routes a config update message to the appropriate session's driver.
// Per ADR-0054, config updates arrive on sessions.{sslug}.config.
// Payload: {sessionId, model, permissionMode, systemPrompt, ...}
// Routes to driver.UpdateConfig(proc, cfg) per the spec "Event Routing" section.
func (a *Agent) handleConfig(msg *nats.Msg) {
	var req struct {
		SessionID      string  `json:"sessionId"`
		Model          *string `json:"model,omitempty"`
		PermissionMode *string `json:"permissionMode,omitempty"`
		SystemPrompt   *string `json:"systemPrompt,omitempty"`
	}
	if len(msg.Data) > 0 {
		_ = json.Unmarshal(msg.Data, &req)
	}

	var sess *Session
	if req.SessionID != "" {
		a.mu.RLock()
		sess = a.sessions[req.SessionID]
		a.mu.RUnlock()
	}
	// Fallback: extract from subject (sessions.{sslug}.config).
	if sess == nil {
		parts := strings.SplitN(msg.Subject, ".sessions.", 2)
		if len(parts) == 2 {
			sslugAndSuffix := parts[1]
			if dotIdx := strings.Index(sslugAndSuffix, "."); dotIdx > 0 {
				sslug := sslugAndSuffix[:dotIdx]
				sess = a.sessionBySlug(sslug)
			}
		}
	}
	if sess == nil {
		a.log.Warn().Str("subject", msg.Subject).Msg("sessions.config: session not found")
		return
	}

	// Route to driver.UpdateConfig per spec §Event Routing "Config updates".
	cfg := drivers.SessionConfig{
		Model:          req.Model,
		PermissionMode: req.PermissionMode,
		SystemPrompt:   req.SystemPrompt,
	}
	sess.sendViaDriver(func(drv drivers.CLIDriver, proc *drivers.Process) error {
		return drv.UpdateConfig(proc, cfg)
	})
}

// sessionBySlug returns the session whose state.Slug matches sslug, or nil.
// Used for subject-based routing in handlers that receive per-session subjects.
func (a *Agent) sessionBySlug(sslug string) *Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, s := range a.sessions {
		st := s.getState()
		if st.Slug == sslug {
			return s
		}
	}
	return nil
}

// handleRestart stops a session and relaunches it with --resume.
// Payload: {sessionId, requestId}
// Success: session state transitions through "restarting" in KV.
// Error: publish api_error event.
func (a *Agent) handleRestart(msg *nats.Msg) {
	var req struct {
		SessionID  string `json:"sessionId"`
		RequestID  string `json:"requestId"`
		ExtraFlags string `json:"extraFlags"`
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
	// If the restart payload includes extraFlags, overwrite the stored value.
	if req.ExtraFlags != "" {
		st.ExtraFlags = req.ExtraFlags
	}
	if err := a.writeSessionKV(st); err != nil {
		a.log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("failed to write KV before restart")
	}

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	// Relaunch with --resume.
	newSess := newSession(st, a.userID)
	newSess.metrics = a.metrics
	newSess.log = a.log

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

// writeSessionKV serialises and persists a SessionState to NATS KV.
// If a.writeSessionKVFn is set (e.g., in tests), it is called instead of
// the real NATS KV write.
func (a *Agent) writeSessionKV(state SessionState) error {
	if a.writeSessionKVFn != nil {
		return a.writeSessionKVFn(state)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	// Prefer slug fields when available; fall back to casting IDs so sessions
	// created before the slug migration are still reachable.
	// Per ADR-0054, the user slug is NOT in the key — it is the per-user bucket name.
	pSlug := a.projectSlug
	sSlug := slug.SessionSlug(state.ID)
	if state.ProjectSlug != "" {
		pSlug = slug.ProjectSlug(state.ProjectSlug)
	}
	if state.Slug != "" {
		sSlug = slug.SessionSlug(state.Slug)
	}
	hSlug := a.hostSlug
	if state.HostSlug != "" {
		hSlug = slug.HostSlug(state.HostSlug)
	}
	key := sessionKVKey(hSlug, pSlug, sSlug)
	_, span := KVWriteSpan(context.Background(), a.sessKVBucket, key)
	_, err = a.sessKV.Put(context.Background(), key, data)
	span.End()
	return err
}

// publishLifecycle publishes a lifecycle event on the per-session lifecycle subject.
// Per ADR-0054, the event type is part of the subject: sessions.{sslug}.lifecycle.{eventType}.
// No-op when a.nc is nil (unit tests that don't need a real NATS connection).
func (a *Agent) publishLifecycle(sessionID, eventType string) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), eventType)
	payload, _ := json.Marshal(map[string]string{
		"type":      eventType,
		"sessionId": sessionID,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// publishLifecycleError publishes a lifecycle.error event with an error message.
// Per ADR-0044, used to report create-time validation failures such as empty
// allowedTools on strict-allowlist sessions.
func (a *Agent) publishLifecycleError(sessionID, errMsg string) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), "error")
	payload, _ := json.Marshal(map[string]string{
		"type":      "error",
		"sessionId": sessionID,
		"error":     errMsg,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// publishLifecycleFailed publishes a session_failed lifecycle event with an
// error message.  Called when sess.start() returns an error so clients know
// the session will never become active.
func (a *Agent) publishLifecycleFailed(sessionID, errMsg string) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), "session_failed")
	payload, _ := json.Marshal(map[string]string{
		"type":      "session_failed",
		"sessionId": sessionID,
		"error":     errMsg,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// publishLifecycleExtra publishes a lifecycle event with additional fields
// beyond type/sessionId/ts. Used by QuotaMonitor for events like
// session_job_complete, session_quota_interrupted, session_job_failed.
func (a *Agent) publishLifecycleExtra(sessionID, eventType string, extra map[string]string) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), eventType)
	payload := map[string]string{
		"type":      eventType,
		"sessionId": sessionID,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range extra {
		payload[k] = v
	}
	out, _ := json.Marshal(payload)
	_ = a.nc.Publish(subject, out)
}

// publishPermDenied publishes a session_permission_denied lifecycle event.
// Called when a strict-allowlist session auto-denies a tool request.
func (a *Agent) publishPermDenied(sessionID, toolName, jobID string) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), "session_permission_denied")
	payload, _ := json.Marshal(map[string]string{
		"type":      "session_permission_denied",
		"sessionId": sessionID,
		"tool":      toolName,
		"jobId":     jobID,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// publishLifecycleWithBranch publishes a lifecycle event that includes a branch
// field. Used for session_created (spec: GAP-SA-K9).
func (a *Agent) publishLifecycleWithBranch(sessionID, eventType, branch string) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), eventType)
	payload, _ := json.Marshal(map[string]string{
		"type":      eventType,
		"sessionId": sessionID,
		"branch":    branch,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// publishLifecycleWithExitCode publishes a lifecycle event that includes an
// exitCode field. Used for session_stopped (spec: GAP-SA-K10).
func (a *Agent) publishLifecycleWithExitCode(sessionID, eventType string, exitCode int) {
	if a.nc == nil {
		return
	}
	subject := subj.UserHostProjectSessionsLifecycle(a.userSlug, a.hostSlug, a.projectSlug, slug.SessionSlug(sessionID), eventType)
	payload, _ := json.Marshal(map[string]interface{}{
		"type":      eventType,
		"sessionId": sessionID,
		"exitCode":  exitCode,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// watchSessionCrash monitors a session's doneCh for unexpected exits and
// auto-restarts the Claude process (spec: GAP-SA-K16).
// On crash: publishes session_failed lifecycle, increments mclaude_claude_restarts_total,
// respawns with --resume {sessionId}.
func (a *Agent) watchSessionCrash(sessionID string, sess *Session) {
	<-sess.doneCh

	// Check if this was an intentional stop.
	sess.mu.Lock()
	wasStopping := sess.stopping
	sess.mu.Unlock()
	if wasStopping {
		return
	}

	// Check if the session is still tracked by the agent (not deleted).
	a.mu.RLock()
	current, tracked := a.sessions[sessionID]
	a.mu.RUnlock()
	if !tracked || current != sess {
		return
	}

	a.log.Warn().Str("sessionId", sessionID).Msg("Claude process crashed unexpectedly — restarting")

	// Publish session_failed lifecycle event.
	a.publishLifecycleFailed(sessionID, "Claude process exited unexpectedly")

	// Increment restart counter.
	if a.metrics != nil {
		a.metrics.ClaudeRestart()
	}

	// Respawn with --resume.
	st := sess.getState()
	clearPendingControlsForResume(&st)
	if err := a.writeSessionKV(st); err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessionID).Msg("crash restart: KV write failed")
	}

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	newSess := newSession(st, a.userID)
	newSess.metrics = a.metrics
	newSess.log = a.log

	// Restart debug unix socket.
	newDbg := NewDebugServer(sessionID,
		func(data []byte) { newSess.sendInput(data) },
		func() { a.publishLifecycle(sessionID, "debug_attached") },
		func() { a.publishLifecycle(sessionID, "debug_detached") },
	)
	if err := newDbg.Start(); err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessionID).Msg("debug socket start failed on crash restart (non-fatal)")
	} else {
		newSess.mu.Lock()
		newSess.debug = newDbg
		newSess.mu.Unlock()
	}

	if err := newSess.start(a.claudePath, true, publish, a.writeSessionKV); err != nil {
		newDbg.Stop()
		a.log.Error().Err(err).Str("sessionId", sessionID).Msg("crash restart: failed to resume session")
		a.publishLifecycleFailed(sessionID, "crash restart failed: "+err.Error())
		return
	}

	a.mu.Lock()
	a.sessions[sessionID] = newSess
	a.mu.Unlock()

	// Start a new crash watcher for the restarted session.
	go a.watchSessionCrash(sessionID, newSess)

	a.publishLifecycle(sessionID, "session_resumed")
	a.log.Info().Str("sessionId", sessionID).Msg("session restarted after crash")
}

// updateReplayFromSeq queries JetStream for the last sequence number of the
// per-user sessions stream and writes it to KV as replayFromSeq for the given session.
// Called after a compact_boundary event is published so that new subscribers
// skip already-compacted history.
func (a *Agent) updateReplayFromSeq(sessionID string) {
	ctx := context.Background()

	// Get the per-user sessions stream handle, then query its current state for the last seq.
	streamName := "MCLAUDE_SESSIONS_" + string(a.userSlug)
	stream, err := a.js.Stream(ctx, streamName)
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
// Before running, it refreshes git credentials if the managed config has changed
// (per spec: re-read managed configs before each git operation).
// Returns nil if the command succeeds.
func (a *Agent) gitWorktreeAdd(repoPath, worktreePath, branch string) error {
	if a.credMgr != nil {
		_ = a.credMgr.RefreshIfChanged(a.gitIdentityID)
	}
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
	ts, err := startTerminal(req.TermID, req.Shell, tr, string(a.userSlug), string(a.hostSlug), string(a.projectSlug))
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
