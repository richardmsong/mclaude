// mclaude-cli: debug attach tool, session manager, host management,
// and session import for mclaude.
//
// Usage:
//
//	mclaude-cli attach <session-id> [flags]
//	mclaude-cli session list [-u <uslug>] [-p <pslug>] [--host <hslug>]
//	mclaude-cli login [--server <url>]
//	mclaude-cli import [--host <hslug>] [--server <url>]
//	mclaude-cli auth anthropic [--token <v>] [--status] [--revoke]
//	mclaude-cli host register --name <name> --nkey-public <key> [--default-platform]
//	mclaude-cli host list
//	mclaude-cli host use <hslug>
//	mclaude-cli host rm <hslug>
//
// Subcommands:
//
//	attach           Connect to a running session agent via unix socket.
//	session list     List sessions for a project (uses ~/.mclaude/context.json defaults).
//	login            Authenticate via device-code flow; stores credentials in ~/.mclaude/auth.json.
//	import           Import existing Claude Code session data into mclaude.
//	auth anthropic   Connect Anthropic OAuth token; deliver to trusted hosts (ADR-0091).
//	host register    Register a host via NATS (--nkey-public required).
//	host list        List all hosts the user owns or has access to.
//	host use         Set the active host.
//	host rm          Remove a host registration.
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
	case "login":
		return runLogin(args)
	case "import":
		return runImport(args)
	case "auth":
		return runAuth(args)
	case "host":
		return runHost(args)
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
	fmt.Fprintln(os.Stderr, "  login                       authenticate via device-code flow")
	fmt.Fprintln(os.Stderr, "    --server <url>            control-plane base URL (default: context or https://api.mclaude.internal)")
	fmt.Fprintln(os.Stderr, "  import                      import existing Claude Code session data")
	fmt.Fprintln(os.Stderr, "    --host <hslug>            host slug (default: ~/.mclaude/active-host)")
	fmt.Fprintln(os.Stderr, "    --server <url>            control-plane base URL (default: context or https://api.mclaude.internal)")
	fmt.Fprintln(os.Stderr, "  auth anthropic              connect Anthropic OAuth token (ADR-0091)")
	fmt.Fprintln(os.Stderr, "    --token <v>              manual token paste (skip OAuth)")
	fmt.Fprintln(os.Stderr, "    --status                 print connected/not-set + per-host pubkey status")
	fmt.Fprintln(os.Stderr, "    --revoke                 delete tokens.anthropic from KV (client-side only)")
	fmt.Fprintln(os.Stderr, "  host register               register a host with the control-plane via NATS")
	fmt.Fprintln(os.Stderr, "    --name <name>             display name (required)")
	fmt.Fprintln(os.Stderr, "    --nkey-public <key>       NKey public key (required, U-prefix, 56-char base32)")
	fmt.Fprintln(os.Stderr, "    --default-platform        set this host as default platform (admin-only)")
	fmt.Fprintln(os.Stderr, "  host list                   list all hosts")
	fmt.Fprintln(os.Stderr, "  host use <hslug>            set the active host")
	fmt.Fprintln(os.Stderr, "  host rm <hslug>             remove a host registration")
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

// runLogin handles the "login" subcommand.
func runLogin(args []string) int {
	flags := cmd.LoginFlags{}

	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--server":
			i++
			if i < len(args) {
				flags.ServerURL = args[i]
			}
		}
	}

	if _, err := cmd.RunLogin(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runImport handles the "import" subcommand.
func runImport(args []string) int {
	flags := cmd.ImportFlags{}

	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			if i < len(args) {
				flags.HostSlug = args[i]
			}
		case "--server":
			i++
			if i < len(args) {
				flags.ServerURL = args[i]
			}
		}
	}

	if _, err := cmd.RunImport(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runAuth handles the "auth" subcommand and its sub-subcommands.
func runAuth(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli auth <provider>")
		fmt.Fprintln(os.Stderr, "  anthropic   connect Anthropic OAuth token")
		return 1
	}
	switch args[2] {
	case "anthropic":
		return runAuthAnthropic(args[3:])
	default:
		fmt.Fprintf(os.Stderr, "unknown auth provider: %s\n", args[2])
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli auth anthropic [--token <v>] [--status] [--revoke]")
		return 1
	}
}

// runAuthAnthropic handles "mclaude auth anthropic".
func runAuthAnthropic(args []string) int {
	flags := cmd.AuthAnthropicFlags{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--token":
			i++
			if i < len(args) {
				flags.Token = args[i]
			}
		case "--status":
			flags.Status = true
		case "--revoke":
			flags.Revoke = true
		case "--server":
			i++
			if i < len(args) {
				flags.ServerURL = args[i]
			}
		}
	}

	result, err := cmd.RunAuthAnthropic(flags, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		// Return the exit code from the result if available.
		if result != nil {
			return result.ExitCode
		}
		return cmd.ExitAuthNoToken
	}
	return result.ExitCode
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
		fmt.Fprintln(os.Stderr, "  register   register a host with the control-plane via NATS")
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
		case "--nkey-public":
			i++
			if i < len(args) {
				flags.NKeyPublic = args[i]
			}
		case "--default-platform":
			flags.DefaultPlatform = true
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

