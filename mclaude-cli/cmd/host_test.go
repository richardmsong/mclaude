package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
	"mclaude-cli/cmd"
	clicontext "mclaude-cli/context"
)

// ── ValidateNKeyPublic ────────────────────────────────────────────────────────

// validUserNKey generates a real U-prefix NKey public key for tests.
func validUserNKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	return pub
}

func TestValidateNKeyPublic_ValidKey(t *testing.T) {
	key := validUserNKey(t)
	if err := cmd.ValidateNKeyPublic(key); err != nil {
		t.Errorf("ValidateNKeyPublic(%q) = %v; want nil", key, err)
	}
}

func TestValidateNKeyPublic_Empty(t *testing.T) {
	err := cmd.ValidateNKeyPublic("")
	if err == nil {
		t.Fatal("ValidateNKeyPublic(\"\") = nil; want error")
	}
	if err.Error() != "invalid NKey public key format" {
		t.Errorf("error = %q; want %q", err.Error(), "invalid NKey public key format")
	}
}

func TestValidateNKeyPublic_WrongPrefix_N(t *testing.T) {
	// N-prefix is a server key — must be rejected.
	kp, err := nkeys.CreateServer()
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	err = cmd.ValidateNKeyPublic(pub)
	if err == nil {
		t.Fatalf("ValidateNKeyPublic(N-key %q) = nil; want error", pub)
	}
	if err.Error() != "invalid NKey public key format" {
		t.Errorf("error = %q; want %q", err.Error(), "invalid NKey public key format")
	}
}

func TestValidateNKeyPublic_TooShort(t *testing.T) {
	// 10-char key — clearly invalid.
	err := cmd.ValidateNKeyPublic("UABCDEFGH1")
	if err == nil {
		t.Fatal("ValidateNKeyPublic(short) = nil; want error")
	}
	if err.Error() != "invalid NKey public key format" {
		t.Errorf("error = %q; want %q", err.Error(), "invalid NKey public key format")
	}
}

func TestValidateNKeyPublic_WrongPrefixLetter(t *testing.T) {
	// Build a 56-char string starting with 'X' (not U).
	key := "X" + strings.Repeat("A", 55)
	err := cmd.ValidateNKeyPublic(key)
	if err == nil {
		t.Fatalf("ValidateNKeyPublic(%q) = nil; want error", key)
	}
	if err.Error() != "invalid NKey public key format" {
		t.Errorf("error = %q; want %q", err.Error(), "invalid NKey public key format")
	}
}

// ── RunHostRegister — validation errors ──────────────────────────────────────

func TestRunHostRegister_NKeyPublicRequired(t *testing.T) {
	flags := cmd.HostRegisterFlags{
		Name: "My Host",
		// NKeyPublic intentionally omitted
	}
	_, err := cmd.RunHostRegister(flags, os.Stderr)
	if err == nil {
		t.Fatal("RunHostRegister with no --nkey-public: expected error; got nil")
	}
	if err.Error() != "--nkey-public is required" {
		t.Errorf("error = %q; want %q", err.Error(), "--nkey-public is required")
	}
}

func TestRunHostRegister_InvalidNKeyFormat(t *testing.T) {
	flags := cmd.HostRegisterFlags{
		Name:       "My Host",
		NKeyPublic: "not-a-valid-nkey",
	}
	_, err := cmd.RunHostRegister(flags, os.Stderr)
	if err == nil {
		t.Fatal("RunHostRegister with invalid nkey: expected error; got nil")
	}
	if err.Error() != "invalid NKey public key format" {
		t.Errorf("error = %q; want %q", err.Error(), "invalid NKey public key format")
	}
}

func TestRunHostRegister_NameRequired(t *testing.T) {
	key := validUserNKey(t)
	flags := cmd.HostRegisterFlags{
		NKeyPublic: key,
		// Name intentionally omitted
	}
	_, err := cmd.RunHostRegister(flags, os.Stderr)
	if err == nil {
		t.Fatal("RunHostRegister with no --name: expected error; got nil")
	}
}

// ── RunHostRegister — NATS publish ───────────────────────────────────────────

// setupHostRegisterEnv creates a temporary directory structure with auth and
// context files for host register tests.
func setupHostRegisterEnv(t *testing.T, natsURL string) (authPath, ctxPath string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MCLAUDE_HOME", dir)

	authPath = filepath.Join(dir, "auth.json")
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUAIBDPBAUTWCWBKIO6XHQNINK5FWJW4OHLXC3HQ2KFE4PEJUA4LNNAL",
		UserSlug: "alice-test",
		NATSUrl:  natsURL,
	}); err != nil {
		t.Fatal(err)
	}

	ctxPath = filepath.Join(dir, "context.json")
	if err := clicontext.Save(ctxPath, &clicontext.Context{
		UserSlug: "alice-test",
	}); err != nil {
		t.Fatal(err)
	}

	return authPath, ctxPath
}

