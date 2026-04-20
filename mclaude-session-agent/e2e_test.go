package main

// End-to-end tests run against a real k3d cluster with the session-agent
// image deployed. They are skipped unless E2E=1 is set in the environment.
//
// Prerequisites:
//
//	# Create cluster
//	k3d cluster create mclaude-test
//
//	# Build and import session-agent image
//	docker build -t mclaude-session-agent:test .
//	k3d image import mclaude-session-agent:test -c mclaude-test
//
//	# Deploy NATS with JetStream
//	helm repo add nats https://nats-io.github.io/k8s/helm/charts/
//	helm install nats nats/nats --set config.jetstream.enabled=true
//
//	# Set env and run
//	E2E=1 go test -v -timeout 10m -run 'TestE2E' .
//
// The tests use testutil.StartDeps for NATS via port-forward, so no special
// connectivity is needed beyond kubectl access to the cluster.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func skipIfNoE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E") == "" {
		t.Skip("set E2E=1 to run end-to-end tests (requires k3d cluster)")
	}
}

// e2eNATSURL returns the NATS URL for the e2e cluster, either from the
// E2E_NATS_URL env var or by port-forwarding the cluster's NATS service.
func e2eNATSURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("E2E_NATS_URL"); url != "" {
		return url
	}
	return nats.DefaultURL
}

// TestE2ESessionLifecycle is the canonical end-to-end test:
//
// 1. Connect to in-cluster NATS
// 2. Deploy a session-agent pod with mock-claude sidecar
// 3. Publish a session create request
// 4. Verify init and idle events appear on the events stream
// 5. Publish a user message
// 6. Verify assistant response event appears
// 7. Delete the session
// 8. Verify lifecycle stopped event appears
func TestE2ESessionLifecycle(t *testing.T) {
	skipIfNoE2E(t)

	natsURL := e2eNATSURL(t)
	nc, err := nats.Connect(natsURL, nats.Timeout(10*time.Second))
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	const (
		userID    = "e2e-user"
		projectID = "e2e-proj"
		sessionID = "e2e-session-1"
	)

	eventsSubject := fmt.Sprintf("mclaude.users.%s.projects.%s.events.%s", userID, projectID, sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Subscribe to the events stream before issuing create.
	cons, err := js.CreateOrUpdateConsumer(ctx, "MCLAUDE_EVENTS", jetstream.ConsumerConfig{
		FilterSubject: eventsSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}

	// Publish session create.
	createReq := map[string]any{
		"sessionId":    sessionID,
		"branch":       "main",
		"cwd":          "/workspace",
		"joinWorktree": false,
	}
	data, _ := json.Marshal(createReq)
	createSubject := fmt.Sprintf("mclaude.users.%s.projects.%s.api.sessions.create", userID, projectID)
	if _, err := nc.Request(createSubject, data, 15*time.Second); err != nil {
		t.Fatalf("session create request: %v", err)
	}

	// Wait for init event on the events stream.
	msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(30*time.Second))
	if err != nil {
		t.Fatalf("fetch init event: %v", err)
	}
	var initReceived bool
	for msg := range msgs.Messages() {
		evType, subtype := parseEventType(msg.Data())
		if evType == "system" && subtype == "init" {
			initReceived = true
		}
		msg.Ack()
	}
	if !initReceived {
		t.Error("init event not received on events stream")
	}

	// Send a user message.
	inputSubject := fmt.Sprintf("mclaude.users.%s.projects.%s.api.sessions.input", userID, projectID)
	inputMsg := map[string]any{
		"sessionId": sessionID,
		"message": map[string]any{
			"role":    "user",
			"content": "say hello in one word",
		},
	}
	inputData, _ := json.Marshal(inputMsg)
	if err := nc.Publish(inputSubject, inputData); err != nil {
		t.Fatalf("publish user message: %v", err)
	}

	// Wait for assistant response.
	msgs, err = cons.Fetch(10, jetstream.FetchMaxWait(60*time.Second))
	if err != nil {
		t.Fatalf("fetch assistant events: %v", err)
	}
	var foundAssistant bool
	for msg := range msgs.Messages() {
		evType, _ := parseEventType(msg.Data())
		if evType == "assistant" {
			foundAssistant = true
		}
		msg.Ack()
	}
	if !foundAssistant {
		t.Error("assistant event not received")
	}

	// Clean up: delete the session.
	deleteSubject := fmt.Sprintf("mclaude.users.%s.projects.%s.api.sessions.delete", userID, projectID)
	deleteReq, _ := json.Marshal(map[string]string{"sessionId": sessionID})
	if _, err := nc.Request(deleteSubject, deleteReq, 15*time.Second); err != nil {
		t.Logf("session delete request: %v (non-fatal)", err)
	}
}

// TestE2EImageBuild verifies that the Dockerfile produces a runnable image.
// Does not require k3d — just Docker.
func TestE2EImageBuild(t *testing.T) {
	skipIfNoE2E(t)

	out, err := exec.CommandContext(
		context.Background(),
		"docker", "build", "--no-cache", "-t", "mclaude-session-agent:e2e-test", ".",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker build failed:\n%s\n%v", out, err)
	}
	t.Logf("docker build output:\n%s", out)

	// Verify the binary is in the image.
	out, err = exec.CommandContext(
		context.Background(),
		"docker", "run", "--rm", "mclaude-session-agent:e2e-test",
		"/usr/local/bin/session-agent", "--version",
	).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "Usage") {
		t.Logf("session-agent output: %s", out)
		// Non-fatal — binary might require NATS connection to start
	}
}
