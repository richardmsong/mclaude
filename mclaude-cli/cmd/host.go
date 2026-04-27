package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	clicontext "mclaude-cli/context"
	mcnats "mclaude.io/common/pkg/nats"
	"mclaude.io/common/pkg/slug"

	"github.com/nats-io/nkeys"
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
// mclaude host register [--name <name>]
// --------------------------------------------------------------------------

// HostRegisterFlags holds parsed flags for "mclaude host register".
type HostRegisterFlags struct {
	// Name is the display name (default: hostname output, slugified).
	Name string
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// ServerURL is the control-plane base URL (read from context/env).
	ServerURL string
}

// HostRegisterResult is returned by RunHostRegister.
type HostRegisterResult struct {
	// Slug is the assigned host slug.
	Slug string
	// PublicKey is the NKey public key.
	PublicKey string
	// DeviceCode is the 6-char code the user must enter in the dashboard.
	DeviceCode string
	// SeedPath is the path where the NKey seed was stored.
	SeedPath string
}

// RunHostRegister performs the device-code registration flow for a BYOH machine:
//  1. Resolve user slug from context.
//  2. Generate NKey pair; write seed to ~/.mclaude/hosts/{hslug}/nkey.seed (0600).
//  3. POST /api/users/{uslug}/hosts/code with {publicKey} to get a device code.
//  4. Print the device code and poll until completion.
//  5. On completion, write nats.creds and config.json; symlink active-host.
//
// Network calls are stubbed out in this stage — the function performs local
// setup (NKey generation, directory creation) and prints the instructions.
// Actual HTTP calls will be wired when the control-plane endpoints are ready.
func RunHostRegister(flags HostRegisterFlags, out io.Writer) (*HostRegisterResult, error) {
	// Resolve context for user slug.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return nil, fmt.Errorf("load context: %w", err)
	}
	uslug := ctx.UserSlug
	if uslug == "" {
		return nil, fmt.Errorf("user slug required: run 'mclaude login' first")
	}

	// Determine host name.
	name := flags.Name
	if name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("get hostname: %w", err)
		}
		name = hostname
	}
	hslug := slug.Slugify(name)
	if hslug == "" {
		return nil, fmt.Errorf("could not derive a valid slug from name %q", name)
	}
	if err := slug.Validate(hslug); err != nil {
		return nil, fmt.Errorf("invalid host slug %q: %w", hslug, err)
	}

	// Generate NKey pair.
	kp, err := nkeys.CreateUser()
	if err != nil {
		return nil, fmt.Errorf("generate nkey pair: %w", err)
	}
	pubKey, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("nkey public key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, fmt.Errorf("nkey seed: %w", err)
	}

	// Create host directory and write seed.
	hdir := hostDir(hslug)
	if err := os.MkdirAll(hdir, 0700); err != nil {
		return nil, fmt.Errorf("create host dir %s: %w", hdir, err)
	}
	seedPath := filepath.Join(hdir, "nkey.seed")
	if err := os.WriteFile(seedPath, seed, 0600); err != nil {
		return nil, fmt.Errorf("write nkey seed: %w", err)
	}

	// TODO(stage7): POST /api/users/{uslug}/hosts/code with {publicKey, name}
	// For now, print the setup instructions.
	fmt.Fprintf(out, "Host registration started for user %s\n", uslug)
	fmt.Fprintf(out, "  Host slug:  %s\n", hslug)
	fmt.Fprintf(out, "  Public key: %s\n", pubKey)
	fmt.Fprintf(out, "  Seed saved: %s\n", seedPath)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next: when control-plane endpoints are available,")
	fmt.Fprintf(out, "  POST /api/users/%s/hosts/code will return a device code.\n", uslug)
	fmt.Fprintln(out, "  Open the dashboard and enter the code to complete registration.")

	return &HostRegisterResult{
		Slug:       hslug,
		PublicKey:  pubKey,
		DeviceCode: "", // populated when HTTP call is wired
		SeedPath:   seedPath,
	}, nil
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
	Type       string     `json:"type"`
	Role       string     `json:"role"`
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
	fmt.Fprintf(out, "  %-20s %-10s %s\n", "SLUG", "TYPE", "ACTIVE")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		active := ""
		if e.Name() == activeHost {
			active = "*"
		}
		// Read config if available to get type.
		hostType := "machine"
		cfgPath := filepath.Join(hostsDir, e.Name(), "config.json")
		if data, err := os.ReadFile(cfgPath); err == nil {
			var cfg HostConfig
			if json.Unmarshal(data, &cfg) == nil && cfg.Slug != "" {
				// Future: cfg could include type info.
				_ = cfg
			}
		}
		fmt.Fprintf(out, "  %-20s %-10s %s\n", e.Name(), hostType, active)
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

// --------------------------------------------------------------------------
// WriteHostCredentials writes nats.creds and config.json for a completed
// registration. Called when the device-code poll returns success.
// --------------------------------------------------------------------------

// WriteHostCredentials writes the NATS credentials file and host config
// after a successful registration. The seed is read from the previously
// stored nkey.seed file.
func WriteHostCredentials(hslug, jwt, hubURL string) error {
	hdir := hostDir(hslug)

	// Read seed from the previously stored file.
	seedPath := filepath.Join(hdir, "nkey.seed")
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		return fmt.Errorf("read nkey seed: %w", err)
	}

	// Write nats.creds.
	credsData := mcnats.FormatNATSCredentials(jwt, seed)
	credsPath := filepath.Join(hdir, "nats.creds")
	if err := os.WriteFile(credsPath, credsData, 0600); err != nil {
		return fmt.Errorf("write nats.creds: %w", err)
	}

	// Write config.json.
	cfg := HostConfig{Slug: hslug, HubURL: hubURL}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal host config: %w", err)
	}
	cfgPath := filepath.Join(hdir, "config.json")
	if err := os.WriteFile(cfgPath, append(cfgData, '\n'), 0600); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}

	return nil
}
