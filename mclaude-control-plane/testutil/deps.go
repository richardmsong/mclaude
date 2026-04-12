// Package testutil provides shared test helpers for mclaude-control-plane tests.
package testutil

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/compose"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Deps holds addresses for test infrastructure dependencies.
type Deps struct {
	// NATSAddr is the nats:// URL for the test NATS server.
	NATSAddr string
	// PostgresDSN is the postgres:// DSN for the test Postgres instance.
	PostgresDSN string
}

// SetupDeps starts NATS and Postgres via Docker Compose and returns their
// addresses plus a cleanup function. Unlike StartDeps, SetupDeps does not
// register cleanup via t.Cleanup — the caller is responsible for calling the
// returned function. This is intended for use in TestMain.
//
// Uses a real NATS server (with JetStream) and real Postgres — not mocks —
// so tests catch subject permission errors, JetStream config issues, and
// real Postgres constraint violations.
func SetupDeps(ctx context.Context) (*Deps, func(), error) {
	_, thisFile, _, _ := runtime.Caller(0)
	composeFile := filepath.Join(filepath.Dir(thisFile), "docker-compose.yml")

	dc, err := compose.NewDockerCompose(composeFile)
	if err != nil {
		return nil, nil, fmt.Errorf("compose.NewDockerCompose: %w", err)
	}

	cleanup := func() {
		if err := dc.Down(context.Background(), compose.RemoveOrphans(true)); err != nil {
			fmt.Printf("testutil.SetupDeps cleanup: compose down: %v\n", err)
		}
	}

	// Wait for NATS client port (4222) — reliable, doesn't require monitoring.
	// Wait for Postgres log twice: first occurrence is during the init server
	// (container first-run init), second is after the real server starts.
	err = dc.
		WaitForService("nats",
			wait.ForListeningPort("4222/tcp").
				WithStartupTimeout(45*time.Second),
		).
		WaitForService("postgres",
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		).
		Up(ctx)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("compose up: %w", err)
	}

	natsC, err := dc.ServiceContainer(ctx, "nats")
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("nats service container: %w", err)
	}
	natsPort, err := natsC.MappedPort(ctx, "4222")
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("nats mapped port: %w", err)
	}

	pgC, err := dc.ServiceContainer(ctx, "postgres")
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("postgres service container: %w", err)
	}
	pgPort, err := pgC.MappedPort(ctx, "5432")
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("postgres mapped port: %w", err)
	}

	deps := &Deps{
		NATSAddr:    fmt.Sprintf("nats://localhost:%s", natsPort.Port()),
		PostgresDSN: fmt.Sprintf("postgres://mclaude:mclaude@localhost:%s/mclaude_test?sslmode=disable", pgPort.Port()),
	}
	return deps, cleanup, nil
}

// StartDeps starts NATS and Postgres via Docker Compose, registers cleanup via
// t.Cleanup, and returns the addresses. For use in individual test functions.
// For TestMain usage, call SetupDeps directly.
func StartDeps(t *testing.T) *Deps {
	t.Helper()
	ctx := context.Background()
	deps, cleanup, err := SetupDeps(ctx)
	if err != nil {
		t.Fatalf("StartDeps: %v", err)
	}
	t.Cleanup(cleanup)
	return deps
}
