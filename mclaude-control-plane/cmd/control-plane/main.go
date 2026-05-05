// mclaude-control-plane: Auth, SSO, SCIM, user/project provisioning,
// NATS-based project lifecycle, host management.
//
// Per ADR-0035: the main server path has zero K8s imports. Project provisioning
// is delegated to mclaude-controller-k8s (cluster) via NATS request/reply.
// The "init-keys" subcommand (Helm pre-install Job) is the only code path
// that uses client-go to write the operator-keys Secret.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"

	cpdb "mclaude-control-plane/db"
	"mclaude-control-plane/server"
)

func main() {
	// Subcommand routing: Helm pre-install Jobs call these.
	if len(os.Args) > 1 && os.Args[1] == "init-keys" {
		runInitKeys()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "gen-host-nkey" {
		runGenHostNkey()
		return
	}


	logger := zerolog.New(os.Stdout).With().
		Str("component", "control-plane").
		Timestamp().
		Logger()

	port := envOr("PORT", "8080")
	databaseDSN := envOr("DATABASE_URL", envOr("DATABASE_DSN", "")) // DATABASE_URL is the k8s/helm convention
	natsURL := envOr("NATS_URL", "nats://localhost:4222")
	natsWsURL := envOr("NATS_WS_URL", "") // external WebSocket URL for browser clients; empty = client derives from origin

	jwtExpiry := 8 * time.Hour
	if v := os.Getenv("JWT_EXPIRY_SECONDS"); v != "" {
		if secs, err := time.ParseDuration(v + "s"); err == nil {
			jwtExpiry = secs
		}
	}

	ctx := context.Background()

	// Database
	var db *cpdb.DB
	if databaseDSN != "" {
		var err error
		db, err = cpdb.ConnectDB(ctx, databaseDSN)
		if err != nil {
			logger.Fatal().Err(err).Msg("connect to postgres")
		}
		defer db.Close()
		if err := db.Migrate(ctx); err != nil {
			logger.Fatal().Err(err).Msg("migrate schema")
		}
	} else {
		logger.Fatal().Msg("DATABASE_DSN required")
	}

	// EXTERNAL_URL is required — control-plane exits on startup if empty.
	externalURL := os.Getenv("EXTERNAL_URL")
	if externalURL == "" {
		logger.Fatal().Msg("EXTERNAL_URL is required (set to the externally-accessible base URL, e.g. https://mclaude.internal)")
	}

	// Account NKey — in production, load from secret; generate ephemeral for dev.
	accountKP, err := loadOrGenerateAccountKey()
	if err != nil {
		logger.Fatal().Err(err).Msg("account nkey")
	}

	srv := server.NewServer(db, accountKP, natsURL, natsWsURL, jwtExpiry)
	srv.SetExternalURL(externalURL)

	// Load OAuth provider config from /etc/mclaude/providers.json (Helm ConfigMap mount).
	providerCfgPath := envOr("PROVIDERS_CONFIG_PATH", "/etc/mclaude/providers.json")
	if err := srv.SetupProviders(ctx, providerCfgPath, externalURL); err != nil {
		logger.Warn().Err(err).Msg("load providers config — continuing without providers")
	}

	// Load S3 config for import and attachment support (ADR-0053).
	if endpoint, bucket, configured := srv.SetupS3(); configured {
		logger.Info().Str("endpoint", endpoint).Str("bucket", bucket).Msg("S3 configured for imports and attachments")
	} else {
		logger.Warn().Msg("S3 not configured — import and attachment features unavailable (set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY)")
	}

	// Load operator + sysAccount seeds from OPERATOR_KEYS_PATH for JWT revocation (ADR-0054).
	// Fatal exit if path is set but unreadable (per spec: "fatal exit on startup").
	operatorKeyPath := envOr("OPERATOR_KEYS_PATH", "/etc/mclaude/operator-keys")
	if operatorSeed, sysAccountSeed, accountJWT, loadErr := loadOperatorKeys(operatorKeyPath); loadErr != nil {
		// Non-fatal in dev: keys mount may not exist. Log warning and continue.
		logger.Warn().Err(loadErr).Str("path", operatorKeyPath).Msg("operator-keys not loaded — JWT revocation disabled (TTL-only fallback)")
	} else {
		srv.SetRevocationCredentials(operatorSeed, sysAccountSeed, accountJWT)
		logger.Info().Msg("operator-keys loaded — JWT revocation via $SYS.REQ.CLAIMS.UPDATE enabled")
	}

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

	// Start ADR-0054 lifecycle subscribers (host lifecycle, agent registration).
	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		logger.Fatal().Err(err).Msg("start lifecycle subscribers")
	}
	logger.Info().Msg("lifecycle subscribers started")

	// Start $SYS.ACCOUNT subscriber for host presence tracking (ADR-0035).
	if err := srv.StartSysSubscriber(nc); err != nil {
		logger.Fatal().Err(err).Msg("start $SYS subscriber")
	}
	logger.Info().Msg("$SYS host presence subscriber started")

	// Start GitLab token refresh goroutine (runs every 15 minutes).
	srv.StartGitLabRefreshGoroutine(ctx)
	logger.Info().Msg("GitLab token refresh goroutine started")

	// Main API mux (public, protected, admin, and metrics routes — all on one port).
	// Admin routes are protected by isAdminMiddleware (JWT + is_admin=true in DB).
	// Metrics are unauthenticated (internal k8s scraping).
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	logger.Info().Str("port", port).Msg("starting")
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		logger.Fatal().Err(err).Msg("listen and serve")
	}
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

// loadOperatorKeys reads operator credentials from the OPERATOR_KEYS_PATH directory.
// Returns (operatorSeed, sysAccountSeed, accountJWT, error).
// The directory is a K8s Secret mount — each key is a separate file.
// If NATS_SYS_ACCOUNT_SEED env var is set, it takes precedence over the file.
func loadOperatorKeys(path string) (operatorSeed, sysAccountSeed, accountJWT string, err error) {
	// Read operatorSeed from file.
	operatorSeedFile := filepath.Join(path, "operatorSeed")
	data, err := os.ReadFile(operatorSeedFile)
	if err != nil {
		return "", "", "", fmt.Errorf("read operatorSeed: %w", err)
	}
	operatorSeed = strings.TrimSpace(string(data))

	// Read accountJWT from file.
	accountJWTFile := filepath.Join(path, "accountJwt")
	data, err = os.ReadFile(accountJWTFile)
	if err != nil {
		return "", "", "", fmt.Errorf("read accountJwt: %w", err)
	}
	accountJWT = strings.TrimSpace(string(data))

	// Read sysAccountSeed: env var takes precedence over file.
	if v := os.Getenv("NATS_SYS_ACCOUNT_SEED"); v != "" {
		sysAccountSeed = v
	} else {
		sysAccountSeedFile := filepath.Join(path, "sysAccountSeed")
		data, err = os.ReadFile(sysAccountSeedFile)
		if err != nil {
			return "", "", "", fmt.Errorf("read sysAccountSeed: %w", err)
		}
		sysAccountSeed = strings.TrimSpace(string(data))
	}

	return operatorSeed, sysAccountSeed, accountJWT, nil
}
