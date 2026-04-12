package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"github.com/nats-io/nats.go"
)

// TerminalSession manages a PTY-based terminal routed through NATS.
// Raw I/O flows between the PTY and NATS subjects:
//
//	mclaude.{userId}.{projectId}.terminal.{termID}.output → PTY stdout → subscribers
//	mclaude.{userId}.{projectId}.terminal.{termID}.input  → PTY stdin ← publishers
type TerminalSession struct {
	mu          sync.Mutex
	id          string
	ptmx        *os.File
	cmd         *exec.Cmd
	unsubscribe func()
	doneCh      chan struct{}
}

// termPubSub is the subset of NATS operations needed by a terminal session.
// *nats.Conn satisfies this through NATSTermPubSub.
type termPubSub struct {
	publish   func(subject string, data []byte) error
	subscribe func(subject string, handler func(data []byte)) (unsubscribe func(), err error)
}

// NATSTermPubSub wraps a *nats.Conn into the termPubSub callback pair
// needed by startTerminal.
func NATSTermPubSub(nc *nats.Conn) termPubSub {
	return termPubSub{
		publish: nc.Publish,
		subscribe: func(subject string, handler func(data []byte)) (func(), error) {
			sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
				handler(msg.Data)
			})
			if err != nil {
				return nil, err
			}
			return func() { _ = sub.Unsubscribe() }, nil
		},
	}
}

// startTerminal spawns a shell in a PTY and bridges it to NATS.
// shell is typically "/bin/bash" or "/bin/sh".
func startTerminal(id, shell string, tr termPubSub, userID, projectID string) (*TerminalSession, error) {
	cmd := exec.Command(shell)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start PTY: %w", err)
	}

	ts := &TerminalSession{
		id:     id,
		ptmx:   ptmx,
		cmd:    cmd,
		doneCh: make(chan struct{}),
	}

	outputSubject := fmt.Sprintf("mclaude.%s.%s.terminal.%s.output", userID, projectID, id)
	inputSubject := fmt.Sprintf("mclaude.%s.%s.terminal.%s.input", userID, projectID, id)

	// PTY output → NATS
	go func() {
		defer close(ts.doneCh)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				_ = tr.publish(outputSubject, chunk)
			}
			if err != nil {
				if err != io.EOF {
					_ = err // PTY closed — normal on shell exit
				}
				return
			}
		}
	}()

	// NATS input → PTY
	unsub, err := tr.subscribe(inputSubject, func(data []byte) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		_, _ = ptmx.Write(data)
	})
	if err != nil {
		ptmx.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("subscribe terminal input: %w", err)
	}
	ts.unsubscribe = unsub

	return ts, nil
}

// resize sends a window size change to the PTY.
func (ts *TerminalSession) resize(rows, cols uint16) error {
	return pty.Setsize(ts.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// stop terminates the shell and cleans up.
func (ts *TerminalSession) stop() {
	if ts.unsubscribe != nil {
		ts.unsubscribe()
	}
	ts.mu.Lock()
	ts.ptmx.Close()
	ts.mu.Unlock()
	// Kill the process explicitly — closing ptmx does not reliably send
	// SIGHUP on all platforms, so cmd.Wait() could block indefinitely.
	if ts.cmd.Process != nil {
		_ = ts.cmd.Process.Kill()
	}
	_ = ts.cmd.Wait()
}

// waitDone blocks until the PTY session ends.
func (ts *TerminalSession) waitDone() {
	<-ts.doneCh
}
