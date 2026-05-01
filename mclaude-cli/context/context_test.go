// Tests for the context package.
// Covers: Load (missing file, valid file, corrupt file), Save (round-trip),
// ValidateUserSlug, ValidateProjectSlug, ValidateHostSlug, ParseProjectSlug
// (with/without "@" prefix), ParseUserSlug.
package context_test

import (
	"os"
	"path/filepath"
	"testing"

	clicontext "mclaude-cli/context"
)

// ── Load ─────────────────────────────────────────────────────────────────────

func TestLoadMissingFile(t *testing.T) {
	ctx, err := clicontext.Load("/tmp/mclaude-cli-test-does-not-exist.json")
	if err != nil {
		t.Fatalf("Load missing file: got error %v; want nil", err)
	}
	if ctx.UserSlug != "" || ctx.ProjectSlug != "" || ctx.HostSlug != "" {
		t.Errorf("Load missing file: got non-empty context %+v", ctx)
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.json")
	if err := os.WriteFile(path, []byte(`{"userSlug":"alice-gmail","projectSlug":"my-project"}`+"\n"), 0600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	ctx, err := clicontext.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ctx.UserSlug != "alice-gmail" {
		t.Errorf("UserSlug = %q; want alice-gmail", ctx.UserSlug)
	}
	if ctx.ProjectSlug != "my-project" {
		t.Errorf("ProjectSlug = %q; want my-project", ctx.ProjectSlug)
	}
	if ctx.HostSlug != "" {
		t.Errorf("HostSlug = %q; want empty", ctx.HostSlug)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := clicontext.Load(path)
	if err == nil {
		t.Fatal("Load corrupt file: got nil error; want parse error")
	}
}

// ── Save / round-trip ────────────────────────────────────────────────────────

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "context.json")

	want := &clicontext.Context{
		UserSlug:    "alice-gmail",
		ProjectSlug: "my-project",
		HostSlug:    "work-mbp",
	}
	if err := clicontext.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := clicontext.Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if *got != *want {
		t.Errorf("round-trip: got %+v; want %+v", *got, *want)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "context.json")

	if err := clicontext.Save(path, &clicontext.Context{UserSlug: "x-y"}); err != nil {
		t.Fatalf("Save: unexpected error creating nested dirs: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestSaveOmitsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.json")

	if err := clicontext.Save(path, &clicontext.Context{UserSlug: "alice-gmail"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(path)
	// "projectSlug" and "hostSlug" should be absent (omitempty)
	content := string(data)
	if contains(content, "projectSlug") {
		t.Errorf("saved file contains projectSlug even though it is empty: %s", content)
	}
	if contains(content, "hostSlug") {
		t.Errorf("saved file contains hostSlug even though it is empty: %s", content)
	}
}

// ── ValidateUserSlug ─────────────────────────────────────────────────────────

func TestValidateUserSlugValid(t *testing.T) {
	cases := []string{"alice-gmail", "bob-rbc", "x1", "a-b-c"}
	for _, s := range cases {
		if err := clicontext.ValidateUserSlug(s); err != nil {
			t.Errorf("ValidateUserSlug(%q): unexpected error %v", s, err)
		}
	}
}

func TestValidateUserSlugEmpty(t *testing.T) {
	// Empty is allowed (means "use context default").
	if err := clicontext.ValidateUserSlug(""); err != nil {
		t.Errorf("ValidateUserSlug(\"\"): unexpected error %v", err)
	}
}

func TestValidateUserSlugInvalid(t *testing.T) {
	cases := []string{"users", "UPPER", "-start", "has space", "has.dot"}
	for _, s := range cases {
		if err := clicontext.ValidateUserSlug(s); err == nil {
			t.Errorf("ValidateUserSlug(%q): expected error; got nil", s)
		}
	}
}

// ── ValidateProjectSlug ──────────────────────────────────────────────────────

func TestValidateProjectSlugValid(t *testing.T) {
	cases := []string{"my-project", "mclaude", "@my-project", "@mclaude"}
	for _, s := range cases {
		if err := clicontext.ValidateProjectSlug(s); err != nil {
			t.Errorf("ValidateProjectSlug(%q): unexpected error %v", s, err)
		}
	}
}

func TestValidateProjectSlugInvalid(t *testing.T) {
	cases := []string{"projects", "HAS_UPPER", "-bad", "bad slug"}
	for _, s := range cases {
		if err := clicontext.ValidateProjectSlug(s); err == nil {
			t.Errorf("ValidateProjectSlug(%q): expected error; got nil", s)
		}
	}
}

// ── ValidateHostSlug ─────────────────────────────────────────────────────────

func TestValidateHostSlugValid(t *testing.T) {
	if err := clicontext.ValidateHostSlug("work-mbp"); err != nil {
		t.Errorf("ValidateHostSlug(\"work-mbp\"): %v", err)
	}
}

func TestValidateHostSlugInvalid(t *testing.T) {
	if err := clicontext.ValidateHostSlug("hosts"); err == nil {
		t.Error("ValidateHostSlug(\"hosts\"): expected error for reserved word; got nil")
	}
}

// ── ParseProjectSlug ─────────────────────────────────────────────────────────

func TestParseProjectSlugBare(t *testing.T) {
	got, err := clicontext.ParseProjectSlug("my-project")
	if err != nil {
		t.Fatalf("ParseProjectSlug: %v", err)
	}
	if got != "my-project" {
		t.Errorf("got %q; want my-project", got)
	}
}

func TestParseProjectSlugAtPrefix(t *testing.T) {
	got, err := clicontext.ParseProjectSlug("@my-project")
	if err != nil {
		t.Fatalf("ParseProjectSlug @-prefix: %v", err)
	}
	if got != "my-project" {
		t.Errorf("got %q; want my-project (@ stripped)", got)
	}
}

func TestParseProjectSlugEmpty(t *testing.T) {
	got, err := clicontext.ParseProjectSlug("")
	if err != nil {
		t.Fatalf("ParseProjectSlug empty: %v", err)
	}
	if got != "" {
		t.Errorf("got %q; want empty", got)
	}
}

func TestParseProjectSlugInvalid(t *testing.T) {
	_, err := clicontext.ParseProjectSlug("has space")
	if err == nil {
		t.Error("ParseProjectSlug invalid: expected error; got nil")
	}
}

// ── ParseUserSlug ─────────────────────────────────────────────────────────────

func TestParseUserSlugValid(t *testing.T) {
	got, err := clicontext.ParseUserSlug("alice-gmail")
	if err != nil {
		t.Fatalf("ParseUserSlug: %v", err)
	}
	if got != "alice-gmail" {
		t.Errorf("got %q; want alice-gmail", got)
	}
}

func TestParseUserSlugEmpty(t *testing.T) {
	got, err := clicontext.ParseUserSlug("")
	if err != nil {
		t.Fatalf("ParseUserSlug empty: %v", err)
	}
	if got != "" {
		t.Errorf("got %q; want empty", got)
	}
}

func TestParseUserSlugInvalid(t *testing.T) {
	_, err := clicontext.ParseUserSlug("users")
	if err == nil {
		t.Error("ParseUserSlug reserved word: expected error; got nil")
	}
}

// ── DefaultPath ──────────────────────────────────────────────────────────────

func TestDefaultPathFromEnv(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "ctx.json")
	t.Setenv("MCLAUDE_CONTEXT_FILE", want)
	if got := clicontext.DefaultPath(); got != want {
		t.Errorf("DefaultPath() = %q; want %q", got, want)
	}
}

func TestDefaultPathFallback(t *testing.T) {
	// Clear env var; should fall back to ~/.mclaude/context.json.
	t.Setenv("MCLAUDE_CONTEXT_FILE", "")
	p := clicontext.DefaultPath()
	if p == "" {
		t.Skip("no home directory available; skipping")
	}
	if !endsWith(p, "context.json") {
		t.Errorf("DefaultPath() = %q; want path ending in context.json", p)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// ── Server field ──────────────────────────────────────────────────────────────

func TestContextServerFieldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.json")

	want := &clicontext.Context{
		UserSlug: "alice-test",
		Server:   "https://api.example.com",
	}
	if err := clicontext.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := clicontext.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Server != "https://api.example.com" {
		t.Errorf("Server = %q; want https://api.example.com", got.Server)
	}
}

func TestContextServerOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.json")

	ctx := &clicontext.Context{UserSlug: "alice-test"}
	if err := clicontext.Save(path, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// "server" must not appear in JSON when empty (omitempty).
	if containsStr(string(data), `"server"`) {
		t.Errorf("JSON contains 'server' field when it should be omitted: %s", data)
	}
}

// ── ResolveServerURL ──────────────────────────────────────────────────────────

func TestResolveServerURLOverrideTakesPriority(t *testing.T) {
	ctx := &clicontext.Context{Server: "https://context.example.com"}
	got := clicontext.ResolveServerURL("https://flag.example.com", ctx)
	if got != "https://flag.example.com" {
		t.Errorf("ResolveServerURL = %q; want flag override", got)
	}
}

func TestResolveServerURLContextFallback(t *testing.T) {
	ctx := &clicontext.Context{Server: "https://context.example.com"}
	got := clicontext.ResolveServerURL("", ctx)
	if got != "https://context.example.com" {
		t.Errorf("ResolveServerURL = %q; want context value", got)
	}
}

func TestResolveServerURLDefault(t *testing.T) {
	ctx := &clicontext.Context{}
	got := clicontext.ResolveServerURL("", ctx)
	if got != clicontext.DefaultServerURL {
		t.Errorf("ResolveServerURL = %q; want %q", got, clicontext.DefaultServerURL)
	}
}

func TestResolveServerURLNilContext(t *testing.T) {
	got := clicontext.ResolveServerURL("", nil)
	if got != clicontext.DefaultServerURL {
		t.Errorf("ResolveServerURL = %q; want %q", got, clicontext.DefaultServerURL)
	}
}
