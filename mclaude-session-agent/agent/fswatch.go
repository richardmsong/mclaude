package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"mclaude.io/common/pkg/slug"
)

// kvKeyForJSONLFile extracts the session slug from a JSONL filename and returns
// the corresponding NATS KV key for the session. Returns "" if the filename
// is not a valid session JSONL filename.
func kvKeyForJSONLFile(filename string, hostSlug slug.HostSlug, projectSlug slug.ProjectSlug) string {
	sessID := strings.TrimSuffix(filename, ".jsonl")
	if sessID == "" || sessID == filename {
		return ""
	}
	return sessionKVKey(hostSlug, projectSlug, slug.SessionSlug(sessID))
}

// jsonlCleanupMaxAge is the maximum age of JSONL files before they are deleted
// by the daily cleanup goroutine. Spec: "daily cleanup deletes files >90 days".
const jsonlCleanupMaxAge = 90 * 24 * time.Hour

// jsonlCleanupInterval is how often the cleanup goroutine runs.
const jsonlCleanupInterval = 24 * time.Hour

// runJSONLCleanup is a background goroutine that runs once daily and deletes
// JSONL files older than 90 days from the session data directory.
// Per spec §JSONL Cleanup Job: "A daily cleanup job ... deletes JSONL files
// older than 90 days from /data/projects/"
func (a *Agent) runJSONLCleanup(ctx context.Context) {
	sessionDataDir := a.importSessionDataDir()

	// Run immediately on startup, then on each tick.
	a.doJSONLCleanup(sessionDataDir)

	ticker := time.NewTicker(jsonlCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.doJSONLCleanup(sessionDataDir)
		}
	}
}

// doJSONLCleanup scans sessionDataDir for .jsonl files and deletes:
//  1. Files older than jsonlCleanupMaxAge (>90 days).
//  2. Files whose session ID is not present in KV_mclaude-sessions-{uslug}
//     (KV-orphan purging per spec §JSONL Cleanup Job).
//
// Errors on individual files are logged and skipped.
func (a *Agent) doJSONLCleanup(sessionDataDir string) {
	cutoff := time.Now().Add(-jsonlCleanupMaxAge)

	entries, err := os.ReadDir(sessionDataDir)
	if err != nil {
		if !os.IsNotExist(err) {
			a.log.Warn().Err(err).Str("dir", sessionDataDir).
				Msg("jsonl-cleanup: failed to read session data dir")
		}
		return
	}

	var deleted, skipped int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(sessionDataDir, entry.Name())

		// Check 1: age-based deletion.
		info, infoErr := entry.Info()
		if infoErr != nil {
			a.log.Warn().Err(infoErr).Str("file", entry.Name()).
				Msg("jsonl-cleanup: failed to stat file; skipping")
			skipped++
			continue
		}
		tooOld := !info.ModTime().After(cutoff)

		// Check 2: KV-orphan detection.
		// A file is an orphan if its session ID has no entry in the sessions KV bucket.
		// Only perform the check when the agent has a live KV connection.
		isOrphan := false
		if !tooOld && a.sessKV != nil {
			kvKey := kvKeyForJSONLFile(entry.Name(), a.hostSlug, a.projectSlug)
			if kvKey != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, kvErr := a.sessKV.Get(ctx, kvKey)
				cancel()
				// If the key is not found (ErrKeyNotFound) the file is an orphan.
				// Other errors (network, etc.) are treated as non-orphan to be safe.
				if kvErr != nil && strings.Contains(kvErr.Error(), "key not found") {
					isOrphan = true
					a.log.Debug().
						Str("file", entry.Name()).
						Str("kvKey", kvKey).
						Msg("jsonl-cleanup: session KV entry not found; marking as orphan")
				}
			}
		}

		if !tooOld && !isOrphan {
			continue // file is current and has a KV entry; keep it
		}

		reason := "age"
		if isOrphan {
			reason = "kv-orphan"
		}

		if rmErr := os.Remove(filePath); rmErr != nil {
			a.log.Warn().Err(rmErr).Str("file", filePath).
				Msg("jsonl-cleanup: failed to delete file; skipping")
			skipped++
			continue
		}
		a.log.Debug().
			Str("file", filePath).
			Str("reason", reason).
			Str("age", time.Since(info.ModTime()).Round(time.Hour).String()).
			Msg("jsonl-cleanup: deleted JSONL file")
		deleted++
	}

	if deleted > 0 || skipped > 0 {
		a.log.Info().
			Int("deleted", deleted).
			Int("skipped", skipped).
			Str("dir", sessionDataDir).
			Msg("jsonl-cleanup: completed")
	}
}

