package main

import (
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// childRestartDelay is the wait between a child crash and restart.
	childRestartDelay = 2 * time.Second
	// shutdownGracePeriod is how long to wait after SIGINT before SIGKILL.
	shutdownGracePeriod = 10 * time.Second
)

// supervisedChild tracks a running session-agent subprocess managed by the
// controller's process supervisor.
type supervisedChild struct {
	projectSlug string
	userSlug    string
	dataDir     string
	hostSlug    string
	authURL     string // CP HTTP URL for agent HTTP challenge-response auth (ADR-0054)
	nkeyPubFile string // path where the agent writes its NKey public key (IPC, ADR-0058)

	mu     sync.Mutex
	cmd    *exec.Cmd
	stopCh chan struct{}
	doneCh chan struct{}
	log    zerolog.Logger
}

// startChild creates and starts a supervised session-agent subprocess for
// the given project. The child process is automatically restarted on crash
// with a 2-second delay.
//
// pslug and uslug are the project and user slugs for this child. dataDir is
// the agent's working directory (worktree path). nkeyPubFile is the file
// path where the agent will write its NKey public key for IPC (ADR-0058).
func (c *Controller) startChild(pslug, uslug, dataDir, nkeyPubFile string) *supervisedChild {
	child := &supervisedChild{
		projectSlug: pslug,
		userSlug:    uslug,
		dataDir:     dataDir,
		hostSlug:    string(c.hostSlug),
		authURL:     c.cpURL,
		nkeyPubFile: nkeyPubFile,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		log:         c.log.With().Str("project", pslug).Str("user", uslug).Logger(),
	}
	go child.run()
	return child
}

// run manages the child process lifecycle with restart-on-crash.
func (s *supervisedChild) run() {
	defer close(s.doneCh)

	for {
		select {
		case <-s.stopCh:
			s.log.Info().Msg("supervisor: stop requested")
			return
		default:
		}

		cmd := s.buildCmd()
		s.mu.Lock()
		s.cmd = cmd
		s.mu.Unlock()

		s.log.Info().Msg("supervisor: starting session-agent")
		if err := cmd.Start(); err != nil {
			s.log.Error().Err(err).Msg("supervisor: session-agent start failed")
		} else {
			if err := cmd.Wait(); err != nil {
				s.log.Warn().Err(err).Msg("supervisor: session-agent crashed — restarting")
			} else {
				s.log.Info().Msg("supervisor: session-agent exited cleanly")
			}
		}

		s.mu.Lock()
		s.cmd = nil
		s.mu.Unlock()

		// Wait before restarting, or exit if stop was requested.
		select {
		case <-s.stopCh:
			return
		case <-time.After(childRestartDelay):
		}
	}
}

// buildCmd constructs the exec.Cmd for the session-agent subprocess.
//
// Per ADR-0054/0058, the agent is started in standalone mode with:
//   - --user-slug, --host (host slug), --project-slug for identity
//   - --data-dir pointing to the project worktree
//   - --nkey-pub-file so the agent writes its NKey public key for IPC
//   - --auth-url (CP URL) so the agent can authenticate via HTTP challenge-response
//
// The agent generates its own NKey pair at startup, writes the public key to
// nkeyPubFile, then authenticates to CP using the HTTP challenge-response flow
// once the host controller has registered the key.
func (s *supervisedChild) buildCmd() *exec.Cmd {
	args := []string{
		"--mode", "standalone",
		"--user-slug", s.userSlug,
		"--host", s.hostSlug,
		"--project-slug", s.projectSlug,
		"--data-dir", s.dataDir,
	}

	// Pass the NKey public key file path so the agent can write it for IPC.
	if s.nkeyPubFile != "" {
		args = append(args, "--nkey-pub-file", s.nkeyPubFile)
	}

	// Pass the CP URL so the agent can authenticate via HTTP challenge-response.
	if s.authURL != "" {
		args = append(args, "--auth-url", s.authURL)
	}

	cmd := exec.Command("mclaude-session-agent", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"USER_SLUG="+s.userSlug,
		"HOST_SLUG="+s.hostSlug,
		"PROJECT_SLUG="+s.projectSlug,
	)
	if s.nkeyPubFile != "" {
		cmd.Env = append(cmd.Env, "NKEY_PUB_FILE="+s.nkeyPubFile)
	}
	if s.authURL != "" {
		cmd.Env = append(cmd.Env, "AUTH_URL="+s.authURL)
	}
	return cmd
}

// stop gracefully shuts down the child process. Sends SIGINT first; if the
// process doesn't exit within shutdownGracePeriod (30s), sends SIGKILL.
func (s *supervisedChild) stop() {
	// Signal the restart loop to exit.
	select {
	case <-s.stopCh:
		// Already stopping.
	default:
		close(s.stopCh)
	}

	// Interrupt the running process if any.
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)

		// Wait for graceful exit or force-kill after grace period.
		timer := time.NewTimer(shutdownGracePeriod)
		defer timer.Stop()

		select {
		case <-s.doneCh:
			// Child exited gracefully.
		case <-timer.C:
			s.log.Warn().Msg("supervisor: grace period expired — sending SIGKILL")
			_ = cmd.Process.Kill()
			<-s.doneCh
		}
	} else {
		// No running process; wait for the supervisor goroutine to finish.
		<-s.doneCh
	}
}
