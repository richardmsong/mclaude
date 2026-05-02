// mclaude-controller-k8s: kubebuilder operator for MCProject CRDs.
// Extracted from mclaude-control-plane per ADR-0035 (stage 5).
// ADR-0063: boot via hostauth.NewHostAuthFromSeed, decode hslug from JWT,
// connect to hub NATS with host JWT, drop account-key and leaf-NATS code paths.
//
// Subscribes to NATS provisioning subjects for its cluster and creates
// MCProject CRs which the reconciler then provisions as K8s resources.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"mclaude.io/common/pkg/hostauth"
)

func main() {
	// controller-runtime v0.23+ requires SetLogger before NewManager.
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	logLevel, err := zerolog.ParseLevel(envOr("LOG_LEVEL", "info"))
	if err != nil {
		logLevel = zerolog.InfoLevel
	}
	logger := zerolog.New(os.Stdout).With().
		Str("component", "controller-k8s").
		Timestamp().
		Logger().
		Level(logLevel)

	// ADR-0063: env vars — HUB_NATS_URL replaces NATS_URL; HOST_NKEY_SEED_PATH and
	// CONTROL_PLANE_URL replace NATS_ACCOUNT_SEED / NATS_CREDENTIALS_PATH / CLUSTER_SLUG.
	hubNATSURL := envOr("HUB_NATS_URL", "nats://localhost:4222")
	controlPlaneURL := os.Getenv("CONTROL_PLANE_URL")
	if controlPlaneURL == "" {
		logger.Fatal().Msg("CONTROL_PLANE_URL is required — set to the control-plane base URL (e.g. https://cp.mclaude.example)")
	}
	seedPath := envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")

	helmReleaseName := envOr("HELM_RELEASE_NAME", "mclaude-worker")
	sessionAgentTemplateCM := envOr("SESSION_AGENT_TEMPLATE_CM", helmReleaseName+"-session-agent-template")
	devOAuthToken := os.Getenv("DEV_OAUTH_TOKEN")

	// ADR-0063 boot sequence:
	// 1. Load NKey seed from mounted Secret file.
	// 2. Refresh() → HTTP challenge-response → receive host JWT.
	//    Retry on ErrNotRegistered: 5s initial, doubling, 60s cap.
	// 3. Decode hslug from the JWT name ("host-{hslug}").
	// 4. Connect to hub NATS with the host JWT.
	// 5. Start background JWT refresh loop.
	auth, err := hostauth.NewHostAuthFromSeed(seedPath, controlPlaneURL, logger)
	if err != nil {
		logger.Fatal().Err(err).Str("seedPath", seedPath).Msg("load NKey seed")
	}

	logger.Info().Str("pubkey", auth.PublicKey()).Msg("NKey loaded — acquiring host JWT via challenge-response")

	ctx := context.Background()
	hostJWT := bootRefresh(ctx, auth, logger)

	hslug, err := hostSlugFromJWT(hostJWT)
	if err != nil {
		logger.Fatal().Err(err).Msg("decode host slug from JWT")
	}
	logger.Info().Str("hslug", hslug).Msg("host slug decoded from JWT")

	// Set up controller-runtime scheme.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Fatal().Err(err).Msg("add client-go scheme")
	}
	if err := AddToScheme(scheme); err != nil {
		logger.Fatal().Err(err).Msg("add MCProject scheme")
	}

	restCfg := ctrl.GetConfigOrDie()

	leaderElectionNs := envOr("LEADER_ELECTION_NAMESPACE", "mclaude-system")

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: envOr("METRICS_ADDR", ":8082")},
		HealthProbeBindAddress:  envOr("HEALTH_PROBE_ADDR", ":8081"),
		LeaderElection:          envOr("LEADER_ELECTION", "false") == "true",
		LeaderElectionID:        "mclaude-controller-k8s",
		LeaderElectionNamespace: leaderElectionNs,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("create controller-runtime manager")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Fatal().Err(err).Msg("setup health check")
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Fatal().Err(err).Msg("setup ready check")
	}

	// Determine control-plane namespace.
	controlPlaneNs := detectNamespace()

	reconciler := &MCProjectReconciler{
		client:                 mgr.GetClient(),
		scheme:                 mgr.GetScheme(),
		controlPlaneNs:         controlPlaneNs,
		releaseName:            helmReleaseName,
		sessionAgentTemplateCM: sessionAgentTemplateCM,
		sessionAgentNATSURL:    hubNATSURL, // session-agents inherit HUB_NATS_URL directly (ADR-0063)
		controlPlaneURL:        controlPlaneURL,
		devOAuthToken:          devOAuthToken,
		clusterSlug:            hslug, // decoded from JWT, not from env
		logger:                 logger.With().Str("reconciler", "mcproject").Logger(),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Fatal().Err(err).Msg("setup MCProject reconciler")
	}

	// Connect to hub NATS using the host JWT (ADR-0063).
	nc, err := nats.Connect(hubNATSURL,
		nats.UserJWT(auth.JWTFunc(), auth.SignFunc()),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect to nats")
	}
	defer nc.Close()

	// Wire the NATS connection into the reconciler for agent NKey registration (ADR-0063 step 6b).
	reconciler.nc = nc

	// Start background JWT refresh loop (ADR-0063).
	auth.StartRefreshLoop(ctx)

	// Start NATS provisioning subscriber.
	provisioner := &NATSProvisioner{
		nc:             nc,
		k8sClient:      mgr.GetClient(),
		controlPlaneNs: controlPlaneNs,
		clusterSlug:    hslug,
		reconciler:     reconciler,
		logger:         logger.With().Str("subscriber", "provisioner").Logger(),
	}
	if err := provisioner.StartNATSSubscriber(); err != nil {
		logger.Fatal().Err(err).Msg("start NATS provisioning subscriber")
	}
	logger.Info().Str("hslug", hslug).Msg("NATS provisioning subscriber started")

	logger.Info().Msg("starting controller-runtime manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Fatal().Err(err).Msg("controller-runtime manager stopped")
	}
}

