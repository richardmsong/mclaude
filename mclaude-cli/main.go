// mclaude-cli: debug attach tool, session manager, and host/cluster management
// for mclaude.
//
// Usage:
//
//	mclaude-cli attach <session-id> [flags]
//	mclaude-cli session list [-u <uslug>] [-p <pslug>] [--host <hslug>]
//	mclaude-cli host register [--name <name>]
//	mclaude-cli host list
//	mclaude-cli host use <hslug>
//	mclaude-cli host rm <hslug>
//	mclaude-cli cluster register --slug <cslug> [--name <display>] --jetstream-domain <jsd> --leaf-url <url> [--direct-nats-url <wss>]
//	mclaude-cli cluster grant <cluster-slug> <uslug>
//	mclaude-cli daemon [--host <hslug>]
//
// Subcommands:
//
//	attach           Connect to a running session agent via unix socket.
//	session list     List sessions for a project (uses ~/.mclaude/context.json defaults).
//	host register    Register this machine as a BYOH host (device-code flow).
//	host list        List all hosts the user owns or has access to.
//	host use         Set the active host.
//	host rm          Remove a host registration.
//	cluster register Register a new K8s worker cluster (admin-only).
//	cluster grant    Grant a user access to a cluster (admin-only).
//	daemon           Start the BYOH local controller daemon.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"mclaude-cli/cmd"
	"mclaude-cli/repl"
)

const (
	version   = "0.1.0"
	socketFmt = "/tmp/mclaude-session-%s.sock"
)

func main() {
	os.Exit(run(os.Args))
}

// run is separated from main so tests can call it directly and capture the
// exit code without calling os.Exit.
func run(args []string) int {
	if len(args) < 2 {
		printUsage()
		return 1
	}

	switch args[1] {
	case "attach":
		return runAttach(args)
	case "session":
		return runSession(args)
	case "host":
		return runHost(args)
	case "cluster":
		return runCluster(args)
	case "daemon":
		return runDaemon(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[1])
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: mclaude-cli <command> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  attach <session-id>         attach to a running session agent")
	fmt.Fprintln(os.Stderr, "    --socket <path>           unix socket path (default: /tmp/mclaude-session-{id}.sock)")
	fmt.Fprintln(os.Stderr, "    --log-machine             JSON log output to stderr")
	fmt.Fprintln(os.Stderr, "    --log-level <level>       debug|info|warn|error")
	fmt.Fprintln(os.Stderr, "  session list                list sessions for current project")
	fmt.Fprintln(os.Stderr, "    -u <uslug>                user slug (default: ~/.mclaude/context.json)")
	fmt.Fprintln(os.Stderr, "    -p <pslug>                project slug, accepts @pslug (default: context)")
	fmt.Fprintln(os.Stderr, "    --host <hslug>            host slug (default: context)")
	fmt.Fprintln(os.Stderr, "  host register               register this machine as a BYOH host")
	fmt.Fprintln(os.Stderr, "    --name <name>             display name (default: hostname)")
	fmt.Fprintln(os.Stderr, "  host list                   list all hosts")
	fmt.Fprintln(os.Stderr, "  host use <hslug>            set the active host")
	fmt.Fprintln(os.Stderr, "  host rm <hslug>             remove a host registration")
	fmt.Fprintln(os.Stderr, "  cluster register            register a K8s worker cluster (admin)")
	fmt.Fprintln(os.Stderr, "    --slug <cslug>            cluster slug (required)")
	fmt.Fprintln(os.Stderr, "    --name <display>          display name (default: slug)")
	fmt.Fprintln(os.Stderr, "    --jetstream-domain <jsd>  JetStream domain (required)")
	fmt.Fprintln(os.Stderr, "    --leaf-url <url>          leaf-node URL (required)")
	fmt.Fprintln(os.Stderr, "    --direct-nats-url <wss>   direct NATS WebSocket URL (optional)")
	fmt.Fprintln(os.Stderr, "  cluster grant <cslug> <u>   grant user access to a cluster (admin)")
	fmt.Fprintln(os.Stderr, "  daemon                      start BYOH local controller daemon")
	fmt.Fprintln(os.Stderr, "    --host <hslug>            host slug (default: ~/.mclaude/active-host)")
}

// runAttach handles the "attach" subcommand.
func runAttach(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli attach <session-id> [flags]")
		fmt.Fprintln(os.Stderr, "  --socket <path>      unix socket path")
		fmt.Fprintln(os.Stderr, "  --log-machine        JSON log output to stderr")
		fmt.Fprintln(os.Stderr, "  --log-level <level>  debug|info|warn|error")
		return 1
	}

	sessionID := args[2]
	socketPath := fmt.Sprintf(socketFmt, sessionID)
	logMachine := false
	logLevel := "info"

	for i := 3; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			i++
			if i < len(args) {
				socketPath = args[i]
			}
		case "--log-machine":
			logMachine = true
		case "--log-level":
			i++
			if i < len(args) {
				logLevel = args[i]
			}
		}
	}

	level, err := zerolog.ParseLevel(logLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	if logMachine {
		log.Logger = zerolog.New(os.Stderr).With().
			Str("component", "mclaude-cli").
			Str("sessionId", sessionID).
			Timestamp().
			Logger()
	} else {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().
			Str("component", "mclaude-cli").
			Str("sessionId", sessionID).
			Logger()
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Error().Err(err).Str("socket", socketPath).Msg("connect failed")
		return 1
	}
	defer conn.Close()

	log.Info().Str("socket", socketPath).Str("version", version).Msg("attached")

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Info().Msg("interrupted")
		cancel()
	}()

	cfg := repl.Config{
		SessionID: sessionID,
		Input:     os.Stdin,
		Output:    os.Stdout,
		Log:       log.Logger,
	}

	if err := repl.Run(ctx, conn, cfg); err != nil {
		if err != context.Canceled {
			log.Error().Err(err).Msg("REPL error")
			return 1
		}
	}
	return 0
}

