package slug_test

import (
	"strings"
	"testing"

	"mclaude-common/pkg/slug"
)

// --------------------------------------------------------------------------
// Slugify tests
// --------------------------------------------------------------------------

func TestSlugify(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "pure ASCII lowercase",
			input: "hello-world",
			want:  "hello-world",
		},
		{
			name:  "pure ASCII with spaces",
			input: "my project name",
			want:  "my-project-name",
		},
		{
			name:  "accented characters é",
			input: "café",
			want:  "cafe",
		},
		{
			name:  "accented character ñ",
			input: "niño",
			want:  "nino",
		},
		{
			name:  "mixed accents",
			input: "résumé",
			want:  "resume",
		},
		{
			name:  "emoji-only input",
			input: "🎉",
			want:  "", // all non-[a-z0-9] stripped, result is empty after trim
		},
		{
			name:  "leading punctuation",
			input: "---hello",
			want:  "hello",
		},
		{
			name:  "trailing punctuation",
			input: "hello---",
			want:  "hello",
		},
		{
			name:  "too-long string",
			input: strings.Repeat("a", 100),
			want:  strings.Repeat("a", 63),
		},
		{
			name:  "reserved word 'users'",
			input: "users",
			want:  "users", // Slugify itself does not reject reserved words; caller uses ValidateOrFallback
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "uppercase",
			input: "Richard Song",
			want:  "richard-song",
		},
		{
			name:  "mixed punctuation",
			input: "hello.world/foo bar",
			want:  "hello-world-foo-bar",
		},
		{
			name:  "leading underscore stripped to dash then trimmed",
			input: "_internal",
			want:  "internal",
		},
		{
			name:  "digits preserved",
			input: "project-42",
			want:  "project-42",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slug.Slugify(tc.input)
			if got != tc.want {
				t.Errorf("Slugify(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Validate tests
// --------------------------------------------------------------------------

func TestValidate(t *testing.T) {
	validCases := []struct {
		name  string
		input string
	}{
		{"simple lowercase", "hello"},
		{"with hyphen", "hello-world"},
		{"starts with digit", "3d-printer"},
		{"max length exactly 63", strings.Repeat("a", 63)},
		{"single char", "a"},
	}

	for _, tc := range validCases {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			if err := slug.Validate(tc.input); err != nil {
				t.Errorf("Validate(%q) returned unexpected error: %v", tc.input, err)
			}
		})
	}

	invalidCases := []struct {
		name      string
		input     string
		wantType  string // error type name fragment
	}{
		{"empty string", "", "ErrEmpty"},
		{"leading underscore", "_internal", "ErrLeadingUnderscore"},
		{"contains uppercase", "Hello", "ErrCharset"},
		{"contains dot", "hello.world", "ErrCharset"},
		{"contains space", "hello world", "ErrCharset"},
		{"starts with hyphen", "-hello", "ErrCharset"},
		{"too long (64 chars)", strings.Repeat("a", 64), "ErrTooLong"},
		{"reserved: users", "users", "ErrReserved"},
		{"reserved: hosts", "hosts", "ErrReserved"},
		{"reserved: projects", "projects", "ErrReserved"},
		{"reserved: sessions", "sessions", "ErrReserved"},
		{"reserved: clusters", "clusters", "ErrReserved"},
		{"reserved: api", "api", "ErrReserved"},
		{"reserved: events", "events", "ErrReserved"},
		{"reserved: lifecycle", "lifecycle", "ErrReserved"},
		{"reserved: quota", "quota", "ErrReserved"},
		{"reserved: terminal", "terminal", "ErrReserved"},
		{"contains slash", "hello/world", "ErrCharset"},
		{"contains wildcard *", "hello*", "ErrCharset"},
		{"contains >", "hello>", "ErrCharset"},
	}

	for _, tc := range invalidCases {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			err := slug.Validate(tc.input)
			if err == nil {
				t.Errorf("Validate(%q) expected error, got nil", tc.input)
			}
		})
	}
}

// --------------------------------------------------------------------------
// ValidateOrFallback tests
// --------------------------------------------------------------------------

