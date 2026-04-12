// mclaude-cli: debug attach tool for mclaude session agents.
//
// Usage:
//
//	mclaude-cli attach <session-id> [flags]
//
// Flags:
//
//	--socket <path>      override default socket (/tmp/mclaude-session-{id}.sock)
//	--log-machine        machine-readable JSON logs on stderr
//	--log-level <level>  debug|info|warn|error  (default: info)
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
	"mclaude-cli/repl"
)

const (
	version    = "0.1.0"
	socketFmt  = "/tmp/mclaude-session-%s.sock"
)

func main() {
	os.Exit(run(os.Args))
}

// run is separated from main so tests can call it directly and capture the
// exit code without calling os.Exit.
func run(args []string) int {
	if len(args) < 3 || args[1] != "attach" {
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
