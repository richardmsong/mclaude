// Package controller implements the mclaude-controller-k8s boot sequence,
// NATS subscriber, and per-user Kubernetes reconciler.
//
// Per ADR-0091, the controller's scope is:
//   - Boot via hostauth.NewHostAuthFromSeed, decode hslug from JWT (ADR-0063).
//   - Subscribe to mclaude.hosts.{hslug}.grants.create → run per-user reconciler.
//   - Keep users.*.projects.*.update handler for OAuth token rotation (user-secrets only).
//
// Removed per ADR-0091:
//   - MCProject CRD reconciliation (delegated to user-agent).
//   - Per-project Deployment, PVCs, agent-nkey-{projectId} Secret.
//   - users.*.projects.*.create and .delete handlers.
//   - DEV_OAUTH_TOKEN env var.
package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"mclaude-control-plane/hostauth"
)

// Run is the entrypoint for the controller. It reads configuration from
// environment variables, performs the ADR-0063 boot sequence, and starts
// the controller-runtime manager.
func Run() {
	// controller-runtime v0.23+ requires SetLogger before NewManager.
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	logLevel, err := zerolog.ParseLevel(EnvOr("LOG_LEVEL", "info"))
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
	hubNATSURL := EnvOr("HUB_NATS_URL", "nats://localhost:4222")
	controlPlaneURL := os.Getenv("CONTROL_PLANE_URL")
	if controlPlaneURL == "" {
		logger.Fatal().Msg("CONTROL_PLANE_URL is required — set to the control-plane base URL (e.g. https://cp.mclaude.example)")
	}
	seedPath := EnvOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")
	helmReleaseName := EnvOr("HELM_RELEASE_NAME", "mclaude-worker")

	// authenticated is set to true after the challenge-response boot sequence succeeds.
	// It gates the /readyz probe — the pod stays "not ready" (503) until authentication
	// completes, but the process is always "alive" (200 on /healthz).
	var authenticated atomic.Bool

	// ADR-0063 boot sequence: load NKey seed. Fatal if missing — Secret not mounted.
	auth, err := hostauth.NewHostAuthFromSeed(seedPath, controlPlaneURL, logger)
	if err != nil {
		logger.Fatal().Err(err).Str("seedPath", seedPath).Msg("load NKey seed")
	}
	logger.Info().Str("pubkey", auth.PublicKey()).Msg("NKey loaded — will acquire host JWT via challenge-response")

	// Set up controller-runtime scheme (standard Kubernetes types only — no MCProject CRD per ADR-0091).
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Fatal().Err(err).Msg("add client-go scheme")
	}

	restCfg := ctrl.GetConfigOrDie()
	leaderElectionNs := EnvOr("LEADER_ELECTION_NAMESPACE", "mclaude-system")

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: EnvOr("METRICS_ADDR", ":8082")},
		// HealthProbeBindAddress uses the manager's built-in health server.
		// The manager starts this server when mgr.Start() is called, before
		// our boot runnable executes — so the liveness probe is satisfied
		// even while BootRefresh (challenge-response auth) is in progress.
		HealthProbeBindAddress:  EnvOr("HEALTH_PROBE_ADDR", ":8081"),
		LeaderElection:          EnvOr("LEADER_ELECTION", "false") == "true",
		LeaderElectionID:        "mclaude-controller-k8s",
		LeaderElectionNamespace: leaderElectionNs,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("create controller-runtime manager")
	}

	// /healthz — always 200 (process is alive, liveness probe).
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		logger.Fatal().Err(err).Msg("add healthz check")
	}

	// /readyz — 200 only after BootRefresh succeeds (readiness probe).
	if err := mgr.AddReadyzCheck("authenticated", func(_ *http.Request) error {
		if !authenticated.Load() {
			return errors.New("not authenticated")
		}
		return nil
	}); err != nil {
		logger.Fatal().Err(err).Msg("add readyz check")
	}

	// Determine controller namespace.
	controlPlaneNs := DetectNamespace()

	// Register the boot runnable. controller-runtime starts all Runnables after
	// the health server is up, so the liveness probe is already satisfied when
	// BootRefresh blocks waiting for the operator to register the NKey.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		// ADR-0063 boot sequence:
		// 1. Refresh() → HTTP challenge-response → receive host JWT. Retry on ErrNotRegistered.
		// 2. Decode hslug from the JWT name ("host-{hslug}").
		// 3. Connect to hub NATS with the host JWT.
		// 4. Start background JWT refresh loop.
		hostJWT := BootRefresh(ctx, auth, logger)

		// Authentication succeeded — mark the pod as ready.
		authenticated.Store(true)

		hslug, err := HostSlugFromJWT(hostJWT)
		if err != nil {
			return fmt.Errorf("decode host slug from JWT: %w", err)
		}
		logger.Info().Str("hslug", hslug).Msg("host slug decoded from JWT")

		// Connect to hub NATS using the host JWT (ADR-0063).
		nc, err := nats.Connect(hubNATSURL,
			nats.UserJWT(auth.JWTFunc(), auth.SignFunc()),
			nats.RetryOnFailedConnect(true),
			nats.MaxReconnects(-1),
		)
		if err != nil {
			return fmt.Errorf("connect to nats: %w", err)
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
			return fmt.Errorf("start NATS subscriber: %w", err)
		}
		logger.Info().Str("hslug", hslug).Msg("NATS subscriber started")

		// Block until context is cancelled (manager shutdown).
		<-ctx.Done()
		return nil
	})); err != nil {
		logger.Fatal().Err(err).Msg("add boot runnable")
	}

	logger.Info().Msg("starting controller-runtime manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Fatal().Err(err).Msg("controller-runtime manager stopped")
	}
}

// Refresher is the subset of *hostauth.HostAuth used by BootRefresh.
// Defined as an interface to allow test injection of a fake.
type Refresher interface {
	Refresh(ctx context.Context) (string, error)
}

// BootRefresh calls auth.Refresh() with exponential backoff until a JWT is acquired.
// Initial wait: 5s, doubles each attempt, capped at 60s (ADR-0063).
func BootRefresh(ctx context.Context, auth Refresher, log zerolog.Logger) string {
	return BootRefreshWithInterval(ctx, auth, log, 5*time.Second, 60*time.Second)
}

// BootRefreshWithInterval is the testable implementation of BootRefresh.
// initialInterval and maxInterval are exposed for test injection of short durations.
func BootRefreshWithInterval(ctx context.Context, auth Refresher, log zerolog.Logger, initialInterval, maxInterval time.Duration) string {
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

// HostSlugFromJWT decodes the JWT payload (without signature verification) and
// extracts the host slug from the claim name field ("host-{hslug}").
func HostSlugFromJWT(token string) (string, error) {
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

// DetectNamespace reads the pod namespace from the service account mount.
// Falls back to "mclaude-system" if not in cluster.
func DetectNamespace() string {
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "mclaude-system"
	}
	return strings.TrimSpace(string(nsBytes))
}

// EnvOr returns the value of the environment variable key, or def if it is unset or empty.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