func TestRunHostRegister_PublishesToNATS(t *testing.T) {
	key := validUserNKey(t)
	authPath, ctxPath := setupHostRegisterEnv(t, "wss://nats.example.com/nats")

	nc := NewMockNATSConn(map[string]mockNATSResponse{
		"hosts._.register": {
			Data: mustJSON(map[string]any{
				"ok":   true,
				"slug": "my-aks-host",
			}),
		},
	})

	flags := cmd.HostRegisterFlags{
		Name:        "AKS Canada Central",
		NKeyPublic:  key,
		ContextPath: ctxPath,
		AuthPath:    authPath,
		NATSConn:    nc,
	}

	result, err := cmd.RunHostRegister(flags, os.Stderr)
	if err != nil {
		t.Fatalf("RunHostRegister: %v", err)
	}
	if result.Slug != "my-aks-host" {
		t.Errorf("result.Slug = %q; want %q", result.Slug, "my-aks-host")
	}

	// Verify the NATS request was sent to the correct subject.
	reqs := nc.Requests()
	if len(reqs) != 1 {
		t.Fatalf("NATS requests = %d; want 1", len(reqs))
	}
	want := "mclaude.users.alice-test.hosts._.register"
	if reqs[0].Subject != want {
		t.Errorf("subject = %q; want %q", reqs[0].Subject, want)
	}

	// Verify the payload contains name and nkey_public.
	var payload map[string]any
	if err := json.Unmarshal(reqs[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["name"] != "AKS Canada Central" {
		t.Errorf("payload.name = %q; want %q", payload["name"], "AKS Canada Central")
	}
	if payload["nkey_public"] != key {
		t.Errorf("payload.nkey_public = %q; want %q", payload["nkey_public"], key)
	}
	// defaultPlatform omitted → should not be present (or false).
	if dp, ok := payload["defaultPlatform"]; ok && dp != false {
		t.Errorf("payload.defaultPlatform = %v; want absent or false", dp)
	}
}

func TestRunHostRegister_DefaultPlatformFlag(t *testing.T) {
	key := validUserNKey(t)
	authPath, ctxPath := setupHostRegisterEnv(t, "wss://nats.example.com/nats")

	nc := NewMockNATSConn(map[string]mockNATSResponse{
		"hosts._.register": {
			Data: mustJSON(map[string]any{
				"ok":   true,
				"slug": "aks-canada",
			}),
		},
	})

	flags := cmd.HostRegisterFlags{
		Name:            "AKS Canada",
		NKeyPublic:      key,
		DefaultPlatform: true,
		ContextPath:     ctxPath,
		AuthPath:        authPath,
		NATSConn:        nc,
	}

	if _, err := cmd.RunHostRegister(flags, os.Stderr); err != nil {
		t.Fatalf("RunHostRegister: %v", err)
	}

	reqs := nc.Requests()
	var payload map[string]any
	if err := json.Unmarshal(reqs[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["defaultPlatform"] != true {
		t.Errorf("payload.defaultPlatform = %v; want true", payload["defaultPlatform"])
	}
}

func TestRunHostRegister_WritesConfigJSON(t *testing.T) {
	key := validUserNKey(t)
	const hubURL = "wss://hub.example.com/nats"
	authPath, ctxPath := setupHostRegisterEnv(t, hubURL)

	nc := NewMockNATSConn(map[string]mockNATSResponse{
		"hosts._.register": {
			Data: mustJSON(map[string]any{
				"ok":   true,
				"slug": "my-host",
			}),
		},
	})

	flags := cmd.HostRegisterFlags{
		Name:        "My Host",
		NKeyPublic:  key,
		ContextPath: ctxPath,
		AuthPath:    authPath,
		NATSConn:    nc,
	}

	if _, err := cmd.RunHostRegister(flags, os.Stderr); err != nil {
		t.Fatalf("RunHostRegister: %v", err)
	}

	// ~/.mclaude/hosts/my-host/config.json must exist.
	mclaudeHome := os.Getenv("MCLAUDE_HOME")
	cfgPath := filepath.Join(mclaudeHome, "hosts", "my-host", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}

	var cfg cmd.HostConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config.json: %v", err)
	}
	if cfg.Slug != "my-host" {
		t.Errorf("config.Slug = %q; want %q", cfg.Slug, "my-host")
	}
	if cfg.HubURL != hubURL {
		t.Errorf("config.HubURL = %q; want %q", cfg.HubURL, hubURL)
	}

	// No nkey.seed, no nats.creds — per ADR-0097.
	for _, banned := range []string{"nkey.seed", "nats.creds"} {
		p := filepath.Join(mclaudeHome, "hosts", "my-host", banned)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("unexpected file %s (should not exist)", p)
		}
	}
}

func TestRunHostRegister_ServerError(t *testing.T) {
	key := validUserNKey(t)
	authPath, ctxPath := setupHostRegisterEnv(t, "wss://nats.example.com/nats")

	nc := NewMockNATSConn(map[string]mockNATSResponse{
		"hosts._.register": {
			Data: mustJSON(map[string]any{
				"ok":    false,
				"error": "admin_required",
			}),
		},
	})

	flags := cmd.HostRegisterFlags{
		Name:        "My Host",
		NKeyPublic:  key,
		ContextPath: ctxPath,
		AuthPath:    authPath,
		NATSConn:    nc,
	}

	_, err := cmd.RunHostRegister(flags, os.Stderr)
	if err == nil {
		t.Fatal("RunHostRegister: expected error for server rejection; got nil")
	}
	if !strings.Contains(err.Error(), "admin_required") {
		t.Errorf("error = %q; want mention of admin_required", err.Error())
	}
}

// ── HostInfo has no Type or Role fields ──────────────────────────────────────

func TestHostInfo_NoTypeNoRole(t *testing.T) {
	// Marshal HostInfo and verify the JSON has no type/role keys.
	h := cmd.HostInfo{
		Slug:   "my-host",
		Name:   "My Host",
		Online: true,
	}
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, forbidden := range []string{"type", "role"} {
		if _, ok := m[forbidden]; ok {
			t.Errorf("HostInfo JSON contains unexpected field %q", forbidden)
		}
	}
}
