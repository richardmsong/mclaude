package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"mclaude.io/common/pkg/slug"
)

const (
	// agentRegisterRetries is the maximum number of attempts to register an
	// agent's NKey public key with the control-plane. Retries handle the race
	// where the project fan-out message arrives before CP has committed the
	// project row to Postgres (ADR-0058).
	agentRegisterRetries = 10
	// agentRegisterInitDelay is the initial exponential backoff delay.
	agentRegisterInitDelay = 100 * time.Millisecond
	// agentRegisterMaxDelay is the maximum backoff interval.
	agentRegisterMaxDelay = 5 * time.Second
	// agentNKeyReadTimeout is how long to wait for the agent to write its NKey
	// public key file before giving up.
	agentNKeyReadTimeout = 15 * time.Second
	// agentNKeyPollInterval is how often to poll for the NKey file.
	agentNKeyPollInterval = 200 * time.Millisecond
)

// childKey uniquely identifies a supervised child across multiple users.
// Format: "{uslug}:{pslug}".
type childKey struct {
	userSlug    string
	projectSlug string
}

func (k childKey) String() string {
	return k.userSlug + ":" + k.projectSlug
}

// Controller is the BYOH machine controller — manages session-agent
// subprocesses for provisioned projects via process supervision.
// Per ADR-0054, it subscribes to the host-scoped subject scheme
// (mclaude.hosts.{hslug}.>) and handles project create/delete fan-out
// messages from the control-plane. Zero JetStream access.
type Controller struct {
	nc       *nats.Conn
	hostSlug slug.HostSlug
	dataDir  string
	cpURL    string // CP HTTP URL for agent NKey registration (optional)
	log      zerolog.Logger

	mu       sync.Mutex
	children map[childKey]*supervisedChild
}

// ProvisionRequest is the payload for project provisioning NATS requests.
// Per ADR-0054/0058, userSlug and projectSlug are extracted from the subject;
// the payload carries additional context (git URL, identity ID).
type ProvisionRequest struct {
	UserID        string `json:"userID"`
	UserSlug      string `json:"userSlug"`
	HostSlug      string `json:"hostSlug"`
	ProjectID     string `json:"projectID"`
	ProjectSlug   string `json:"projectSlug"`
	GitURL        string `json:"gitUrl"`
	GitIdentityID string `json:"gitIdentityId"`
}

// ProvisionReply is the response to a project provisioning NATS request.
type ProvisionReply struct {
	OK          bool   `json:"ok"`
	ProjectSlug string `json:"projectSlug,omitempty"`
	Error       string `json:"error,omitempty"`
	Code        string `json:"code,omitempty"`
}

// agentRegisterRequest is the payload for registering an agent's NKey public
// key with the control-plane via mclaude.hosts.{hslug}.api.agents.register.
type agentRegisterRequest struct {
	UserSlug    string `json:"user_slug"`
	HostSlug    string `json:"host_slug"`
	ProjectSlug string `json:"project_slug"`
	NKeyPublic  string `json:"nkey_public"`
}

// agentRegisterReply is the response from the control-plane agent registration.
type agentRegisterReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

// NewController creates a BYOH machine controller.
// cpURL may be empty — agent NKey registration via NATS still works (the host
// JWT has mclaude.hosts.{hslug}.> in Pub.Allow), but the host credential
// refresh loop requires cpURL for HTTP challenge-response.
func NewController(nc *nats.Conn, hostSlug slug.HostSlug, dataDir, cpURL string, log zerolog.Logger) *Controller {
	return &Controller{
		nc:       nc,
		hostSlug: hostSlug,
		dataDir:  dataDir,
		cpURL:    cpURL,
		log:      log,
		children: make(map[childKey]*supervisedChild),
	}
}

