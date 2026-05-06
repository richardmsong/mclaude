package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	clicontext "mclaude-cli/context"

	"github.com/nats-io/nkeys"

	"mclaude-control-plane/slug"
)

// --------------------------------------------------------------------------
// Host directory management — ~/.mclaude/hosts/{hslug}/
// --------------------------------------------------------------------------

// mclaudeDir returns ~/.mclaude, respecting MCLAUDE_HOME for tests.
func mclaudeDir() string {
	if v := os.Getenv("MCLAUDE_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mclaude")
}

// hostDir returns ~/.mclaude/hosts/{hslug}.
func hostDir(hslug string) string {
	return filepath.Join(mclaudeDir(), "hosts", hslug)
}

// activeHostPath returns ~/.mclaude/active-host.
func activeHostPath() string {
	return filepath.Join(mclaudeDir(), "active-host")
}

// ResolveActiveHost reads the active-host symlink and returns the target host
// slug. Returns "" if the symlink does not exist.
func ResolveActiveHost() string {
	target, err := os.Readlink(activeHostPath())
	if err != nil {
		return ""
	}
	// The symlink points to "hosts/{hslug}" — extract the slug.
	return filepath.Base(target)
}

// --------------------------------------------------------------------------
// Host config stored at ~/.mclaude/hosts/{hslug}/config.json
// --------------------------------------------------------------------------

// HostConfig is persisted when a host registration completes.
type HostConfig struct {
	Slug   string `json:"slug"`
	HubURL string `json:"hubUrl"`
}

// --------------------------------------------------------------------------
// NKey validation
// --------------------------------------------------------------------------

// ValidateNKeyPublic validates that key is a valid NKey public key for a
// user identity (U-prefix, 56-character base32-encoded Ed25519 public key).
// Returns nil if valid, an error with the message "invalid NKey public key
// format" if the key fails format validation.
//
// Spec (ADR-0097, spec-cli.md §host register):
//   - 56 characters long
//   - Base32 alphabet (uppercase A-Z, 2-7)
//   - First character is 'U' (user-level NKey only; N-prefix server keys rejected)
func ValidateNKeyPublic(key string) error {
	if !nkeys.IsValidPublicUserKey(key) {
		return fmt.Errorf("invalid NKey public key format")
	}
	return nil
}

// --------------------------------------------------------------------------
// mclaude host register --name <name> --nkey-public <key> [--default-platform]
// --------------------------------------------------------------------------

// HostRegisterFlags holds parsed flags for "mclaude host register".
type HostRegisterFlags struct {
	// Name is the display name for the host (required).
	Name string
	// NKeyPublic is the NKey public key of the host (required, U-prefix, 56-char base32).
	NKeyPublic string
	// DefaultPlatform sets this host as the default platform (admin-only).
	DefaultPlatform bool
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// AuthPath overrides ~/.mclaude/auth.json (for tests).
	AuthPath string
	// NATSConn allows injecting a pre-connected NATS connection for tests.
	// If nil, RunHostRegister connects to NATS using the JWT + NKeySeed from auth.
	NATSConn NATSConn
}

// HostRegisterResult is returned by RunHostRegister on success.
type HostRegisterResult struct {
	// Slug is the assigned host slug returned by the control-plane.
	Slug string
}

// hostRegisterRequest is the NATS request payload for host registration.
// Subject: mclaude.users.{uslug}.hosts._.register
// Spec: spec-nats-payload-schema.md §mclaude.users.{uslug}.hosts._.register
type hostRegisterRequest struct {
	ID              string `json:"id"`
	Ts              int64  `json:"ts"`
	Name            string `json:"name"`
	NKeyPublic      string `json:"nkey_public"`
	DefaultPlatform bool   `json:"defaultPlatform,omitempty"`
}

// hostRegisterResponse is the NATS response from host registration.
type hostRegisterResponse struct {
	OK    bool   `json:"ok"`
	Slug  string `json:"slug,omitempty"`
	Error string `json:"error,omitempty"`
}

