package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"

	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"
)

// ProjectKVState is the value written to the mclaude-projects JetStream KV bucket.
// Must match the TypeScript ProjectKVState in mclaude-web/src/types.ts.
// ADR-0050 adds Slug, UserSlug, HostSlug so the SPA can construct correct
// host-scoped NATS subjects. Key format migration ({uslug}.{hslug}.{pslug}) is
// deferred to a separate ADR — the key currently stays {userID}.{projectID}.
type ProjectKVState struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	UserSlug      string  `json:"userSlug"`
	HostSlug      string  `json:"hostSlug"`
	Name          string  `json:"name"`
	GitURL        string  `json:"gitUrl"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"createdAt"`
	GitIdentityID *string `json:"gitIdentityId,omitempty"`
}

// ProvisionRequest is the NATS request payload sent from control-plane
// to the appropriate controller (K8s or local) per ADR-0035.
// ADR-0050 adds UserID and ProjectID (UUIDs) so the controller has both
// UUIDs (for K8s resource naming) and slugs (for NATS subjects + env vars).
type ProvisionRequest struct {
	UserID        string `json:"userId"`
	UserSlug      string `json:"userSlug"`
	HostSlug      string `json:"hostSlug"`
	ProjectID     string `json:"projectId"`
	ProjectSlug   string `json:"projectSlug"`
	GitURL        string `json:"gitUrl,omitempty"`
	GitIdentityID string `json:"gitIdentityId,omitempty"`
}

// ProvisionReply is the NATS reply from the controller.
type ProvisionReply struct {
	OK          bool   `json:"ok"`
	ProjectSlug string `json:"projectSlug,omitempty"`
	Error       string `json:"error,omitempty"`
	Code        string `json:"code,omitempty"`
}

// DefaultProvisionTimeoutSeconds is the default NATS request/reply timeout for project provisioning.
const DefaultProvisionTimeoutSeconds = 10

// provisionTimeoutSeconds returns the configured provision timeout.
// Reads PROVISION_TIMEOUT_SECONDS from env (default 10).
func provisionTimeoutSeconds() time.Duration {
	v := os.Getenv("PROVISION_TIMEOUT_SECONDS")
	if v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return DefaultProvisionTimeoutSeconds * time.Second
}

// StartProjectsSubscriber connects to NATS, ensures the shared mclaude-hosts KV
// bucket exists, and subscribes to project creation NATS subjects.
// Per ADR-0054, per-user KV buckets (mclaude-sessions-{uslug}, mclaude-projects-{uslug})
// and the per-user sessions stream (MCLAUDE_SESSIONS_{uslug}) are created on user
// registration/first login, not at startup.
// The caller owns the *nats.Conn lifetime — Close() it on shutdown.
func (s *Server) StartProjectsSubscriber(nc *nats.Conn) error {
	js, err := nc.JetStream()
	if err != nil {
		return err
	}

	// Only the shared mclaude-hosts KV bucket is created at startup.
	// Per-user buckets are created on user registration (ensureUserResources).
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		return err
	}
	s.hostsKV = hostsKV

	// CROSS-1 (ADR-0052): Subscribe to BOTH the legacy user-scoped subject and the
	// host-scoped subject. The SPA publishes to the host-scoped subject
	// mclaude.users.{uslug}.hosts.{hslug}.api.projects.create (via subjProjectsCreate),
	// but the previous subscription only matched the user-scoped pattern
	// mclaude.users.*.api.projects.create (wildcard * = one token only).
	// Both subscriptions share the same handler via handleProjectCreate.

	// Legacy user-scoped subject: mclaude.users.{userSlug}.api.projects.create
	_, err = nc.Subscribe("mclaude.users.*.api.projects.create", func(msg *nats.Msg) {
		// Extract userSlug from subject: mclaude.users.{userSlug}.api.projects.create
		parts := strings.Split(msg.Subject, ".")
		if len(parts) < 3 {
			replyError(msg, "malformed subject")
			return
		}
		userSlug := parts[2]
		s.handleProjectCreate(nc, msg, userSlug, "" /* hostSlug from payload */)
	})
	if err != nil {
		return err
	}

	// CROSS-1: Host-scoped subject: mclaude.users.{uslug}.hosts.{hslug}.api.projects.create
	// This is the subject the SPA actually publishes to (ADR-0035).
	_, err = nc.Subscribe("mclaude.users.*.hosts.*.api.projects.create", func(msg *nats.Msg) {
		// Extract userSlug and hostSlug from subject:
		// mclaude.users.{userSlug}.hosts.{hostSlug}.api.projects.create
		parts := strings.Split(msg.Subject, ".")
		if len(parts) < 5 {
			replyError(msg, "malformed subject")
			return
		}
		userSlug := parts[2]
		hostSlug := parts[4]
		s.handleProjectCreate(nc, msg, userSlug, hostSlug)
	})
	return err
}

