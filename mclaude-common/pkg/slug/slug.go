// Package slug provides typed slug wrappers, validation, and derivation helpers
// for mclaude identifiers used in NATS subjects, HTTP URLs, and KV keys.
// See docs/adr-0024-typed-slugs.md for the full specification.
package slug

import (
	"encoding/base32"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// --------------------------------------------------------------------------
// Typed slug wrappers
// --------------------------------------------------------------------------

// UserSlug is a validated, URL-safe slug for a user.
type UserSlug string

// ProjectSlug is a validated, URL-safe slug for a project.
type ProjectSlug string

// SessionSlug is a validated, URL-safe slug for a session.
type SessionSlug string

// HostSlug is a validated, URL-safe slug for a host (BYOH, ADR-0035).
type HostSlug string

// ClusterSlug is a validated, URL-safe slug for a cluster.
type ClusterSlug string

// --------------------------------------------------------------------------
// Kind enum
// --------------------------------------------------------------------------

// Kind identifies which entity type a slug belongs to.
type Kind int

const (
	KindUser    Kind = iota // u-
	KindProject             // p-
	KindSession             // s-
	KindHost                // h-
	KindCluster             // c-
)

// kindPrefix returns the single-letter fallback prefix for a Kind.
func kindPrefix(k Kind) string {
	switch k {
	case KindUser:
		return "u"
	case KindProject:
		return "p"
	case KindSession:
		return "s"
	case KindHost:
		return "h"
	case KindCluster:
		return "c"
	default:
		return "x"
	}
}

// --------------------------------------------------------------------------
// Reserved-word blocklist — typed constants (append-only; removals are unsafe)
// --------------------------------------------------------------------------

// reservedWord is a type for reserved literal tokens. Using a distinct type
// ensures the compiler catches accidental additions as raw string literals.
type reservedWord string

const (
	reservedUsers     reservedWord = "users"
	reservedHosts     reservedWord = "hosts"
	reservedProjects  reservedWord = "projects"
	reservedSessions  reservedWord = "sessions"
	reservedClusters  reservedWord = "clusters"
	reservedAPI       reservedWord = "api"
	reservedEvents    reservedWord = "events"
	reservedLifecycle reservedWord = "lifecycle"
	reservedQuota     reservedWord = "quota"
	reservedTerminal  reservedWord = "terminal"
	// ADR-0054: session subject verb tokens added to prevent slug/verb
	// ambiguity in the consolidated sessions.> hierarchy.
	// e.g. sessions.create must never collide with a session slug "create".
	reservedCreate  reservedWord = "create"
	reservedDelete  reservedWord = "delete"
	reservedInput   reservedWord = "input"
	reservedConfig  reservedWord = "config"
	reservedControl reservedWord = "control"
)

// reservedSet is the complete blocklist for fast lookup.
var reservedSet = map[string]struct{}{
	string(reservedUsers):     {},
	string(reservedHosts):     {},
	string(reservedProjects):  {},
	string(reservedSessions):  {},
	string(reservedClusters):  {},
	string(reservedAPI):       {},
	string(reservedEvents):    {},
	string(reservedLifecycle): {},
	string(reservedQuota):     {},
	string(reservedTerminal):  {},
	string(reservedCreate):    {},
	string(reservedDelete):    {},
	string(reservedInput):     {},
	string(reservedConfig):    {},
	string(reservedControl):   {},
}

// --------------------------------------------------------------------------
// Charset constant (documentation only; enforcement is in Validate)
// --------------------------------------------------------------------------

// Charset is the documentation constant for the slug character class:
// [a-z0-9][a-z0-9-]{0,62}, max 63 characters, no leading underscore,
// no reserved-word match.
const Charset = `[a-z0-9][a-z0-9-]{0,62}`

// MaxLen is the maximum slug length in characters.
const MaxLen = 63

// --------------------------------------------------------------------------
// Slugify
// --------------------------------------------------------------------------

// Slugify converts a display name into a valid slug token:
//  1. Lowercase
//  2. NFD Unicode decomposition
//  3. Strip combining marks
//  4. Replace runs of non-[a-z0-9] characters with a single "-"
//  5. Trim leading/trailing "-"
//  6. Truncate to 63 characters
//
// If the result is empty after all steps, an empty string is returned.
// Callers should use ValidateOrFallback to handle the empty / reserved case.
func Slugify(displayName string) string {
	// 1. Lowercase
	s := strings.ToLower(displayName)

	// 2. NFD decomposition
	s = norm.NFD.String(s)

	// 3. Strip combining marks (Unicode category Mn)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	s = b.String()

	// 4. Replace runs of non-[a-z0-9] with a single "-"
	var result strings.Builder
	result.Grow(len(s))
	inRun := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
			inRun = false
		} else {
			if !inRun {
				result.WriteRune('-')
				inRun = true
			}
		}
	}
	s = result.String()

	// 5. Trim leading/trailing "-"
	s = strings.Trim(s, "-")

	// 6. Truncate to MaxLen
	if len(s) > MaxLen {
		s = s[:MaxLen]
		s = strings.TrimRight(s, "-")
	}

	return s
}

// --------------------------------------------------------------------------
// Validate
// --------------------------------------------------------------------------

// ErrReserved is returned when a slug matches a reserved word.
type ErrReserved struct{ Slug string }

func (e ErrReserved) Error() string { return fmt.Sprintf("slug %q is a reserved word", e.Slug) }

// ErrLeadingUnderscore is returned when a slug starts with "_".
type ErrLeadingUnderscore struct{ Slug string }

func (e ErrLeadingUnderscore) Error() string {
	return fmt.Sprintf("slug %q must not start with '_'", e.Slug)
}

