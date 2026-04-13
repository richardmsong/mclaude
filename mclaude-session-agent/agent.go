package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"
)

const (
	heartbeatInterval  = 30 * time.Second
	kvBucketSessions   = "mclaude-sessions"
	kvBucketProjects   = "mclaude-projects"
	kvBucketHeartbeats = "mclaude-heartbeats"
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

// Run starts NATS subscriptions and the heartbeat loop. Blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.subscribeAPI(); err != nil {
		return err
	}
	a.runHeartbeat(ctx)
	<-ctx.Done()
	return nil
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

func (a *Agent) handleCreate(msg *nats.Msg) {
	a.log.Info().
		Str("component", "session-agent").
		Str("userId", a.userID).
		Str("projectId", a.projectID).
		Msg("session create request received")
	if a.metrics != nil {
		a.metrics.SessionOpened()
	}
}

func (a *Agent) handleDelete(msg *nats.Msg) {
	a.log.Info().
		Str("component", "session-agent").
		Str("userId", a.userID).
		Str("projectId", a.projectID).
		Msg("session delete request received")
	if a.metrics != nil {
		a.metrics.SessionClosed()
	}
}

func (a *Agent) handleInput(msg *nats.Msg) {
	a.log.Info().Msg("session input received")
}

func (a *Agent) handleControl(msg *nats.Msg) {
	a.log.Info().Msg("session control received")
}

func (a *Agent) handleRestart(msg *nats.Msg) {
	a.log.Info().Msg("session restart received")
}

func (a *Agent) runHeartbeat(ctx context.Context) {
	go func() {
		tick := time.NewTicker(heartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				key := fmt.Sprintf("%s/%s", a.userID, a.projectID)
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
	key := fmt.Sprintf("%s/%s/%s", a.userID, state.ProjectID, state.ID)
	_, span := KVWriteSpan(context.Background(), kvBucketSessions, key)
	_, err = a.sessKV.Put(context.Background(), key, data)
	span.End()
	return err
}
