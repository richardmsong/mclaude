package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog/log"
)

// StartLifecycleSubscribers subscribes to host lifecycle and agent registration
// NATS subjects per ADR-0054. These are started alongside the existing projects
// subscriber in StartProjectsSubscriber.
func (s *Server) StartLifecycleSubscribers(nc *nats.Conn) error {
	// Agent public key registration:
	// mclaude.hosts.{hslug}.api.agents.register
	if _, err := nc.Subscribe("mclaude.hosts.*.api.agents.register", func(msg *nats.Msg) {
		s.handleAgentRegister(msg)
	}); err != nil {
		return err
	}

	// Host registration via NATS:
	// mclaude.users.*.hosts._.register
	// The _ sentinel in hslug position cannot collide with real slugs ([a-z0-9-]+).
	if _, err := nc.Subscribe("mclaude.users.*.hosts._.register", func(msg *nats.Msg) {
		s.handleNATSHostRegister(msg)
	}); err != nil {
		return err
	}

	// Host lifecycle management:
	// mclaude.users.*.hosts.*.manage.grant
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.manage.grant", func(msg *nats.Msg) {
		s.handleManageGrant(msg)
	}); err != nil {
		return err
	}

	// mclaude.users.*.hosts.*.manage.revoke-access
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.manage.revoke-access", func(msg *nats.Msg) {
		s.handleManageRevokeAccess(msg)
	}); err != nil {
		return err
	}

	// mclaude.users.*.hosts.*.manage.deregister
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.manage.deregister", func(msg *nats.Msg) {
		s.handleManageDeregister(msg)
	}); err != nil {
		return err
	}

	// mclaude.users.*.hosts.*.manage.revoke (emergency credential revocation)
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.manage.revoke", func(msg *nats.Msg) {
		s.handleManageRevoke(msg)
	}); err != nil {
		return err
	}

	// mclaude.users.*.hosts.*.manage.rekey (rotate host NKey)
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.manage.rekey", func(msg *nats.Msg) {
		s.handleManageRekey(msg)
	}); err != nil {
		return err
	}

	// mclaude.users.*.hosts.*.manage.update (rename / update type)
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.manage.update", func(msg *nats.Msg) {
		s.handleManageUpdate(msg)
	}); err != nil {
		return err
	}

	// check-slug: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.check-slug
	// (ADR-0053 — slug availability check before project creation)
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.check-slug", func(msg *nats.Msg) {
		s.handleCheckSlug(msg)
	}); err != nil {
		return err
	}

	// ADR-0053: Import NATS handlers.
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.import.request", func(msg *nats.Msg) {
		s.handleNATSImportRequest(msg)
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.import.confirm", func(msg *nats.Msg) {
		s.handleNATSImportConfirm(msg)
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.import.download", func(msg *nats.Msg) {
		s.handleNATSImportDownload(msg)
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.import.complete", func(msg *nats.Msg) {
		s.handleNATSImportComplete(msg)
	}); err != nil {
		return err
	}

	// ADR-0053: Attachment NATS handlers.
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.attachments.upload", func(msg *nats.Msg) {
		s.handleNATSAttachmentUpload(msg)
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.attachments.confirm", func(msg *nats.Msg) {
		s.handleNATSAttachmentConfirm(msg)
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.attachments.download", func(msg *nats.Msg) {
		s.handleNATSAttachmentDownload(msg)
	}); err != nil {
		return err
	}

	return nil
}

// parseManageSubject extracts userSlug and hostSlug from a manage.* subject.
// Pattern: mclaude.users.{uslug}.hosts.{hslug}.manage.{action}
func parseManageSubject(subject string) (uslug, hslug string) {
	parts := strings.Split(subject, ".")
	// mclaude . users . {uslug} . hosts . {hslug} . manage . {action}
	// 0       1 2      3        4 5      6         7 8
	if len(parts) >= 7 {
		return parts[2], parts[4]
	}
	return "", ""
}

// ----- Agent Registration -----

