// mclaude-controller-k8s: Kubernetes controller for per-user namespace management.
//
// Per ADR-0091, this controller's scope is reduced to:
//   - Boot via hostauth.NewHostAuthFromSeed, decode hslug from JWT (ADR-0063).
//   - Subscribe to mclaude.hosts.{hslug}.grants.create → run per-user reconciler.
//   - Keep users.*.projects.*.update handler for OAuth token rotation (user-secrets only).
//
// Removed per ADR-0091:
//   - MCProject CRD reconciliation (delegated to user-agent).
//   - Per-project Deployment, PVCs, agent-nkey-{projectId} Secret.
//   - users.*.projects.*.create and .delete handlers.
//   - DEV_OAUTH_TOKEN env var.
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
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

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

	// ADR-0063 / ADR-0091: env vars.
	// DEV_OAUTH_TOKEN removed per ADR-0091.
	hubNATSURL := envOr("HUB_NATS_URL", "nats://localhost:4222")
	controlPlaneURL := os.Getenv("CONTROL_PLANE_URL")
	if controlPlaneURL == "" {
		logger.Fatal().Msg("CONTROL_PLANE_URL is required — set to the control-plane base URL (e.g. https://cp.mclaude.example)")
	}
	seedPath := envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")
	helmReleaseName := envOr("HELM_RELEASE_NAME", "mclaude-worker")

	// ADR-0063 boot sequence:
	// 1. Load NKey seed from mounted Secret file.
	// 2. Refresh() → HTTP challenge-response → receive host JWT. Retry on ErrNotRegistered.
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

	// Set up controller-runtime scheme (standard Kubernetes types only — no MCProject CRD per ADR-0091).
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Fatal().Err(err).Msg("add client-go scheme")
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

	// Determine controller namespace.
	controlPlaneNs := detectNamespace()

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

	// Start background JWT refresh loop (ADR-0063).
	auth.StartRefreshLoop(ctx)

	// Per-user reconciler (ADR-0091).
	userReconciler := &UserReconciler{
		client:          mgr.GetClient(),
		controlPlaneNs:  controlPlaneNs,
		releaseName:     helmReleaseName,
		hslug:           hslug,
		controlPlaneURL: controlPlaneURL,
		hubNATSURL:      hubNATSURL,
		nc:              nc,
		logger:          logger.With().Str("reconciler", "user").Logger(),
	}

	// NATS subscriber — grants.create + projects.update.
	subscriber := &NATSSubscriber{
		nc:             nc,
		hslug:          hslug,
		userReconciler: userReconciler,
		logger:         logger.With().Str("subscriber", "nats").Logger(),
	}
	if err := subscriber.StartNATSSubscriber(); err != nil {
		logger.Fatal().Err(err).Msg("start NATS subscriber")
	}
	logger.Info().Str("hslug", hslug).Msg("NATS subscriber started")

	logger.Info().Msg("starting controller-runtime manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Fatal().Err(err).Msg("controller-runtime manager stopped")
	}
}

// refresher is the subset of *hostauth.HostAuth used by bootRefresh.
// Defined as an interface to allow test injection of a fake.
type refresher interface {
	Refresh(ctx context.Context) (string, error)
}

// bootRefresh calls auth.Refresh() with exponential backoff until a JWT is acquired.
// Initial wait: 5s, doubles each attempt, capped at 60s (ADR-0063).
func bootRefresh(ctx context.Context, auth refresher, log zerolog.Logger) string {
	return bootRefreshWithInterval(ctx, auth, log, 5*time.Second, 60*time.Second)
}

// bootRefreshWithInterval is the testable implementation of bootRefresh.
// initialInterval and maxInterval are exposed for test injection of short durations.
func bootRefreshWithInterval(ctx context.Context, auth refresher, log zerolog.Logger, initialInterval, maxInterval time.Duration) string {
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
