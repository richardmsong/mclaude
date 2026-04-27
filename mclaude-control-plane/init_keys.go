package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	mclaudenats "mclaude.io/common/pkg/nats"
)

// runInitKeys implements the "init-keys" subcommand: generates operator + account
// NKey pairs and JWTs, writes them to a K8s Secret, and optionally creates a
// bootstrap admin user in Postgres.
//
// Called by the Helm pre-install Job (charts/mclaude-cp/templates/init-keys-job.yaml).
// Idempotent: exits 0 if the Secret already exists.
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

	// 1. Check if Secret already exists — idempotent.
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		logger.Info().
			Str("namespace", namespace).
			Str("secret", secretName).
			Msg("operator-keys Secret already exists — nothing to do")
		return
	}
	if !kerrors.IsNotFound(err) {
		logger.Fatal().Err(err).Msg("check existing Secret")
	}

	// 2–4. Generate operator + account NKey pairs and JWTs.
	oa, err := mclaudenats.GenerateOperatorAccount("mclaude-operator", "mclaude-account")
	if err != nil {
		logger.Fatal().Err(err).Msg("generate operator/account keys")
	}
	logger.Info().
		Str("operatorPub", oa.OperatorPublicKey).
		Str("accountPub", oa.AccountPublicKey).
		Msg("generated NKey pairs and JWTs")

	// 5. Create K8s Secret.
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
			return
		}
		logger.Fatal().Err(err).Msg("create operator-keys Secret")
	}
	logger.Info().
		Str("namespace", namespace).
		Str("secret", secretName).
		Msg("created operator-keys Secret")

	// 6. Optionally bootstrap admin user in Postgres.
	if bootstrapEmail != "" && databaseURL != "" {
		if err := bootstrapAdminUser(ctx, logger, databaseURL, bootstrapEmail); err != nil {
			logger.Fatal().Err(err).Msg("bootstrap admin user")
		}
	} else if bootstrapEmail != "" {
		logger.Warn().Msg("BOOTSTRAP_ADMIN_EMAIL set but DATABASE_URL is empty — skipping admin bootstrap")
	}

	logger.Info().Msg("init-keys completed successfully")
}

// bootstrapAdminUser connects to Postgres and inserts a bootstrap admin user
// if one doesn't already exist with the given email. Idempotent.
func bootstrapAdminUser(ctx context.Context, logger zerolog.Logger, databaseURL, email string) error {
	db, err := ConnectDB(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}

	// Check if user already exists — idempotent.
	existing, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("check existing user: %w", err)
	}
	if existing != nil {
		logger.Info().Str("email", email).Bool("isAdmin", existing.IsAdmin).Msg("bootstrap admin user already exists")
		// Ensure is_admin is set even if the user was previously created without it.
		if !existing.IsAdmin {
			if err := db.SetUserAdmin(ctx, existing.ID, true); err != nil {
				return fmt.Errorf("promote existing user to admin: %w", err)
			}
			logger.Info().Str("email", email).Msg("promoted existing user to admin")
		}
		return nil
	}

	// Create the bootstrap admin user (no password, SSO-only).
	user, err := db.CreateUser(ctx, uuid.NewString(), email, "Admin", "")
	if err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}
	if err := db.SetUserAdmin(ctx, user.ID, true); err != nil {
		return fmt.Errorf("set admin flag: %w", err)
	}

	logger.Info().Str("email", email).Str("userId", user.ID).Msg("created bootstrap admin user")
	return nil
}
