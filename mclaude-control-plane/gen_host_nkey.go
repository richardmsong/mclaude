package main

import (
	"context"
	"os"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// runGenHostNkey implements the "gen-host-nkey" subcommand: generates a U-prefix
// NKey pair for the worker cluster host identity and writes the seed to a K8s Secret.
//
// Called by the mclaude-worker Helm pre-install/pre-upgrade Job
// (charts/mclaude-worker/templates/gen-host-nkey-job.yaml).
// Idempotent: exits 0 if the Secret already exists.
//
// The operator reads the generated public key from the Job log via:
//
//	kubectl logs job/{release}-gen-host-nkey -n mclaude-system
//
// and passes it to "mclaude host register --nkey-public <key>" to associate
// the host identity with the control plane.
//
// Ref: ADR-0063, ADR-0073.
func runGenHostNkey() {
	logger := zerolog.New(os.Stdout).With().
		Str("component", "control-plane").
		Str("subcommand", "gen-host-nkey").
		Timestamp().
		Logger()

	namespace := envOr("NAMESPACE", "mclaude-system")
	secretName := os.Getenv("HOST_CREDS_SECRET")
	if secretName == "" {
		logger.Fatal().Msg("HOST_CREDS_SECRET env is required")
	}

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

	if err := genHostNkeyWithClient(ctx, logger, clientset, namespace, secretName); err != nil {
		logger.Fatal().Err(err).Msg("gen-host-nkey failed")
	}
}

// genHostNkeyWithClient is the testable core of runGenHostNkey.
// It checks for an existing Secret, generates an NKey pair if absent, and creates the Secret.
func genHostNkeyWithClient(ctx context.Context, logger zerolog.Logger, clientset kubernetes.Interface, namespace, secretName string) error {
	// Check if Secret already exists — idempotent.
	_, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		logger.Info().
			Str("namespace", namespace).
			Str("secret", secretName).
			Msg("host-creds Secret already exists — skipping")
		return nil
	}
	if !kerrors.IsNotFound(err) {
		return err
	}

	// Generate NKey pair for the host identity (U-prefix user key per ADR-0063).
	kp, err := nkeys.CreateUser()
	if err != nil {
		return err
	}

	seed, err := kp.Seed()
	if err != nil {
		return err
	}

	pub, err := kp.PublicKey()
	if err != nil {
		return err
	}

	// Create K8s Secret with the seed.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mclaude-gen-host-nkey",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"nkey_seed": seed,
		},
	}
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			// Race condition: another gen-host-nkey pod created it between our Get and Create.
			logger.Info().Msg("Secret was created by another gen-host-nkey instance — idempotent exit")
			return nil
		}
		return err
	}

	logger.Info().
		Str("namespace", namespace).
		Str("secret", secretName).
		Str("nkeyPublic", pub).
		Msg("generated host NKey")
	return nil
}
