package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

const (
	// secretMountPath is where the user-secrets K8s Secret is mounted in the pod.
	secretMountPath = "/home/node/.user-secrets"

	// configPVCPath is the PVC-persisted config directory.
	configPVCPath = "/data/.config"

	// configHomePath is the standard XDG config directory.
	configHomePath = "~/.config"
)

// gitAuthErrPatterns are the stderr strings that indicate a git authentication failure.
// These are provider-agnostic — covers GitHub, GitLab, and common HTTPS auth errors.
var gitAuthErrPatterns = []string{
	"Authentication failed",
	"HTTP Basic: Access denied",
	"Invalid username or password",
	"could not read Username",
}

// IsGitAuthError returns true if the given exit code and stderr output indicate
// a git authentication failure. Per spec: exit code 128 + any auth error string.
func IsGitAuthError(exitCode int, stderr string) bool {
	if exitCode != 128 {
		return false
	}
	for _, pat := range gitAuthErrPatterns {
		if strings.Contains(stderr, pat) {
			return true
		}
	}
	return false
}

// NormalizeGitURL converts SCP-style git URLs to HTTPS if a credential helper
// is registered for that host. Only SCP-style shorthand is normalized
// (git@{host}:{path}) — ssh:// URLs are left as-is.
//
// Examples:
//
//	"git@github.com:rsong/mclaude"          → "https://github.com/rsong/mclaude"
//	"git@github.com:rsong/mclaude.git"      → "https://github.com/rsong/mclaude.git"
//	"ssh://git@github.com/rsong/mclaude"    → unchanged (ssh:// scheme)
//	"https://github.com/rsong/mclaude"      → unchanged (already HTTPS)
func NormalizeGitURL(rawURL string, registeredHosts map[string]bool) string {
	// Only normalize SCP-style: git@{host}:{path}
	// Must start with "git@" and contain ":" but not "://" (which indicates ssh://).
	if !strings.HasPrefix(rawURL, "git@") {
		return rawURL
	}
	if strings.Contains(rawURL, "://") {
		// This is ssh:// or similar — leave as-is.
		return rawURL
	}

	// Parse: git@{host}:{path}
	after := strings.TrimPrefix(rawURL, "git@")
	colonIdx := strings.Index(after, ":")
	if colonIdx < 0 {
		return rawURL
	}
	host := after[:colonIdx]
	path := after[colonIdx+1:]

	if !registeredHosts[host] {
		// No credential helper for this host — leave as-is.
		return rawURL
	}

	return "https://" + host + "/" + path
}

// ghHostsConfig represents the parsed structure of gh's hosts.yml.
// Format: map[hostname]hostEntry
type ghHostsConfig map[string]ghHostEntry

// ghHostEntry is a single host's configuration in gh's hosts.yml.
type ghHostEntry struct {
	// Users is the multi-account map (gh 2.40+). Key = username.
	Users map[string]ghUserEntry `yaml:"users,omitempty"`
	// User is the active (default) username for this host.
	User string `yaml:"user,omitempty"`
	// OAuthToken is only set on single-account configs (no Users map).
	OAuthToken string `yaml:"oauth_token,omitempty"`
	// GitProtocol is the git protocol setting.
	GitProtocol string `yaml:"git_protocol,omitempty"`
}

// ghUserEntry is a user account entry in the Users map.
type ghUserEntry struct {
	OAuthToken string `yaml:"oauth_token,omitempty"`
}

// glabHostsConfig represents the parsed structure of glab's config.yml.
type glabHostsConfig struct {
	Hosts map[string]glabHostEntry `yaml:"hosts,omitempty"`
}

// glabHostEntry is a single host's configuration in glab's config.yml.
type glabHostEntry struct {
	Token   string `yaml:"token,omitempty"`
	APIHost string `yaml:"api_host,omitempty"`
	User    string `yaml:"user,omitempty"`
}

