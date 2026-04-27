package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	clicontext "mclaude-cli/context"
	"mclaude.io/common/pkg/slug"
)

// --------------------------------------------------------------------------
// mclaude daemon --host <hslug>
// --------------------------------------------------------------------------

// DaemonFlags holds parsed flags for "mclaude daemon".
type DaemonFlags struct {
	// HostSlug is the host to run the daemon for.
	// If empty, read from ~/.mclaude/active-host symlink.
	HostSlug string
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
}

// DaemonConfig is the resolved configuration for the daemon.
type DaemonConfig struct {
	// UserSlug is the authenticated user.
	UserSlug string
	// HostSlug is the resolved host slug.
	HostSlug string
	// HostDir is the path to ~/.mclaude/hosts/{hslug}/.
	HostDir string
	// CredsPath is the path to the NATS credentials file.
	CredsPath string
}

// ResolveDaemonConfig resolves the daemon configuration from flags and context.
// It validates that the host directory and credentials file exist.
//
// This does NOT start the daemon — it returns the resolved config for the
// caller to use. The actual daemon loop (NATS connection, project subscription,
// session-agent process supervision) is implemented in mclaude-controller-local.
func ResolveDaemonConfig(flags DaemonFlags, out io.Writer) (*DaemonConfig, error) {
	// Resolve context.
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

	// Resolve host slug: flag > active-host symlink > context.
	hslug := flags.HostSlug
	if hslug == "" {
		hslug = ResolveActiveHost()
	}
	if hslug == "" {
		hslug = ctx.HostSlug
	}
	if hslug == "" {
		return nil, fmt.Errorf("host slug required: use --host <hslug> or run 'mclaude host use <hslug>'")
	}
	if err := slug.Validate(hslug); err != nil {
		return nil, fmt.Errorf("invalid host slug %q: %w", hslug, err)
	}

	// Verify host directory exists.
	hdir := hostDir(hslug)
	if _, err := os.Stat(hdir); os.IsNotExist(err) {
		return nil, fmt.Errorf("host directory %s not found; register with 'mclaude host register' first", hdir)
	}

	// Verify credentials file exists.
	credsPath := filepath.Join(hdir, "nats.creds")
	if _, err := os.Stat(credsPath); os.IsNotExist(err) {
		// Creds may not exist yet if registration is still in progress.
		fmt.Fprintf(out, "warning: credentials file %s not found; registration may be incomplete\n", credsPath)
	}

	cfg := &DaemonConfig{
		UserSlug:  uslug,
		HostSlug:  hslug,
		HostDir:   hdir,
		CredsPath: credsPath,
	}

	fmt.Fprintf(out, "Daemon config resolved:\n")
	fmt.Fprintf(out, "  User:  %s\n", uslug)
	fmt.Fprintf(out, "  Host:  %s\n", hslug)
	fmt.Fprintf(out, "  Dir:   %s\n", hdir)
	fmt.Fprintf(out, "  Creds: %s\n", credsPath)

	return cfg, nil
}
