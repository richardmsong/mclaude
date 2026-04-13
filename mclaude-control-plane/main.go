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
	"github.com/nats-io/nats.go"
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
	natsWsURL := envOr("NATS_WS_URL", "") // external WebSocket URL for browser clients; empty = client derives from origin
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

	srv := NewServer(db, accountKP, natsURL, natsWsURL, jwtExpiry, adminToken)

	// NATS connection — used for project subscriptions and KV writes.
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect to nats")
	}
	defer nc.Close()

	if err := srv.StartProjectsSubscriber(nc); err != nil {
		logger.Fatal().Err(err).Msg("start projects subscriber")
	}
	logger.Info().Msg("projects subscriber started")

	// Dev seed: create a known account + default project when DEV_SEED=true and DB is available.
	// Runs after NATS connects so the seed can write to the mclaude-projects KV bucket.
	if os.Getenv("DEV_SEED") == "true" && db != nil {
		if err := seedDev(ctx, db, nc, logger); err != nil {
			logger.Error().Err(err).Msg("dev seed failed")
		}
	}

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

// seedDev creates a dev user and default project if they don't exist.
// Only called when DEV_SEED=true. Safe to call on every startup — idempotent.
func seedDev(ctx context.Context, db *DB, nc *nats.Conn, logger zerolog.Logger) error {
	const devEmail = "dev@mclaude.local"
	const devPassword = "dev"

	user, err := db.GetUserByEmail(ctx, devEmail)
	if err != nil {
		return fmt.Errorf("check dev user: %w", err)
	}

	if user == nil {
		hash, err := HashPassword(devPassword)
		if err != nil {
			return fmt.Errorf("hash dev password: %w", err)
		}
		user, err = db.CreateUser(ctx, uuid.NewString(), devEmail, "Dev User", hash)
		if err != nil {
			return fmt.Errorf("create dev user: %w", err)
		}
		logger.Warn().
			Str("email", devEmail).
			Str("password", devPassword).
			Msg("DEV_SEED: created dev account — do not use in production")
	} else {
		logger.Info().Str("email", devEmail).Msg("dev user already exists")
	}

	// Seed a default project for the dev user.
	projects, err := db.GetProjectsByUser(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("check dev projects: %w", err)
	}
	if len(projects) == 0 {
		proj, err := db.CreateProject(ctx, uuid.NewString(), user.ID, "Default Project", "")
		if err != nil {
			return fmt.Errorf("create dev project: %w", err)
		}
		projects = []*Project{proj}
		logger.Warn().Str("userId", user.ID).Str("projectId", proj.ID).Msg("DEV_SEED: created default project")
	}
	// Always rewrite KV entries — ensures correct key format even if a previous
	// startup wrote with the wrong separator.
	for _, proj := range projects {
		if err := writeProjectKV(nc, user.ID, proj); err != nil {
			logger.Error().Err(err).Str("projectId", proj.ID).Msg("DEV_SEED: write project KV failed (non-fatal)")
		}
	}

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