// MergeGHHostsYAML merges managed gh hosts.yml (from Secret mount) into the
// existing gh hosts.yml (from PVC, which may have manual gh auth login entries).
//
// Strategy: for each host in managed, add/update managed accounts in existing.
// Do NOT remove accounts only in existing (preserves manual gh auth login).
// If a managed account and a manual account share the same username on the same
// host, the managed token wins (overwrite).
//
// Returns the merged YAML bytes.
func MergeGHHostsYAML(existing, managed []byte) ([]byte, error) {
	// Parse existing (may be empty/nil).
	existingCfg := make(ghHostsConfig)
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &existingCfg); err != nil {
			// If existing is unparseable, start fresh from managed.
			existingCfg = make(ghHostsConfig)
		}
	}

	// Parse managed.
	managedCfg := make(ghHostsConfig)
	if len(managed) > 0 {
		if err := yaml.Unmarshal(managed, &managedCfg); err != nil {
			return nil, fmt.Errorf("parse managed gh-hosts.yml: %w", err)
		}
	}

	// Merge: for each host in managed, upsert into existing.
	for host, managedEntry := range managedCfg {
		existingEntry, exists := existingCfg[host]
		if !exists {
			existingEntry = ghHostEntry{}
		}

		// Ensure the Users map exists in the existing entry.
		if existingEntry.Users == nil {
			existingEntry.Users = make(map[string]ghUserEntry)
		}

		// If the managed entry uses the multi-account Users map, merge account by account.
		if len(managedEntry.Users) > 0 {
			for username, userEntry := range managedEntry.Users {
				existingEntry.Users[username] = userEntry
			}
			// If managed sets a default user, propagate it (managed authority wins).
			if managedEntry.User != "" {
				existingEntry.User = managedEntry.User
			}
		} else if managedEntry.OAuthToken != "" && managedEntry.User != "" {
			// Single-account format: convert to multi-account in the merge.
			existingEntry.Users[managedEntry.User] = ghUserEntry{OAuthToken: managedEntry.OAuthToken}
			if existingEntry.User == "" {
				existingEntry.User = managedEntry.User
			}
		}

		// Preserve other fields from managed that may not be in existing.
		if managedEntry.GitProtocol != "" {
			existingEntry.GitProtocol = managedEntry.GitProtocol
		}

		existingCfg[host] = existingEntry
	}

	return yaml.Marshal(existingCfg)
}

// MergeGLabConfigYAML merges managed glab config.yml (from Secret mount) into
// the existing glab config.yml (from PVC).
//
// Strategy: for each host in managed, add/update in existing. glab is
// single-account per host (no multi-identity switching), so managed wins
// on conflict.
func MergeGLabConfigYAML(existing, managed []byte) ([]byte, error) {
	// Parse existing.
	existingCfg := glabHostsConfig{Hosts: make(map[string]glabHostEntry)}
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &existingCfg); err != nil {
			existingCfg = glabHostsConfig{Hosts: make(map[string]glabHostEntry)}
		}
		if existingCfg.Hosts == nil {
			existingCfg.Hosts = make(map[string]glabHostEntry)
		}
	}

	// Parse managed.
	managedCfg := glabHostsConfig{}
	if len(managed) > 0 {
		if err := yaml.Unmarshal(managed, &managedCfg); err != nil {
			return nil, fmt.Errorf("parse managed glab-config.yml: %w", err)
		}
	}

	// Merge: managed wins on per-host basis.
	for host, entry := range managedCfg.Hosts {
		existingCfg.Hosts[host] = entry
	}

	return yaml.Marshal(existingCfg)
}