// AgentRegisterRequest is the NATS request payload for agent NKey registration.
type AgentRegisterRequest struct {
	UserSlug    string `json:"user_slug"`
	ProjectSlug string `json:"project_slug"`
	NKeyPublic  string `json:"nkey_public"`
}

// handleAgentRegister processes mclaude.hosts.{hslug}.api.agents.register.
// Host controllers register spawned agent NKey public keys here.
// CP validates host access + project ownership + host assignment, then stores
// the credential in agent_credentials.
func (s *Server) handleAgentRegister(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	// Extract host slug from subject: mclaude.hosts.{hslug}.api.agents.register
	parts := strings.Split(msg.Subject, ".")
	if len(parts) < 5 {
		replyNATSError(msg, "malformed subject")
		return
	}
	hslug := parts[2]

	var req AgentRegisterRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyNATSError(msg, "invalid request body")
		return
	}
	if req.UserSlug == "" || req.ProjectSlug == "" || req.NKeyPublic == "" {
		replyNATSError(msg, "user_slug, project_slug, and nkey_public are required")
		return
	}

	ctx := context.Background()

	// Resolve user by slug.
	user, err := s.db.GetUserBySlug(ctx, req.UserSlug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Validate host access: user must have access to the host.
	hostSlugs, err := s.db.GetHostAccessSlugs(ctx, user.ID)
	if err != nil {
		replyNATSError(msg, "internal error")
		return
	}
	hasAccess := false
	for _, s := range hostSlugs {
		if s == hslug {
			hasAccess = true
			break
		}
	}
	if !hasAccess {
		replyNATSForbidden(msg, "user does not have access to host")
		return
	}

	// Validate project ownership and host assignment.
	proj, err := s.db.GetProjectByUserAndSlug(ctx, user.ID, req.ProjectSlug)
	if err != nil || proj == nil {
		replyNATSError(msg, "project not found")
		return
	}
	if proj.HostSlug != hslug {
		replyNATSNotFound(msg, "project not assigned to host")
		return
	}

	// Store the agent credential (upsert — replaces on re-registration).
	credID := uuid.NewString()
	if err := s.db.UpsertAgentCredential(ctx, credID, user.ID, hslug, req.ProjectSlug, req.NKeyPublic); err != nil {
		replyNATSError(msg, "failed to store agent credential")
		return
	}

	log.Info().
		Str("userSlug", req.UserSlug).
		Str("hostSlug", hslug).
		Str("projectSlug", req.ProjectSlug).
		Msg("agent credential registered")

	replyNATSOK(msg)
}

// ----- Host Registration via NATS -----

// NATSHostRegisterRequest is the payload for mclaude.users.*.hosts._.register.
type NATSHostRegisterRequest struct {
	Name       string `json:"name"`
	Type       string `json:"type"`        // "machine" or "cluster"
	NKeyPublic string `json:"nkey_public"` // host controller's NKey public key
}

// handleNATSHostRegister processes mclaude.users.*.hosts._.register.
// CLI publishes this to register a new host. CP creates the host record and
// returns {ok, slug} — no JWT (host authenticates via HTTP challenge-response).
func (s *Server) handleNATSHostRegister(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	// Extract user slug from subject: mclaude.users.{uslug}.hosts._.register
	parts := strings.Split(msg.Subject, ".")
	if len(parts) < 5 {
		replyNATSError(msg, "malformed subject")
		return
	}
	uslug := parts[2]

	var req NATSHostRegisterRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyNATSError(msg, "invalid request body")
		return
	}
	if req.Name == "" || req.NKeyPublic == "" {
		replyNATSError(msg, "name and nkey_public are required")
		return
	}
	if req.Type == "" {
		req.Type = "machine"
	}

	ctx := context.Background()

	// Resolve user by slug.
	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Slugify the name for the host slug.
	hslug := slugify(req.Name)
	if hslug == "" {
		hslug = "host-" + uuid.NewString()[:8]
	}

	// Insert host row.
	hostID := uuid.NewString()
	_, execErr := s.db.pool.Exec(ctx, `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key, created_at)
		VALUES ($1, $2, $3, $4, $5, 'owner', $6, $7)
		ON CONFLICT (user_id, slug) DO UPDATE SET
			name = EXCLUDED.name,
			public_key = EXCLUDED.public_key`,
		hostID, user.ID, hslug, req.Name, req.Type, req.NKeyPublic, time.Now().UTC())
	if execErr != nil {
		replyNATSError(msg, "failed to create host")
		return
	}

	log.Info().
		Str("userSlug", uslug).
		Str("hostSlug", hslug).
		Str("type", req.Type).
		Msg("host registered via NATS")

	// Reply with {ok, slug} — no JWT. Host authenticates via HTTP challenge-response.
	reply, _ := json.Marshal(map[string]any{"ok": true, "slug": hslug})
	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// ----- Host Access Grant -----

