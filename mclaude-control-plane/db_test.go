package main

import (
	"strings"
	"testing"
)

// TestSchema_ContainsRequiredTables verifies the embedded DDL schema defines
// the tables and columns the application expects. This is the pure-logic
// counterpart to the integration test that actually applies the schema to Postgres.
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
	// Verify IF NOT EXISTS is used so Migrate() can be called repeatedly.
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
		"js_domain",
		"leaf_url",
		"account_jwt",
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

func TestSchema_BackfillLocalHost(t *testing.T) {
	if !strings.Contains(schema, "'local'") {
		t.Error("schema missing backfill for default 'local' machine host")
	}
}