// RunHostRegister registers a host with the control-plane via NATS.
//
// Validates --nkey-public (required, U-prefix, 56-char base32), then
// publishes to mclaude.users.{uslug}.hosts._.register with {name, nkey_public,
// defaultPlatform?}. On success, writes ~/.mclaude/hosts/{hslug}/config.json
// with hubUrl (from auth.json.natsUrl) and prints the host slug.
//
// Errors:
//   - "--nkey-public is required" if --nkey-public is omitted
//   - "invalid NKey public key format" if key fails format validation
func RunHostRegister(flags HostRegisterFlags, out io.Writer) (*HostRegisterResult, error) {
	// Validate --nkey-public: required.
	if flags.NKeyPublic == "" {
		return nil, fmt.Errorf("--nkey-public is required")
	}
	// Validate NKey format.
	if err := ValidateNKeyPublic(flags.NKeyPublic); err != nil {
		return nil, err
	}
	// Validate --name: required.
	if flags.Name == "" {
		return nil, fmt.Errorf("--name is required")
	}

	// Resolve context.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		ctx = &clicontext.Context{}
	}

	// Load auth credentials (required for NATS connection and userSlug).
	authPath := flags.AuthPath
	if authPath == "" {
		authPath = DefaultAuthPath()
	}
	creds, err := LoadAuth(authPath)
	if err != nil {
		return nil, err
	}

	uslug := creds.UserSlug
	if uslug == "" {
		uslug = ctx.UserSlug
	}
	if uslug == "" {
		return nil, fmt.Errorf("user slug not found: run 'mclaude login' first")
	}

	// Connect to NATS (or use injected connection for tests).
	var serverURL string
	if ctx != nil {
		serverURL = clicontext.ResolveServerURL("", ctx)
	}
	natsURL := creds.NATSUrl
	if natsURL == "" {
		natsURL = clicontext.DeriveNATSURL(serverURL)
	}

	nc := flags.NATSConn
	if nc == nil {
		fmt.Fprintf(out, "Connecting to NATS at %s...\n", natsURL)
		realNC, err := connectNATS(natsURL, creds.JWT, creds.NKeySeed)
		if err != nil {
			return nil, err
		}
		defer realNC.Close()
		nc = realNC
	}

	// Publish to mclaude.users.{uslug}.hosts._.register
	subject := fmt.Sprintf("mclaude.users.%s.hosts._.register", uslug)
	payload := hostRegisterRequest{
		ID:              generateID(),
		Ts:              time.Now().UnixMilli(),
		Name:            flags.Name,
		NKeyPublic:      flags.NKeyPublic,
		DefaultPlatform: flags.DefaultPlatform,
	}
	reqData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal register request: %w", err)
	}

	fmt.Fprintf(out, "Registering host %q...\n", flags.Name)
	msg, err := nc.Request(subject, reqData, natsRequestTimeout)
	if err != nil {
		return nil, fmt.Errorf("host register request to %s: %w", subject, err)
	}

	var resp hostRegisterResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("parse register response: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("host registration failed: %s", resp.Error)
	}
	if resp.Slug == "" {
		return nil, fmt.Errorf("host registration: server returned empty slug")
	}
	hslug := resp.Slug

	// Write ~/.mclaude/hosts/{hslug}/config.json with hubUrl from auth.json.natsUrl.
	hdir := hostDir(hslug)
	if err := os.MkdirAll(hdir, 0700); err != nil {
		return nil, fmt.Errorf("create host dir %s: %w", hdir, err)
	}

	cfg := HostConfig{Slug: hslug, HubURL: creds.NATSUrl}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal host config: %w", err)
	}
	cfgPath := filepath.Join(hdir, "config.json")
	if err := os.WriteFile(cfgPath, append(cfgData, '\n'), 0600); err != nil {
		return nil, fmt.Errorf("write config.json: %w", err)
	}

	fmt.Fprintf(out, "Host registered: %s\n", hslug)
	fmt.Fprintf(out, "Config written:  %s\n", cfgPath)

	return &HostRegisterResult{Slug: hslug}, nil
}

// --------------------------------------------------------------------------
// mclaude host list
// --------------------------------------------------------------------------

// HostListFlags holds parsed flags for "mclaude host list".
type HostListFlags struct {
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// ServerURL is the control-plane base URL.
	ServerURL string
}

