// Package cmd — import.go implements the mclaude import command.
//
// Imports existing Claude Code session data from the local machine into mclaude.
// See spec-cli.md §mclaude import and ADR-0053 §User Flow — Import.
//
// Flow:
//  1. Load auth credentials from ~/.mclaude/auth.json (errors if not logged in).
//  2. Read active host from context (~/.mclaude/context.json); --host overrides.
//  3. Derive encoded CWD from current directory using Claude Code's path encoding.
//  4. Discover session data at ~/.claude/projects/{encoded-cwd}/.
//  5. Connect to NATS using stored JWT + NKey seed.
//  6. Derive project name; check slug availability via NATS request/reply.
//  7. If slug taken: prompt user for a new name. Loop until available.
//  8. Package data into import-{slug}.tar.gz with metadata.json.
//  9. Request pre-signed upload URL from CP via NATS.
// 10. Upload archive directly to S3 using the signed URL.
// 11. Confirm upload via NATS; wait for provisioning acknowledgement.
package cmd

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	clicontext "mclaude-cli/context"
)

// ImportFlags holds parsed flags for "mclaude import".
type ImportFlags struct {
	// HostSlug overrides the active host from context/symlink.
	HostSlug string
	// ServerURL overrides the control-plane base URL.
	ServerURL string
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// AuthPath overrides ~/.mclaude/auth.json (for tests).
	AuthPath string
	// ClaudeProjectsDir overrides ~/.claude/projects/ (for tests).
	ClaudeProjectsDir string
	// CWD overrides os.Getwd() (for tests).
	CWD string
	// Input is the reader for user prompts (default: os.Stdin).
	Input io.Reader
}

// ImportResult is returned by RunImport on success.
type ImportResult struct {
	// ProjectSlug is the slug of the newly created project.
	ProjectSlug string
	// SessionCount is the number of sessions imported.
	SessionCount int
	// ArchivePath is the local path of the generated tar.gz.
	ArchivePath string
}

// ImportMetadata is written as metadata.json inside the archive.
// Spec: ADR-0053 §Data Model.
type ImportMetadata struct {
	CWD               string    `json:"cwd"`
	GitRemote         string    `json:"gitRemote,omitempty"`
	GitBranch         string    `json:"gitBranch,omitempty"`
	ImportedAt        time.Time `json:"importedAt"`
	SessionIDs        []string  `json:"sessionIds"`
	ClaudeCodeVersion string    `json:"claudeCodeVersion,omitempty"`
}

// EncodeCWD derives the encoded CWD from an absolute path using Claude Code's
// path encoding algorithm:
//   - Take the absolute path (e.g. "/Users/rsong/work/mclaude").
//   - Replace every "/" with "-".
//   - Strip the leading "-".
//   - Result: "Users-rsong-work-mclaude".
//
// This matches the directory names under ~/.claude/projects/.
func EncodeCWD(absPath string) string {
	encoded := strings.ReplaceAll(absPath, "/", "-")
	return strings.TrimPrefix(encoded, "-")
}

// DiscoverSessions returns the JSONL session IDs found in the Claude Code
// project directory for the given encoded CWD.
// Returns the list of session IDs (filenames without .jsonl extension).
func DiscoverSessions(claudeProjectsDir, encodedCWD string) ([]string, error) {
	projectDir := filepath.Join(claudeProjectsDir, encodedCWD)
	entries, err := os.ReadDir(projectDir)
	if os.IsNotExist(err) {
		// List available encoded directories for the hint.
		available, listErr := listEncodedDirs(claudeProjectsDir)
		hint := ""
		if listErr == nil && len(available) > 0 {
			hint = fmt.Sprintf("\nAvailable project directories:\n  %s", strings.Join(available, "\n  "))
		}
		return nil, fmt.Errorf("no Claude Code session data found at %s%s", projectDir, hint)
	}
	if err != nil {
		return nil, fmt.Errorf("read project dir %s: %w", projectDir, err)
	}

	var sessionIDs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			sessionID := strings.TrimSuffix(e.Name(), ".jsonl")
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	return sessionIDs, nil
}

