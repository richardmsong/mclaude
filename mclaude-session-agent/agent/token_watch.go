package agent

// token_watch.go — Secret mount watcher for Anthropic token recovery (ADR-0092).
//
// When the Anthropic API returns 401/403 the session-agent sets the session
// status to token_expired (handled in session.go handleSideEffect). This file
// watches the mounted Secret directory (/secrets/tokens/) via fsnotify (falling
// back to polling every 30 s if fsnotify is unavailable). When the token file
// is updated the agent restarts every token_expired session so it picks up the
// new token and transitions back to idle/running.

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// tokenSecretsDir is the directory where the user-tokens Secret is mounted.
	// Per ADR-0091 the entrypoint exports CLAUDE_CODE_OAUTH_TOKEN from
	// /secrets/tokens/${TOKEN_PROVIDER}, so watching the directory detects any
	// provider token update.
	tokenSecretsDir = "/secrets/tokens"

	// tokenPollInterval is the fallback poll interval when fsnotify cannot watch
	// the secrets directory (e.g. some read-only overlay filesystems).
	tokenPollInterval = 30 * time.Second
)

// watchTokenSecrets watches /secrets/tokens/ for file changes (fsnotify) and
// falls back to polling every 30 s. On any file write/create/rename event the
// agent calls recoverTokenExpiredSessions to restart all token_expired sessions
// with the updated token.
//
// This goroutine runs for the lifetime of the agent (cancelled by ctx).
func (a *Agent) watchTokenSecrets(ctx context.Context) {
	// Try to set up fsnotify on the secrets directory.
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		if watchErr := watcher.Add(tokenSecretsDir); watchErr != nil {
			// Directory may not exist in dev/BYOH mode — that is fine; fall back to polling.
			_ = watcher.Close()
			watcher = nil
		}
	}

	if watcher != nil {
		defer watcher.Close()
		a.log.Info().Str("dir", tokenSecretsDir).
			Msg("token-watch: watching secrets directory for token updates (fsnotify)")
		a.runTokenWatcherFsnotify(ctx, watcher)
	} else {
		// Polling fallback.
		a.log.Info().
			Dur("interval", tokenPollInterval).
			Msg("token-watch: fsnotify unavailable — polling for token updates")
		a.runTokenWatcherPoll(ctx)
	}
}

// runTokenWatcherFsnotify drives the fsnotify-based watcher loop.
func (a *Agent) runTokenWatcherFsnotify(ctx context.Context, watcher *fsnotify.Watcher) {
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-watcher.Events:
			if !ok {
				// Channel closed — watcher stopped. Switch to polling.
				a.runTokenWatcherPoll(ctx)
				return
			}
			// React to Write, Create, or Rename events on any file in the directory.
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				a.log.Info().
					Str("file", filepath.Base(event.Name)).
					Msg("token-watch: token file updated; recovering token_expired sessions")
				a.recoverTokenExpiredSessions()
			}

		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return
			}
			a.log.Warn().Err(watchErr).Msg("token-watch: fsnotify error")
		}
	}
}

// runTokenWatcherPoll drives the poll-based fallback watcher loop.
// It samples the modification time of files in tokenSecretsDir every
// tokenPollInterval seconds. If the mtime advances, it triggers recovery.
func (a *Agent) runTokenWatcherPoll(ctx context.Context) {
	lastMtime := latestMtime(tokenSecretsDir)

	ticker := time.NewTicker(tokenPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := latestMtime(tokenSecretsDir)
			if !current.IsZero() && current.After(lastMtime) {
				lastMtime = current
				a.log.Info().Msg("token-watch: token file mtime advanced; recovering token_expired sessions")
				a.recoverTokenExpiredSessions()
			}
		}
	}
}

