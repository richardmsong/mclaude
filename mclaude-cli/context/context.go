// Package context manages the ~/.mclaude/context.json file used by mclaude-cli
// to store current user/project/host slug defaults. Commands read this file to
// avoid requiring explicit slug flags on every invocation; flags override the
// context values when provided.
//
// Spec: docs/adr-0024-typed-slugs.md — "CLI identifier surface":
//
//	~/.mclaude/context.json gains userSlug, projectSlug, hostSlug.
//	Commands accept short forms: context file supplies defaults; -p overrides;
//	@pslug positional short form is accepted.
package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mclaude.io/common/pkg/slug"
)

// DefaultPath returns the default path for the context file.
// It respects MCLAUDE_CONTEXT_FILE if set (for tests).
func DefaultPath() string {
	if v := os.Getenv("MCLAUDE_CONTEXT_FILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mclaude", "context.json")
}

// Context holds the current slug defaults for mclaude-cli.
// All fields are optional — empty string means "not set".
type Context struct {
	// UserSlug is the current user's slug (e.g. "alice-gmail").
	// Derived from the login response's userSlug field.
	UserSlug string `json:"userSlug,omitempty"`

	// ProjectSlug is the current project's slug (e.g. "mclaude").
	// Set by project selection or on project creation.
	ProjectSlug string `json:"projectSlug,omitempty"`

	// HostSlug is the current host's slug (reserved for ADR-0004 BYOH;
	// populated when the daemon registers the local host).
	HostSlug string `json:"hostSlug,omitempty"`

	// Server is the control-plane base URL (e.g. "https://api.mclaude.internal").
	// Individual command --server flags override this value.
	// Default when absent: "https://api.mclaude.internal".
	Server string `json:"server,omitempty"`
}

// DefaultServerURL is the fallback control-plane URL used when context.Server
// is empty and no --server flag is provided.
const DefaultServerURL = "https://api.mclaude.internal"

// ResolveServerURL returns the effective control-plane server URL.
// Priority: explicit override > context file > DefaultServerURL.
func ResolveServerURL(override string, ctx *Context) string {
	if override != "" {
		return override
	}
	if ctx != nil && ctx.Server != "" {
		return ctx.Server
	}
	return DefaultServerURL
}

// Load reads the context file at path and returns the parsed Context.
// If the file does not exist, an empty Context is returned (not an error).
// Returns an error if the file exists but cannot be parsed.
func Load(path string) (*Context, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Context{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read context file %s: %w", path, err)
	}
	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("parse context file %s: %w", path, err)
	}
	return &ctx, nil
}

// Save writes ctx to the context file at path, creating the directory if needed.
func Save(path string, ctx *Context) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create context dir: %w", err)
	}
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write context file %s: %w", path, err)
	}
	return nil
}

// ValidateUserSlug validates s as a user slug. Returns an error suitable for
// user-facing display. Empty string is allowed — it means "use context default".
func ValidateUserSlug(s string) error {
	if s == "" {
		return nil
	}
	return formatSlugErr("user", s, slug.Validate(s))
}

// ValidateProjectSlug validates s as a project slug. Empty string is allowed.
// Strips a leading "@" before validation — "@pslug" short form is accepted.
func ValidateProjectSlug(s string) error {
	if s == "" {
		return nil
	}
	bare := strings.TrimPrefix(s, "@")
	return formatSlugErr("project", bare, slug.Validate(bare))
}

// ValidateHostSlug validates s as a host slug. Empty string is allowed.
func ValidateHostSlug(s string) error {
	if s == "" {
		return nil
	}
	return formatSlugErr("host", s, slug.Validate(s))
}

// ParseProjectSlug parses s (possibly with "@" prefix) into a validated
// project slug string. Returns "" for empty input.
func ParseProjectSlug(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	bare := strings.TrimPrefix(s, "@")
	if err := slug.Validate(bare); err != nil {
		return "", fmt.Errorf("invalid project slug %q: %w", s, err)
	}
	return bare, nil
}

// ParseUserSlug parses s into a validated user slug string. Returns "" for
// empty input.
func ParseUserSlug(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if err := slug.Validate(s); err != nil {
		return "", fmt.Errorf("invalid user slug %q: %w", s, err)
	}
	return s, nil
}

// ParseHostSlug parses s into a validated host slug string. Returns "" for
// empty input.
func ParseHostSlug(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if err := slug.Validate(s); err != nil {
		return "", fmt.Errorf("invalid host slug %q: %w", s, err)
	}
	return s, nil
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func formatSlugErr(kind, s string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("invalid %s slug %q: %w", kind, s, err)
}