// ManageGrantRequest is the payload for manage.grant.
type ManageGrantRequest struct {
	UserSlug string `json:"userSlug"`
}

// handleManageGrant processes mclaude.users.*.hosts.*.manage.grant.
// Only the host owner can grant access.
func (s *Server) handleManageGrant(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug := parseManageSubject(msg.Subject)
	if uslug == "" || hslug == "" {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req ManageGrantRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.UserSlug == "" {
		replyNATSError(msg, "userSlug is required")
		return
	}

	ctx := context.Background()

	// Resolve requesting user.
	owner, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || owner == nil {
		replyNATSError(msg, "owner not found")
		return
	}

	// Validate host ownership.
	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}
	if host.UserID != owner.ID {
		replyNATSForbidden(msg, "only the host owner can grant access")
		return
	}

	// Resolve grantee.
	grantee, err := s.db.GetUserBySlug(ctx, req.UserSlug)
	if err != nil || grantee == nil {
		replyNATSError(msg, "grantee user not found")
		return
	}

	// Grant access.
	if err := s.db.GrantHostAccess(ctx, host.ID, grantee.ID); err != nil {
		replyNATSError(msg, "failed to grant access")
		return
	}

	// Revoke grantee's current JWT so they reconnect with updated host list.
	if grantee.NKeyPublic != nil && *grantee.NKeyPublic != "" {
		_ = s.revokeNKeyJWT(ctx, *grantee.NKeyPublic)
	}

	log.Info().
		Str("owner", uslug).
		Str("host", hslug).
		Str("grantee", req.UserSlug).
		Msg("host access granted")

	replyNATSOK(msg)
}

// ----- Host Access Revocation -----

// ManageRevokeAccessRequest is the payload for manage.revoke-access.
type ManageRevokeAccessRequest struct {
	UserSlug string `json:"userSlug"`
}

// handleManageRevokeAccess processes mclaude.users.*.hosts.*.manage.revoke-access.
// Only the host owner can revoke access.
func (s *Server) handleManageRevokeAccess(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug := parseManageSubject(msg.Subject)
	if uslug == "" || hslug == "" {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req ManageRevokeAccessRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.UserSlug == "" {
		replyNATSError(msg, "userSlug is required")
		return
	}

	ctx := context.Background()

	// Resolve requesting user (owner).
	owner, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || owner == nil {
		replyNATSError(msg, "owner not found")
		return
	}

	// Validate host ownership.
	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}
	if host.UserID != owner.ID {
		replyNATSForbidden(msg, "only the host owner can revoke access")
		return
	}

	// Resolve grantee.
	grantee, err := s.db.GetUserBySlug(ctx, req.UserSlug)
	if err != nil || grantee == nil {
		replyNATSError(msg, "grantee user not found")
		return
	}

	// Revoke access from host_access table.
	if err := s.db.RevokeHostAccess(ctx, host.ID, grantee.ID); err != nil {
		replyNATSError(msg, "failed to revoke access")
		return
	}

	// Revoke all agent JWTs for grantee's projects on this host.
	creds, _ := s.db.GetAgentCredentialsByHostUser(ctx, hslug, grantee.ID)
	for _, cred := range creds {
		_ = s.revokeNKeyJWT(ctx, cred.NKeyPublic)
	}

	// Revoke grantee's user JWT.
	if grantee.NKeyPublic != nil && *grantee.NKeyPublic != "" {
		_ = s.revokeNKeyJWT(ctx, *grantee.NKeyPublic)
	}

	log.Info().
		Str("owner", uslug).
		Str("host", hslug).
		Str("grantee", req.UserSlug).
		Msg("host access revoked")

	replyNATSOK(msg)
}

