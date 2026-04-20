// Tests for cmd.RunSessionList.
//
// Covers: context defaults, flag overrides, @pslug short form,
// invalid slug rejection, missing user/project slug errors,
// and pkg/subj output correctness.
package cmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mclaude-cli/cmd"
	clicontext "mclaude-cli/context"
)

// writeContext writes a context.json to a temp dir and returns the path.
func writeContext(t *testing.T, ctx clicontext.Context) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "context.json")
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatalf("write context: %v", err)
	}
	return path
}

// ── Context defaults ──────────────────────────────────────────────────────────

func TestSessionListContextDefaults(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{
		UserSlug:    "alice-gmail",
		ProjectSlug: "my-project",
	})

	var out bytes.Buffer
	result, err := cmd.RunSessionList(cmd.SessionListFlags{ContextPath: ctxPath}, &out)
	if err != nil {
		t.Fatalf("RunSessionList: %v", err)
	}

	if result.UserSlug != "alice-gmail" {
		t.Errorf("UserSlug = %q; want alice-gmail", result.UserSlug)
	}
	if result.ProjectSlug != "my-project" {
		t.Errorf("ProjectSlug = %q; want my-project", result.ProjectSlug)
	}
	// KV prefix should be "{uslug}.{pslug}"
	if result.KVKeyPrefix != "alice-gmail.my-project" {
		t.Errorf("KVKeyPrefix = %q; want alice-gmail.my-project", result.KVKeyPrefix)
	}
}

// ── Flag override ─────────────────────────────────────────────────────────────

func TestSessionListProjectFlagOverride(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{
		UserSlug:    "alice-gmail",
		ProjectSlug: "default-project",
	})

	var out bytes.Buffer
	result, err := cmd.RunSessionList(cmd.SessionListFlags{
		ContextPath: ctxPath,
		ProjectSlug: "other-project",
	}, &out)
	if err != nil {
		t.Fatalf("RunSessionList: %v", err)
	}

	if result.ProjectSlug != "other-project" {
		t.Errorf("ProjectSlug = %q; want other-project (flag override)", result.ProjectSlug)
	}
	if strings.Contains(result.KVKeyPrefix, "default-project") {
		t.Errorf("KVKeyPrefix %q still contains default-project; flag was not applied", result.KVKeyPrefix)
	}
}

// ── @pslug short form ─────────────────────────────────────────────────────────

func TestSessionListAtPrefixProjectFlag(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{UserSlug: "bob-rbc"})

	var out bytes.Buffer
	result, err := cmd.RunSessionList(cmd.SessionListFlags{
		ContextPath: ctxPath,
		ProjectSlug: "@my-project",
	}, &out)
	if err != nil {
		t.Fatalf("RunSessionList with @-prefix: %v", err)
	}

	// @ should be stripped from the resolved slug.
	if result.ProjectSlug != "my-project" {
		t.Errorf("ProjectSlug = %q; want my-project (@ stripped)", result.ProjectSlug)
	}
}

// ── Slug validation ───────────────────────────────────────────────────────────

func TestSessionListInvalidProjectSlug(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{UserSlug: "alice-gmail"})

	var out bytes.Buffer
	_, err := cmd.RunSessionList(cmd.SessionListFlags{
		ContextPath: ctxPath,
		ProjectSlug: "projects", // reserved word
	}, &out)
	if err == nil {
		t.Error("RunSessionList: expected error for reserved word 'projects'; got nil")
	}
}

func TestSessionListInvalidUserSlug(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{})

	var out bytes.Buffer
	_, err := cmd.RunSessionList(cmd.SessionListFlags{
		ContextPath: ctxPath,
		UserSlug:    "HAS_UPPER",
	}, &out)
	if err == nil {
		t.Error("RunSessionList: expected error for uppercase user slug; got nil")
	}
}

// ── Missing required slugs ────────────────────────────────────────────────────

func TestSessionListMissingUserSlug(t *testing.T) {
	// Empty context, no flag — user slug must be required.
	ctxPath := writeContext(t, clicontext.Context{ProjectSlug: "my-project"})

	var out bytes.Buffer
	_, err := cmd.RunSessionList(cmd.SessionListFlags{ContextPath: ctxPath}, &out)
	if err == nil {
		t.Error("RunSessionList: expected error when user slug is missing; got nil")
	}
	if !strings.Contains(err.Error(), "user slug required") {
		t.Errorf("error %q; want 'user slug required'", err.Error())
	}
}

func TestSessionListMissingProjectSlug(t *testing.T) {
	// User slug set, project slug missing.
	ctxPath := writeContext(t, clicontext.Context{UserSlug: "alice-gmail"})

	var out bytes.Buffer
	_, err := cmd.RunSessionList(cmd.SessionListFlags{ContextPath: ctxPath}, &out)
	if err == nil {
		t.Error("RunSessionList: expected error when project slug is missing; got nil")
	}
	if !strings.Contains(err.Error(), "project slug required") {
		t.Errorf("error %q; want 'project slug required'", err.Error())
	}
}

// ── pkg/subj output ──────────────────────────────────────────────────────────

func TestSessionListKVKeyPrefixShape(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{
		UserSlug:    "richard-rbc",
		ProjectSlug: "mclaude",
	})

	var out bytes.Buffer
	result, err := cmd.RunSessionList(cmd.SessionListFlags{ContextPath: ctxPath}, &out)
	if err != nil {
		t.Fatalf("RunSessionList: %v", err)
	}

	// KV key must match the ADR-0024 format: {uslug}.{pslug}
	wantKV := "richard-rbc.mclaude"
	if result.KVKeyPrefix != wantKV {
		t.Errorf("KVKeyPrefix = %q; want %q", result.KVKeyPrefix, wantKV)
	}

	// Events subject must start with the correct typed-slug prefix.
	wantEventsPrefix := "mclaude.users.richard-rbc.projects.mclaude.events."
	if !strings.HasPrefix(result.EventsSubject, wantEventsPrefix) {
		t.Errorf("EventsSubject = %q; want prefix %q", result.EventsSubject, wantEventsPrefix)
	}
}

// ── Output text ──────────────────────────────────────────────────────────────

func TestSessionListOutputContainsSlug(t *testing.T) {
	ctxPath := writeContext(t, clicontext.Context{
		UserSlug:    "alice-gmail",
		ProjectSlug: "my-project",
	})

	var out bytes.Buffer
	_, err := cmd.RunSessionList(cmd.SessionListFlags{ContextPath: ctxPath}, &out)
	if err != nil {
		t.Fatalf("RunSessionList: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "alice-gmail") {
		t.Errorf("output %q missing user slug", output)
	}
	if !strings.Contains(output, "my-project") {
		t.Errorf("output %q missing project slug", output)
	}
}