// subscriptionSubject returns the wildcard NATS subject for all host-scoped
// project lifecycle and API messages:
//
//	mclaude.hosts.{hslug}.>
//
// Per ADR-0054, the host controller subscribes with a single wildcard that
// captures all project lifecycle messages for any user with access to this host.
func (c *Controller) subscriptionSubject() string {
	return "mclaude.hosts." + string(c.hostSlug) + ".>"
}

// agentRegisterSubject returns the NATS subject for registering agent NKey
// credentials with the control-plane:
//
//	mclaude.hosts.{hslug}.api.agents.register
func (c *Controller) agentRegisterSubject() string {
	return "mclaude.hosts." + string(c.hostSlug) + ".api.agents.register"
}

// Run subscribes to the host-scoped project API subjects and blocks until
// ctx is cancelled. On cancellation, gracefully shuts down all children.
func (c *Controller) Run(ctx context.Context) error {
	subject := c.subscriptionSubject()
	sub, err := c.nc.Subscribe(subject, c.handleMessage)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	defer sub.Unsubscribe()

	c.log.Info().Str("subject", subject).Msg("subscribed to host-scoped project API")

	<-ctx.Done()
	c.shutdownChildren()
	return nil
}

// handleMessage routes incoming NATS messages based on the subject pattern.
// Project lifecycle messages have the form:
//
//	mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create|delete}
func (c *Controller) handleMessage(msg *nats.Msg) {
	tokens := strings.Split(msg.Subject, ".")
	// Expected: mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{action}
	//           0        1      2        3      4         5      6      7
	if len(tokens) == 8 &&
		tokens[0] == "mclaude" &&
		tokens[1] == "hosts" &&
		tokens[3] == "users" &&
		tokens[5] == "projects" {

		uslug := tokens[4]
		pslug := tokens[6]
		action := tokens[7]

		switch action {
		case "create":
			c.handleCreate(msg, uslug, pslug)
		case "delete":
			c.handleDelete(msg, uslug, pslug)
		default:
			c.log.Warn().Str("subject", msg.Subject).Str("action", action).Msg("unknown project action")
			c.reply(msg, ProvisionReply{
				OK:    false,
				Error: "unknown action: " + action,
				Code:  "unknown_action",
			})
		}
		return
	}

	// Other host-scoped messages (e.g. api.agents.register replies) are
	// handled by the NATS client's request/reply mechanism internally.
	c.log.Debug().Str("subject", msg.Subject).Msg("ignoring non-project-lifecycle message")
}