// ----- Host Deregistration -----

// handleManageDeregister processes mclaude.users.*.hosts.*.manage.deregister.
// Drains all active projects, revokes host credential, cleans up DB and KV.
func (s *Server) handleManageDeregister(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug := parseManageSubject(msg.Subject)
	if uslug == "" || hslug == "" {
		replyNATSError(msg, "malformed subject")
		return
	}

	ctx := context.Background()

	// Resolve requesting user.
	requester, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || requester == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Validate host ownership (or admin).
	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}
	if host.UserID != requester.ID && !requester.IsAdmin {
		replyNATSForbidden(msg, "only the host owner or platform operator can deregister")
		return
	}

	// 1. Drain active projects — publish delete for each + S3 prefix cleanup.
	// Fetch projects and owner unconditionally so both NATS notifications and
	// S3 cleanup can run regardless of which subsystem is available.
	{
		projects, _ := s.db.GetProjectsByHostSlug(ctx, host.UserID, hslug)
		owner, _ := s.db.GetUserByID(ctx, host.UserID)
		if owner != nil {
			nc := s.nc
			for _, p := range projects {
				if nc != nil {
					publishProjectsDeleteToHost(nc, owner.Slug, hslug, p.Slug, p.ID)
				}
				// ADR-0053: Delete S3 prefix for this project (best-effort).
				if s.s3 != nil && p.Slug != "" {
					prefix := owner.Slug + "/" + hslug + "/" + p.Slug + "/"
					if s3Err := s.s3.s3DeletePrefix(prefix); s3Err != nil {
						log.Warn().Err(s3Err).Str("prefix", prefix).Msg("host deregister: S3 cleanup failed (non-fatal)")
					}
				}
			}
		}
	}

	// 2. Revoke host credential.
	if host.PublicKey != nil {
		_ = s.revokeNKeyJWT(ctx, *host.PublicKey)
	}

	// 3. Revoke all agent credentials on this host.
	creds, _ := s.db.GetAgentCredentialsByHost(ctx, hslug)
	for _, cred := range creds {
		_ = s.revokeNKeyJWT(ctx, cred.NKeyPublic)
	}

	// 4. Delete agent credentials from DB.
	_ = s.db.DeleteAgentCredentialsByHost(ctx, hslug)

	// 5. Delete host row from Postgres (cascades to projects, host_access).
	_, _ = s.db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, host.ID)

	// 6. Tombstone hosts KV entry.
	if s.hostsKV != nil {
		_ = s.hostsKV.Delete(hslug)
	}

	log.Info().
		Str("requester", uslug).
		Str("host", hslug).
		Msg("host deregistered")

	replyNATSOK(msg)
}

// ----- Emergency Credential Revocation -----

