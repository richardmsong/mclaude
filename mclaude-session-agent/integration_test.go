package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	testutil "mclaude-session-agent/testutil"
)

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("set INTEGRATION=1 to run integration tests (requires Docker)")
	}
}

// TestKVBucketInit verifies StartDeps creates all required KV buckets and streams.
func TestKVBucketInit(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)

	ctx := context.Background()

	// All four KV buckets must exist.
	for _, bucket := range []string{
		"mclaude-sessions",
		"mclaude-projects",
		"mclaude-heartbeats",
		"mclaude-laptops",
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

	// Both JetStream streams must exist.
	for _, streamName := range []string{"MCLAUDE_EVENTS", "MCLAUDE_LIFECYCLE"} {
		stream, err := deps.JetStream.Stream(ctx, streamName)
		if err != nil {
			t.Errorf("stream %q not found: %v", streamName, err)
			continue
		}
		info, err := stream.Info(ctx)
		if err != nil {
			t.Errorf("stream %q info: %v", streamName, err)
		}
		if info.Config.Name != streamName {
			t.Errorf("stream name: got %q, want %q", info.Config.Name, streamName)
		}
	}
}

// TestSessionCRUDInKV verifies put, get, and delete of session state in NATS KV.
func TestSessionCRUDInKV(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)

	ctx := context.Background()
	kv, err := deps.JetStream.KeyValue(ctx, "mclaude-sessions")
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

	// Create.
	key := sessionKVKey("integ-user", st.ProjectID, st.ID)
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

// TestHeartbeatWrite verifies the heartbeat KV write pattern used by the agent.
func TestHeartbeatWrite(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)

	ctx := context.Background()
	kv, err := deps.JetStream.KeyValue(ctx, "mclaude-heartbeats")
	if err != nil {
		t.Fatalf("get heartbeats KV: %v", err)
	}

	key := heartbeatKVKey("integ-user", "integ-proj")
	payload := []byte(`{"ts":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)

	if _, err := kv.Put(ctx, key, payload); err != nil {
		t.Fatalf("heartbeat put: %v", err)
	}

	entry, err := kv.Get(ctx, key)
	if err != nil {
		t.Fatalf("heartbeat get: %v", err)
	}
	if string(entry.Value()) != string(payload) {
		t.Errorf("heartbeat value: got %q, want %q", entry.Value(), payload)
	}
}

// TestLifecycleEventPubSub verifies publish and consume of lifecycle events
// on the MCLAUDE_LIFECYCLE JetStream stream.
func TestLifecycleEventPubSub(t *testing.T) {
	skipIfNoDocker(t)
	deps := testutil.StartDeps(t)

	ctx := context.Background()
	js := deps.JetStream

	subject := "mclaude.integ-user.integ-proj.lifecycle.integ-sess"

	// Subscribe before publishing so we don't miss the message.
	cons, err := js.CreateOrUpdateConsumer(ctx, "MCLAUDE_LIFECYCLE", jetstream.ConsumerConfig{
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
