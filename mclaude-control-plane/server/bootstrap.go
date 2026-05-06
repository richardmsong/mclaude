package server

// bootstrapAdminUser UPSERTs a bootstrap admin user in Postgres (ADR-0094).
//
// This is the server-package equivalent of the cmd/control-plane bootstrapAdminUser
// function. It is exposed here so integration tests in the server package can exercise
// the exact same semantics without importing the cmd package.
//
// Semantics (per ADR-0094 and spec-control-plane.md § Two-Phase Deployment):
//   - Fresh user + non-empty password: inserts with is_admin=true and bcrypt hash.
//   - Fresh user + empty password: returns an error (unusable account — login impossible).
//   - Existing user + non-empty password: promotes to admin and rotates password_hash.
//   - Existing user + empty password: promotes to admin, preserves existing password_hash.
//
// The underlying SQL is INSERT ... ON CONFLICT (email) DO UPDATE, making this safe
// to re-run on every helm upgrade.
import (
	"context"
	"fmt"

	"github.com/google/uuid"
	cpdb "mclaude-control-plane/db"
	"github.com/rs/zerolog"
)

func bootstrapAdminUser(ctx context.Context, _ zerolog.Logger, databaseURL, email, password string) error {
	database, err := cpdb.ConnectDB(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer database.Close()

	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}

	// Validate: empty password is only allowed when the user already exists
	// (we preserve the existing hash in that case).
	if password == "" {
		existing, err := database.GetUserByEmail(ctx, email)
		if err != nil {
			return fmt.Errorf("check existing user: %w", err)
		}
		if existing == nil {
			return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD is required when BOOTSTRAP_ADMIN_EMAIL is set and no user with that email exists yet")
		}
	}

	// Compute bcrypt hash when password is provided.
	var passwordHash string
	if password != "" {
		h, hashErr := HashPassword(password)
		if hashErr != nil {
			return fmt.Errorf("hash bootstrap admin password: %w", hashErr)
		}
		passwordHash = h
	}

	userID := uuid.NewString()
	userSlug := cpdb.ComputeUserSlug(email)

	if err := database.UpsertBootstrapAdmin(ctx, userID, email, passwordHash, userSlug); err != nil {
		return fmt.Errorf("upsert bootstrap admin user: %w", err)
	}

	return nil
}
