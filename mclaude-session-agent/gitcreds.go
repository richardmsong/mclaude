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

// ConvertGHHostsToOldFormat converts a managed gh-hosts.yml (multi-account
// users: format from the K8s Secret) to old single-account format suitable
// for writing to ~/.config/gh/hosts.yml on Alpine (no D-Bus keyring).
//
// Per host logic:
//   - If activeUsername is non-empty AND the host equals activeHost AND the host
//     contains that username in its users: map, use that account's token.
//   - Otherwise use the user: default account's token.
//   - Always write git_protocol: https.
//   - If a host has no users: map (already old format), pass through as-is
//     but still set git_protocol: https.
//
// The activeUsername and activeHost parameters come from resolving
// GIT_IDENTITY_ID → conn-{id}-username → which host has that username in managed.
// Both may be empty (no identity binding), in which case the user: default is
// used for every host.
func ConvertGHHostsToOldFormat(managed []byte, activeUsername, activeHost string) ([]byte, error) {
	if len(managed) == 0 {
		return nil, nil
	}

	managedCfg := make(ghHostsConfig)
	if err := yaml.Unmarshal(managed, &managedCfg); err != nil {
		return nil, fmt.Errorf("parse managed gh-hosts.yml: %w", err)
	}

	result := make(ghHostsConfig)
	for host, entry := range managedCfg {
		var token string
		var selectedUser string

		if len(entry.Users) > 0 {
			// Multi-account format: pick the active account's token.
			if activeUsername != "" && host == activeHost {
				// Use the identity-bound account's token for this host.
				if u, ok := entry.Users[activeUsername]; ok {
					token = u.OAuthToken
					selectedUser = activeUsername
				}
			}
			if token == "" && entry.User != "" {
				// Fall back to the user: default account.
				if u, ok := entry.Users[entry.User]; ok {
					token = u.OAuthToken
					selectedUser = entry.User
				}
			}
			if token == "" {
				// Last resort: pick the first user (map iteration order is random;
				// determinism is best-effort here since this path means the config
				// has no user: field and no matching identity).
				for username, u := range entry.Users {
					token = u.OAuthToken
					selectedUser = username
					break
				}
			}
		} else {
			// Already old format (oauth_token at root level) — pass through.
			token = entry.OAuthToken
			selectedUser = entry.User
		}

		result[host] = ghHostEntry{
			OAuthToken:  token,
			User:        selectedUser,
			GitProtocol: "https",
		}
	}

	return yaml.Marshal(result)
}

// findHostForUsernameInManaged looks up which host in the managed config (multi-account
// format) contains the given username in its users: map or User field.
// Returns empty string if not found.
func findHostForUsernameInManaged(managedCfg ghHostsConfig, username string) string {
	for host, entry := range managedCfg {
		if _, ok := entry.Users[username]; ok {
			return host
		}
		// Also check old-format User field (no users: map).
		if entry.User == username && len(entry.Users) == 0 {
			return host
		}
	}
	return ""
}

