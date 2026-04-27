// Package cmd implements mclaude-cli top-level commands.
//
// Session commands:
//
//	mclaude session list [-u <uslug>] [-p <pslug>] [-h <hslug>]
//
// Slug flags follow ADR-0024 conventions: @-prefix is stripped from project
// slugs, all slugs are validated before any network call, and defaults are
// read from ~/.mclaude/context.json.
package cmd

import (
	"fmt"
	"io"

	clicontext "mclaude-cli/context"
	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"
)

// SessionListFlags holds parsed flags for "mclaude session list".
type SessionListFlags struct {
	// UserSlug overrides the context default. Empty means "use context".
	UserSlug string
	// ProjectSlug overrides the context default. Accepts "@pslug" prefix.
	ProjectSlug string
	// HostSlug overrides the context default. Empty means "use context".
	HostSlug string
	// ContextPath is the path to ~/.mclaude/context.json.
	// Defaults to clicontext.DefaultPath() when empty.
	ContextPath string
}

// SessionListResult is returned by RunSessionList.
type SessionListResult struct {
	// UserSlug is the resolved user slug (from flags or context).
	UserSlug string
	// ProjectSlug is the resolved project slug (from flags or context).
	ProjectSlug string
	// HostSlug is the resolved host slug (from flags or context).
	HostSlug string
	// KVKeyPrefix is the mclaude-sessions KV key prefix that would be watched:
	// "{uslug}.{hslug}.{pslug}" — written using pkg/subj helpers.
	KVKeyPrefix string
	// EventsSubject is the NATS subject wildcard for events on this project.
	EventsSubject string
}

// RunSessionList resolves slug defaults, validates flags, and returns the
// parameters that would be used for the actual session-list API call.
//
// It does NOT make any network call — callers are responsible for using the
// returned KVKeyPrefix / EventsSubject to query the actual NATS KV or HTTP
// API once connection infrastructure is available.
//
// Returns an error if:
//   - A flag contains an invalid slug
//   - Both the flag and context are empty for a required field (userSlug)
func RunSessionList(flags SessionListFlags, out io.Writer) (*SessionListResult, error) {
	// Resolve context path.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}

	// Load context defaults.
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return nil, fmt.Errorf("load context: %w", err)
	}

	// Resolve user slug: flag > context.
	rawUser := flags.UserSlug
	if rawUser == "" {
		rawUser = ctx.UserSlug
	}
	uslug, err := clicontext.ParseUserSlug(rawUser)
	if err != nil {
		return nil, err
	}
	if uslug == "" {
		return nil, fmt.Errorf("user slug required: set MCLAUDE_USER_SLUG or run 'mclaude login'")
	}

	// Resolve project slug: flag (with @ stripping) > context.
	rawProject := flags.ProjectSlug
	if rawProject == "" {
		rawProject = ctx.ProjectSlug
	}
	pslug, err := clicontext.ParseProjectSlug(rawProject)
	if err != nil {
		return nil, err
	}
	if pslug == "" {
		return nil, fmt.Errorf("project slug required: use -p <project> or set a default project in context")
	}

	// Resolve host slug: flag > context.
	rawHost := flags.HostSlug
	if rawHost == "" {
		rawHost = ctx.HostSlug
	}
	hslug, err := clicontext.ParseHostSlug(rawHost)
	if err != nil {
		return nil, err
	}
	if hslug == "" {
		return nil, fmt.Errorf("host slug required: use --host <host>, run 'mclaude host use <hslug>', or register a host")
	}

	// Build typed slug wrappers for pkg/subj helpers.
	typedUser := slug.UserSlug(uslug)
	typedHost := slug.HostSlug(hslug)
	typedProject := slug.ProjectSlug(pslug)

	// Compute the KV key prefix: "{uslug}.{hslug}.{pslug}" (all sessions for the project).
	// The wildcard session slug is appended by the caller when watching KV.
	kvKey := subj.ProjectsKVKey(typedUser, typedHost, typedProject)

	// Compute the NATS events subject wildcard for all sessions in this project.
	// Per ADR-0004, host slug is inserted between user and project levels.
	eventsSubj := "mclaude.users." + uslug + ".hosts." + hslug + ".projects." + pslug + ".events.*"

	result := &SessionListResult{
		UserSlug:      uslug,
		ProjectSlug:   pslug,
		HostSlug:      hslug,
		KVKeyPrefix:   kvKey,
		EventsSubject: eventsSubj,
	}

	fmt.Fprintf(out, "sessions for %s/%s/%s (KV prefix: %s)\n",
		uslug, hslug, pslug, kvKey)

	return result, nil
}
