package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"mclaude.io/common/pkg/slug"
)

// Controller is the BYOH machine controller — manages session-agent
// subprocesses for provisioned projects via process supervision.
type Controller struct {
	nc       *nats.Conn
	userSlug slug.UserSlug
	hostSlug slug.HostSlug
	dataDir  string
	log      zerolog.Logger

	mu       sync.Mutex
	children map[string]*supervisedChild // keyed by projectSlug
}

// ProvisionRequest is the payload for project provisioning NATS requests.
// Shared by both controller-k8s and controller-local per ADR-0035.
type ProvisionRequest struct {
	UserSlug      string `json:"userSlug"`
	HostSlug      string `json:"hostSlug"`
	ProjectSlug   string `json:"projectSlug"`
	GitURL        string `json:"gitUrl"`
	GitIdentityID string `json:"gitIdentityId"`
}

// ProvisionReply is the response to a project provisioning NATS request.
// Shared by both controller-k8s and controller-local per ADR-0035.
type ProvisionReply struct {
	OK          bool   `json:"ok"`
	ProjectSlug string `json:"projectSlug,omitempty"`
	Error       string `json:"error,omitempty"`
	Code        string `json:"code,omitempty"`
}

// NewController creates a BYOH machine controller.
func NewController(nc *nats.Conn, userSlug slug.UserSlug, hostSlug slug.HostSlug, dataDir string, log zerolog.Logger) *Controller {
	return &Controller{
		nc:       nc,
		userSlug: userSlug,
		hostSlug: hostSlug,
		dataDir:  dataDir,
		log:      log,
		children: make(map[string]*supervisedChild),
	}
}

// subjectPrefix returns the NATS subject prefix for this controller's
// host-scoped project API:
//
//	mclaude.users.{uslug}.hosts.{hslug}.api.projects.
//
// Per ADR-0035, the local controller subscribes to its own user/host only
// (no wildcard at the user level — that's controller-k8s).
func (c *Controller) subjectPrefix() string {
	return "mclaude.users." + string(c.userSlug) + ".hosts." + string(c.hostSlug) + ".api.projects."
}

// Run subscribes to the host-scoped project API subjects and blocks until
// ctx is cancelled. On cancellation, gracefully shuts down all children.
func (c *Controller) Run(ctx context.Context) error {
	// Subscribe to mclaude.users.{uslug}.hosts.{hslug}.api.projects.>
	subject := c.subjectPrefix() + ">"
	sub, err := c.nc.Subscribe(subject, c.handleProjectRequest)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	defer sub.Unsubscribe()

	c.log.Info().Str("subject", subject).Msg("subscribed to project API")

	<-ctx.Done()
	c.shutdownChildren()
	return nil
}

// handleProjectRequest routes incoming NATS requests to the appropriate handler
// based on the action suffix extracted from the subject.
func (c *Controller) handleProjectRequest(msg *nats.Msg) {
	prefix := c.subjectPrefix()
	if len(msg.Subject) <= len(prefix) {
		c.log.Warn().Str("subject", msg.Subject).Msg("unexpected subject (no action)")
		return
	}
	action := msg.Subject[len(prefix):]

	switch action {
	case "provision", "create":
		c.handleProvision(msg)
	case "delete":
		c.handleDelete(msg)
	case "update":
		c.handleUpdate(msg)
	default:
		c.log.Warn().Str("action", action).Msg("unknown project action")
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "unknown action: " + action,
			Code:  "unknown_action",
		})
	}
}

// handleProvision materializes the project worktree directory and starts
// a session-agent subprocess for the project.
func (c *Controller) handleProvision(msg *nats.Msg) {
	var req ProvisionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		c.log.Warn().Err(err).Msg("failed to parse provision request")
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "invalid request payload",
			Code:  "invalid_payload",
		})
		return
	}

	if req.ProjectSlug == "" {
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "projectSlug required",
			Code:  "invalid_payload",
		})
		return
	}
	pslug := req.ProjectSlug

	c.log.Info().Str("project", pslug).Msg("provisioning project")

	// Create project worktree directory.
	worktreeDir := filepath.Join(c.dataDir, pslug, "worktree")
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
	if _, exists := c.children[pslug]; exists {
		c.mu.Unlock()
		c.log.Info().Str("project", pslug).Msg("session-agent already running for project")
		c.reply(msg, ProvisionReply{OK: true, ProjectSlug: pslug})
		return
	}
	c.mu.Unlock()

	// Start session-agent subprocess with process supervision.
	child := c.startChild(pslug, worktreeDir)
	c.mu.Lock()
	c.children[pslug] = child
	c.mu.Unlock()

	c.reply(msg, ProvisionReply{OK: true, ProjectSlug: pslug})
}

// handleDelete stops the session-agent subprocess and removes the project
// directory. Idempotent: if the project is already gone, replies success.
func (c *Controller) handleDelete(msg *nats.Msg) {
	var req ProvisionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		c.log.Warn().Err(err).Msg("failed to parse delete request")
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "invalid request payload",
			Code:  "invalid_payload",
		})
		return
	}

	pslug := req.ProjectSlug
	if pslug == "" {
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "projectSlug required",
			Code:  "invalid_payload",
		})
		return
	}

	c.log.Info().Str("project", pslug).Msg("deleting project")

	// Stop child if running.
	c.mu.Lock()
	child, exists := c.children[pslug]
	if exists {
		delete(c.children, pslug)
	}
	c.mu.Unlock()

	if exists {
		child.stop()
	}

	// Remove project directory. Idempotent: if already gone, succeed.
	projectDir := filepath.Join(c.dataDir, pslug)
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

// handleUpdate refreshes credentials for a provisioned project.
// Currently a no-op — future: refresh credentials and signal session-agent reload.
func (c *Controller) handleUpdate(msg *nats.Msg) {
	var req ProvisionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "invalid request payload",
			Code:  "invalid_payload",
		})
		return
	}

	pslug := req.ProjectSlug
	if pslug == "" {
		c.reply(msg, ProvisionReply{
			OK:    false,
			Error: "projectSlug required",
			Code:  "invalid_payload",
		})
		return
	}

	c.mu.Lock()
	_, exists := c.children[pslug]
	c.mu.Unlock()

	if !exists {
		c.reply(msg, ProvisionReply{
			OK:          false,
			ProjectSlug: pslug,
			Error:       "project not found",
			Code:        "not_found",
		})
		return
	}

	// Future: refresh credentials in ~/.mclaude/projects/{pslug}/.credentials/
	// and signal the session-agent to reload.
	c.reply(msg, ProvisionReply{OK: true, ProjectSlug: pslug})
}

// reply publishes a JSON reply to a NATS request message.
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
	children := make(map[string]*supervisedChild, len(c.children))
	for k, v := range c.children {
		children[k] = v
	}
	c.mu.Unlock()

	for pslug, child := range children {
		c.log.Info().Str("project", pslug).Msg("stopping session-agent")
		child.stop()
	}
}
