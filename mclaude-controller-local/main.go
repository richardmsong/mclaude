package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"mclaude.io/common/pkg/hostauth"
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
		flagHost     = flag.String("host", os.Getenv("HOST_SLUG"), "Host slug (hslug) per ADR-0054 (required)")
		flagUserSlug = flag.String("user-slug", os.Getenv("USER_SLUG"), "Host owner's user slug (uslug) — informational; per-message user slug extracted from NATS subject (ADR-0054)")
		flagHubURL   = flag.String("hub-url", os.Getenv("HUB_URL"), "Hub NATS WebSocket URL (required)")
		flagCPURL    = flag.String("cp-url", os.Getenv("CP_URL"), "Control-plane HTTP base URL for host JWT refresh and agent NKey auth (e.g. https://cp.example.com). If empty, host JWT refresh is disabled.")
		flagCreds    = flag.String("creds-file", os.Getenv("NATS_CREDS_FILE"), "Path to NATS host credentials file (default ~/.mclaude/hosts/{hslug}/nats.creds). ADR-0054 host-scoped JWT (zero JetStream).")
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

	// Log owner user slug if provided (informational only — per-message user slugs
	// come from NATS subjects under mclaude.hosts.{hslug}.users.{uslug}.*).
	if userSlugStr := *flagUserSlug; userSlugStr != "" {
		if err := slug.Validate(userSlugStr); err != nil {
			log.Warn().Err(err).Str("user", userSlugStr).Msg("invalid owner user slug — ignoring")
		} else {
			log.Info().Str("owner_user_slug", userSlugStr).Msg("host owner user slug")
		}
	}

	// Hub NATS URL.
	hubURL := *flagHubURL
	if hubURL == "" {
		log.Fatal().Msg("FATAL: HUB_URL required (set via --hub-url flag or HUB_URL env)")
	}

	// CP HTTP base URL (optional — if empty, host JWT refresh and agent
	// NKey authentication are disabled).
	cpURL := *flagCPURL

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

	// Deprecation notice: mclaude-session-agent --daemon mode is superseded by
	// mclaude-controller-local + per-project-agent architecture (ADR-0058 Phase 1).
	// The --daemon mode will stop working entirely when ADR-0054 permission
	// tightening is deployed (Phase 2). Users with launchd/systemd units pointing
	// at mclaude-session-agent --daemon should migrate to mclaude-controller-local.
	log.Info().Msg("NOTICE: mclaude-session-agent --daemon mode is deprecated (ADR-0058). " +
		"mclaude-controller-local is now the BYOH process supervisor. " +
		"The --daemon mode will stop working when ADR-0054 host JWT tightening is deployed.")

	// Connect to hub NATS.
	// If CP URL is provided, use HostAuth for dynamic JWT refresh (ADR-0054).
	// Otherwise fall back to static creds file.
	var natsOpts []nats.Option
	var ha *hostauth.HostAuth

	if cpURL != "" {
		// Load the NKey seed and current JWT from the .creds file.
		// HostAuth will proactively refresh the JWT before the 5-minute TTL expires.
		var haErr error
		ha, haErr = hostauth.NewHostAuthFromCredsFile(credsFile, cpURL, log.Logger)
		if haErr != nil {
			log.Fatal().Err(haErr).Str("creds_file", credsFile).Msg("failed to load credentials")
		}
		natsOpts = append(natsOpts, nats.UserJWT(ha.JWTFunc(), ha.SignFunc()))

		// On NATS permissions violation, trigger an immediate JWT refresh (ADR-0054).
		// This handles the edge case where the host's cached JWT predates a permission change.
		natsOpts = append(natsOpts, nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			if err != nil && isPermissionsViolation(err) {
				log.Warn().Err(err).Msg("NATS permissions violation — triggering immediate JWT refresh")
				if _, refreshErr := ha.Refresh(context.Background()); refreshErr != nil {
					log.Warn().Err(refreshErr).Msg("immediate JWT refresh failed")
				} else {
					log.Info().Msg("JWT refreshed after permissions violation")
				}
			}
		}))
		log.Info().Str("cp_url", cpURL).Msg("host JWT refresh enabled via HTTP challenge-response")
	} else {
		// Static creds file — no JWT refresh. Host JWT expires after 5 minutes
		// if no CP URL is configured.
		if credsFile != "" {
			natsOpts = append(natsOpts, nats.UserCredentials(credsFile))
		}
		log.Warn().Msg("CP URL not configured — host JWT refresh disabled. JWT will expire in ~5 minutes.")
	}

	nc, err := nats.Connect(hubURL, natsOpts...)
	if err != nil {
		log.Fatal().Err(err).Str("hub_url", hubURL).Msg("failed to connect to NATS")
	}
	defer nc.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start host JWT refresh loop (proactive, before 5-minute TTL expiry).
	if ha != nil {
		ha.StartRefreshLoop(ctx)
	}

	ctrl := NewController(nc, hostSlug, dataDir, cpURL, log.Logger)

	log.Info().
		Str("host_slug", hostSlugStr).
		Str("hub_url", hubURL).
		Str("data_dir", dataDir).
		Bool("jwt_refresh", cpURL != "").
		Msg("controller-local started (ADR-0054 host-scoped subject scheme)")

	if err := ctrl.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("controller run failed")
	}
	log.Info().Msg("controller-local stopped")
}

// isPermissionsViolation returns true if the NATS error is a permissions violation.
// NATS surfaces these as errors containing "permissions violation" or
// "Authorization Violation" in the message.
func isPermissionsViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permissions violation") ||
		strings.Contains(msg, "authorization violation")
}
