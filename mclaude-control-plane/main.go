// mclaude-control-plane: Auth, SSO, SCIM, user/project provisioning,
// K8s namespace management, NATS JWT issuance.
//
// See docs/plan-k8s-integration.md for full architecture.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
)

func main() {
	logger := zerolog.New(os.Stdout).With().
		Str("component", "control-plane").
		Timestamp().
		Logger()

	port := envOr("PORT", "8080")
	adminPort := envOr("ADMIN_PORT", "9091")
	databaseDSN := envOr("DATABASE_URL", envOr("DATABASE_DSN", "")) // DATABASE_URL is the k8s/helm convention
	natsURL := envOr("NATS_URL", "nats://localhost:4222")
	adminToken := envOr("ADMIN_TOKEN", "")

	jwtExpiry := 8 * time.Hour
	if v := os.Getenv("JWT_EXPIRY_SECONDS"); v != "" {
		if secs, err := time.ParseDuration(v + "s"); err == nil {
			jwtExpiry = secs
		}
	}

	ctx := context.Background()

	// Database
	var db *DB
	if databaseDSN != "" {
		var err error
		db, err = ConnectDB(ctx, databaseDSN)
		if err != nil {
			logger.Fatal().Err(err).Msg("connect to postgres")
		}
		defer db.Close()
		if err := db.Migrate(ctx); err != nil {
			logger.Fatal().Err(err).Msg("migrate schema")
		}
	} else {
		logger.Warn().Msg("DATABASE_DSN not set — running without persistence")
	}

	// Account NKey — in production, load from secret; generate ephemeral for dev.
	accountKP, err := loadOrGenerateAccountKey()
	if err != nil {
		logger.Fatal().Err(err).Msg("account nkey")
	}

	// Dev seed: create a known account when DEV_SEED=true and DB is available.
	if os.Getenv("DEV_SEED") == "true" && db != nil {
		if err := seedDevUser(ctx, db, logger); err != nil {
			logger.Error().Err(err).Msg("dev seed failed")
		}
	}

	srv := NewServer(db, accountKP, natsURL, jwtExpiry, adminToken)

	// Main API mux (public + protected routes)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Admin mux on separate port
	go func() {
		logger.Info().Str("port", adminPort).Msg("admin listener starting")
		if err := http.ListenAndServe("127.0.0.1:"+adminPort, srv.AdminMux()); err != nil {
			log.Printf("admin listener: %v", err)
		}
	}()

	logger.Info().Str("port", port).Msg("starting")
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		logger.Fatal().Err(err).Msg("listen and serve")
	}
}

// seedDevUser creates a dev account (dev@mclaude.local / dev) if it doesn't exist.
// Only called when DEV_SEED=true. Safe to call on every startup — no-op if the
// user already exists.
func seedDevUser(ctx context.Context, db *DB, logger zerolog.Logger) error {
	const devEmail = "dev@mclaude.local"
	const devPassword = "dev"

	existing, err := db.GetUserByEmail(ctx, devEmail)
	if err != nil {
		return fmt.Errorf("check dev user: %w", err)
	}
	if existing != nil {
		logger.Info().Str("email", devEmail).Msg("dev user already exists")
		return nil
	}

	hash, err := HashPassword(devPassword)
	if err != nil {
		return fmt.Errorf("hash dev password: %w", err)
	}

	id := uuid.NewString()
	if _, err := db.CreateUser(ctx, id, devEmail, "Dev User", hash); err != nil {
		return fmt.Errorf("create dev user: %w", err)
	}

	logger.Warn().
		Str("email", devEmail).
		Str("password", devPassword).
		Msg("DEV_SEED: created dev account — do not use in production")
	return nil
}

// loadOrGenerateAccountKey loads the account NKey from NATS_ACCOUNT_SEED env,
// or generates an ephemeral one (dev/test only).
func loadOrGenerateAccountKey() (nkeys.KeyPair, error) {
	if seed := os.Getenv("NATS_ACCOUNT_SEED"); seed != "" {
		return nkeys.FromSeed([]byte(seed))
	}
	kp, err := nkeys.CreateAccount()
	if err != nil {
		return nil, err
	}
	return kp, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