// handleProjectCreate is the shared handler for both the legacy user-scoped and
// the ADR-0035 host-scoped project creation NATS subscriptions (CROSS-1).
// subjectHostSlug is the host slug extracted from the NATS subject (host-scoped path),
// or empty if the message arrived on the legacy user-scoped subject.
func (s *Server) handleProjectCreate(nc *nats.Conn, msg *nats.Msg, userSlug, subjectHostSlug string) {
	if s.db == nil {
		replyError(msg, "service unavailable")
		return
	}

	// Resolve user by slug to get UUID (needed for DB writes and ProvisionRequest.UserID).
	user, err := s.db.GetUserBySlug(context.Background(), userSlug)
	if err != nil {
		replyError(msg, "internal error")
		return
	}
	if user == nil {
		replyError(msg, "user not found")
		return
	}
	userID := user.ID

	var req struct {
		Name          string  `json:"name"`
		GitURL        string  `json:"gitUrl"`
		GitIdentityID *string `json:"gitIdentityId"`
		HostSlug      string  `json:"hostSlug,omitempty"` // ADR-0035: target host
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.Name == "" {
		replyError(msg, "name required")
		return
	}

	// CROSS-1: Prefer host slug from the NATS subject (host-scoped path).
	// Fall back to the payload's hostSlug for backward compatibility.
	hostSlug := subjectHostSlug
	if hostSlug == "" {
		hostSlug = req.HostSlug
	}

	// Validate gitIdentityId if provided.
	if req.GitIdentityID != nil && *req.GitIdentityID != "" {
		conn, err := s.db.GetOAuthConnectionByID(context.Background(), *req.GitIdentityID)
		if err != nil || conn == nil || conn.UserID != userID {
			replyError(msg, "invalid gitIdentityId")
			return
		}
		// Validate hostname matches gitUrl.
		if req.GitURL != "" {
			projHost := extractHost(req.GitURL)
			connHost := extractHost(conn.BaseURL)
			if projHost != "" && connHost != "" && projHost != connHost {
				replyError(msg, "gitIdentityId hostname does not match gitUrl")
				return
			}
		}
	}

	id := uuid.NewString()
	proj, err := s.db.CreateProjectWithIdentity(context.Background(), id, userID, req.Name, req.GitURL, req.GitIdentityID)
	if err != nil {
		replyError(msg, "failed to create project")
		return
	}

	// ADR-0035 + ADR-0050: publish provisioning request to the host-scoped subject
	// so the appropriate controller (K8s or local) can provision resources.
	// ProvisionRequest includes both UUIDs (K8s naming) and slugs (NATS subjects/env vars).
	provisionFailed := false
	if hostSlug != "" {
		gitIdentityIDStr := ""
		if req.GitIdentityID != nil {
			gitIdentityIDStr = *req.GitIdentityID
		}
		provReq := ProvisionRequest{
			UserID:        userID,
			UserSlug:      userSlug,
			HostSlug:      hostSlug,
			ProjectID:     proj.ID,
			ProjectSlug:   proj.Slug,
			GitURL:        req.GitURL,
			GitIdentityID: gitIdentityIDStr,
		}
		provData, _ := json.Marshal(provReq)
		// ADR-0054: publish to host-scoped fan-out subject.
		provSubject := subj.HostUserProjectsCreate(slug.HostSlug(hostSlug), slug.UserSlug(userSlug), slug.ProjectSlug(proj.Slug))
		provReply, err := nc.Request(provSubject, provData, provisionTimeoutSeconds())
		if err != nil {
			log.Error().Err(err).
				Str("userSlug", userSlug).
				Str("hostSlug", hostSlug).
				Str("projectId", id).
				Msg("provisioning request timed out — host unreachable")
			provisionFailed = true
		} else {
			var reply ProvisionReply
			if jsonErr := json.Unmarshal(provReply.Data, &reply); jsonErr == nil && !reply.OK {
				log.Error().
					Str("userSlug", userSlug).
					Str("projectId", id).
					Str("error", reply.Error).
					Msg("provisioning failed")
				provisionFailed = true
			}
		}
	}

	// GAP-CP-02: On provisioning failure, mark project status='failed' and update KV.
	if provisionFailed {
		if dbErr := s.db.UpdateProjectStatus(context.Background(), id, "failed"); dbErr != nil {
			log.Error().Err(dbErr).Str("projectId", id).Msg("failed to mark project as failed")
		}
		proj.Status = "failed"
		// Write failed state to KV so SPA sees it.
		if kvErr := writeProjectKV(nc, userID, userSlug, hostSlug, proj); kvErr != nil {
			log.Warn().Err(kvErr).Str("projectId", id).Msg("project create (failed): write KV failed (non-fatal)")
		}
		// Publish projects.updated broadcast so SPA learns of the failure.
		publishProjectsUpdated(nc, userSlug)
		replyError(msg, "host "+hostSlug+" unreachable")
		return
	}

	// Write to KV so session-store watchers pick it up immediately.
	// ADR-0050: pass userSlug and hostSlug so value includes slug fields.
	if kvErr := writeProjectKV(nc, userID, userSlug, hostSlug, proj); kvErr != nil {
		// Non-fatal: DB row was created; KV is best-effort.
		log.Warn().Err(kvErr).Str("projectId", id).Msg("project create: write KV failed (non-fatal)")
	}

	// GAP-CP-10: Broadcast project state change to SPA.
	publishProjectsUpdated(nc, userSlug)

	reply, _ := json.Marshal(map[string]string{"id": id})
	_ = msg.Respond(reply)
}

// writeProjectKV writes a Project to the per-user mclaude-projects-{uslug} KV bucket.
// ADR-0054: per-user bucket with hierarchical key format hosts.{hslug}.projects.{pslug}.
func writeProjectKV(nc *nats.Conn, userID, userSlug, hostSlug string, proj *Project) error {
	if userSlug == "" || hostSlug == "" || proj.Slug == "" {
		return nil // not enough info to construct the per-user key
	}
	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	kv, err := ensurePerUserProjectsKV(js, userSlug)
	if err != nil {
		return err
	}
	state := ProjectKVState{
		ID:            proj.ID,
		Slug:          proj.Slug,
		UserSlug:      userSlug,
		HostSlug:      hostSlug,
		Name:          proj.Name,
		GitURL:        proj.GitURL,
		Status:        proj.Status,
		CreatedAt:     proj.CreatedAt.UTC().Format(time.RFC3339),
		GitIdentityID: proj.GitIdentityID,
	}
	val, _ := json.Marshal(state)
	// ADR-0054: hierarchical key format with literal type-tokens.
	key := "hosts." + hostSlug + ".projects." + proj.Slug
	_, err = kv.Put(key, val)
	return err
}

// ensurePerUserProjectsKV creates the per-user mclaude-projects-{uslug} KV bucket (ADR-0054).
func ensurePerUserProjectsKV(js nats.JetStreamContext, uslug string) (nats.KeyValue, error) {
	bucket := userProjectsBucket(uslug)
	kv, err := js.KeyValue(bucket)
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucket,
		History: 1,
	})
}

