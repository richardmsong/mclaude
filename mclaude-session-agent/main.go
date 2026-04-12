package main

import (
	"context"
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

	// Observability setup — must run before anything that emits spans/metrics.
	SetupPropagator()
	m := NewMetrics(prometheus.DefaultRegisterer)
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9091"
	}
	metricsSrv := StartMetricsServer(metricsAddr, prometheus.DefaultGatherer)
	defer metricsSrv.Shutdown(context.Background())

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	userID := os.Getenv("USER_ID")
	projectID := os.Getenv("PROJECT_ID")
	claudePath := os.Getenv("CLAUDE_PATH")
	if claudePath == "" {
		claudePath = "claude"
	}

	nc, err := nats.Connect(natsURL)
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
