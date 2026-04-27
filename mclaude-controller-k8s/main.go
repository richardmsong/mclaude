// mclaude-controller-k8s: kubebuilder operator for MCProject CRDs.
// Extracted from mclaude-control-plane per ADR-0035 (stage 5).
//
// Subscribes to NATS provisioning subjects for its cluster and creates
// MCProject CRs which the reconciler then provisions as K8s resources.
package main

import (
	"os"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	logger := zerolog.New(os.Stdout).With().
		Str("component", "controller-k8s").
		Timestamp().
		Logger()

	natsURL := envOr("NATS_URL", "nats://localhost:4222")
	clusterSlug := os.Getenv("CLUSTER_SLUG")
	if clusterSlug == "" {
		logger.Fatal().Msg("CLUSTER_SLUG is required — set to this cluster's host slug (e.g. us-east)")
	}
	helmReleaseName := envOr("HELM_RELEASE_NAME", "mclaude")
	devOAuthToken := os.Getenv("DEV_OAUTH_TOKEN")

	// Account NKey — loaded from NATS_ACCOUNT_SEED env (required in production).
	accountKP, err := loadAccountKey()
	if err != nil {
		logger.Fatal().Err(err).Msg("account nkey")
	}

	// Set up controller-runtime scheme.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Fatal().Err(err).Msg("add client-go scheme")
	}
	if err := AddToScheme(scheme); err != nil {
		logger.Fatal().Err(err).Msg("add MCProject scheme")
	}

	restCfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                        scheme,
		Metrics:                       metricsserver.Options{BindAddress: envOr("METRICS_ADDR", ":8082")},
		HealthProbeBindAddress:        envOr("HEALTH_PROBE_ADDR", ":8081"),
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("create controller-runtime manager")
	}

	// Determine control-plane namespace.
	controlPlaneNs := detectNamespace()

	// sessionAgentNATSURL: FQDN NATS URL for pods in other namespaces.
	saURL := sessionAgentNATSURL(natsURL, controlPlaneNs)

	reconciler := &MCProjectReconciler{
		client:              mgr.GetClient(),
		scheme:              mgr.GetScheme(),
		controlPlaneNs:      controlPlaneNs,
		releaseName:         helmReleaseName,
		sessionAgentNATSURL: saURL,
		accountKP:           accountKP,
		devOAuthToken:       devOAuthToken,
		logger:              logger.With().Str("reconciler", "mcproject").Logger(),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Fatal().Err(err).Msg("setup MCProject reconciler")
	}

	// NATS connection — for receiving provisioning requests from control-plane.
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect to nats")
	}
	defer nc.Close()

	// Start NATS provisioning subscriber.
	provisioner := &NATSProvisioner{
		nc:             nc,
		k8sClient:      mgr.GetClient(),
		controlPlaneNs: controlPlaneNs,
		clusterSlug:    clusterSlug,
		logger:         logger.With().Str("subscriber", "provisioner").Logger(),
	}
	if err := provisioner.StartNATSSubscriber(); err != nil {
		logger.Fatal().Err(err).Msg("start NATS provisioning subscriber")
	}
	logger.Info().Str("clusterSlug", clusterSlug).Msg("NATS provisioning subscriber started")

	logger.Info().Msg("starting controller-runtime manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Fatal().Err(err).Msg("controller-runtime manager stopped")
	}
}

// loadAccountKey loads the account NKey from NATS_ACCOUNT_SEED env.
// In production, this is always set. For dev/test, generates an ephemeral key.
func loadAccountKey() (nkeys.KeyPair, error) {
	if seed := os.Getenv("NATS_ACCOUNT_SEED"); seed != "" {
		return nkeys.FromSeed([]byte(seed))
	}
	return nkeys.CreateAccount()
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

// sessionAgentNATSURL returns a FQDN NATS URL suitable for pods in other namespaces.
func sessionAgentNATSURL(rawURL, ns string) string {
	withoutScheme := strings.TrimPrefix(rawURL, "nats://")
	parts := strings.SplitN(withoutScheme, ":", 2)
	hostname := parts[0]
	port := ""
	if len(parts) == 2 {
		port = ":" + parts[1]
	}
	if strings.Contains(hostname, ".") {
		return rawURL
	}
	return "nats://" + hostname + "." + ns + ".svc.cluster.local" + port
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
