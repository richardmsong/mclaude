package main

import (
	"context"
	"fmt"
	"os"
	"time"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// runGenLeafCreds implements the "gen-leaf-creds" subcommand: reads the account
// seed from the operator-keys Secret, generates a NATS user JWT + NKey seed,
// and writes them as a .creds file into a new K8s Secret.
//
// Called by the Helm pre-install Job (charts/mclaude-worker/templates/gen-leaf-creds-job.yaml).
// Idempotent: exits 0 if the leaf-creds Secret already exists.
func runGenLeafCreds() {
	logger := zerolog.New(os.Stdout).With().
		Str("component", "control-plane").
		Str("subcommand", "gen-leaf-creds").
		Timestamp().
		Logger()

	namespace := envOr("NAMESPACE", "mclaude-system")
	secretName := envOr("LEAF_CREDS_SECRET", "mclaude-worker-nats-leaf-creds")
	accountSeedSecret := envOr("ACCOUNT_SEED_SECRET", "operator-keys")
	accountSeedKey := envOr("ACCOUNT_SEED_KEY", "accountSeed")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("load in-cluster config (are we running inside K8s?)")
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("create kubernetes client")
	}

	// Check if leaf-creds Secret already exists — idempotent.
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		logger.Info().
			Str("namespace", namespace).
			Str("secret", secretName).
			Msg("leaf-creds Secret already exists — nothing to do")
		return
	}
	if !kerrors.IsNotFound(err) {
		logger.Fatal().Err(err).Msg("check existing leaf-creds Secret")
	}

	// Read account seed from operator-keys Secret.
	opSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, accountSeedSecret, metav1.GetOptions{})
	if err != nil {
		logger.Fatal().Err(err).Str("secret", accountSeedSecret).Msg("read operator-keys Secret")
	}
	accountSeed := opSecret.Data[accountSeedKey]
	if len(accountSeed) == 0 {
		logger.Fatal().Str("key", accountSeedKey).Msg("account seed not found in Secret")
	}

	accountKP, err := nkeys.FromSeed(accountSeed)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse account seed")
	}

	userKP, err := nkeys.CreateUser()
	if err != nil {
		logger.Fatal().Err(err).Msg("create user nkey")
	}
	userPub, _ := userKP.PublicKey()
	userSeed, _ := userKP.Seed()

	claims := natsjwt.NewUserClaims(userPub)
	claims.Name = "leaf-node"
	claims.IssuerAccount, _ = accountKP.PublicKey()

	jwt, err := claims.Encode(accountKP)
	if err != nil {
		logger.Fatal().Err(err).Msg("encode user jwt")
	}

	creds := fmt.Sprintf("-----BEGIN NATS USER JWT-----\n%s\n------END NATS USER JWT------\n\n-----BEGIN USER NKEY SEED-----\n%s\n------END USER NKEY SEED------\n", jwt, string(userSeed))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mclaude-gen-leaf-creds",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"leaf.creds": []byte(creds),
		},
	}
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			logger.Info().Msg("leaf-creds Secret created by another instance — idempotent exit")
			return
		}
		logger.Fatal().Err(err).Msg("create leaf-creds Secret")
	}
	logger.Info().
		Str("namespace", namespace).
		Str("secret", secretName).
		Msg("created leaf-creds Secret")
}
