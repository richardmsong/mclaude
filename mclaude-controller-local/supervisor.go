package main

import (
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"mclaude.io/common/pkg/slug"
)

const (
	// childRestartDelay is the wait between a child crash and restart.
	childRestartDelay = 2 * time.Second
	// shutdownGracePeriod is how long to wait after SIGINT before SIGKILL.
	shutdownGracePeriod = 30 * time.Second
)

// supervisedChild tracks a running session-agent subprocess managed by the
// controller's process supervisor.
type supervisedChild struct {
	projectSlug string
	dataDir     string
	userSlug    slug.UserSlug
	hostSlug    slug.HostSlug

	mu     sync.Mutex
	cmd    *exec.Cmd
	stopCh chan struct{}
	doneCh chan struct{}
	log    zerolog.Logger
}

// startChild creates and starts a supervised session-agent subprocess for
// the given project. The child process is automatically restarted on crash
// with a 2-second delay.
func (c *Controller) startChild(pslug string, dataDir string) *supervisedChild {
	child := &supervisedChild{
		projectSlug: pslug,
		dataDir:     dataDir,
		userSlug:    c.userSlug,
		hostSlug:    c.hostSlug,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		log:         c.log.With().Str("project", pslug).Logger(),
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
// Passes USER_SLUG, HOST_SLUG, PROJECT_SLUG as both flags and env vars
// per ADR-0035.
func (s *supervisedChild) buildCmd() *exec.Cmd {
	cmd := exec.Command("mclaude-session-agent",
		"--mode", "standalone",
		"--user-slug", string(s.userSlug),
		"--host-slug", string(s.hostSlug),
		"--project-slug", s.projectSlug,
		"--data-dir", s.dataDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"USER_SLUG="+string(s.userSlug),
		"HOST_SLUG="+string(s.hostSlug),
		"PROJECT_SLUG="+s.projectSlug,
	)
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