// handleManageRevoke processes mclaude.users.*.hosts.*.manage.revoke.
// Emergency: immediately revoke host JWT and all agent JWTs for that host.
func (s *Server) handleManageRevoke(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug := parseManageSubject(msg.Subject)
	if uslug == "" || hslug == "" {
		replyNATSError(msg, "malformed subject")
		return
	}

	ctx := context.Background()

	// Resolve requesting user.
	requester, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || requester == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Validate host ownership (or admin).
	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}
	if host.UserID != requester.ID && !requester.IsAdmin {
		replyNATSForbidden(msg, "only the host owner or platform operator can revoke")
		return
	}

	// Revoke host JWT.
	if host.PublicKey != nil {
		_ = s.revokeNKeyJWT(ctx, *host.PublicKey)
	}

	// Revoke all agent JWTs for this host.
	creds, _ := s.db.GetAgentCredentialsByHost(ctx, hslug)
	for _, cred := range creds {
		_ = s.revokeNKeyJWT(ctx, cred.NKeyPublic)
	}

	// Mark host as revoked in KV — read-modify-write to preserve existing fields.
	if s.hostsKV != nil {
		existing, gerr := s.hostsKV.Get(hslug)
		var state HostKVState
		if gerr == nil {
			_ = json.Unmarshal(existing.Value(), &state)
		} else {
			state = HostKVState{Slug: hslug}
		}
		state.Online = false
		if val, merr := json.Marshal(state); merr == nil {
			_, _ = s.hostsKV.Put(hslug, val)
		}
	}

	log.Warn().
		Str("requester", uslug).
		Str("host", hslug).
		Msg("host credentials revoked (emergency)")

	replyNATSOK(msg)
}

// ----- Host NKey Rekey -----

// ManageRekeyRequest is the payload for manage.rekey.
type ManageRekeyRequest struct {
	NKeyPublic string `json:"nkeyPublic"` // new NKey public key
}

// handleManageRekey processes mclaude.users.*.hosts.*.manage.rekey.
// Owner-only: rotates the host's NKey public key. Old JWT becomes useless.
func (s *Server) handleManageRekey(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug := parseManageSubject(msg.Subject)
	if uslug == "" || hslug == "" {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req ManageRekeyRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.NKeyPublic == "" {
		replyNATSError(msg, "nkeyPublic is required")
		return
	}

	ctx := context.Background()

	// Validate ownership.
	requester, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || requester == nil {
		replyNATSError(msg, "user not found")
		return
	}

	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}
	if host.UserID != requester.ID {
		replyNATSForbidden(msg, "only the host owner can rekey")
		return
	}

	// Revoke old JWT.
	if host.PublicKey != nil {
		_ = s.revokeNKeyJWT(ctx, *host.PublicKey)
	}

	// Store new public key.
	if err := s.db.RegisterHostNKeyPublic(ctx, host.ID, req.NKeyPublic); err != nil {
		replyNATSError(msg, "failed to update host key")
		return
	}

	log.Info().
		Str("owner", uslug).
		Str("host", hslug).
		Msg("host NKey rotated")

	replyNATSOK(msg)
}

// ----- Host Update -----

// ManageUpdateRequest is the payload for manage.update.
type ManageUpdateRequest struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

// handleManageUpdate processes mclaude.users.*.hosts.*.manage.update.
func (s *Server) handleManageUpdate(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug := parseManageSubject(msg.Subject)
	if uslug == "" || hslug == "" {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req ManageUpdateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyNATSError(msg, "invalid request body")
		return
	}
	if req.Name == "" && req.Type == "" {
		replyNATSError(msg, "name or type is required")
		return
	}

	ctx := context.Background()

	// Validate ownership.
	owner, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || owner == nil {
		replyNATSError(msg, "user not found")
		return
	}

	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}
	if host.UserID != owner.ID && !owner.IsAdmin {
		replyNATSForbidden(msg, "only the host owner can update host")
		return
	}

	// Update name if provided.
	if req.Name != "" {
		_, _ = s.db.pool.Exec(ctx, `UPDATE hosts SET name = $1 WHERE id = $2`, req.Name, host.ID)
	}

	// Update hosts KV with new name.
	if s.hostsKV != nil && req.Name != "" {
		existing, gerr := s.hostsKV.Get(hslug)
		if gerr == nil {
			var state HostKVState
			if jerr := json.Unmarshal(existing.Value(), &state); jerr == nil {
				state.Name = req.Name
				if val, merr := json.Marshal(state); merr == nil {
					_, _ = s.hostsKV.Put(hslug, val)
				}
			}
		}
	}

	replyNATSOK(msg)
}

// ----- check-slug -----

// CheckSlugRequest is the payload for check-slug.
type CheckSlugRequest struct {
	Slug string `json:"slug"`
}