// MergeGHHostsYAML merges managed gh hosts.yml (already converted to old
// single-account format) into the existing gh hosts.yml (from PVC, which may
// have manual gh auth login entries, also in old format).
//
// Strategy: for each host in managed, write/overwrite in existing.
// Do NOT remove hosts only in existing (preserves manual gh auth login).
// If a managed host and a manual entry share the same host, the managed token wins.
//
// Both existing and managed are expected to be in old single-account format
// (oauth_token at root level, no users: map). If existing contains multi-account
// users: entries (e.g. from a prior manual gh auth login), they are preserved
// for hosts not covered by managed — but managed always wins per host.
//
// Returns the merged YAML bytes in old format.
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

	// Merge: managed wins per host.
	for host, managedEntry := range managedCfg {
		existingCfg[host] = managedEntry
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
//  2. Merge managed tokens into CLI configs (with format conversion + identity selection)
//  3. Register credential helpers (gh auth setup-git, glab auth setup-git)
//
// Identity selection happens inside mergeAndSetup via the gitIdentityID parameter.
// No gh auth switch call is made — the old-format hosts.yml already contains the
// correct account's token per host.
//
// It is safe to call Setup multiple times. Steps 2-3 are re-run only if the
// managed config content has changed since the last call.
func (cm *CredentialManager) Setup(gitIdentityID string) error {
	if err := cm.symlinkPVCConfig(); err != nil {
		return fmt.Errorf("symlink PVC config: %w", err)
	}

	if _, err := cm.mergeAndSetup(gitIdentityID); err != nil {
		// Non-fatal: log and continue. Git operations fall back to SSH key auth.
		cm.log.Warn().Err(err).Msg("credential helper merge failed (non-fatal)")
	}

	return nil
}

// RefreshIfChanged re-reads the managed configs from the Secret mount and
// re-runs merge + setup-git if the content has changed. Called before each
// git operation.
//
// Identity selection (gitIdentityID) is applied during the merge step, not via
// gh auth switch.
func (cm *CredentialManager) RefreshIfChanged(gitIdentityID string) error {
	if _, err := cm.mergeAndSetup(gitIdentityID); err != nil {
		cm.log.Warn().Err(err).Msg("credential helper refresh failed (non-fatal)")
		return nil // non-fatal
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

// mergeAndSetup reads managed configs from Secret mount, converts gh-hosts.yml
// from multi-account format to old single-account format (with identity selection),
// merges into ~/.config/, and runs gh/glab auth setup-git if content changed.
// Returns true if content changed.
//
// Identity selection: if gitIdentityID is non-empty, resolves it to a username
// and host via the Secret mount, then selects that account's token when converting
// the managed gh-hosts.yml for that host. All other hosts use the user: default.
func (cm *CredentialManager) mergeAndSetup(gitIdentityID string) (bool, error) {
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

	// Ensure ~/.config/gh/ exists and config.yml is present unconditionally.
	// This guarantees gh 2.40+ sees version: "1" and skips the D-Bus keyring
	// migration on Alpine even when no managed gh-hosts.yml is present.
	ghDir := filepath.Join(cm.homeDir, ".config", "gh")
	if err := os.MkdirAll(ghDir, 0755); err == nil {
		ghMainConfig := filepath.Join(ghDir, "config.yml")
		if _, statErr := os.Stat(ghMainConfig); os.IsNotExist(statErr) {
			if writeErr := os.WriteFile(ghMainConfig, []byte("version: \"1\"\n"), 0600); writeErr != nil {
				cm.log.Warn().Err(writeErr).Str("path", ghMainConfig).Msg("write gh config.yml failed")
			}
		}
	}

	// Merge gh-hosts.yml.
	if managedGHHosts != nil {
		ghConfigPath := filepath.Join(ghDir, "hosts.yml")

		if err := os.MkdirAll(ghDir, 0755); err == nil {
			// Parse the managed multi-account config so we can resolve the identity.
			managedCfg := make(ghHostsConfig)
			if parseErr := yaml.Unmarshal(managedGHHosts, &managedCfg); parseErr != nil {
				mergeErr = fmt.Errorf("parse managed gh-hosts.yml: %w", parseErr)
			} else {
				// Resolve GIT_IDENTITY_ID → (username, host) from the managed config.
				activeUsername, activeHost, resolveErr := cm.resolveIdentityFromManaged(gitIdentityID, managedCfg)
				if resolveErr != nil {
					// Non-fatal: log and use defaults.
					cm.log.Warn().Err(resolveErr).Str("gitIdentityID", gitIdentityID).
						Msg("identity resolution failed — using default account (non-fatal)")
				}

				// Convert managed multi-account format → old single-account format.
				convertedManaged, convertErr := ConvertGHHostsToOldFormat(managedGHHosts, activeUsername, activeHost)
				if convertErr != nil {
					mergeErr = convertErr
				} else {
					// Merge converted managed (old format) with existing (old format from PVC).
					existing, _ := os.ReadFile(ghConfigPath)
					merged, mergeErrInner := MergeGHHostsYAML(existing, convertedManaged)
					if mergeErrInner != nil {
						mergeErr = mergeErrInner
					} else {
						if writeErr := os.WriteFile(ghConfigPath, merged, 0600); writeErr != nil {
							cm.log.Warn().Err(writeErr).Str("path", ghConfigPath).Msg("write gh hosts.yml failed")
						}
					}
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

	// Update last-seen raw managed content (for change detection on next call).
	cm.mu.Lock()
	cm.lastGHHosts = managedGHHosts
	cm.lastGLabConfig = managedGLabConfig
	cm.mu.Unlock()

	return true, mergeErr
}

// resolveIdentityFromManaged resolves GIT_IDENTITY_ID to (username, host) by:
//  1. Reading conn-{id}-username from the Secret mount.
//  2. Finding which host in the managed config has that username.
//
// Returns ("", "", nil) when gitIdentityID is empty or the username key is missing.
func (cm *CredentialManager) resolveIdentityFromManaged(gitIdentityID string, managedCfg ghHostsConfig) (username, host string, err error) {
	if gitIdentityID == "" {
		return "", "", nil
	}

	key := fmt.Sprintf("conn-%s-username", gitIdentityID)
	data, readErr := ReadSecretFile(key)
	if readErr != nil {
		return "", "", fmt.Errorf("read %s: %w", key, readErr)
	}
	if len(data) == 0 {
		// Key missing — fall back to default (non-fatal).
		cm.log.Warn().Str("gitIdentityID", gitIdentityID).Msg("conn-username key not found in Secret mount — using default account")
		return "", "", nil
	}

	resolvedUsername := strings.TrimSpace(string(data))
	resolvedHost := findHostForUsernameInManaged(managedCfg, resolvedUsername)
	if resolvedHost == "" {
		cm.log.Warn().
			Str("gitIdentityID", gitIdentityID).
			Str("username", resolvedUsername).
			Msg("username not found in any managed gh host — using default account")
		return "", "", nil
	}

	return resolvedUsername, resolvedHost, nil
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
