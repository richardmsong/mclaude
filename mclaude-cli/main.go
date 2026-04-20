// mclaude-cli: debug attach tool and session manager for mclaude.
//
// Usage:
//
//	mclaude-cli attach <session-id> [flags]
//	mclaude-cli session list [-u <uslug>] [-p <pslug>]
//
// Subcommands:
//
//	attach       Connect to a running session agent via unix socket.
//	session list List sessions for a project (uses ~/.mclaude/context.json defaults).
//
// Flags for attach:
//
//	--socket <path>      override default socket (/tmp/mclaude-session-{id}.sock)
//	--log-machine        machine-readable JSON logs on stderr
//	--log-level <level>  debug|info|warn|error  (default: info)
//
// Flags for session list:
//
//	-u <uslug>   user slug (default: context file)
//	-p <pslug>   project slug, accepts @pslug short form (default: context file)
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
		fmt.Fprintln(os.Stderr, "Usage: mclaude-cli session list [-u <uslug>] [-p <pslug>]")
		return 1
	}
}

// runSessionList handles "mclaude-cli session list".
// Reads context defaults; accepts -u and -p slug flags with validation.
// The -p flag accepts @pslug short form per ADR-0024.
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
		}
	}

	if _, err := cmd.RunSessionList(flags, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
