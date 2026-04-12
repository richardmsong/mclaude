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
		"id",
		"email",
		"name",
		"password_hash",
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
