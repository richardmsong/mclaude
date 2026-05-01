package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mclaude-cli/cmd"
)

// ── SaveAuth / LoadAuth ───────────────────────────────────────────────────────

func TestSaveAuthCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANKEY...",
		UserSlug: "alice-test",
	}
	if err := cmd.SaveAuth(path, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	// File must exist with mode 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o; want 0600", info.Mode().Perm())
	}
}

func TestSaveAuthWritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	creds := &cmd.AuthCredentials{
		JWT:      "nats-jwt-value",
		NKeySeed: "nkey-seed-value",
		UserSlug: "bob-test",
	}
	if err := cmd.SaveAuth(path, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m["jwt"] != "nats-jwt-value" {
		t.Errorf("jwt = %q; want nats-jwt-value", m["jwt"])
	}
	if m["nkeySeed"] != "nkey-seed-value" {
		t.Errorf("nkeySeed = %q; want nkey-seed-value", m["nkeySeed"])
	}
	if m["userSlug"] != "bob-test" {
		t.Errorf("userSlug = %q; want bob-test", m["userSlug"])
	}
}

func TestLoadAuthMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	_, err := cmd.LoadAuth(path)
	if err == nil {
		t.Fatal("LoadAuth: expected error for missing file; got nil")
	}
	// Error must mention 'mclaude login'.
	if !containsAny(err.Error(), "mclaude login", "not logged in") {
		t.Errorf("error %q; want mention of 'mclaude login'", err.Error())
	}
}

func TestLoadAuthRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	orig := &cmd.AuthCredentials{
		JWT:      "jwt-abc",
		NKeySeed: "seed-def",
		UserSlug: "carol-test",
	}
	if err := cmd.SaveAuth(path, orig); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	loaded, err := cmd.LoadAuth(path)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if loaded.JWT != orig.JWT {
		t.Errorf("JWT = %q; want %q", loaded.JWT, orig.JWT)
	}
	if loaded.NKeySeed != orig.NKeySeed {
		t.Errorf("NKeySeed = %q; want %q", loaded.NKeySeed, orig.NKeySeed)
	}
	if loaded.UserSlug != orig.UserSlug {
		t.Errorf("UserSlug = %q; want %q", loaded.UserSlug, orig.UserSlug)
	}
}

func TestLoadAuthMissingJWT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Write a file with missing jwt field.
	data := []byte(`{"nkeySeed":"seed","userSlug":"alice"}` + "\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := cmd.LoadAuth(path)
	if err == nil {
		t.Fatal("LoadAuth: expected error for missing jwt; got nil")
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
