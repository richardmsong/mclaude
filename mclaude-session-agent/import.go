package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"
)

// importMetadata holds the metadata.json from an import archive.
type importMetadata struct {
	CWD               string   `json:"cwd"`
	GitRemote         string   `json:"gitRemote"`
	GitBranch         string   `json:"gitBranch"`
	ImportedAt        string   `json:"importedAt"`
	SessionIDs        []string `json:"sessionIds"`
	ClaudeCodeVersion string   `json:"claudeCodeVersion"`
}

// importDownloadResponse is the CP response to import.download request.
type importDownloadResponse struct {
	DownloadURL string `json:"downloadUrl"`
}

// checkImport checks the project KV for a pending import (importRef field).
// If found, downloads the archive from S3, unpacks it, clears the importRef,
// and signals completion to the control-plane.
// Per ADR-0053 / spec-session-agent.md §Import Handler.
func (a *Agent) checkImport(ctx context.Context) error {
	projKey := subj.ProjectsKVKey(a.hostSlug, a.projectSlug)
	entry, err := a.projKV.Get(ctx, projKey)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			return nil // no project KV entry yet; skip
		}
		return fmt.Errorf("projects KV get(%s): %w", projKey, err)
	}

	var projState struct {
		ImportRef string `json:"importRef"`
	}
	if err := json.Unmarshal(entry.Value(), &projState); err != nil {
		return fmt.Errorf("projects KV unmarshal: %w", err)
	}
	if projState.ImportRef == "" {
		return nil // no import pending
	}

	a.log.Info().Str("importRef", projState.ImportRef).Msg("pending import detected; starting download")

	// Request pre-signed download URL from CP via NATS request/reply.
	downloadSubject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + ".import.download"
	reqData, _ := json.Marshal(map[string]string{"importId": projState.ImportRef})

	reply, err := a.nc.RequestWithContext(ctx, downloadSubject, reqData)
	if err != nil {
		return fmt.Errorf("import.download request failed: %w", err)
	}

	var dlResp importDownloadResponse
	if err := json.Unmarshal(reply.Data, &dlResp); err != nil {
		return fmt.Errorf("import.download response parse: %w", err)
	}
	if dlResp.DownloadURL == "" {
		return fmt.Errorf("import.download returned empty URL")
	}

	// Download the archive from S3 using the pre-signed URL.
	tmpFile, err := os.CreateTemp("", "mclaude-import-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if err := a.downloadS3(ctx, dlResp.DownloadURL, tmpFile); err != nil {
		// Pre-signed URL may have expired — request a new one and retry once.
		a.log.Warn().Err(err).Msg("import S3 download failed; requesting new URL and retrying")
		reply2, retryErr := a.nc.RequestWithContext(ctx, downloadSubject, reqData)
		if retryErr != nil {
			return fmt.Errorf("import.download retry request failed: %w", retryErr)
		}
		var dlResp2 importDownloadResponse
		if parseErr := json.Unmarshal(reply2.Data, &dlResp2); parseErr != nil || dlResp2.DownloadURL == "" {
			return fmt.Errorf("import.download retry returned empty URL")
		}
		tmpFile.Seek(0, io.SeekStart)
		tmpFile.Truncate(0)
		if err2 := a.downloadS3(ctx, dlResp2.DownloadURL, tmpFile); err2 != nil {
			a.publishImportFailed("S3 download failed after retry: " + err2.Error())
			return fmt.Errorf("import S3 download retry failed: %w", err2)
		}
	}

	// Seek back to start for reading.
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek import archive: %w", err)
	}

	// Determine unpack target directory.
	// K8s mode: PVC at /data/projects/ ; BYOH mode: ~/.claude/projects/{encoded-cwd}
	sessionDataDir := a.importSessionDataDir()

	// Unpack archive and create SessionState KV entries.
	if err := a.unpackImportArchive(ctx, tmpFile, sessionDataDir); err != nil {
		a.publishImportFailed("archive unpack failed: " + err.Error())
		// Leave importRef set so the import can be retried on next agent restart.
		return fmt.Errorf("import unpack: %w", err)
	}

	// Clear importRef from project KV state.
	if err := a.clearImportRef(ctx); err != nil {
		a.log.Warn().Err(err).Msg("failed to clear importRef from project KV (non-fatal)")
	}

	// Signal completion to CP: CP will delete the S3 object.
	completeSubject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + ".import.complete"
	completeData, _ := json.Marshal(map[string]interface{}{
		"id": projState.ImportRef,
		"ts": time.Now().UnixMilli(),
	})
	if err := a.nc.Publish(completeSubject, completeData); err != nil {
		a.log.Warn().Err(err).Msg("failed to publish import.complete (non-fatal)")
	}

	// Publish lifecycle event.
	a.publishImportComplete()

	a.log.Info().Str("importRef", projState.ImportRef).Msg("session import complete")
	return nil
}

// importSessionDataDir returns the session data directory for import unpacking.
// In K8s mode: /data/projects/. In BYOH mode: ~/.claude/projects/.
func (a *Agent) importSessionDataDir() string {
	effectiveDataDir := a.dataDir
	if effectiveDataDir == "" {
		effectiveDataDir = "/data"
	}
	// Prefer /data/projects/ (K8s PVC layout).
	projDir := filepath.Join(effectiveDataDir, "projects")
	if fi, err := os.Stat(projDir); err == nil && fi.IsDir() {
		return projDir
	}
	// BYOH fallback: ~/.claude/projects/
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude", "projects")
	}
	return filepath.Join(effectiveDataDir, "projects")
}