// runSession handles the "session" subcommand and its sub-subcommands.
func runSession(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli session <subcommand>")
		fmt.Fprintln(os.Stderr, "  list   list sessions for current project")
		return 1
	}

	switch args[2] {
	case "list":
		return runSessionList(args[3:])
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n", args[2])
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli session list [-u <uslug>] [-p <pslug>] [--host <hslug>]")
		return 1
	}
}

// runSessionList handles "mclaude-cli session list".
func runSessionList(args []string) int {
	flags := cmd.SessionListFlags{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-u":
			i++
			if i < len(args) {
				flags.UserSlug = args[i]
			}
		case "-p":
			i++
			if i < len(args) {
				flags.ProjectSlug = args[i]
			}
		case "--host":
			i++
			if i < len(args) {
				flags.HostSlug = args[i]
			}
		}
	}

	if _, err := cmd.RunSessionList(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// --------------------------------------------------------------------------
// host subcommand
// --------------------------------------------------------------------------

func runHost(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli host <subcommand>")
		fmt.Fprintln(os.Stderr, "  register   register this machine as a BYOH host")
		fmt.Fprintln(os.Stderr, "  list       list all hosts")
		fmt.Fprintln(os.Stderr, "  use        set the active host")
		fmt.Fprintln(os.Stderr, "  rm         remove a host registration")
		return 1
	}

	switch args[2] {
	case "register":
		return runHostRegister(args[3:])
	case "list":
		return runHostList(args[3:])
	case "use":
		return runHostUse(args[3:])
	case "rm":
		return runHostRm(args[3:])
	default:
		fmt.Fprintf(os.Stderr, "unknown host subcommand: %s\n", args[2])
		return 1
	}
}

func runHostRegister(args []string) int {
	flags := cmd.HostRegisterFlags{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i < len(args) {
				flags.Name = args[i]
			}
		}
	}

	if _, err := cmd.RunHostRegister(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runHostList(args []string) int {
	flags := cmd.HostListFlags{}
	// No flags to parse currently; reserved for future use.
	_ = args

	if err := cmd.RunHostList(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runHostUse(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli host use <hslug>")
		return 1
	}
	hslug := args[0]

	if err := cmd.RunHostUse(hslug, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runHostRm(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli host rm <hslug>")
		return 1
	}
	hslug := args[0]
	flags := cmd.HostRmFlags{}

	if err := cmd.RunHostRm(hslug, flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// --------------------------------------------------------------------------
// cluster subcommand
// --------------------------------------------------------------------------

func runCluster(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli cluster <subcommand>")
		fmt.Fprintln(os.Stderr, "  register   register a K8s worker cluster (admin-only)")
		fmt.Fprintln(os.Stderr, "  grant      grant user access to a cluster (admin-only)")
		return 1
	}

	switch args[2] {
	case "register":
		return runClusterRegister(args[3:])
	case "grant":
		return runClusterGrant(args[3:])
	default:
		fmt.Fprintf(os.Stderr, "unknown cluster subcommand: %s\n", args[2])
		return 1
	}
}

func runClusterRegister(args []string) int {
	flags := cmd.ClusterRegisterFlags{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--slug":
			i++
			if i < len(args) {
				flags.Slug = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				flags.Name = args[i]
			}
		case "--jetstream-domain":
			i++
			if i < len(args) {
				flags.JetStreamDomain = args[i]
			}
		case "--leaf-url":
			i++
			if i < len(args) {
				flags.LeafURL = args[i]
			}
		case "--direct-nats-url":
			i++
			if i < len(args) {
				flags.DirectNatsURL = args[i]
			}
		}
	}

	if _, err := cmd.RunClusterRegister(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runClusterGrant(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli cluster grant <cluster-slug> <uslug>")
		return 1
	}
	clusterSlug := args[0]
	userSlug := args[1]

	flags := cmd.ClusterGrantFlags{}

	if err := cmd.RunClusterGrant(clusterSlug, userSlug, flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// --------------------------------------------------------------------------
// daemon subcommand
// --------------------------------------------------------------------------

func runDaemon(args []string) int {
	flags := cmd.DaemonFlags{}

	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			if i < len(args) {
				flags.HostSlug = args[i]
			}
		}
	}

	cfg, err := cmd.ResolveDaemonConfig(flags, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// TODO(stage7): Start the actual daemon loop:
	// 1. Connect to hub NATS using cfg.CredsPath
	// 2. Subscribe to mclaude.users.{uslug}.hosts.{hslug}.api.projects.>
	// 3. Start session-agent subprocesses for each provisioned project
	// This will be implemented in mclaude-controller-local.
	fmt.Fprintf(os.Stdout, "\nDaemon for %s/%s would start here.\n", cfg.UserSlug, cfg.HostSlug)
	fmt.Fprintln(os.Stdout, "The actual NATS connection and process supervision loop")
	fmt.Fprintln(os.Stdout, "will be implemented in mclaude-controller-local.")

	return 0
}
