package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	level, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stderr).With().
		Str("component", "session-agent").
		Timestamp().
		Logger()

	// Health / readiness probe mode — used by K8s liveness and readiness probes.
	// session-agent --health  → exit 0 if process is alive (liveness)
	// session-agent --ready   → exit 0 if NATS is reachable (readiness)
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--health":
			// Liveness: just being alive is enough.
			// The health check verifies the binary can be executed and the Go runtime
			// is not deadlocked. No NATS check — NATS outage must not kill the pod
			// (the break-glass admin port must stay reachable).
			os.Exit(0)
		case "--ready":
			// Readiness: verify NATS is reachable.
			natsURL := os.Getenv("NATS_URL")
			if natsURL == "" {
				natsURL = nats.DefaultURL
			}
			natsCredsFile := os.Getenv("NATS_CREDS_FILE")
			nc, err := natsConnect(natsURL, natsCredsFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "readiness: NATS not reachable: %v\n", err)
				os.Exit(1)
			}
			nc.Close()
			os.Exit(0)
		}
	}

	// CLI flags — accepted from entrypoint.sh or direct invocation.
	// All flags also fall back to env vars for backward compatibility.
	var (
		flagNATSURL   = flag.String("nats-url", os.Getenv("NATS_URL"), "NATS server URL")
		flagNATSCreds = flag.String("nats-creds", os.Getenv("NATS_CREDS_FILE"), "Path to NATS credentials file")
		flagUserID    = flag.String("user-id", os.Getenv("USER_ID"), "User ID (required)")
		flagProjectID = flag.String("project-id", os.Getenv("PROJECT_ID"), "Project ID (required)")
		flagClaudePath = flag.String("claude-path", os.Getenv("CLAUDE_PATH"), "Path to claude binary")
		// --data-dir and --mode are accepted but not yet used by the core agent.
		_ = flag.String("data-dir", "/data", "Data directory (unused by agent binary, consumed by entrypoint)")
		_ = flag.String("mode", "standalone", "Run mode: k8s | standalone")
	)
	flag.Parse()

	natsURL := *flagNATSURL
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	userID := *flagUserID
	projectID := *flagProjectID
	claudePath := *flagClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}
	natsCredsFile := *flagNATSCreds

	// Observability setup — must run before anything that emits spans/metrics.
	SetupPropagator()
	m := NewMetrics(prometheus.DefaultRegisterer)
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9091"
	}
	metricsSrv := StartMetricsServer(metricsAddr, prometheus.DefaultGatherer)
	defer metricsSrv.Shutdown(context.Background())

	nc, err := natsConnect(natsURL, natsCredsFile)
	if err != nil {
		log.Fatal().Err(err).Str("nats_url", natsURL).Msg("failed to connect to NATS")
	}
	defer nc.Close()

	agent, err := NewAgent(nc, userID, projectID, claudePath, log.Logger, m)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create agent")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Info().Str("user_id", userID).Str("project_id", projectID).Msg("session agent started")

	if err := agent.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("agent run failed")
	}

	log.Info().Msg("session agent stopped")
}

// natsConnect connects to NATS, using a credentials file if one is provided.
func natsConnect(natsURL, credsFile string) (*nats.Conn, error) {
	opts := []nats.Option{}
	if credsFile != "" {
		opts = append(opts, nats.UserCredentials(credsFile))
	}
	return nats.Connect(natsURL, opts...)
}