// downloadS3 downloads content from a pre-signed S3 URL to a writer.
func (a *Agent) downloadS3(ctx context.Context, downloadURL string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("S3 GET returned %d", resp.StatusCode)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// unpackImportArchive extracts a tar.gz import archive into the session data directory.
// For each JSONL session file, it creates a SessionState KV entry if one doesn't exist.
// Validates archive integrity (metadata.json schema, JSONL line format) per spec.
func (a *Agent) unpackImportArchive(ctx context.Context, r io.Reader, sessionDataDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	var meta *importMetadata
	// Pass 1: extract all files and validate.
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Sanitize path (prevent path traversal).
		target := filepath.Join(sessionDataDir, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, sessionDataDir) {
			continue // skip suspicious paths
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			f, err := os.Create(target)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}

			// Validate JSONL files: check each line is valid JSON.
			if strings.HasSuffix(hdr.Name, ".jsonl") {
				h := sha256.New()
				tee := io.TeeReader(tr, h)
				if validateErr := validateJSONLLines(tee, f); validateErr != nil {
					f.Close()
					return fmt.Errorf("JSONL validation for %s: %w", hdr.Name, validateErr)
				}
				f.Close()
			} else if filepath.Base(hdr.Name) == "metadata.json" {
				// Read and validate metadata.json.
				data, readErr := io.ReadAll(tr)
				if readErr != nil {
					f.Close()
					return fmt.Errorf("read metadata.json: %w", readErr)
				}
				var m importMetadata
				if jsonErr := json.Unmarshal(data, &m); jsonErr != nil {
					f.Close()
					return fmt.Errorf("metadata.json invalid JSON: %w", jsonErr)
				}
				meta = &m
				if _, writeErr := f.Write(data); writeErr != nil {
					f.Close()
					return fmt.Errorf("write metadata.json: %w", writeErr)
				}
				f.Close()
			} else {
				if _, copyErr := io.Copy(f, tr); copyErr != nil {
					f.Close()
					return fmt.Errorf("copy %s: %w", hdr.Name, copyErr)
				}
				f.Close()
			}
		}
	}

	if meta == nil {
		return fmt.Errorf("archive missing metadata.json")
	}

	// For each session ID in the archive, create a SessionState KV entry if absent.
	for _, sessID := range meta.SessionIDs {
		if err := a.createImportedSessionKVEntry(ctx, sessID); err != nil {
			a.log.Warn().Err(err).Str("sessionId", sessID).Msg("import: failed to create session KV entry (non-fatal; continuing)")
		}
	}

	return nil
}

// validateJSONLLines reads from r, validates each line is JSON, and writes to w.
func validateJSONLLines(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return fmt.Errorf("invalid JSON line: %.100s", line)
		}
		if _, err := w.Write(line); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return fmt.Errorf("write newline: %w", err)
		}
	}
	return scanner.Err()
}

// createImportedSessionKVEntry creates a SessionState KV entry for an imported session
// if one does not already exist. Imported sessions are marked as status:"completed"
// (historical, read-only).
func (a *Agent) createImportedSessionKVEntry(ctx context.Context, sessionID string) error {
	sessSlug := slug.SessionSlug(sessionID)
	key := sessionKVKey(a.hostSlug, a.projectSlug, sessSlug)

	// Check if entry already exists.
	if _, err := a.sessKV.Get(ctx, key); err == nil {
		// Entry exists — skip with a warning (session ID collision handling per spec).
		a.log.Warn().Str("sessionId", sessionID).Str("key", key).
			Msg("import: session ID collision — skipping (existing KV entry preserved)")
		return nil
	}

	now := time.Now().UTC()
	state := SessionState{
		ID:          sessionID,
		Slug:        sessionID,
		UserSlug:    string(a.userSlug),
		HostSlug:    string(a.hostSlug),
		ProjectSlug: string(a.projectSlug),
		ProjectID:   a.projectID,
		State:       StatusCompleted, // imported sessions are historical/read-only
		StateSince:  now,
		CreatedAt:   now,
		Name:        "Imported session",
	}

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	_, err = a.sessKV.Put(ctx, key, data)
	return err
}

// clearImportRef updates the project KV entry to clear the importRef field.
func (a *Agent) clearImportRef(ctx context.Context) error {
	projKey := subj.ProjectsKVKey(a.hostSlug, a.projectSlug)
	entry, err := a.projKV.Get(ctx, projKey)
	if err != nil {
		return fmt.Errorf("projKV get: %w", err)
	}

	// Parse the current project state as a generic map so we don't lose unknown fields.
	var projState map[string]json.RawMessage
	if err := json.Unmarshal(entry.Value(), &projState); err != nil {
		return fmt.Errorf("projKV unmarshal: %w", err)
	}
	// Remove importRef.
	delete(projState, "importRef")

	updated, err := json.Marshal(projState)
	if err != nil {
		return fmt.Errorf("re-marshal: %w", err)
	}
	_, err = a.projKV.Put(ctx, projKey, updated)
	return err
}

// publishImportFailed publishes a session_import_failed lifecycle event.
func (a *Agent) publishImportFailed(errMsg string) {
	if a.nc == nil {
		return
	}
	// Use a synthetic session ID for import lifecycle events (no session yet).
	subject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) +
		".sessions._import.lifecycle.session_import_failed"
	payload, _ := json.Marshal(map[string]string{
		"type":  "session_import_failed",
		"error": errMsg,
		"ts":    time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}

// publishImportComplete publishes a session_import_complete lifecycle event.
func (a *Agent) publishImportComplete() {
	if a.nc == nil {
		return
	}
	subject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) +
		".sessions._import.lifecycle.session_import_complete"
	payload, _ := json.Marshal(map[string]string{
		"type": "session_import_complete",
		"ts":   time.Now().UTC().Format(time.RFC3339),
	})
	_ = a.nc.Publish(subject, payload)
}
