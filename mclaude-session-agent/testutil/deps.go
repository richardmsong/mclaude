// Package testutil provides shared test infrastructure for mclaude-session-agent.
package testutil

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go/modules/compose"
)

// Deps holds live test dependencies started by StartDeps.
type Deps struct {
	NATSConn  *nats.Conn
	JetStream jetstream.JetStream
	NATSURL   string
}

// StartDeps starts NATS + Postgres via Docker Compose and returns a Deps
// with live connections. Registers t.Cleanup to stop services.
func StartDeps(t *testing.T) *Deps {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	composePath := filepath.Join(filepath.Dir(thisFile), "docker-compose.yml")

	stack, err := compose.NewDockerCompose(composePath)
	if err != nil {
		t.Fatalf("docker-compose init: %v", err)
	}

	ctx := context.Background()

	if err := stack.Up(ctx, compose.Wait(true)); err != nil {
		t.Fatalf("docker-compose up: %v", err)
	}
	t.Cleanup(func() {
		if err := stack.Down(context.Background(), compose.RemoveOrphans(true)); err != nil {
			t.Logf("docker-compose down: %v", err)
		}
	})

	natsContainer, err := stack.ServiceContainer(ctx, "nats")
	if err != nil {
		t.Fatalf("get nats container: %v", err)
	}
	natsPort, err := natsContainer.MappedPort(ctx, "4222")
	if err != nil {
		t.Fatalf("get nats port: %v", err)
	}
	natsURL := fmt.Sprintf("nats://127.0.0.1:%s", natsPort.Port())

	var nc *nats.Conn
	deadline := time.Now().Add(30 * time.Second)
	for {
		nc, err = nats.Connect(natsURL)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("NATS not ready after 30s: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() { nc.Close() })

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	return &Deps{
		NATSConn:  nc,
		JetStream: js,
		NATSURL:   natsURL,
	}
}

// CreateUserResources creates the per-user JetStream resources required by the
// session-agent per ADR-0054: two KV buckets and one sessions stream.
// Must be called before NewAgent for the given userSlug.
func CreateUserResources(t *testing.T, js jetstream.JetStream, userSlug string) {
	t.Helper()
	ctx := context.Background()

	for _, bucket := range []string{
		"mclaude-sessions-" + userSlug,
		"mclaude-projects-" + userSlug,
	} {
		if _, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: bucket,
		}); err != nil {
			t.Fatalf("CreateUserResources: create KV bucket %q: %v", bucket, err)
		}
	}

	streamName := "MCLAUDE_SESSIONS_" + userSlug
	subjects := []string{"mclaude.users." + userSlug + ".hosts.*.projects.*.sessions.>"}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  subjects,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
		Storage:   jetstream.FileStorage,
		Discard:   jetstream.DiscardOld,
	}); err != nil {
		t.Fatalf("CreateUserResources: create stream %q: %v", streamName, err)
	}
}

// MockClaudePath builds the mock-claude binary and returns its path.
// The binary is placed in t.TempDir() and cleaned up automatically.
func MockClaudePath(t *testing.T) string {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	srcDir := filepath.Join(filepath.Dir(thisFile), "mock-claude")
	binPath := filepath.Join(t.TempDir(), "mock-claude")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock-claude:\n%s\n%v", out, err)
	}
	return binPath
}

// TranscriptPath returns the absolute path to a named transcript file.
func TranscriptPath(name string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "transcripts", name)
}