// ensurePerUserSessionsKV creates the per-user mclaude-sessions-{uslug} KV bucket (ADR-0054).
// History=64 allows replay of recent session state updates.
func ensurePerUserSessionsKV(js nats.JetStreamContext, uslug string) (nats.KeyValue, error) {
	bucket := userSessionsBucket(uslug)
	kv, err := js.KeyValue(bucket)
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucket,
		History: 64,
	})
}

// ensureHostsKV creates the shared mclaude-hosts KV bucket (ADR-0054).
// Key format: {hslug} (flat key — hosts are globally unique).
// Read access is scoped per-host in user JWTs.
func ensureHostsKV(js nats.JetStreamContext) (nats.KeyValue, error) {
	kv, err := js.KeyValue("mclaude-hosts")
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  "mclaude-hosts",
		History: 1,
	})
}

// ensurePerUserSessionsStream creates the per-user MCLAUDE_SESSIONS_{uslug} JetStream stream (ADR-0054).
// Replaces the previous three shared streams (MCLAUDE_EVENTS, MCLAUDE_API, MCLAUDE_LIFECYCLE).
func ensurePerUserSessionsStream(js nats.JetStreamContext, uslug string) error {
	streamName := userSessionsStream(uslug)
	subject := "mclaude.users." + uslug + ".hosts.*.projects.*.sessions.>"

	// Check if stream already exists
	_, err := js.StreamInfo(streamName)
	if err == nil {
		return nil // already exists
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{subject},
		Retention:  nats.LimitsPolicy,
		MaxAge:     30 * 24 * time.Hour, // 30 days
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
	})
	return err
}

