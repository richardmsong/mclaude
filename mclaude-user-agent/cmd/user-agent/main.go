package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	agentpkg "mclaude-user-agent/agent"
	agentauth "mclaude-user-agent/auth"
	agentcrypto "mclaude-user-agent/crypto"
)

func envRequired(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatal().Str("env", key).Msg("required environment variable not set")
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	level, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stderr).With().
		Str("component", "user-agent").
		Timestamp().
		Logger()

	cfg := &agentpkg.Config{
		UserSlug:        envRequired("USER_SLUG"),
		HostSlug:        envRequired("HOST_SLUG"),
		Namespace:       envOr("NAMESPACE", "mclaude-"+os.Getenv("USER_SLUG")),
		NATSURL:         envRequired("NATS_URL"),
		ControlPlaneURL: envRequired("CONTROL_PLANE_URL"),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatal().Err(err).Msg("user-agent exiting with error")
	}
}

func run(ctx context.Context, cfg *agentpkg.Config) error {
	// Step 1: Read nkey-seed from /secrets/agent/nkey-seed. Fatal if missing.
	seedBytes, err := os.ReadFile("/secrets/agent/nkey-seed")
	if err != nil {
		return fmt.Errorf("read nkey-seed: %w (controller did not provision correctly)", err)
	}

	// Step 2: Build auth client.
	agentAuth, err := agentauth.NewUserAgentAuth(seedBytes, cfg.ControlPlaneURL, log.Logger)
	if err != nil {
		return fmt.Errorf("create user-agent auth: %w", err)
	}

	// Step 3: Initialize k8s client.
	k8sClient, err := newK8sClient()
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Step 4: Load or generate X25519 keypair (Boot step 2).
	keypair, err := agentcrypto.LoadOrGenerateX25519Keypair(ctx, k8sClient, cfg.Namespace, "user-agent-key")
	if err != nil {
		return fmt.Errorf("load/generate X25519 keypair: %w", err)
	}
	agentAuth.SetX25519Pub(keypair.Public[:])
	log.Info().
		Str("x25519_pub", fmt.Sprintf("%x", keypair.Public)).
		Msg("X25519 keypair ready")

	// Step 5: Authenticate with CP (Boot step 3) with exponential backoff.
	_, err = agentpkg.AuthenticateWithBackoff(ctx, agentAuth)
	if err != nil {
		return fmt.Errorf("authenticate with CP: %w", err)
	}
	log.Info().Msg("authenticated with CP")

	// Step 6: Create subsystem objects.
	tokenMgr := agentpkg.NewTokenManager(k8sClient, cfg.Namespace, cfg.UserSlug, keypair, log.Logger)
	provisioner := agentpkg.NewProvisioner(k8sClient, cfg.Namespace, cfg, log.Logger)

	// Step 7: Start JWT refresh loop. The refresh loop re-authenticates before TTL expiry
	// so the NATS JWTFunc always returns a valid JWT for reconnects.
	agentAuth.StartRefreshLoop(ctx, func(newJWT string) {
		log.Debug().Msg("JWT refreshed — NATS will use new JWT on next reconnect")
		_ = newJWT
	})

	// Step 8: NATS connect + subscribe loop.
	// On activation: drain connection, reconnect with full JWT, re-subscribe.
	return agentpkg.RunNATSLoop(ctx, cfg, agentAuth, tokenMgr, provisioner, agentpkg.ConnectNATS)
}

// newK8sClient creates an in-cluster Kubernetes client.
func newK8sClient() (kubernetes.Interface, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("create k8s client: %w", err)
	}
	return client, nil
}
