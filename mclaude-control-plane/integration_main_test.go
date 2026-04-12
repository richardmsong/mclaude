//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"mclaude-control-plane/testutil"
)

// integDeps is the shared compose stack for all integration tests.
// Started once in TestMain, shared across all TestIntegration_* functions.
var integDeps *testutil.Deps

func TestMain(m *testing.M) {
	deps, cleanup, err := testutil.SetupDeps(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration TestMain: %v\n", err)
		os.Exit(1)
	}
	integDeps = deps
	code := m.Run()
	cleanup()
	os.Exit(code)
}