// handleCheckSlug processes mclaude.users.{uslug}.hosts.{hslug}.projects.*.check-slug.
// Returns {available: bool, suggestion: string (if not available)}.
func (s *Server) handleCheckSlug(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	// Extract from subject: mclaude.users.{uslug}.hosts.{hslug}.projects.*.check-slug
	parts := strings.Split(msg.Subject, ".")
	if len(parts) < 7 {
		replyNATSError(msg, "malformed subject")
		return
	}
	uslug := parts[2]

	var req CheckSlugRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.Slug == "" {
		replyNATSError(msg, "slug is required")
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Check if the slug is taken for this user (across any host).
	var count int
	row := s.db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM projects WHERE user_id = $1 AND slug = $2`,
		user.ID, req.Slug)
	if err := row.Scan(&count); err != nil {
		replyNATSError(msg, "database error")
		return
	}

	var reply []byte
	if count == 0 {
		reply, _ = json.Marshal(map[string]any{"available": true})
	} else {
		// Generate a suggestion by appending -2, -3, etc.
		suggestion := req.Slug + "-2"
		for i := 2; i <= 99; i++ {
			candidate := req.Slug + "-" + strings.ReplaceAll(strings.TrimLeft(fmt.Sprint(i), "0123456789"), "", "")
			candidate = req.Slug + "-" + fmt.Sprint(i)
			var c2 int
			r2 := s.db.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM projects WHERE user_id = $1 AND slug = $2`,
				user.ID, candidate)
			if r2.Scan(&c2) == nil && c2 == 0 {
				suggestion = candidate
				break
			}
		}
		reply, _ = json.Marshal(map[string]any{"available": false, "suggestion": suggestion})
	}

	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// ----- JWT Revocation -----