// handleCreate materializes the project worktree directory and starts
// a session-agent subprocess for the project. After starting the agent,
// it reads the agent's NKey public key via local IPC and registers it
// with the control-plane (ADR-0058).
func (c *Controller) handleCreate(msg *nats.Msg, uslug, pslug string) {
	// Parse optional payload for git URL and identity.
	var req ProvisionRequest
	if len(msg.Data) > 0 {
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			c.log.Warn().Err(err).Msg("failed to parse create request payload")
			c.reply(msg, ProvisionReply{
				OK:    false,
				Error: "invalid request payload",
				Code:  "invalid_payload",
			})
			return
		}
	}

	// Validate slugs from subject (subject is authoritative per ADR-0054).
	if err := slug.Validate(uslug); err != nil {
		c.reply(msg, ProvisionReply{OK: false, Error: "invalid user slug in subject", Code: "invalid_payload"})
		return
	}
	if err := slug.Validate(pslug); err != nil {
		c.reply(msg, ProvisionReply{OK: false, Error: "invalid project slug in subject", Code: "invalid_payload"})
		return
	}

	key := childKey{userSlug: uslug, projectSlug: pslug}
	c.log.Info().Str("user", uslug).Str("project", pslug).Msg("provisioning project")

	// Project data directory: {dataDir}/{uslug}/{pslug}/
	projectDir := filepath.Join(c.dataDir, uslug, pslug)
	worktreeDir := filepath.Join(projectDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		c.log.Error().Err(err).Str("dir", worktreeDir).Msg("failed to create worktree directory")
		c.reply(msg, ProvisionReply{
			OK:          false,
			ProjectSlug: pslug,
			Error:       fmt.Sprintf("mkdir: %v", err),
			Code:        "filesystem_error",
		})
		return
	}

	// Check if session-agent is already running for this project.
	c.mu.Lock()
	if _, exists := c.children[key]; exists {
		c.mu.Unlock()
		c.log.Info().Str("user", uslug).Str("project", pslug).Msg("session-agent already running")
		c.reply(msg, ProvisionReply{OK: true, ProjectSlug: pslug})
		return
	}
	c.mu.Unlock()

	// Git clone if URL provided and worktree doesn't already have a repo (idempotent).
	// Per the spec, if the clone fails we return git_clone_failed immediately.
	if req.GitURL != "" {
		gitDir := filepath.Join(worktreeDir, ".git")
		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			c.log.Info().Str("git_url", req.GitURL).Str("dir", worktreeDir).Msg("cloning git repository")
			cloneCmd := exec.Command("git", "clone", req.GitURL, worktreeDir)
			cloneCmd.Stdout = os.Stdout
			cloneCmd.Stderr = os.Stderr
			if err := cloneCmd.Run(); err != nil {
				c.log.Error().Err(err).Str("git_url", req.GitURL).Msg("git clone failed")
				c.reply(msg, ProvisionReply{
					OK:          false,
					ProjectSlug: pslug,
					Error:       fmt.Sprintf("git clone: %v", err),
					Code:        "git_clone_failed",
				})
				return
			}
		}
	}

	// NKey public key file path — well-known path for IPC (ADR-0058).
	nkeyPubFile := filepath.Join(projectDir, ".nkey-pub")
	// Remove stale file from a previous run.
	_ = os.Remove(nkeyPubFile)

	// Start session-agent subprocess with process supervision.
	child := c.startChild(pslug, uslug, worktreeDir, nkeyPubFile)
	c.mu.Lock()
	c.children[key] = child
	c.mu.Unlock()

	// Read agent NKey public key via local IPC (ADR-0058).
	// The agent writes its public key to nkeyPubFile on startup.
	nkeyPublic, err := c.waitForNKeyFile(nkeyPubFile)
	if err != nil {
		c.log.Warn().Err(err).Str("project", pslug).Msg("failed to read agent NKey public key — skipping registration")
		// Provisioning continues; the agent may retry registration on its own.
	} else {
		// Register the agent's NKey public key with the control-plane (ADR-0058).
		if regErr := c.registerAgentKey(uslug, pslug, nkeyPublic); regErr != nil {
			c.log.Warn().Err(regErr).Str("project", pslug).Msg("agent NKey registration failed")
			// Non-fatal: the agent can retry authentication later.
		} else {
			c.log.Info().Str("project", pslug).Str("nkey_public", nkeyPublic).Msg("agent NKey registered with CP")
		}
	}

	c.reply(msg, ProvisionReply{OK: true, ProjectSlug: pslug})
}

// waitForNKeyFile polls for the NKey public key file written by the agent
// via local IPC. Returns the public key string on success, or an error if
// the file doesn't appear within agentNKeyReadTimeout.
func (c *Controller) waitForNKeyFile(path string) (string, error) {
	deadline := time.Now().Add(agentNKeyReadTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			// The file contains the public key followed by a newline.
			return strings.TrimSpace(string(data)), nil
		}
		time.Sleep(agentNKeyPollInterval)
	}
	return "", fmt.Errorf("timed out waiting for NKey file at %s after %v", path, agentNKeyReadTimeout)
}