// bootRefresh calls auth.Refresh() with exponential backoff until a JWT is acquired.
// Initial wait: 5s, doubles each attempt, capped at 60s (ADR-0063).
func bootRefresh(ctx context.Context, auth *hostauth.HostAuth, log zerolog.Logger) string {
	const (
		initialInterval = 5 * time.Second
		maxInterval     = 60 * time.Second
	)
	interval := initialInterval
	for {
		jwt, err := auth.Refresh(ctx)
		if err == nil {
			return jwt
		}
		if errors.Is(err, hostauth.ErrNotRegistered) {
			// Logged by Refresh(); just wait.
		} else {
			log.Error().Err(err).Msg("challenge-response failed — retrying")
		}
		select {
		case <-ctx.Done():
			log.Fatal().Msg("context cancelled during boot refresh")
			return ""
		case <-time.After(interval):
		}
		interval *= 2
		if interval > maxInterval {
			interval = maxInterval
		}
	}
}

// hostSlugFromJWT decodes the JWT payload (without signature verification) and
// extracts the host slug from the claim name field ("host-{hslug}").
func hostSlugFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}
	// Decode the payload (second part) — base64url without padding.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("unmarshal JWT payload: %w", err)
	}
	// Host JWTs are named "host-{hslug}" (see control-plane nkeys.go IssueHostJWT).
	if !strings.HasPrefix(claims.Name, "host-") {
		return "", fmt.Errorf("unexpected JWT name %q — expected 'host-{slug}' prefix", claims.Name)
	}
	return strings.TrimPrefix(claims.Name, "host-"), nil
}

// detectNamespace reads the pod namespace from the service account mount.
// Falls back to "mclaude-system" if not in cluster.
func detectNamespace() string {
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "mclaude-system"
	}
	return strings.TrimSpace(string(nsBytes))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