// listEncodedDirs returns the list of encoded directory names under claudeProjectsDir.
func listEncodedDirs(claudeProjectsDir string) ([]string, error) {
	entries, err := os.ReadDir(claudeProjectsDir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs, nil
}

// BuildArchive packages the session data into a tar.gz file at archivePath.
// Returns the size of the archive in bytes.
func BuildArchive(archivePath, claudeProjectsDir, encodedCWD string, meta ImportMetadata) (int64, error) {
	f, err := os.Create(archivePath)
	if err != nil {
		return 0, fmt.Errorf("create archive %s: %w", archivePath, err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Write metadata.json.
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshal metadata: %w", err)
	}
	metaData = append(metaData, '\n')
	if err := addFileToArchive(tw, "metadata.json", metaData); err != nil {
		return 0, fmt.Errorf("add metadata.json: %w", err)
	}

	projectDir := filepath.Join(claudeProjectsDir, encodedCWD)

	// Add JSONL session transcripts and subagent directories.
	for _, sessionID := range meta.SessionIDs {
		// Add {sessionId}.jsonl.
		jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
		if err := addDiskFileToArchive(tw, jsonlPath, filepath.Join("sessions", sessionID+".jsonl")); err != nil {
			// Warn but continue — partial imports are acceptable.
			fmt.Fprintf(os.Stderr, "warning: could not add %s: %v\n", jsonlPath, err)
		}

		// Add {sessionId}/subagents/ if present.
		subagentDir := filepath.Join(projectDir, sessionID, "subagents")
		if _, statErr := os.Stat(subagentDir); statErr == nil {
			if err := addDirToArchive(tw, subagentDir, filepath.Join("sessions", sessionID, "subagents")); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not add subagents for %s: %v\n", sessionID, err)
			}
		}
	}

	// Add memory/ directory if present.
	memoryDir := filepath.Join(projectDir, "memory")
	if _, statErr := os.Stat(memoryDir); statErr == nil {
		if err := addDirToArchive(tw, memoryDir, "memory"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not add memory/: %v\n", err)
		}
	}

	// Add CLAUDE.md — check CWD/.claude/CLAUDE.md first, then CWD/CLAUDE.md.
	cwd := meta.CWD
	for _, claudeMD := range []string{
		filepath.Join(cwd, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, "CLAUDE.md"),
	} {
		if _, statErr := os.Stat(claudeMD); statErr == nil {
			if err := addDiskFileToArchive(tw, claudeMD, "claude.md"); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not add CLAUDE.md: %v\n", err)
			}
			break
		}
	}

	// Close writers before stat-ing the file size.
	if err := tw.Close(); err != nil {
		return 0, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return 0, fmt.Errorf("close gzip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("close archive file: %w", err)
	}

	info, err := os.Stat(archivePath)
	if err != nil {
		return 0, fmt.Errorf("stat archive: %w", err)
	}
	return info.Size(), nil
}

func addFileToArchive(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func addDiskFileToArchive(tw *tar.Writer, diskPath, archiveName string) error {
	data, err := os.ReadFile(diskPath)
	if err != nil {
		return err
	}
	return addFileToArchive(tw, archiveName, data)
}

func addDirToArchive(tw *tar.Writer, diskDir, archivePrefix string) error {
	return filepath.WalkDir(diskDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(diskDir, path)
		if err != nil {
			return nil
		}
		return addDiskFileToArchive(tw, path, filepath.Join(archivePrefix, rel))
	})
}

// gitInfo returns the git remote and branch for the given directory.
// Returns empty strings if git is not available or the directory is not a repo.
func gitInfo(dir string) (remote, branch string) {
	if out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output(); err == nil {
		remote = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	return
}

// claudeCodeVersion returns the Claude Code CLI version if available.
func claudeCodeVersion() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// UploadToS3 uploads data from r (sizeBytes long) to a pre-signed S3 URL via HTTP PUT.
func UploadToS3(signedURL string, r io.Reader, sizeBytes int64) error {
	req, err := http.NewRequest(http.MethodPut, signedURL, r)
	if err != nil {
		return fmt.Errorf("create S3 PUT request: %w", err)
	}
	req.ContentLength = sizeBytes
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("S3 PUT: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("S3 PUT returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// RunImport performs the full import flow.
func RunImport(flags ImportFlags, out io.Writer) (*ImportResult, error) {
	// Resolve context.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return nil, fmt.Errorf("load context: %w", err)
	}

	// 1. Load auth credentials.
	authPath := flags.AuthPath
	if authPath == "" {
		authPath = DefaultAuthPath()
	}
	creds, err := LoadAuth(authPath)
	if err != nil {
		return nil, err
	}

	// 2. Resolve host slug: flag > active-host symlink > context.
	hslug := flags.HostSlug
	if hslug == "" {
		hslug = ResolveActiveHost()
	}
	if hslug == "" {
		hslug = ctx.HostSlug
	}
	if hslug == "" {
		return nil, fmt.Errorf("no active host: run 'mclaude host register' and 'mclaude host use <hslug>' first")
	}

	uslug := creds.UserSlug
	if uslug == "" {
		uslug = ctx.UserSlug
	}
	if uslug == "" {
		return nil, fmt.Errorf("user slug not found in credentials or context: run 'mclaude login' again")
	}

	// 3. Derive encoded CWD and discover session data.
	cwd := flags.CWD
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	claudeProjectsDir := flags.ClaudeProjectsDir
	if claudeProjectsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		claudeProjectsDir = filepath.Join(home, ".claude", "projects")
	}

	encodedCWD := EncodeCWD(cwd)
	fmt.Fprintf(out, "Importing sessions for: %s\n", cwd)
	fmt.Fprintf(out, "Encoded CWD:            %s\n", encodedCWD)

	sessionIDs, err := DiscoverSessions(claudeProjectsDir, encodedCWD)
	if err != nil {
		return nil, err
	}
	if len(sessionIDs) == 0 {
		return nil, fmt.Errorf("no JSONL session files found in ~/.claude/projects/%s/", encodedCWD)
	}
	fmt.Fprintf(out, "Found %d session(s)\n", len(sessionIDs))

	// Collect git info.
	gitRemote, gitBranch := gitInfo(cwd)

	// 4. Derive project name and slug from last path component of CWD.
	projectName := filepath.Base(cwd)
	pslug := slugifyProjectName(projectName)
	if pslug == "" {
		pslug = "imported-project"
	}

	// 5. Connect to NATS and check slug availability.
	serverURL := clicontext.ResolveServerURL(flags.ServerURL, ctx)
	_ = serverURL // used when NATS is wired

	// TODO: Connect to NATS using creds.JWT + creds.NKeySeed and perform
	// request/reply to check slug availability. For now, print what would happen.
	fmt.Fprintf(out, "Project slug:           %s\n", pslug)
	fmt.Fprintf(out, "  Would check: mclaude.users.%s.hosts.%s.projects.check-slug\n", uslug, hslug)

	// 6. Prompt for new name if slug is taken (stub: assume available).
	// In the full implementation, loop here based on NATS response.

	// 7. Package data into tar.gz.
	meta := ImportMetadata{
		CWD:               cwd,
		GitRemote:         gitRemote,
		GitBranch:         gitBranch,
		ImportedAt:        time.Now().UTC(),
		SessionIDs:        sessionIDs,
		ClaudeCodeVersion: claudeCodeVersion(),
	}

	archiveName := fmt.Sprintf("import-%s.tar.gz", pslug)
	archivePath := filepath.Join(os.TempDir(), archiveName)
	fmt.Fprintf(out, "Packaging sessions into %s...\n", archiveName)

	archiveSize, err := BuildArchive(archivePath, claudeProjectsDir, encodedCWD, meta)
	if err != nil {
		return nil, fmt.Errorf("build archive: %w", err)
	}
	fmt.Fprintf(out, "Archive size: %d bytes\n", archiveSize)

	// 8–11. NATS request/reply operations (stub).
	// TODO: When NATS is available:
	// 8. Request pre-signed URL: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request
	// 9. Upload archive to S3 via UploadToS3(signedURL, archiveFile, archiveSize)
	// 10. Confirm: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm {importId}
	// 11. Wait for provisioning acknowledgement.
	fmt.Fprintf(out, "\nImport prepared (NATS operations pending control-plane availability):\n")
	fmt.Fprintf(out, "  User:    %s\n", uslug)
	fmt.Fprintf(out, "  Host:    %s\n", hslug)
	fmt.Fprintf(out, "  Project: %s\n", pslug)
	fmt.Fprintf(out, "  Archive: %s (%d sessions)\n", archivePath, len(sessionIDs))
	fmt.Fprintf(out, "  Subjects to use:\n")
	fmt.Fprintf(out, "    import.request:  mclaude.users.%s.hosts.%s.projects.%s.import.request\n", uslug, hslug, pslug)
	fmt.Fprintf(out, "    import.confirm:  mclaude.users.%s.hosts.%s.projects.%s.import.confirm\n", uslug, hslug, pslug)

	return &ImportResult{
		ProjectSlug:  pslug,
		SessionCount: len(sessionIDs),
		ArchivePath:  archivePath,
	}, nil
}

// slugifyProjectName converts a directory name into a valid slug.
// Uses simple rules: lowercase, replace non-alphanumeric with "-", trim edges.
func slugifyProjectName(name string) string {
	var sb strings.Builder
	prev := '-'
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prev = r
		} else if r == '-' || r == '_' || r == '.' || r == ' ' {
			if prev != '-' {
				sb.WriteRune('-')
				prev = '-'
			}
		}
	}
	result := strings.Trim(sb.String(), "-")
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}

// promptProjectName reads a project name from r, returning the trimmed input.
// Used to ask the user for a new name when the slug is taken.
func promptProjectName(r io.Reader, out io.Writer) (string, error) {
	fmt.Fprint(out, "Enter a new project name: ")
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no input provided")
	}
	return strings.TrimSpace(scanner.Text()), nil
}