// ReadSecretFile reads a file from the secret mount path.
// Returns nil (not an error) if the file does not exist.
func ReadSecretFile(name string) ([]byte, error) {
	path := filepath.Join(secretMountPath, name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

// CredentialManager manages gh/glab credential helper setup for the session-agent.
// It tracks the last-seen managed config content to avoid re-running setup-git
// when nothing has changed.
type CredentialManager struct {
	mu             sync.Mutex
	lastGHHosts    []byte // last merged content of gh-hosts.yml
	lastGLabConfig []byte // last merged content of glab-config.yml
	homeDir        string // $HOME, resolved once at construction
	log            zerolog.Logger
}

// NewCredentialManager creates a CredentialManager. homeDir is the process home directory.
func NewCredentialManager(homeDir string, log zerolog.Logger) *CredentialManager {
	return &CredentialManager{
		homeDir: homeDir,
		log:     log,
	}
}

// Setup runs the full credential helper setup sequence:
//  1. Symlink PVC config (/data/.config → ~/.config)
//  2. Merge managed tokens into CLI configs
//  3. Register credential helpers (gh auth setup-git, glab auth setup-git)
//  4. Switch to project identity (if GIT_IDENTITY_ID is set)
//
// It is safe to call Setup multiple times. Steps 2-4 are re-run only if the
// managed config content has changed since the last call.
func (cm *CredentialManager) Setup(gitIdentityID string) error {
	if err := cm.symlinkPVCConfig(); err != nil {
		return fmt.Errorf("symlink PVC config: %w", err)
	}

	changed, err := cm.mergeAndSetup()
	if err != nil {
		// Non-fatal: log and continue. Git operations fall back to SSH key auth.
		cm.log.Warn().Err(err).Msg("credential helper merge failed (non-fatal)")
	}

	if changed || gitIdentityID != "" {
		if switchErr := cm.switchProjectIdentity(gitIdentityID); switchErr != nil {
			// Non-fatal per spec: log warning, use default active account.
			cm.log.Warn().Err(switchErr).Str("gitIdentityID", gitIdentityID).
				Msg("gh auth switch failed — using default active account (non-fatal)")
		}
	}

	return nil
}

// RefreshIfChanged re-reads the managed configs from the Secret mount and
// re-runs merge + setup-git if the content has changed. Called before each
// git operation.
func (cm *CredentialManager) RefreshIfChanged(gitIdentityID string) error {
	changed, err := cm.mergeAndSetup()
	if err != nil {
		cm.log.Warn().Err(err).Msg("credential helper refresh failed (non-fatal)")
		return nil // non-fatal
	}
	if changed && gitIdentityID != "" {
		if switchErr := cm.switchProjectIdentity(gitIdentityID); switchErr != nil {
			cm.log.Warn().Err(switchErr).Str("gitIdentityID", gitIdentityID).
				Msg("gh auth switch failed after refresh (non-fatal)")
		}
	}
	return nil
}

// RegisteredHosts returns the set of hostnames for which credential helpers
// are currently registered (i.e. present in the merged gh-hosts.yml).
// Used for SSH→HTTPS URL normalization.
func (cm *CredentialManager) RegisteredHosts() map[string]bool {
	cm.mu.Lock()
	content := make([]byte, len(cm.lastGHHosts))
	copy(content, cm.lastGHHosts)
	cm.mu.Unlock()

	hosts := make(map[string]bool)
	if len(content) == 0 {
		return hosts
	}

	cfg := make(ghHostsConfig)
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return hosts
	}
	for host := range cfg {
		hosts[host] = true
	}
	return hosts
}

// symlinkPVCConfig implements step 1 of the session start sequence:
// Remove any pre-existing ~/.config/, create /data/.config/ if needed,
// then symlink /data/.config/ → ~/.config/.
func (cm *CredentialManager) symlinkPVCConfig() error {
	configHome := filepath.Join(cm.homeDir, ".config")

	// Remove any pre-existing ~/.config/ (safe: $HOME is an emptyDir, fresh each boot).
	if err := os.RemoveAll(configHome); err != nil {
		return fmt.Errorf("rm -rf %s: %w", configHome, err)
	}

	// Ensure /data/.config/ exists.
	if err := os.MkdirAll(configPVCPath, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", configPVCPath, err)
	}

	// Symlink /data/.config/ → ~/.config/.
	if err := os.Symlink(configPVCPath, configHome); err != nil {
		return fmt.Errorf("symlink %s → %s: %w", configPVCPath, configHome, err)
	}

	return nil
}

// mergeAndSetup reads managed configs from Secret mount, merges into ~/.config/,
// and runs gh/glab auth setup-git if content changed. Returns true if content changed.
func (cm *CredentialManager) mergeAndSetup() (bool, error) {
	// Read managed configs from Secret mount.
	managedGHHosts, err := ReadSecretFile("gh-hosts.yml")
	if err != nil {
		return false, fmt.Errorf("read gh-hosts.yml: %w", err)
	}
	managedGLabConfig, err := ReadSecretFile("glab-config.yml")
	if err != nil {
		return false, fmt.Errorf("read glab-config.yml: %w", err)
	}

	cm.mu.Lock()
	ghChanged := !equalBytes(cm.lastGHHosts, managedGHHosts)
	glabChanged := !equalBytes(cm.lastGLabConfig, managedGLabConfig)
	cm.mu.Unlock()

	if !ghChanged && !glabChanged {
		return false, nil
	}

	var mergeErr error

	// Merge gh-hosts.yml.
	if managedGHHosts != nil {
		ghConfigPath := filepath.Join(cm.homeDir, ".config", "gh", "hosts.yml")
		ghDir := filepath.Dir(ghConfigPath)

		if err := os.MkdirAll(ghDir, 0755); err == nil {
			// Write config.yml BEFORE hosts.yml so that gh 2.40+ sees
			// version: "1" and skips the D-Bus keyring migration on Alpine.
			ghMainConfig := filepath.Join(ghDir, "config.yml")
			if _, statErr := os.Stat(ghMainConfig); os.IsNotExist(statErr) {
				if writeErr := os.WriteFile(ghMainConfig, []byte("version: \"1\"\n"), 0600); writeErr != nil {
					cm.log.Warn().Err(writeErr).Str("path", ghMainConfig).Msg("write gh config.yml failed")
				}
			}

			existing, _ := os.ReadFile(ghConfigPath)
			merged, err := MergeGHHostsYAML(existing, managedGHHosts)
			if err != nil {
				mergeErr = err
			} else {
				if err := os.WriteFile(ghConfigPath, merged, 0600); err != nil {
					cm.log.Warn().Err(err).Str("path", ghConfigPath).Msg("write gh hosts.yml failed")
				}
			}
		}
	}

	// Merge glab-config.yml.
	if managedGLabConfig != nil {
		glabConfigPath := filepath.Join(cm.homeDir, ".config", "glab-cli", "config.yml")
		existing, _ := os.ReadFile(glabConfigPath)

		merged, err := MergeGLabConfigYAML(existing, managedGLabConfig)
		if err != nil {
			if mergeErr == nil {
				mergeErr = err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(glabConfigPath), 0755); err == nil {
				if err := os.WriteFile(glabConfigPath, merged, 0600); err != nil {
					cm.log.Warn().Err(err).Str("path", glabConfigPath).Msg("write glab config.yml failed")
				}
			}
		}
	}

	// Register credential helpers.
	// gh auth setup-git — registers gh as git's credential helper for all hosts.
	if ghSetupErr := runCmd("gh", "auth", "setup-git"); ghSetupErr != nil {
		cm.log.Warn().Err(ghSetupErr).Msg("gh auth setup-git failed (non-fatal)")
		// Non-fatal per spec.
	}

	// glab auth setup-git — same for GitLab hosts.
	if glabSetupErr := runCmd("glab", "auth", "setup-git"); glabSetupErr != nil {
		cm.log.Warn().Err(glabSetupErr).Msg("glab auth setup-git failed (non-fatal)")
		// Non-fatal per spec.
	}

	// Update last-seen content.
	cm.mu.Lock()
	cm.lastGHHosts = managedGHHosts
	cm.lastGLabConfig = managedGLabConfig
	cm.mu.Unlock()

	return true, mergeErr
}

// switchProjectIdentity switches gh to the correct account for this project.
// If gitIdentityID is empty, this is a no-op.
func (cm *CredentialManager) switchProjectIdentity(gitIdentityID string) error {
	if gitIdentityID == "" {
		return nil
	}

	// Read conn-{id}-username from Secret mount.
	username, err := cm.resolveUsername(gitIdentityID)
	if err != nil {
		return fmt.Errorf("resolve username for identity %s: %w", gitIdentityID, err)
	}
	if username == "" {
		return fmt.Errorf("no username found for identity %s in Secret mount", gitIdentityID)
	}

	// Find which host this username belongs to in gh-hosts.yml.
	host, err := cm.findHostForUsername(username)
	if err != nil {
		return fmt.Errorf("find host for username %s: %w", username, err)
	}
	if host == "" {
		return fmt.Errorf("username %s not found in any gh host config", username)
	}

	// Run: gh auth switch --user {username} --hostname {host}
	if err := runCmd("gh", "auth", "switch", "--user", username, "--hostname", host); err != nil {
		return fmt.Errorf("gh auth switch --user %s --hostname %s: %w", username, host, err)
	}

	cm.log.Info().
		Str("gitIdentityID", gitIdentityID).
		Str("username", username).
		Str("host", host).
		Msg("switched to project identity")

	return nil
}

// resolveUsername reads conn-{id}-username from the Secret mount.
func (cm *CredentialManager) resolveUsername(connectionID string) (string, error) {
	key := fmt.Sprintf("conn-%s-username", connectionID)
	data, err := ReadSecretFile(key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// findHostForUsername parses the merged gh-hosts.yml and returns the hostname
// that has the given username in its users map.
func (cm *CredentialManager) findHostForUsername(username string) (string, error) {
	ghConfigPath := filepath.Join(cm.homeDir, ".config", "gh", "hosts.yml")
	data, err := os.ReadFile(ghConfigPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", ghConfigPath, err)
	}

	cfg := make(ghHostsConfig)
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse gh hosts.yml: %w", err)
	}

	for host, entry := range cfg {
		// Check multi-account Users map.
		if _, ok := entry.Users[username]; ok {
			return host, nil
		}
		// Check single-account User field.
		if entry.User == username {
			return host, nil
		}
	}

	return "", nil
}

// InitRepo performs the initial git repository setup that previously lived in
// entrypoint.sh. It handles two cases:
//   - GIT_URL is set: clone --bare into /data/repo
//   - GIT_URL is empty: init a bare repo with an initial empty commit
//
// If /data/repo already exists (pod restart), it fetches from GIT_URL if set.
// The credMgr is used to refresh credentials before the clone operation.
func InitRepo(dataDir, gitURL, gitIdentityID string, credMgr *CredentialManager, log zerolog.Logger) error {
	repoPath := filepath.Join(dataDir, "repo")
	worktreesPath := filepath.Join(dataDir, "worktrees")

	// Check if bare repo already exists.
	headPath := filepath.Join(repoPath, "HEAD")
	_, headErr := os.Stat(headPath)
	repoExists := headErr == nil

	if repoExists {
		// Repo already initialized — fetch if GIT_URL is set.
		if gitURL != "" {
			// Refresh credentials before fetch.
			if credMgr != nil {
				normalized := NormalizeGitURL(gitURL, credMgr.RegisteredHosts())
				_ = normalized // fetch uses the stored URL; credential helper handles auth
				_ = credMgr.RefreshIfChanged(gitIdentityID)
			}
			cmd := exec.Command("git", "-C", repoPath, "fetch", "--all", "--prune")
			if out, err := cmd.CombinedOutput(); err != nil {
				// Fetch failure is non-fatal (matches original entrypoint.sh `|| true`).
				log.Warn().Err(err).Str("output", string(out)).Msg("git fetch failed (non-fatal)")
			}
		}
	} else {
		if gitURL != "" {
			// Normalize URL before clone.
			cloneURL := gitURL
			if credMgr != nil {
				_ = credMgr.RefreshIfChanged(gitIdentityID)
				cloneURL = NormalizeGitURL(gitURL, credMgr.RegisteredHosts())
			}

			log.Info().Str("url", cloneURL).Msg("cloning bare repo")
			cmd := exec.Command("git", "clone", "--bare", cloneURL, repoPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Check for auth error — publish provider_auth_failed if so.
				exitCode := 0
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
				if IsGitAuthError(exitCode, string(out)) {
					return &GitAuthError{URL: cloneURL, Stderr: string(out)}
				}
				return fmt.Errorf("git clone --bare %s: %w: %s", cloneURL, err, out)
			}
		} else {
			// Scratch project: init bare repo with empty initial commit.
			if err := initScratchRepo(repoPath, log); err != nil {
				return fmt.Errorf("init scratch repo: %w", err)
			}
		}
	}

	// Ensure worktrees directory exists.
	if err := os.MkdirAll(worktreesPath, 0755); err != nil {
		return fmt.Errorf("mkdir worktrees: %w", err)
	}

	return nil
}

// GitAuthError is returned when a git operation fails due to authentication.
// The caller should publish a session_failed event with reason provider_auth_failed.
type GitAuthError struct {
	URL    string
	Stderr string
}

func (e *GitAuthError) Error() string {
	return fmt.Sprintf("git auth failed for %s: %s", e.URL, e.Stderr)
}

// initScratchRepo initializes a bare git repo with an initial empty commit.
// This mirrors the bash logic previously in entrypoint.sh for scratch projects.
func initScratchRepo(repoPath string, log zerolog.Logger) error {
	log.Info().Str("path", repoPath).Msg("initializing scratch bare repo")

	if err := runCmd("git", "init", "--bare", repoPath); err != nil {
		return fmt.Errorf("git init --bare: %w", err)
	}

	// Set default branch to main.
	if err := runCmd("git", "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return fmt.Errorf("set default branch: %w", err)
	}

	// Create initial empty commit using plumbing commands (no working tree needed).
	// Step 1: create an empty tree object.
	treeHashCmd := exec.Command("git", "-C", repoPath, "hash-object", "-t", "tree", "/dev/null")
	treeHashOut, err := treeHashCmd.Output()
	if err != nil {
		return fmt.Errorf("hash-object tree: %w", err)
	}
	treeHash := strings.TrimSpace(string(treeHashOut))

	// Step 2: create a commit object pointing to the empty tree.
	commitCmd := exec.Command("git", "-C", repoPath, "commit-tree", treeHash, "-m", "init")
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=mclaude",
		"GIT_AUTHOR_EMAIL=mclaude@local",
		"GIT_COMMITTER_NAME=mclaude",
		"GIT_COMMITTER_EMAIL=mclaude@local",
		fmt.Sprintf("GIT_AUTHOR_DATE=%d +0000", time.Now().Unix()),
		fmt.Sprintf("GIT_COMMITTER_DATE=%d +0000", time.Now().Unix()),
	)
	commitHashOut, err := commitCmd.Output()
	if err != nil {
		return fmt.Errorf("commit-tree: %w", err)
	}
	commitHash := strings.TrimSpace(string(commitHashOut))

	// Step 3: point refs/heads/main at the new commit.
	if err := runCmd("git", "-C", repoPath, "update-ref", "refs/heads/main", commitHash); err != nil {
		return fmt.Errorf("update-ref: %w", err)
	}

	return nil
}

// RunGitOpWithCredsRefresh refreshes credentials before a git operation and
// normalizes the URL. It then runs the provided git command builder with the
// (possibly normalized) URL.
//
// If the git command fails with an auth error, it returns a *GitAuthError.
// The caller should publish session_failed with reason provider_auth_failed.
func RunGitOpWithCredsRefresh(rawURL, gitIdentityID string, credMgr *CredentialManager, log zerolog.Logger, runGit func(url string) ([]byte, int, error)) error {
	if credMgr != nil {
		_ = credMgr.RefreshIfChanged(gitIdentityID)
	}

	effectiveURL := rawURL
	if credMgr != nil {
		effectiveURL = NormalizeGitURL(rawURL, credMgr.RegisteredHosts())
	}

	out, exitCode, err := runGit(effectiveURL)
	if err != nil {
		if IsGitAuthError(exitCode, string(out)) {
			return &GitAuthError{URL: effectiveURL, Stderr: string(out)}
		}
		return fmt.Errorf("git operation failed (exit %d): %w: %s", exitCode, err, out)
	}
	return nil
}

// runCmd runs a command and returns an error if it exits non-zero.
// stdout and stderr are discarded (errors are returned).
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// equalBytes returns true if two byte slices have identical content.
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