// HostInfo represents a single host entry returned by the API.
type HostInfo struct {
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// RunHostList lists all hosts for the authenticated user.
// Network calls are stubbed — prints locally-registered hosts from
// ~/.mclaude/hosts/ until the control-plane API is wired.
func RunHostList(flags HostListFlags, out io.Writer) error {
	// Resolve context for user slug.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return fmt.Errorf("load context: %w", err)
	}
	uslug := ctx.UserSlug
	if uslug == "" {
		return fmt.Errorf("user slug required: run 'mclaude login' first")
	}

	// TODO(stage7): GET /api/users/{uslug}/hosts — for now list local dirs.
	hostsDir := filepath.Join(mclaudeDir(), "hosts")
	entries, err := os.ReadDir(hostsDir)
	if os.IsNotExist(err) {
		fmt.Fprintf(out, "No hosts registered for %s\n", uslug)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read hosts dir: %w", err)
	}

	activeHost := ResolveActiveHost()

	fmt.Fprintf(out, "Hosts for %s:\n", uslug)
	fmt.Fprintf(out, "  %-20s %s\n", "SLUG", "ACTIVE")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		active := ""
		if e.Name() == activeHost {
			active = "*"
		}
		fmt.Fprintf(out, "  %-20s %s\n", e.Name(), active)
	}

	return nil
}

// --------------------------------------------------------------------------
// mclaude host use <hslug>
// --------------------------------------------------------------------------

// RunHostUse sets the active host by symlinking ~/.mclaude/active-host → hosts/{hslug}.
func RunHostUse(hslug string, out io.Writer) error {
	if err := slug.Validate(hslug); err != nil {
		return fmt.Errorf("invalid host slug %q: %w", hslug, err)
	}

	hdir := hostDir(hslug)
	if _, err := os.Stat(hdir); os.IsNotExist(err) {
		return fmt.Errorf("host %q not found at %s; register it first with 'mclaude host register'", hslug, hdir)
	}

	linkPath := activeHostPath()
	// Remove existing symlink if present.
	os.Remove(linkPath)

	// Symlink points to the host slug directory name (relative).
	target := filepath.Join("hosts", hslug)
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	// Update context with the active host slug.
	ctxPath := clicontext.DefaultPath()
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		ctx = &clicontext.Context{}
	}
	ctx.HostSlug = hslug
	if err := clicontext.Save(ctxPath, ctx); err != nil {
		// Non-fatal: symlink is the primary source of truth.
		fmt.Fprintf(out, "warning: could not update context file: %v\n", err)
	}

	fmt.Fprintf(out, "Active host set to %q\n", hslug)
	return nil
}

// --------------------------------------------------------------------------
// mclaude host rm <hslug>
// --------------------------------------------------------------------------

// HostRmFlags holds parsed flags for "mclaude host rm".
type HostRmFlags struct {
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// ServerURL is the control-plane base URL.
	ServerURL string
}

// RunHostRm removes a host registration.
// Calls DELETE /api/users/{uslug}/hosts/{hslug} (stubbed) and removes the
// local ~/.mclaude/hosts/{hslug}/ directory. If the removed host is the
// active host, the active-host symlink is also removed.
func RunHostRm(hslug string, flags HostRmFlags, out io.Writer) error {
	if err := slug.Validate(hslug); err != nil {
		return fmt.Errorf("invalid host slug %q: %w", hslug, err)
	}

	// Resolve context for user slug.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return fmt.Errorf("load context: %w", err)
	}
	uslug := ctx.UserSlug
	if uslug == "" {
		return fmt.Errorf("user slug required: run 'mclaude login' first")
	}

	// TODO(stage7): DELETE /api/users/{uslug}/hosts/{hslug}
	_ = uslug

	// Remove local host directory.
	hdir := hostDir(hslug)
	if err := os.RemoveAll(hdir); err != nil {
		return fmt.Errorf("remove host dir %s: %w", hdir, err)
	}

	// If this was the active host, remove the symlink and clear context.
	activeHost := ResolveActiveHost()
	if activeHost == hslug {
		os.Remove(activeHostPath())
		if ctx.HostSlug == hslug {
			ctx.HostSlug = ""
			_ = clicontext.Save(ctxPath, ctx)
		}
	}

	fmt.Fprintf(out, "Host %q removed\n", hslug)
	return nil
}

