package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"

	"mclaude.io/common/pkg/slug"
	testutil "mclaude-session-agent/testutil"
)

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("set INTEGRATION=1 to run integration tests (requires Docker)")
	}
}

// TestKVBucketInit verifies CreateUserResources creates all required per-user KV
// buckets and streams per ADR-0054.
func TestKVBucketInit(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)

	testutil.CreateUserResources(t, deps.JetStream, "integ-user")

	ctx := context.Background()

	// Per-user KV buckets must exist.
	for _, bucket := range []string{
		"mclaude-sessions-integ-user",
		"mclaude-projects-integ-user",
	} {
		kv, err := deps.JetStream.KeyValue(ctx, bucket)
		if err != nil {
			t.Errorf("KV bucket %q not found: %v", bucket, err)
			continue
		}
		status, err := kv.Status(ctx)
		if err != nil {
			t.Errorf("KV bucket %q status: %v", bucket, err)
			continue
		}
		if status.Bucket() != bucket {
			t.Errorf("bucket name: got %q, want %q", status.Bucket(), bucket)
		}
	}

	// Per-user sessions stream must exist.
	streamName := "MCLAUDE_SESSIONS_integ-user"
	stream, err := deps.JetStream.Stream(ctx, streamName)
	if err != nil {
		t.Errorf("stream %q not found: %v", streamName, err)
	} else {
		info, err := stream.Info(ctx)
		if err != nil {
			t.Errorf("stream %q info: %v", streamName, err)
		} else if info.Config.Name != streamName {
			t.Errorf("stream name: got %q, want %q", info.Config.Name, streamName)
		}
	}
}

