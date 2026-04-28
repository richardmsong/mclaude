// mclaude-control-plane: Auth, SSO, SCIM, user/project provisioning,
// NATS-based project lifecycle, host management.
//
// Per ADR-0035: the main server path has zero K8s imports. Project provisioning
// is delegated to mclaude-controller-k8s (cluster) or mclaude-controller-local
// (BYOH) via NATS request/reply. The "init-keys" subcommand (Helm pre-install
// Job) is the only code path that uses client-go to write the operator-keys Secret.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
)

func main() {
	// Subcommand routing: Helm pre-install Jobs call these.
	if len(os.Args) > 1 && os.Args[1] == "init-keys" {
		runInitKeys()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "gen-leaf-creds" {
		runGenLeafCreds()
		return
	}

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

	// EXTERNAL_URL is required — control-plane exits on startup if empty.
	externalURL := os.Getenv("EXTERNAL_URL")
	if externalURL == "" {
		logger.Fatal().Msg("EXTERNAL_URL is required (set to the externally-accessible base URL, e.g. https://mclaude.internal)")
	}

	// Load OAuth provider config from /etc/mclaude/providers.json (Helm ConfigMap mount).
	providerCfgPath := envOr("PROVIDERS_CONFIG_PATH", "/etc/mclaude/providers.json")
	loadedProviders, err := LoadProviders(ctx, providerCfgPath)
	if err != nil {
		logger.Warn().Err(err).Msg("load providers config — continuing without providers")
	}
	provReg := &providerRegistry{
		providers:   loadedProviders,
		stateStore:  NewOAuthStateStore(),
		externalURL: externalURL,
	}

	// Account NKey — in production, load from secret; generate ephemeral for dev.
	accountKP, err := loadOrGenerateAccountKey()
	if err != nil {
		logger.Fatal().Err(err).Msg("account nkey")
	}

	srv := NewServer(db, accountKP, natsURL, natsWsURL, jwtExpiry, adminToken)
	srv.providers = provReg

	// NATS connection — used for project subscriptions, KV writes, and provisioning.
	userJWT, userKP, err := generateNATSUserCreds(accountKP)
	if err != nil {
		logger.Fatal().Err(err).Msg("generate NATS user credentials")
	}

	nc, err := nats.Connect(natsURL,
		nats.UserJWT(
			func() (string, error) { return userJWT, nil },
			func(nonce []byte) ([]byte, error) { return userKP.Sign(nonce) },
		),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect to nats")
	}
	defer nc.Close()

	// Wire NATS connection into Server so HTTP handlers can write to KV buckets.
	srv.SetNATSConn(nc)

	if err := srv.StartProjectsSubscriber(nc); err != nil {
		logger.Fatal().Err(err).Msg("start projects subscriber")
	}
	logger.Info().Msg("projects subscriber started")

	// Start $SYS.ACCOUNT subscriber for host presence tracking (ADR-0035).
	if err := srv.StartSysSubscriber(nc); err != nil {
		logger.Fatal().Err(err).Msg("start $SYS subscriber")
	}
	logger.Info().Msg("$SYS host presence subscriber started")

	// Start GitLab token refresh goroutine (runs every 15 minutes).
	srv.StartGitLabRefreshGoroutine(ctx)
	logger.Info().Msg("GitLab token refresh goroutine started")

	// Dev seed: create a known account + default project when DEV_SEED=true and DB is available.
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
	// Always rewrite KV entries.
	for _, proj := range projects {
		if err := writeProjectKV(nc, user.ID, proj); err != nil {
			logger.Error().Err(err).Str("projectId", proj.ID).Msg("DEV_SEED: write project KV failed (non-fatal)")
		}
	}

	// Write the local machine host to mclaude-hosts KV so the SPA renders it
	// as online. The local dev host has no NKey, so no $SYS CONNECT fires (ADR-0046).
	if user.Slug != "" {
		var localHostSlug, localHostName, localHostType, localHostRole string
		qerr := db.pool.QueryRow(ctx, `
			SELECT slug, name, type, role FROM hosts
			WHERE user_id = $1 AND slug = 'local' LIMIT 1`,
			user.ID).Scan(&localHostSlug, &localHostName, &localHostType, &localHostRole)
		if qerr == nil {
			js, jerr := nc.JetStream()
			if jerr == nil {
				hostsKV, kerr := ensureHostsKV(js)
				if kerr == nil {
					now := time.Now().UTC().Format(time.RFC3339)
					state := HostKVState{
						Slug:       localHostSlug,
						Type:       localHostType,
						Name:       localHostName,
						Role:       localHostRole,
						Online:     true,
						LastSeenAt: &now,
					}
					if val, merr := json.Marshal(state); merr == nil {
						key := user.Slug + "." + localHostSlug
						if _, perr := hostsKV.Put(key, val); perr != nil {
							logger.Error().Err(perr).Str("key", key).Msg("DEV_SEED: write local host KV failed (non-fatal)")
						} else {
							logger.Info().Str("key", key).Msg("DEV_SEED: wrote local host to mclaude-hosts KV")
						}
					}
				}
			}
		}
	}

	return nil
}

// generateNATSUserCreds creates an ephemeral user JWT signed by the account key,
// allowing the control-plane to authenticate against a NATS server running
// operator JWT auth.
func generateNATSUserCreds(accountKP nkeys.KeyPair) (userJWT string, userKP nkeys.KeyPair, err error) {
	userKP, err = nkeys.CreateUser()
	if err != nil {
		return "", nil, fmt.Errorf("create user nkey: %w", err)
	}
	userPub, err := userKP.PublicKey()
	if err != nil {
		return "", nil, fmt.Errorf("user public key: %w", err)
	}
	claims := natsjwt.NewUserClaims(userPub)
	claims.Name = "control-plane"
	claims.IssuerAccount, _ = accountKP.PublicKey()
	jwt, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode user jwt: %w", err)
	}
	return jwt, userKP, nil
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