// ensureUserResources creates all per-user JetStream resources on user registration.
// Per ADR-0054: creates mclaude-sessions-{uslug}, mclaude-projects-{uslug},
// and MCLAUDE_SESSIONS_{uslug} stream.
func ensureUserResources(nc *nats.Conn, uslug string) error {
	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	if _, err := ensurePerUserSessionsKV(js, uslug); err != nil {
		return err
	}
	if _, err := ensurePerUserProjectsKV(js, uslug); err != nil {
		return err
	}
	if err := ensurePerUserSessionsStream(js, uslug); err != nil {
		return err
	}
	return nil
}

// replyError sends a JSON error reply if the message has a reply subject.
func replyError(msg *nats.Msg, errMsg string) {
	if msg.Reply == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{"error": errMsg})
	_ = msg.Respond(b)
}

// publishProjectsUpdated publishes a notification to mclaude.users.{uslug}.api.projects.updated
// so the SPA knows project state has changed (GAP-CP-10).
func publishProjectsUpdated(nc *nats.Conn, userSlug string) {
	if nc == nil || userSlug == "" {
		return
	}
	subject := subj.UserAPIProjectsUpdated(slug.UserSlug(userSlug))
	payload, _ := json.Marshal(map[string]string{"event": "updated"})
	if err := nc.Publish(subject, payload); err != nil {
		log.Warn().Err(err).Str("subject", subject).Msg("publish projects.updated failed (non-fatal)")
	}
}

// publishProjectsUpdateToHost publishes a NATS message to
// mclaude.users.{uslug}.hosts.{hslug}.api.projects.update so the controller
// refreshes user-secrets (GAP-CP-03, GAP-CP-05).
// This subject still exists per spec-control-plane.md (PATCH /api/projects/{id}).
func publishProjectsUpdateToHost(nc *nats.Conn, userSlug, hostSlug string) {
	if nc == nil || userSlug == "" || hostSlug == "" {
		return
	}
	// Inline construction: mclaude.users.{uslug}.hosts.{hslug}.api.projects.update
	subject := "mclaude.users." + userSlug + ".hosts." + hostSlug + ".api.projects.update"
	payload, _ := json.Marshal(map[string]string{"event": "credentials_changed"})
	if err := nc.Publish(subject, payload); err != nil {
		log.Warn().Err(err).Str("subject", subject).Msg("publish projects.update failed (non-fatal)")
	}
}

// publishProjectsDeleteToHost publishes a NATS message to
// mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete (ADR-0054 fan-out)
// so the controller tears down per-project resources (GAP-CP-04).
func publishProjectsDeleteToHost(nc *nats.Conn, userSlug, hostSlug, projectSlug, projectID string) {
	if nc == nil || userSlug == "" || hostSlug == "" {
		return
	}
	var subject string
	if projectSlug != "" {
		subject = subj.HostUserProjectsDelete(slug.HostSlug(hostSlug), slug.UserSlug(userSlug), slug.ProjectSlug(projectSlug))
	} else {
		// Fallback for callers without project slug: use legacy user-scoped subject.
		subject = "mclaude.users." + userSlug + ".hosts." + hostSlug + ".api.projects.delete"
	}
	payload, _ := json.Marshal(map[string]string{"projectId": projectID, "projectSlug": projectSlug, "event": "deleted"})
	if err := nc.Publish(subject, payload); err != nil {
		log.Warn().Err(err).Str("subject", subject).Str("projectId", projectID).Msg("publish projects.delete failed (non-fatal)")
	}
}