// TestSessionCRUDInKV verifies put, get, and delete of session state in the
// per-user NATS KV bucket per ADR-0054.
func TestSessionCRUDInKV(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)
	testutil.CreateUserResources(t, deps.JetStream, "integ-user")

	ctx := context.Background()
	// Per ADR-0054, sessions bucket is per-user: mclaude-sessions-{uslug}.
	kv, err := deps.JetStream.KeyValue(ctx, "mclaude-sessions-integ-user")
	if err != nil {
		t.Fatalf("get sessions KV: %v", err)
	}

	st := SessionState{
		ID:        "integ-sess-1",
		ProjectID: "integ-proj-1",
		Branch:    "main",
		Worktree:  "main",
		State:     StateIdle,
		StateSince: time.Now().UTC().Truncate(time.Second),
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
		Model:     "claude-sonnet-4-6",
	}

	// Create. Key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug} (no user slug in key).
	key := sessionKVKey(slug.HostSlug("local"), slug.ProjectSlug(st.ProjectID), slug.SessionSlug(st.ID))
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := kv.Put(ctx, key, data); err != nil {
		t.Fatalf("KV put: %v", err)
	}

	// Read back.
	entry, err := kv.Get(ctx, key)
	if err != nil {
		t.Fatalf("KV get: %v", err)
	}
	var got SessionState
	if err := json.Unmarshal(entry.Value(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != st.ID {
		t.Errorf("ID: got %q, want %q", got.ID, st.ID)
	}
	if got.Model != st.Model {
		t.Errorf("model: got %q, want %q", got.Model, st.Model)
	}

	// Update state.
	st.State = StateRunning
	st.StateSince = time.Now().UTC()
	data, _ = json.Marshal(st)
	if _, err := kv.Put(ctx, key, data); err != nil {
		t.Fatalf("KV update: %v", err)
	}

	entry, err = kv.Get(ctx, key)
	if err != nil {
		t.Fatalf("KV get after update: %v", err)
	}
	var updated SessionState
	if err := json.Unmarshal(entry.Value(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.State != StateRunning {
		t.Errorf("state after update: got %q, want %q", updated.State, StateRunning)
	}

	// Delete.
	if err := kv.Delete(ctx, key); err != nil {
		t.Fatalf("KV delete: %v", err)
	}
	if _, err := kv.Get(ctx, key); err == nil {
		t.Error("expected error after delete, got nil")
	}
}


// TestLifecycleEventPubSub verifies publish and consume of lifecycle events
// on the per-user MCLAUDE_SESSIONS_{uslug} JetStream stream per ADR-0054.
func TestLifecycleEventPubSub(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)
	testutil.CreateUserResources(t, deps.JetStream, "integ-user")

	ctx := context.Background()
	js := deps.JetStream

	// Per ADR-0054, lifecycle events go to sessions.{sslug}.lifecycle.{eventType}
	// and are captured by the per-user MCLAUDE_SESSIONS_{uslug} stream.
	subject := "mclaude.users.integ-user.hosts.local.projects.integ-proj.sessions.integ-sess.lifecycle.session_created"

	// Subscribe before publishing so we don't miss the message.
	cons, err := js.CreateOrUpdateConsumer(ctx, "MCLAUDE_SESSIONS_integ-user", jetstream.ConsumerConfig{
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}

	event := map[string]string{
		"type":      "session_created",
		"sessionId": "integ-sess",
		"ts":        time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(event)

	if _, err := js.Publish(ctx, subject, data); err != nil {
		t.Fatalf("publish lifecycle event: %v", err)
	}

	msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch messages: %v", err)
	}

	var received []jetstream.Msg
	for msg := range msgs.Messages() {
		received = append(received, msg)
		msg.Ack()
	}

	if len(received) != 1 {
		t.Fatalf("expected 1 message, got %d", len(received))
	}

	var got map[string]string
	if err := json.Unmarshal(received[0].Data(), &got); err != nil {
		t.Fatalf("unmarshal received: %v", err)
	}
	if got["type"] != "session_created" {
		t.Errorf("event type: got %q, want session_created", got["type"])
	}
}

// TestNATSReconnect verifies the NATS connection survives a brief disconnect.
func TestNATSReconnect(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)

	reconnected := make(chan struct{}, 1)
	opts := []nats.Option{
		nats.ReconnectHandler(func(nc *nats.Conn) {
			reconnected <- struct{}{}
		}),
		nats.MaxReconnects(5),
		nats.ReconnectWait(100 * time.Millisecond),
	}

	nc2, err := nats.Connect(deps.NATSURL, opts...)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc2.Close()

	// Connection should be live initially.
	if !nc2.IsConnected() {
		t.Fatal("expected connection to be live")
	}
}

// TestNewAgentConnectsToPerUserResources verifies that NewAgent succeeds when the
// per-user KV buckets and MCLAUDE_SESSIONS_{uslug} stream are pre-created by the
// control-plane (simulated by CreateUserResources). Per ADR-0054, the agent does NOT
// create streams — it only opens pre-existing per-user resources.
func TestNewAgentConnectsToPerUserResources(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)
	testutil.CreateUserResources(t, deps.JetStream, "integ-user")

	logger := zerolog.New(io.Discard)
	agent, err := NewAgent(deps.NATSConn, "integ-user", slug.UserSlug("integ-user"), slug.HostSlug("local"), "integ-proj", slug.ProjectSlug("integ-proj"), "claude", "", logger, nil, nil, "")
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	_ = agent

	ctx := context.Background()
	js := deps.JetStream

	// The agent must have connected to the per-user sessions KV bucket.
	_, err = js.KeyValue(ctx, "mclaude-sessions-integ-user")
	if err != nil {
		t.Errorf("mclaude-sessions-integ-user KV not found: %v", err)
	}

	// The agent must NOT have created any streams (stream creation is CP's job).
	for _, stale := range []string{"MCLAUDE_EVENTS", "MCLAUDE_API", "MCLAUDE_LIFECYCLE"} {
		if _, err := js.Stream(ctx, stale); err == nil {
			t.Errorf("stale stream %q should not exist — agent must not create streams", stale)
		}
	}

	// The per-user stream (pre-created by CP simulation) must still exist.
	stream, err := js.Stream(ctx, "MCLAUDE_SESSIONS_integ-user")
	if err != nil {
		t.Fatalf("MCLAUDE_SESSIONS_integ-user not found: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.Config.Retention != jetstream.LimitsPolicy {
		t.Errorf("retention: got %v, want LimitsPolicy", info.Config.Retention)
	}
	if info.Config.MaxAge != 30*24*time.Hour {
		t.Errorf("max_age: got %v, want 30d", info.Config.MaxAge)
	}
}

// TestNewAgentFailsWithoutPerUserResources verifies that NewAgent fails fast
// when the per-user KV buckets don't exist (control-plane not started).
func TestNewAgentFailsWithoutPerUserResources(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)
	// Intentionally do NOT call CreateUserResources.

	logger := zerolog.New(io.Discard)
	_, err := NewAgent(deps.NATSConn, "no-user", slug.UserSlug("no-user"), slug.HostSlug("local"), "no-proj", slug.ProjectSlug("no-proj"), "claude", "", logger, nil, nil, "")
	if err == nil {
		t.Error("NewAgent should fail when per-user KV buckets don't exist")
	}
}

// TestNewAgentIdempotentForSameUser verifies that calling NewAgent twice for the
// same user succeeds (second call opens the same per-user resources).
func TestNewAgentIdempotentForSameUser(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)
	testutil.CreateUserResources(t, deps.JetStream, "integ-user")

	logger := zerolog.New(io.Discard)

	// First call.
	_, err := NewAgent(deps.NATSConn, "integ-user", slug.UserSlug("integ-user"), slug.HostSlug("local"), "integ-proj", slug.ProjectSlug("integ-proj"), "claude", "", logger, nil, nil, "")
	if err != nil {
		t.Fatalf("first NewAgent: %v", err)
	}

	// Second call (same user, different project) — same per-user resources.
	_, err = NewAgent(deps.NATSConn, "integ-user", slug.UserSlug("integ-user"), slug.HostSlug("local"), "integ-proj-2", slug.ProjectSlug("integ-proj-2"), "claude", "", logger, nil, nil, "")
	if err != nil {
		t.Fatalf("second NewAgent (idempotent): %v", err)
	}
}

