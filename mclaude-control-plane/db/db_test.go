package db

import (
	"strings"
	"testing"
)

// ---- computeUserSlug ----

func TestComputeUserSlug_BasicEmail(t *testing.T) {
	// ADR-0062: slugify the full email (not just local-part).
	// Algorithm: lowercase, replace all non-[a-z0-9] runs with '-', trim leading/trailing '-', truncate to 63 chars.
	cases := []struct {
		email string
		want  string
	}{
		// Full email slugification — domain included to prevent collisions.
		{"alice@example.com", "alice-example-com"},
		{"bob.smith@example.com", "bob-smith-example-com"},
		{"jane_doe@example.com", "jane-doe-example-com"},
		{"user+tag@example.com", "user-tag-example-com"},
		{"ALICE@EXAMPLE.COM", "alice-example-com"},
		{"mixed.CASE.User@domain.io", "mixed-case-user-domain-io"},
		{"multi--dash@example.com", "multi-dash-example-com"},
		// ADR-0062 canonical examples.
		{"dev@mclaude.local", "dev-mclaude-local"},
		{"richard.song@gmail.com", "richard-song-gmail-com"},
		{"richard@rbc.com", "richard-rbc-com"},
	}
	for _, tc := range cases {
		got := ComputeUserSlug(tc.email)
		if got != tc.want {
			t.Errorf("ComputeUserSlug(%q) = %q; want %q", tc.email, got, tc.want)
		}
	}
}

func TestComputeUserSlug_CollisionResistance(t *testing.T) {
	s1 := ComputeUserSlug("richard@rbc.com")
	s2 := ComputeUserSlug("richard@gmail.com")
	if s1 == s2 {
		t.Errorf("ComputeUserSlug collision: both richard@rbc.com and richard@gmail.com produced %q", s1)
	}
}

func TestSchema_ContainsRequiredTables(t *testing.T) {
	required := []string{
		"CREATE TABLE IF NOT EXISTS users",
		"CREATE TABLE IF NOT EXISTS hosts",
		"CREATE TABLE IF NOT EXISTS projects",
		"CREATE TABLE IF NOT EXISTS oauth_connections",
		"id",
		"email",
		"name",
		"password_hash",
		"oauth_id",
		"is_admin",
		"created_at",
	}
	for _, fragment := range required {
		if !strings.Contains(schema, fragment) {
			t.Errorf("schema missing required fragment %q", fragment)
		}
	}
}

func TestSchema_IdempotentCreate(t *testing.T) {
	if !strings.Contains(schema, "IF NOT EXISTS") {
		t.Error("schema CREATE TABLE should use IF NOT EXISTS for idempotent migrations")
	}
}

func TestSchema_HasUniqueEmailConstraint(t *testing.T) {
	if !strings.Contains(schema, "UNIQUE") {
		t.Error("schema users.email should have a UNIQUE constraint")
	}
}

func TestSchema_HostsTableColumns(t *testing.T) {
	hostCols := []string{
		"user_id",
		"slug",
		"type",
		"role",
		"direct_nats_url",
		"public_key",
		"user_jwt",
		"last_seen_at",
	}
	for _, col := range hostCols {
		if !strings.Contains(schema, col) {
			t.Errorf("schema hosts table missing column %q", col)
		}
	}
}

func TestSchema_ADR0063_DropLegacyColumns(t *testing.T) {
	if !strings.Contains(schema, "js_domain IS NOT NULL") {
		t.Error("ADR-0063 migration missing: expected original CHECK constraint referencing js_domain")
	}
	if !strings.Contains(schema, "DROP COLUMN js_domain") {
		t.Error("ADR-0063 migration missing: DROP COLUMN js_domain")
	}
	if !strings.Contains(schema, "DROP COLUMN leaf_url") {
		t.Error("ADR-0063 migration missing: DROP COLUMN leaf_url")
	}
	if !strings.Contains(schema, "DROP COLUMN account_jwt") {
		t.Error("ADR-0063 migration missing: DROP COLUMN account_jwt")
	}
}

func TestSchema_HostsTypeCheck(t *testing.T) {
	if !strings.Contains(schema, "CHECK (type IN ('machine', 'cluster'))") {
		t.Error("schema hosts table missing type CHECK constraint")
	}
}

func TestSchema_ProjectsHostID(t *testing.T) {
	if !strings.Contains(schema, "host_id") {
		t.Error("schema projects table missing host_id column")
	}
}

func TestSchema_ProjectsUniqueIndex(t *testing.T) {
	if !strings.Contains(schema, "projects_user_id_host_id_slug_uniq") {
		t.Error("schema missing projects unique index on (user_id, host_id, slug)")
	}
}

func TestSchema_NoLocalHostBackfill(t *testing.T) {
	if strings.Contains(schema, "INSERT INTO hosts") && strings.Contains(schema, "'local'") {
		t.Error("schema still contains backfill INSERT for auto-created local host stubs — removed by ADR-0095")
	}
}

func TestSchema_UsersSlugColumn(t *testing.T) {
	if !strings.Contains(schema, "slug") {
		t.Error("schema users table missing slug column (ADR-0046)")
	}
}

func TestSchema_UsersSlugUniqueIndex(t *testing.T) {
	if !strings.Contains(schema, "users_slug_uniq") {
		t.Error("schema missing users_slug_uniq unique index (ADR-0046)")
	}
}

func TestSchema_HasOAuthConnectionsTable(t *testing.T) {
	if !strings.Contains(schema, "oauth_connections") {
		t.Error("schema missing oauth_connections table")
	}
}

func TestSchema_HasGitIdentityIDColumn(t *testing.T) {
	if !strings.Contains(schema, "git_identity_id") {
		t.Error("schema missing git_identity_id column on projects")
	}
}

func TestSchema_OAuthConnectionsHasUniqueConstraint(t *testing.T) {
	if !strings.Contains(schema, "UNIQUE(user_id, base_url, provider_user_id)") {
		t.Error("oauth_connections should have UNIQUE(user_id, base_url, provider_user_id)")
	}
}

func TestSchema_GitIdentityIDCascadeSetNull(t *testing.T) {
	if !strings.Contains(schema, "ON DELETE SET NULL") {
		t.Error("git_identity_id should have ON DELETE SET NULL")
	}
}
