package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"mclaude.io/common/pkg/slug"
)

func main() {
	level, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stderr).With().
		Str("component", "controller-local").
		Timestamp().
		Logger()

	var (
		flagHost     = flag.String("host", os.Getenv("HOST_SLUG"), "Host slug (hslug) per ADR-0035 (required)")
		flagUserSlug = flag.String("user-slug", os.Getenv("USER_SLUG"), "User slug (uslug) per ADR-0024 (required)")
		flagHubURL   = flag.String("hub-url", os.Getenv("HUB_URL"), "Hub NATS WebSocket URL (required)")
		flagCreds    = flag.String("creds-file", os.Getenv("NATS_CREDS_FILE"), "Path to NATS creds file (default ~/.mclaude/hosts/{hslug}/nats.creds)")
		flagDataDir  = flag.String("data-dir", os.Getenv("DATA_DIR"), "Root for per-project worktrees (default ~/.mclaude/projects/)")
	)
	flag.Parse()

	// Resolve host slug: flag → env → active-host symlink.
	hostSlugStr := *flagHost
	if hostSlugStr == "" {
		home, _ := os.UserHomeDir()
		link := filepath.Join(home, ".mclaude", "active-host")
		if target, linkErr := os.Readlink(link); linkErr == nil {
			hostSlugStr = filepath.Base(target)
		}
	}
	if hostSlugStr == "" {
		log.Fatal().Msg("FATAL: HOST_SLUG required (set via --host flag, HOST_SLUG env, or ~/.mclaude/active-host symlink)")
	}
	if err := slug.Validate(hostSlugStr); err != nil {
		log.Fatal().Err(err).Str("host", hostSlugStr).Msg("invalid host slug")
	}
	hostSlug := slug.HostSlug(hostSlugStr)

	// Resolve user slug.
	userSlugStr := *flagUserSlug
	if userSlugStr == "" {
		log.Fatal().Msg("FATAL: USER_SLUG required (set via --user-slug flag or USER_SLUG env)")
	}
	if err := slug.Validate(userSlugStr); err != nil {
		log.Fatal().Err(err).Str("user", userSlugStr).Msg("invalid user slug")
	}
	userSlug := slug.UserSlug(userSlugStr)

	// Hub NATS URL.
	hubURL := *flagHubURL
	if hubURL == "" {
		log.Fatal().Msg("FATAL: HUB_URL required (set via --hub-url flag or HUB_URL env)")
	}

	// Default creds-file: ~/.mclaude/hosts/{hslug}/nats.creds
	credsFile := *flagCreds
	if credsFile == "" {
		home, _ := os.UserHomeDir()
		credsFile = filepath.Join(home, ".mclaude", "hosts", hostSlugStr, "nats.creds")
	}

	// Default data-dir: ~/.mclaude/projects/
	dataDir := *flagDataDir
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".mclaude", "projects")
	}

	// Connect to hub NATS with per-host user JWT credentials.
	var natsOpts []nats.Option
	if credsFile != "" {
		natsOpts = append(natsOpts, nats.UserCredentials(credsFile))
	}
	nc, err := nats.Connect(hubURL, natsOpts...)
	if err != nil {
		log.Fatal().Err(err).Str("hub_url", hubURL).Msg("failed to connect to NATS")
	}
	defer nc.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctrl := NewController(nc, userSlug, hostSlug, dataDir, log.Logger)

	log.Info().
		Str("user_slug", userSlugStr).
		Str("host_slug", hostSlugStr).
		Str("hub_url", hubURL).
		Str("data_dir", dataDir).
		Msg("controller-local started")

	if err := ctrl.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("controller run failed")
	}
	log.Info().Msg("controller-local stopped")
}