// revokeNKeyJWT adds the NKey public key to the NATS account JWT revocation list.
// This revokes all JWTs issued for that key by modifying the account JWT and
// publishing it to $SYS.REQ.CLAIMS.UPDATE using system account credentials.
// Per ADR-0054: the NATS server then immediately closes connections whose JWT
// was issued before the revocation timestamp.
//
// If revocation credentials are not configured (dev mode), logs a warning and
// falls back to TTL-based expiry (5-min for hosts/agents, ~8h for users).
func (s *Server) revokeNKeyJWT(_ context.Context, nkeyPublic string) error {
	if nkeyPublic == "" {
		return nil
	}
	if s.nc == nil {
		return nil
	}
	if s.operatorSeed == "" || s.sysAccountSeed == "" || s.accountJWTCache == "" {
		// Revocation not configured — JWTs expire naturally (5-min TTL for hosts/agents).
		log.Warn().Str("nkey", nkeyPublic[:min(8, len(nkeyPublic))]+"...").
			Msg("JWT revocation skipped — operator/sysAccount seed not configured (TTL-based fallback)")
		return nil
	}

	// Step 1: Parse the operator key pair from seed.
	operatorKP, err := nkeys.FromSeed([]byte(s.operatorSeed))
	if err != nil {
		return fmt.Errorf("parse operator seed: %w", err)
	}
	defer operatorKP.Wipe()

	// Step 2: Decode the current account JWT.
	accountClaims, err := natsjwt.DecodeAccountClaims(s.accountJWTCache)
	if err != nil {
		return fmt.Errorf("decode account jwt: %w", err)
	}

	// Step 3: Add the NKey to the revocation list with current timestamp.
	// This revokes all JWTs issued for this key before now.
	if accountClaims.Revocations == nil {
		accountClaims.Revocations = make(natsjwt.RevocationList)
	}
	accountClaims.Revocations.Revoke(nkeyPublic, time.Now())

	// Step 4: Re-sign the account JWT with the operator key.
	updatedAccountJWT, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return fmt.Errorf("re-sign account jwt: %w", err)
	}

	// Step 5: Publish to $SYS.REQ.CLAIMS.UPDATE using system account credentials.
	// We need a separate NATS connection using sysAccount credentials for this.
	sysKP, err := nkeys.FromSeed([]byte(s.sysAccountSeed))
	if err != nil {
		return fmt.Errorf("parse sysAccount seed: %w", err)
	}
	defer sysKP.Wipe()

	sysPub, err := sysKP.PublicKey()
	if err != nil {
		return fmt.Errorf("sysAccount public key: %w", err)
	}

	// Create a short-lived sysAccount user JWT for the $SYS publish.
	sysUserKP, err := nkeys.CreateUser()
	if err != nil {
		return fmt.Errorf("create sysUser nkey: %w", err)
	}
	defer sysUserKP.Wipe()
	sysUserPub, err := sysUserKP.PublicKey()
	if err != nil {
		return fmt.Errorf("sysUser public key: %w", err)
	}
	sysUserClaims := natsjwt.NewUserClaims(sysUserPub)
	sysUserClaims.Name = "cp-revocation"
	sysUserClaims.IssuerAccount = sysPub
	sysUserClaims.Expires = time.Now().Unix() + 60
	sysUserJWT, err := sysUserClaims.Encode(sysKP)
	if err != nil {
		return fmt.Errorf("encode sysUser jwt: %w", err)
	}

	// Connect to NATS with sysAccount credentials for the claims update.
	sysNC, err := nats.Connect(s.natsURL,
		nats.UserJWT(
			func() (string, error) { return sysUserJWT, nil },
			func(nonce []byte) ([]byte, error) { return sysUserKP.Sign(nonce) },
		),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		return fmt.Errorf("connect sysAccount to NATS: %w", err)
	}
	defer sysNC.Close()

	// Step 6: Publish the updated account JWT to $SYS.REQ.CLAIMS.UPDATE.
	reply, err := sysNC.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(updatedAccountJWT), 5*time.Second)
	if err != nil {
		return fmt.Errorf("publish claims update: %w", err)
	}

	// Parse the reply to confirm success.
	var updateReply struct {
		Server struct {
			Name string `json:"name"`
		} `json:"server"`
		Data struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"data"`
		Error *struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(reply.Data, &updateReply); err != nil {
		log.Warn().Err(err).Msg("JWT revocation: could not parse NATS reply (may still have succeeded)")
	} else if updateReply.Error != nil {
		return fmt.Errorf("NATS claims update error %d: %s", updateReply.Error.Code, updateReply.Error.Description)
	}

	// Update the cached account JWT with the revocation-updated version.
	s.accountJWTCache = updatedAccountJWT

	log.Info().Str("nkey", nkeyPublic[:min(8, len(nkeyPublic))]+"...").
		Msg("JWT revocation published to $SYS.REQ.CLAIMS.UPDATE")
	return nil
}

// ----- NATS reply helpers -----

// replyNATSOK sends a {"ok":true} JSON reply.
func replyNATSOK(msg *nats.Msg) {
	if msg.Reply == "" {
		return
	}
	reply, _ := json.Marshal(map[string]bool{"ok": true})
	_ = msg.Respond(reply)
}

// replyNATSError sends a {"ok":false,"error":...} reply.
func replyNATSError(msg *nats.Msg, errMsg string) {
	if msg.Reply == "" {
		log.Warn().Str("error", errMsg).Str("subject", msg.Subject).Msg("NATS handler error (no reply)")
		return
	}
	reply, _ := json.Marshal(map[string]any{"ok": false, "error": errMsg})
	_ = msg.Respond(reply)
}

// replyNATSForbidden sends a forbidden error reply.
func replyNATSForbidden(msg *nats.Msg, errMsg string) {
	if msg.Reply == "" {
		return
	}
	reply, _ := json.Marshal(map[string]any{"ok": false, "error": errMsg, "code": "FORBIDDEN"})
	_ = msg.Respond(reply)
}

// replyNATSNotFound sends a not-found error reply.
func replyNATSNotFound(msg *nats.Msg, errMsg string) {
	if msg.Reply == "" {
		return
	}
	reply, _ := json.Marshal(map[string]any{"ok": false, "error": errMsg, "code": "NOT_FOUND"})
	_ = msg.Respond(reply)
}