// watchSessionDataDir runs an fsnotify watcher on the session data directory.
// When a new .jsonl file is detected (created or renamed into the directory),
// it creates a SessionState KV entry for the session (if one doesn't already exist).
//
// Per ADR-0053 / spec-session-agent.md §fsnotify Watcher:
// "The watcher runs for the lifetime of the agent process, enabling live discovery
// of sessions placed into the directory by any mechanism."
func (a *Agent) watchSessionDataDir(ctx context.Context) {
	sessionDataDir := a.importSessionDataDir()

	// Ensure the directory exists.
	if err := os.MkdirAll(sessionDataDir, 0755); err != nil {
		a.log.Warn().Err(err).Str("dir", sessionDataDir).
			Msg("fsnotify: cannot create session data dir; watcher disabled")
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		a.log.Warn().Err(err).Msg("fsnotify: failed to create watcher; live JSONL discovery disabled")
		return
	}
	defer watcher.Close()

	if err := watcher.Add(sessionDataDir); err != nil {
		a.log.Warn().Err(err).Str("dir", sessionDataDir).
			Msg("fsnotify: failed to watch session data dir; live JSONL discovery disabled")
		return
	}

	a.log.Info().Str("dir", sessionDataDir).Msg("fsnotify: watching session data directory for new JSONL files")

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only react to Create or Rename events on .jsonl files.
			if event.Op&(fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if !strings.HasSuffix(event.Name, ".jsonl") {
				continue
			}
			a.log.Debug().Str("file", event.Name).Msg("fsnotify: new JSONL file detected")
			go a.handleNewJSONLFile(ctx, event.Name)

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			a.log.Warn().Err(err).Msg("fsnotify: watcher error")
		}
	}
}

// handleNewJSONLFile extracts session metadata from a newly detected JSONL file
// and creates a SessionState KV entry for it (if one doesn't exist).
// Per spec: "Extract session metadata from the first few lines of the JSONL file:
// session ID, timestamps, branch, model."
func (a *Agent) handleNewJSONLFile(ctx context.Context, filePath string) {
	// Wait briefly for the file to be fully written (race between create and write).
	time.Sleep(100 * time.Millisecond)

	// Extract session ID from filename (Claude Code uses {sessionId}.jsonl).
	base := filepath.Base(filePath)
	sessionID := strings.TrimSuffix(base, ".jsonl")
	if sessionID == "" || sessionID == base {
		return // not a standard session file name
	}

	// Extract metadata from first few JSONL lines.
	meta := extractJSONLSessionMeta(filePath)

	// Use filename as session ID if we couldn't extract it from content.
	if meta.sessionID == "" {
		meta.sessionID = sessionID
	}

	// Check if a SessionState KV entry already exists.
	sessSlug := slug.SessionSlug(meta.sessionID)
	key := sessionKVKey(a.hostSlug, a.projectSlug, sessSlug)
	if _, err := a.sessKV.Get(ctx, key); err == nil {
		// Entry exists — skip (duplicate detection per spec).
		a.log.Debug().Str("sessionId", meta.sessionID).
			Msg("fsnotify: session KV entry already exists; skipping")
		return
	}

	// Create SessionState KV entry with status "completed" (imported/historical).
	now := time.Now().UTC()
	createdAt := now
	if !meta.createdAt.IsZero() {
		createdAt = meta.createdAt
	}
	state := SessionState{
		ID:          meta.sessionID,
		Slug:        meta.sessionID,
		UserSlug:    string(a.userSlug),
		HostSlug:    string(a.hostSlug),
		ProjectSlug: string(a.projectSlug),
		ProjectID:   a.projectID,
		State:       StatusCompleted,
		StateSince:  now,
		CreatedAt:   createdAt,
		Name:        "Imported session",
		Model:       meta.model,
		Branch:      meta.branch,
	}

	data, err := json.Marshal(state)
	if err != nil {
		a.log.Warn().Err(err).Str("sessionId", meta.sessionID).
			Msg("fsnotify: failed to marshal session state")
		return
	}
	if _, err := a.sessKV.Put(ctx, key, data); err != nil {
		a.log.Warn().Err(err).Str("sessionId", meta.sessionID).
			Msg("fsnotify: failed to write session KV entry")
		return
	}

	a.log.Info().
		Str("sessionId", meta.sessionID).
		Str("file", filePath).
		Msg("fsnotify: created session KV entry for new JSONL file")
}

// jsonlSessionMeta holds metadata extracted from the first few lines of a JSONL file.
type jsonlSessionMeta struct {
	sessionID string
	createdAt time.Time
	model     string
	branch    string
}

// extractJSONLSessionMeta reads the first few lines of a JSONL file to extract
// session metadata (session ID, timestamps, model, branch).
// Returns a best-effort struct; missing fields have zero values.
func extractJSONLSessionMeta(filePath string) jsonlSessionMeta {
	var meta jsonlSessionMeta

	f, err := os.Open(filePath)
	if err != nil {
		return meta
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	maxLines := 10 // only scan the first few lines
	for i := 0; i < maxLines && scanner.Scan(); i++ {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Parse as generic JSON object.
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		// Extract session_id.
		if meta.sessionID == "" {
			if v, ok := obj["session_id"]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					meta.sessionID = s
				}
			}
		}

		// Extract model.
		if meta.model == "" {
			if v, ok := obj["model"]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					meta.model = s
				}
			}
		}

		// Extract timestamp (various field names used by Claude Code).
		if meta.createdAt.IsZero() {
			for _, tsField := range []string{"timestamp", "ts", "created_at"} {
				if v, ok := obj[tsField]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil {
						if t, parseErr := time.Parse(time.RFC3339, s); parseErr == nil {
							meta.createdAt = t
						}
					}
				}
			}
		}

		// Extract branch from cwd or branch field.
		if meta.branch == "" {
			for _, brField := range []string{"branch", "git_branch"} {
				if v, ok := obj[brField]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil && s != "" {
						meta.branch = s
						break
					}
				}
			}
		}
	}

	return meta
}
