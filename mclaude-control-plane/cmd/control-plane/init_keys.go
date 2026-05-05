package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	cpdb "mclaude-control-plane/db"
	mclaudenats "mclaude-control-plane/nkeys"
)

// runInitKeys implements the "init-keys" subcommand: generates operator + account
// NKey pairs and JWTs, writes them to a K8s Secret, and UPSERTs the bootstrap
// admin user in Postgres.
//
// Called by the Helm pre-install Job (charts/mclaude-cp/templates/init-keys-job.yaml).
//
// Control flow per ADR-0094:
//  1. Key generation: writes the operator-keys Secret. Skipped (idempotent) if the
//     Secret already exists — the early-return is scoped to this step only.
//  2. Bootstrap admin UPSERT: runs unconditionally on every helm upgrade when
//     BOOTSTRAP_ADMIN_EMAIL is set, regardless of whether keys were just generated
//     or already existed.
func runInitKeys() {
	logger := zerolog.New(os.Stdout).With().
		Str("component", "control-plane").
		Str("subcommand", "init-keys").
		Timestamp().
		Logger()

	namespace := envOr("NAMESPACE", "mclaude-system")
	secretName := envOr("OPERATOR_KEYS_SECRET", "operator-keys")
	databaseURL := envOr("DATABASE_URL", "")
	bootstrapEmail := os.Getenv("BOOTSTRAP_ADMIN_EMAIL")
	bootstrapPassword := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Build in-cluster K8s client.
	cfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("load in-cluster config (are we running inside K8s?)")
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("create kubernetes client")
	}

	// Step 1: Key generation — idempotent, scoped early-return.
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		logger.Info().
			Str("namespace", namespace).
			Str("secret", secretName).
			Msg("operator-keys Secret already exists — skipping key generation")
		// Do NOT return here — fall through to admin bootstrap below.
	} else if !kerrors.IsNotFound(err) {
		logger.Fatal().Err(err).Msg("check existing Secret")
	} else {
		// Secret does not exist: generate and create it.
		oa, err := mclaudenats.GenerateOperatorAccount("mclaude-operator", "mclaude-account")
		if err != nil {
			logger.Fatal().Err(err).Msg("generate operator/account keys")
		}
		logger.Info().
			Str("operatorPub", oa.OperatorPublicKey).
			Str("accountPub", oa.AccountPublicKey).
			Msg("generated NKey pairs and JWTs")

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "mclaude-init-keys",
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"operatorJwt":      []byte(oa.OperatorJWT),
				"accountJwt":       []byte(oa.AccountJWT),
				"accountSeed":      oa.AccountSeed,
				"operatorSeed":     oa.OperatorSeed,
				"sysAccountSeed":   oa.SysAccountSeed,
				"accountPublicKey": []byte(oa.AccountPublicKey),
				"resolverPreload": []byte(
					`"` + oa.SysAccountPublicKey + `": ` + oa.SysAccountJWT + "\n" +
						`"` + oa.AccountPublicKey + `": ` + oa.AccountJWT,
				),
			},
		}
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			if kerrors.IsAlreadyExists(err) {
				// Race condition: another init-keys pod created it between our Get and Create.
				logger.Info().Msg("Secret was created by another init-keys instance — idempotent exit")
				// Fall through to admin bootstrap.
			} else {
				logger.Fatal().Err(err).Msg("create operator-keys Secret")
			}
		} else {
			logger.Info().
				Str("namespace", namespace).
				Str("secret", secretName).
				Msg("created operator-keys Secret")
		}
	}

	// Step 2: Bootstrap admin UPSERT — runs unconditionally on every helm upgrade
	// when BOOTSTRAP_ADMIN_EMAIL is set (ADR-0094).
	if bootstrapEmail != "" {
		if databaseURL == "" {
			logger.Fatal().Msg("BOOTSTRAP_ADMIN_EMAIL set but DATABASE_URL is empty — cannot bootstrap admin")
		}
		if err := bootstrapAdminUser(ctx, logger, databaseURL, bootstrapEmail, bootstrapPassword); err != nil {
			logger.Fatal().Err(err).Msg("bootstrap admin user")
		}
	}

	logger.Info().Msg("init-keys completed successfully")
}

// bootstrapAdminUser UPSERTs a bootstrap admin user in Postgres (ADR-0094).
//
// Semantics:
//   - Fresh user + non-empty password: inserts with is_admin=true and bcrypt hash.
//   - Fresh user + empty password: returns an error (unusable account — login impossible).
//   - Existing user + non-empty password: promotes to admin and rotates password_hash.
//   - Existing user + empty password: promotes to admin, preserves existing password_hash.
//
// The underlying SQL is a single INSERT ... ON CONFLICT (email) DO UPDATE, making
// this safe to re-run on every helm upgrade.
func bootstrapAdminUser(ctx context.Context, logger zerolog.Logger, databaseURL, email, password string) error {
	database, err := cpdb.ConnectDB(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer database.Close()

	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}

	// Validate: empty password is only allowed when the user already exists
	// (we preserve the existing hash in that case). Check existence first.
	if password == "" {
		existing, err := database.GetUserByEmail(ctx, email)
		if err != nil {
			return fmt.Errorf("check existing user: %w", err)
		}
		if existing == nil {
			return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD is required when BOOTSTRAP_ADMIN_EMAIL is set and no user with that email exists yet")
		}
		// User exists with empty password — just ensure is_admin. No hash change.
	}

	// Compute bcrypt hash when password is provided (empty string → sentinel for "preserve").
	var passwordHash string
	if password != "" {
		h, hashErr := hashPassword(password)
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

	logger.Info().Str("email", email).Msg("bootstrap admin user upserted")
	return nil
}

// hashPassword generates a bcrypt hash of the given password (cost 12).
// Duplicated here to keep cmd/control-plane self-contained without importing server/.
func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