// latestMtime returns the most-recent modification time of any regular file
// in dir. Returns zero time if the directory does not exist or is empty.
func latestMtime(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// recoverTokenExpiredSessions restarts every session whose KV status is
// token_expired. Per ADR-0092, the restart picks up the new token from the
// updated Secret mount file and transitions the session back to idle/running.
// Sessions in any other state are unaffected.
func (a *Agent) recoverTokenExpiredSessions() {
	a.mu.RLock()
	var expired []*Session
	for _, sess := range a.sessions {
		st := sess.getState()
		if st.State == StatusTokenExpired {
			expired = append(expired, sess)
		}
	}
	a.mu.RUnlock()

	if len(expired) == 0 {
		return
	}

	a.log.Info().Int("count", len(expired)).Msg("token-watch: restarting token_expired sessions")

	for _, sess := range expired {
		a.restartTokenExpiredSession(sess)
	}
}

// restartTokenExpiredSession stops and resumes a single token_expired session
// so it picks up the refreshed token from the Secret mount.
//
// The recovery sequence mirrors handleRestart (agent.go) but is triggered
// internally by the token watcher rather than by a user NATS request.
func (a *Agent) restartTokenExpiredSession(sess *Session) {
	sessID := sess.getState().ID

	a.log.Info().Str("sessionId", sessID).Msg("token-watch: restarting token_expired session")
	a.publishLifecycle(sessID, "session_restarting")

	if err := sess.stopAndWait(sessionDeleteTimeout); err != nil {
		a.log.Warn().Err(err).
			Str("sessionId", sessID).
			Msg("token-watch: session did not stop cleanly before restart")
	}

	// Clear pending controls and reset the state to restarting.
	st := sess.getState()
	clearPendingControlsForResume(&st)

	if err := a.writeSessionKV(st); err != nil {
		a.log.Warn().Err(err).Str("sessionId", sessID).
			Msg("token-watch: KV write before restart failed (non-fatal)")
	}

	publish := func(subject string, data []byte) error {
		return a.nc.Publish(subject, data)
	}

	// Build the new session. Pass the updated CLAUDE_CODE_OAUTH_TOKEN so the
	// subprocess reads the refreshed token from the Secret mount file.
	newSess := newSession(st, a.userID)
	newSess.metrics = a.metrics
	newSess.log = a.log

	// Inject the updated token into the subprocess environment.
	// The entrypoint set CLAUDE_CODE_OAUTH_TOKEN from the file; after the file
	// is updated by the user-agent the running process still holds the old env
	// value. We re-read the file here and pass it as an extra env override so
	// the resumed subprocess authenticates with the new token.
	if tokenEnv := readTokenEnv(); tokenEnv != "" {
		newSess.extraEnv = append(newSess.extraEnv, tokenEnv)
	}

	// Start debug socket if one was running before.
	restartID := sessID
	sess.mu.Lock()
	hadDebug := sess.debug != nil
	sess.mu.Unlock()
	if hadDebug {
		newDbg := NewDebugServer(restartID,
			func(data []byte) { newSess.sendInput(data) },
			func() { a.publishLifecycle(restartID, "debug_attached") },
			func() { a.publishLifecycle(restartID, "debug_detached") },
		)
		if err := newDbg.Start(); err != nil {
			a.log.Warn().Err(err).Str("sessionId", restartID).
				Msg("token-watch: debug socket start failed on restart (non-fatal)")
		} else {
			newSess.mu.Lock()
			newSess.debug = newDbg
			newSess.mu.Unlock()
		}
	}

	if err := newSess.start(a.claudePath, true, publish, a.writeSessionKV); err != nil {
		a.log.Error().Err(err).Str("sessionId", sessID).
			Msg("token-watch: failed to resume session after token refresh")
		return
	}

	a.mu.Lock()
	a.sessions[sessID] = newSess
	a.mu.Unlock()

	a.publishLifecycle(sessID, "session_resumed")

	if a.metrics != nil {
		a.metrics.ClaudeRestart()
	}

	a.log.Info().Str("sessionId", sessID).Msg("token-watch: session resumed after token refresh")
}

// readTokenEnv reads the token from the Secret mount file (tokenSecretsDir) and
// returns the CLAUDE_CODE_OAUTH_TOKEN=<value> env string. Returns "" if the
// file cannot be read or is empty (e.g. in BYOH/dev mode where
// /secrets/tokens/ doesn't exist).
func readTokenEnv() string {
	return readTokenEnvFromDir(tokenSecretsDir)
}

// readTokenEnvFromDir is the testable inner implementation of readTokenEnv.
// It reads the token from dir/{TOKEN_PROVIDER} (defaulting to "anthropic") and
// returns the CLAUDE_CODE_OAUTH_TOKEN=<value> env string.
func readTokenEnvFromDir(dir string) string {
	provider := os.Getenv("TOKEN_PROVIDER")
	if provider == "" {
		provider = "anthropic"
	}
	tokenPath := filepath.Join(dir, provider)
	data, err := os.ReadFile(tokenPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	// Trim trailing newline added by some editors or Secret mounts.
	token := string(data)
	for len(token) > 0 && (token[len(token)-1] == '\n' || token[len(token)-1] == '\r') {
		token = token[:len(token)-1]
	}
	if token == "" {
		return ""
	}
	return "CLAUDE_CODE_OAUTH_TOKEN=" + token
}
