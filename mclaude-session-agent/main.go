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

	// Health / readiness probe mode.
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--health":
			os.Exit(0)
		case "--ready":
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

	// CLI flags.
	var (
		flagNATSURL    = flag.String("nats-url", os.Getenv("NATS_URL"), "NATS server URL")
		flagNATSCreds  = flag.String("nats-creds", os.Getenv("NATS_CREDS_FILE"), "Path to NATS credentials file")
		flagUserID     = flag.String("user-id", os.Getenv("USER_ID"), "User ID (required)")
		flagProjectID  = flag.String("project-id", os.Getenv("PROJECT_ID"), "Project ID (required in standalone mode)")
		flagClaudePath = flag.String("claude-path", os.Getenv("CLAUDE_PATH"), "Path to claude binary")
		flagDataDir    = flag.String("data-dir", "/data", "Data directory (project PVC mount: repo + worktrees)")
		flagMode       = flag.String("mode", "standalone", "Run mode: k8s | standalone")
		flagDaemon     = flag.Bool("daemon", false, "Run as laptop daemon launcher (spawns one child agent per project)")
		flagHostname   = flag.String("hostname", os.Getenv("HOSTNAME"), "Hostname for laptop collision detection (--daemon only)")
		flagMachineID  = flag.String("machine-id", os.Getenv("MACHINE_ID"), "Machine ID for laptop collision detection (--daemon only)")
		flagRefreshURL = flag.String("refresh-url", os.Getenv("REFRESH_URL"), "POST /auth/refresh URL for JWT refresh (--daemon only)")
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
	dataDir := *flagDataDir
	_ = *flagMode

	// Observability.
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *flagDaemon {
		// Laptop daemon mode: spawn one session-agent child per project.
		hostname := *flagHostname
		if hostname == "" {
			h, _ := os.Hostname()
			hostname = h
		}
		machineID := *flagMachineID
		if machineID == "" {
			// Use hostname as a fallback machine ID when not explicitly set.
			machineID = hostname
		}

		cfg := DaemonConfig{
			NATSCredsFile: natsCredsFile,
			RefreshURL:    *flagRefreshURL,
			UserID:        userID,
			Hostname:      hostname,
			MachineID:     machineID,
			AgentBinary:   os.Args[0],
			// Pass through all flags except --daemon itself so children get the
			// same NATS config, claude path, data dir, etc.
			AgentArgs: []string{
				"--nats-url", natsURL,
				"--nats-creds", natsCredsFile,
				"--claude-path", claudePath,
				"--data-dir", dataDir,
			},
			Log: log.Logger,
		}

		daemon, err := NewDaemon(nc, cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create daemon")
		}

		log.Info().Str("user_id", userID).Str("hostname", hostname).Msg("laptop daemon started")
		if err := daemon.Run(ctx); err != nil {
			log.Fatal().Err(err).Msg("daemon run failed")
		}
		log.Info().Msg("laptop daemon stopped")
		return
	}

	// Standalone (K8s or single-project laptop) mode.
	if projectID == "" {
		log.Fatal().Msg("--project-id is required in standalone mode")
	}

	agent, err := NewAgent(nc, userID, projectID, claudePath, dataDir, log.Logger, m)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create agent")
	}

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