// ErrEmpty is returned when a slug is empty.
type ErrEmpty struct{}

func (e ErrEmpty) Error() string { return "slug must not be empty" }

// ErrTooLong is returned when a slug exceeds MaxLen characters.
type ErrTooLong struct{ Slug string }

func (e ErrTooLong) Error() string {
	return fmt.Sprintf("slug %q exceeds maximum length of %d", e.Slug, MaxLen)
}

// ErrCharset is returned when a slug contains an invalid character or
// does not start with [a-z0-9].
type ErrCharset struct{ Slug string }

func (e ErrCharset) Error() string {
	return fmt.Sprintf("slug %q contains invalid characters (must match %s)", e.Slug, Charset)
}

// Validate checks whether s is a valid slug:
//   - Non-empty
//   - Starts with [a-z0-9]
//   - Contains only [a-z0-9-]
//   - Does not start with '_'
//   - Length ≤ MaxLen
//   - Not in the reserved-word blocklist
func Validate(s string) error {
	if s == "" {
		return ErrEmpty{}
	}
	if strings.HasPrefix(s, "_") {
		return ErrLeadingUnderscore{s}
	}
	if len(s) > MaxLen {
		return ErrTooLong{s}
	}
	for i, r := range s {
		if r == '-' {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		_ = i
		return ErrCharset{s}
	}
	// Must start with [a-z0-9] (not '-')
	first := rune(s[0])
	if first == '-' {
		return ErrCharset{s}
	}
	if _, ok := reservedSet[s]; ok {
		return ErrReserved{s}
	}
	return nil
}

// --------------------------------------------------------------------------
// ValidateOrFallback
// --------------------------------------------------------------------------

// base32NoPad is the encoding used for fallback slug generation.
// Lowercase standard base32 without padding, using only [a-z2-7].
var base32NoPad = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ValidateOrFallback returns candidate if it is a valid slug; otherwise it
// generates a deterministic fallback slug of the form "{prefix}-{6 base32
// chars}" where prefix is derived from kind and the 6 chars are derived from
// the first 30 bits (4 bytes) of uuidSeed.
//
// The fallback is guaranteed to be valid: prefix is always [u|p|s|h|c] and
// the base32 alphabet ([a-z2-7]) is fully within the slug charset.
func ValidateOrFallback(candidate string, kind Kind, uuidSeed [16]byte) string {
	if Validate(candidate) == nil {
		return candidate
	}
	return fallbackSlug(kind, uuidSeed)
}

// fallbackSlug generates "{prefix}-{6 base32 chars}" from the first 4 bytes
// of uuidSeed. 4 bytes = 32 bits; base32 encodes 5 bits per char; 6 chars =
// 30 bits. We use the first 4 bytes (32 bits) and take the first 6 encoded
// characters.
func fallbackSlug(kind Kind, uuidSeed [16]byte) string {
	encoded := base32NoPad.EncodeToString(uuidSeed[:4])
	// encoded is 7 chars (ceil(32/5)); take first 6
	suffix := encoded[:6]
	return kindPrefix(kind) + "-" + suffix
}

// --------------------------------------------------------------------------
// MustParse helpers — panic if validation fails
// --------------------------------------------------------------------------

// MustParseUserSlug validates s and returns a UserSlug. Panics if invalid.
func MustParseUserSlug(s string) UserSlug {
	if err := Validate(s); err != nil {
		panic(fmt.Sprintf("invalid UserSlug %q: %v", s, err))
	}
	return UserSlug(s)
}

// MustParseProjectSlug validates s and returns a ProjectSlug. Panics if invalid.
func MustParseProjectSlug(s string) ProjectSlug {
	if err := Validate(s); err != nil {
		panic(fmt.Sprintf("invalid ProjectSlug %q: %v", s, err))
	}
	return ProjectSlug(s)
}

// MustParseSessionSlug validates s and returns a SessionSlug. Panics if invalid.
func MustParseSessionSlug(s string) SessionSlug {
	if err := Validate(s); err != nil {
		panic(fmt.Sprintf("invalid SessionSlug %q: %v", s, err))
	}
	return SessionSlug(s)
}

// MustParseHostSlug validates s and returns a HostSlug. Panics if invalid.
func MustParseHostSlug(s string) HostSlug {
	if err := Validate(s); err != nil {
		panic(fmt.Sprintf("invalid HostSlug %q: %v", s, err))
	}
	return HostSlug(s)
}

// MustParseClusterSlug validates s and returns a ClusterSlug. Panics if invalid.
func MustParseClusterSlug(s string) ClusterSlug {
	if err := Validate(s); err != nil {
		panic(fmt.Sprintf("invalid ClusterSlug %q: %v", s, err))
	}
	return ClusterSlug(s)
}

// --------------------------------------------------------------------------
// DeriveUserSlug
// --------------------------------------------------------------------------

// DeriveUserSlug derives a user slug from a full email address (ADR-0062).
//
// Algorithm: Slugify(email) — lowercase the full email, replace all runs of
// non-[a-z0-9] characters (including '@' and '.') with '-', trim leading and
// trailing '-', truncate to 63 characters.
//
// The full domain is included to prevent collisions between users on different
// domains (e.g. richard@rbc.com → "richard-rbc-com" ≠ richard@gmail.com →
// "richard-gmail-com").
//
// The caller is responsible for collision detection and appending a numeric
// suffix ("-2", "-3", ...) if needed.
//
// Examples:
//
//	DeriveUserSlug("dev@mclaude.local")      → "dev-mclaude-local"
//	DeriveUserSlug("richard.song@gmail.com") → "richard-song-gmail-com"
func DeriveUserSlug(email string) string {
	return Slugify(email)
}