// registerAgentKey registers the agent's NKey public key with the control-plane
// via NATS request/reply on mclaude.hosts.{hslug}.api.agents.register.
// Retries with exponential backoff on NOT_FOUND (race: project provisioning
// fan-out may arrive before CP has committed the project row to Postgres).
func (c *Controller) registerAgentKey(uslug, pslug, nkeyPublic string) error {
	reqBody := agentRegisterRequest{
		UserSlug:    uslug,
		HostSlug:    string(c.hostSlug),
		ProjectSlug: pslug,
		NKeyPublic:  nkeyPublic,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal agent register request: %w", err)
	}

	subject := c.agentRegisterSubject()
	delay := agentRegisterInitDelay

	for attempt := 1; attempt <= agentRegisterRetries; attempt++ {
		msg, err := c.nc.Request(subject, data, 5*time.Second)
		if err != nil {
			c.log.Warn().Err(err).Int("attempt", attempt).Msg("agent register NATS request failed")
			time.Sleep(delay)
			delay = minDuration(delay*2, agentRegisterMaxDelay)
			continue
		}

		var reply agentRegisterReply
		if err := json.Unmarshal(msg.Data, &reply); err != nil {
			return fmt.Errorf("unmarshal agent register reply: %w", err)
		}

		if reply.OK {
			return nil
		}

		// Retry on NOT_FOUND — CP may not have processed the project create yet.
		if reply.Code == "NOT_FOUND" {
			c.log.Debug().Int("attempt", attempt).Str("project", pslug).Msg("agent register: project not found — retrying")
			time.Sleep(delay)
			delay = minDuration(delay*2, agentRegisterMaxDelay)
			continue
		}

		// Any other error (FORBIDDEN, etc.) is terminal.
		return fmt.Errorf("agent register rejected: %s (code: %s)", reply.Error, reply.Code)
	}

	return fmt.Errorf("agent register failed after %d attempts (last code: NOT_FOUND)", agentRegisterRetries)
}

// handleDelete stops the session-agent subprocess and removes the project
// directory. Idempotent: if the project is already gone, replies success.
func (c *Controller) handleDelete(msg *nats.Msg, uslug, pslug string) {
	key := childKey{userSlug: uslug, projectSlug: pslug}
	c.log.Info().Str("user", uslug).Str("project", pslug).Msg("deleting project")

	// Stop child if running.
	c.mu.Lock()
	child, exists := c.children[key]
	if exists {
		delete(c.children, key)
	}
	c.mu.Unlock()

	if exists {
		child.stop()
	}

	// Remove project directory. Idempotent: if already gone, succeed.
	projectDir := filepath.Join(c.dataDir, uslug, pslug)
	if err := os.RemoveAll(projectDir); err != nil && !os.IsNotExist(err) {
		c.log.Error().Err(err).Str("dir", projectDir).Msg("failed to remove project directory")
		c.reply(msg, ProvisionReply{
			OK:          false,
			ProjectSlug: pslug,
			Error:       fmt.Sprintf("rmdir: %v", err),
			Code:        "filesystem_error",
		})
		return
	}

	c.reply(msg, ProvisionReply{OK: true, ProjectSlug: pslug})
}

// reply publishes a JSON reply to a NATS request message.
// If the message has no reply-to subject (fan-out, not request/reply), reply
// is a no-op.
func (c *Controller) reply(msg *nats.Msg, r ProvisionReply) {
	if msg.Reply == "" {
		return
	}
	data, err := json.Marshal(r)
	if err != nil {
		c.log.Error().Err(err).Msg("failed to marshal reply")
		return
	}
	if err := msg.Respond(data); err != nil {
		c.log.Error().Err(err).Msg("failed to send reply")
	}
}

// shutdownChildren gracefully stops all running session-agent subprocesses.
func (c *Controller) shutdownChildren() {
	c.mu.Lock()
	children := make(map[childKey]*supervisedChild, len(c.children))
	for k, v := range c.children {
		children[k] = v
	}
	c.mu.Unlock()

	for key, child := range children {
		c.log.Info().Str("user", key.userSlug).Str("project", key.projectSlug).Msg("stopping session-agent")
		child.stop()
	}
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