func TestValidateOrFallback(t *testing.T) {
	seed := [16]byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	t.Run("valid candidate is returned unchanged", func(t *testing.T) {
		got := slug.ValidateOrFallback("my-project", slug.KindProject, seed)
		if got != "my-project" {
			t.Errorf("want %q, got %q", "my-project", got)
		}
	})

	t.Run("empty candidate triggers fallback", func(t *testing.T) {
		got := slug.ValidateOrFallback("", slug.KindUser, seed)
		if !strings.HasPrefix(got, "u-") {
			t.Errorf("expected fallback to start with 'u-', got %q", got)
		}
		if err := slug.Validate(got); err != nil {
			t.Errorf("fallback slug %q is not valid: %v", got, err)
		}
	})

	t.Run("reserved word triggers fallback for project", func(t *testing.T) {
		got := slug.ValidateOrFallback("projects", slug.KindProject, seed)
		if !strings.HasPrefix(got, "p-") {
			t.Errorf("expected fallback to start with 'p-', got %q", got)
		}
		if err := slug.Validate(got); err != nil {
			t.Errorf("fallback slug %q is not valid: %v", got, err)
		}
	})

	t.Run("emoji-only triggers fallback for session", func(t *testing.T) {
		// Slugify("🎉") == "" — ValidateOrFallback receives that empty string
		candidate := slug.Slugify("🎉")
		got := slug.ValidateOrFallback(candidate, slug.KindSession, seed)
		if !strings.HasPrefix(got, "s-") {
			t.Errorf("expected fallback to start with 's-', got %q", got)
		}
		if err := slug.Validate(got); err != nil {
			t.Errorf("fallback slug %q is not valid: %v", got, err)
		}
	})

	t.Run("fallback is deterministic", func(t *testing.T) {
		a := slug.ValidateOrFallback("", slug.KindCluster, seed)
		b := slug.ValidateOrFallback("", slug.KindCluster, seed)
		if a != b {
			t.Errorf("expected identical fallbacks, got %q and %q", a, b)
		}
	})

	t.Run("different seeds produce different fallbacks", func(t *testing.T) {
		seed2 := [16]byte{0x00, 0x00, 0x00, 0x01}
		a := slug.ValidateOrFallback("", slug.KindUser, seed)
		b := slug.ValidateOrFallback("", slug.KindUser, seed2)
		if a == b {
			t.Errorf("expected different fallbacks for different seeds, both got %q", a)
		}
	})

	t.Run("host kind produces h- prefix", func(t *testing.T) {
		got := slug.ValidateOrFallback("", slug.KindHost, seed)
		if !strings.HasPrefix(got, "h-") {
			t.Errorf("expected fallback to start with 'h-', got %q", got)
		}
	})
}

// --------------------------------------------------------------------------
// Round-trip: Slugify → Validate
// --------------------------------------------------------------------------

func TestSlugifyRoundTrip(t *testing.T) {
	// Any non-empty result from Slugify must be valid (except reserved words,
	// which Slugify passes through — those need ValidateOrFallback).
	inputs := []string{
		"hello world",
		"Richard Song",
		"café au lait",
		"123-test",
		"My Project Name",
		"a",
		strings.Repeat("b", 200),
	}
	for _, input := range inputs {
		got := slug.Slugify(input)
		if got == "" {
			continue // empty results need ValidateOrFallback, not Validate
		}
		if err := slug.Validate(got); err != nil {
			// Reserved-word results are expected to fail Validate — that is the
			// intended contract: callers use ValidateOrFallback.
			if _, ok := err.(slug.ErrReserved); ok {
				continue
			}
			t.Errorf("Validate(Slugify(%q)) = %q, got error: %v", input, got, err)
		}
	}
}

// --------------------------------------------------------------------------
// DeriveUserSlug tests
// --------------------------------------------------------------------------

func TestDeriveUserSlug(t *testing.T) {
	cases := []struct {
		name        string
		displayName string
		email       string
		want        string
	}{
		{
			name:        "standard user",
			displayName: "Richard",
			email:       "richard@rbc.com",
			want:        "richard-rbc",
		},
		{
			name:        "multi-part name",
			displayName: "Richard Song",
			email:       "richard@rbc.com",
			want:        "richard-song-rbc",
		},
		{
			name:        "alice gmail",
			displayName: "Alice",
			email:       "alice@gmail.com",
			want:        "alice-gmail",
		},
		{
			name:        "domain with multiple segments — first segment only",
			displayName: "user",
			email:       "user@rbc.co.uk",
			want:        "user-rbc",
		},
		{
			name:        "no display name — use email local-part",
			displayName: "",
			email:       "alice@gmail.com",
			want:        "alice-gmail",
		},
		{
			name:        "display name overrides local-part",
			displayName: "Bob",
			email:       "robert@company.org",
			want:        "bob-company",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slug.DeriveUserSlug(tc.displayName, tc.email)
			if got != tc.want {
				t.Errorf("DeriveUserSlug(%q, %q) = %q, want %q",
					tc.displayName, tc.email, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// MustParse panic tests
// --------------------------------------------------------------------------

func TestMustParseUserSlugPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid slug, got none")
		}
	}()
	slug.MustParseUserSlug("invalid slug!")
}

func TestMustParseUserSlugValid(t *testing.T) {
	s := slug.MustParseUserSlug("alice-gmail")
	if s != "alice-gmail" {
		t.Errorf("expected %q, got %q", "alice-gmail", s)
	}
}
