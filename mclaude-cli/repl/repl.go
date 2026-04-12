// Package repl implements the interactive text REPL for mclaude-cli.
// It is designed for testability: I/O is injected, not hard-coded to os.Stdin/Stdout.
package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
	"mclaude-cli/events"
	"mclaude-cli/renderer"
)

// isConnClosedErr reports whether err is the expected "use of closed network
// connection" sentinel that the Go net package emits when we close a socket
// ourselves while another goroutine is blocked on Read.
func isConnClosedErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "use of closed network connection")
}

// Config holds injectable dependencies for the REPL.
type Config struct {
	SessionID string
	Input     io.Reader // user input (os.Stdin in production)
	Output    io.Writer // display (os.Stdout in production)
	Log       zerolog.Logger
}

// pendingPerm is a queued permission request waiting for user y/n.
type pendingPerm struct {
	requestID string
	toolName  string
}

// Run connects to conn (a unix socket to the session agent) and drives the
// interactive REPL until ctx is cancelled, the socket closes, or Input reaches EOF.
//
// Events from the socket are rendered to Output.  User lines typed to Input are
// sent as user messages, unless a permission prompt is pending in which case
// "y"/"n" are sent as control_response.
func Run(ctx context.Context, conn net.Conn, cfg Config) error {
	rend := renderer.New(cfg.Output)
	log := cfg.Log

	var perm atomic.Pointer[pendingPerm]
	var wg sync.WaitGroup
	socketErrCh := make(chan error, 1)

	fmt.Fprintf(cfg.Output, "[session %s]\n> ", cfg.SessionID)

	// Goroutine: read events from the socket, render them.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0), 1<<20)
		for scanner.Scan() {
			evt, err := events.Parse(scanner.Bytes())
			if err != nil {
				log.Debug().Err(err).Msg("parse event")
				continue
			}
			log.Debug().Str("type", evt.Type).Str("subtype", evt.Subtype).Msg("recv")
			rend.Render(evt)

			if evt.IsPermissionRequest() {
				perm.Store(&pendingPerm{
					requestID: evt.RequestID,
					toolName:  evt.Request.ToolName,
				})
			}

			// When the session agent delivers tool results as a user message,
			// render them immediately.
			for _, b := range evt.ToolResultBlocks() {
				rend.RenderToolResult(b.ToolUseID, b.Content, b.IsError)
			}
		}
		if err := scanner.Err(); err != nil && !isConnClosedErr(err) {
			select {
			case socketErrCh <- err:
			default:
			}
		}
	}()

	// Main goroutine: read user input and write to the socket.
	inputScanner := bufio.NewScanner(cfg.Input)
	for {
		select {
		case <-ctx.Done():
			conn.Close()
			wg.Wait()
			return ctx.Err()
		default:
		}

		if !inputScanner.Scan() {
			break
		}
		line := strings.TrimSpace(inputScanner.Text())

		if line == "" {
			fmt.Fprint(cfg.Output, "> ")
			continue
		}

		// If a permission is pending, treat this line as y/n.
		if p := perm.Swap(nil); p != nil {
			approved := strings.ToLower(line) == "y"
			if err := sendControlResponse(conn, p.requestID, approved); err != nil {
				log.Error().Err(err).Msg("send control_response")
				return err
			}
			log.Info().Str("requestId", p.requestID).Bool("approved", approved).Msg("permission")
			fmt.Fprint(cfg.Output, "> ")
			continue
		}

		if err := sendUserMessage(conn, line); err != nil {
			log.Error().Err(err).Msg("send user message")
			return err
		}
		log.Debug().Str("text", line).Msg("sent")
		// Don't re-print "> " here; it will appear after events are rendered.
	}

	// Input reached EOF.  Half-close the write side so the server knows no more
	// messages are coming, but keep the read side open so the event goroutine
	// can drain any buffered events the server is still sending.  When the
	// server sees our write-EOF it closes its side, which gives the event
	// goroutine a clean EOF and lets wg.Wait() return.
	//
	// If the connection does not support half-close (should not happen for unix
	// sockets) fall back to a full close.
	type halfCloser interface{ CloseWrite() error }
	if hc, ok := conn.(halfCloser); ok {
		hc.CloseWrite() //nolint:errcheck
	} else {
		conn.Close()
	}
	wg.Wait()

	select {
	case err := <-socketErrCh:
		return err
	default:
		return inputScanner.Err()
	}
}

// ---------- wire format helpers ----------

type userMsg struct {
	Type    string      `json:"type"`
	Message userMsgBody `json:"message"`
}

type userMsgBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func sendUserMessage(conn net.Conn, text string) error {
	data, err := json.Marshal(userMsg{
		Type:    "user",
		Message: userMsgBody{Role: "user", Content: text},
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(conn, "%s\n", data)
	return err
}

type controlResponseMsg struct {
	Type     string               `json:"type"`
	Response controlResponseInner `json:"response"`
}

type controlResponseInner struct {
	Subtype   string              `json:"subtype"`
	RequestID string              `json:"request_id"`
	Response  controlResponseBody `json:"response"`
}

type controlResponseBody struct {
	Behavior string `json:"behavior"` // "allow" | "deny"
}

func sendControlResponse(conn net.Conn, requestID string, approved bool) error {
	behavior := "deny"
	if approved {
		behavior = "allow"
	}
	data, err := json.Marshal(controlResponseMsg{
		Type: "control_response",
		Response: controlResponseInner{
			Subtype:   "success",
			RequestID: requestID,
			Response:  controlResponseBody{Behavior: behavior},
		},
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(conn, "%s\n", data)
	return err
}
