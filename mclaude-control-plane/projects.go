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

// StartProjectsSubscriber connects to NATS, ensures the mclaude-projects,
// mclaude-job-queue, and mclaude-hosts KV buckets exist, and subscribes to
// mclaude.users.*.api.projects.create (ADR-0050: slug-based subject).
// The caller owns the *nats.Conn lifetime — Close() it on shutdown.
func (s *Server) StartProjectsSubscriber(nc *nats.Conn) error {
	js, err := nc.JetStream()
	if err != nil {
		return err
	}

	// Ensure buckets exist at startup so they're ready before subscriptions fire.
	if _, err := ensureProjectsKV(js); err != nil {
		return err
	}

	if _, err := ensureJobQueueKV(js); err != nil {
		return err
	}

	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		return err
	}
	s.hostsKV = hostsKV

	// Spec startup step 7: ensure mclaude-sessions KV bucket exists.
	// Control-plane doesn't write to it — bucket creation only.
	if _, err := ensureSessionsKV(js); err != nil {
		return err
	}

	// subject pattern: mclaude.users.{userSlug}.api.projects.create
	// SPA publishes to this subject (subjProjectsCreate in mclaude-web).
	// ADR-0050: subject updated from old "mclaude.{UUID}.api.projects.create"
	// to match SPA's new user-scoped slug-based format.
	_, err = nc.Subscribe("mclaude.users.*.api.projects.create", func(msg *nats.Msg) {
		// Extract userSlug from subject: mclaude.users.{userSlug}.api.projects.create
		parts := strings.Split(msg.Subject, ".")
		if len(parts) < 3 {
			replyError(msg, "malformed subject")
			return
		}
		userSlug := parts[2]

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
		if req.HostSlug != "" {
			gitIdentityIDStr := ""
			if req.GitIdentityID != nil {
				gitIdentityIDStr = *req.GitIdentityID
			}
			provReq := ProvisionRequest{
				UserID:        userID,
				UserSlug:      userSlug,
				HostSlug:      req.HostSlug,
				ProjectID:     proj.ID,
				ProjectSlug:   proj.Slug,
				GitURL:        req.GitURL,
				GitIdentityID: gitIdentityIDStr,
			}
			provData, _ := json.Marshal(provReq)
			provSubject := subj.UserHostAPIProjectsProvision(slug.UserSlug(userSlug), slug.HostSlug(req.HostSlug))
			provReply, err := nc.Request(provSubject, provData, provisionTimeoutSeconds())
			if err != nil {
				log.Error().Err(err).
					Str("userSlug", userSlug).
					Str("hostSlug", req.HostSlug).
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
			hostSlug := req.HostSlug
			if kvErr := writeProjectKV(nc, userID, userSlug, hostSlug, proj); kvErr != nil {
				log.Warn().Err(kvErr).Str("projectId", id).Msg("project create (failed): write KV failed (non-fatal)")
			}
			// Publish projects.updated broadcast so SPA learns of the failure.
			publishProjectsUpdated(nc, userSlug)
			replyError(msg, "host "+req.HostSlug+" unreachable")
			return
		}

		// Write to KV so session-store watchers pick it up immediately.
		// ADR-0050: pass userSlug and hostSlug so value includes slug fields.
		hostSlug := req.HostSlug // may be empty if no host was specified
		if kvErr := writeProjectKV(nc, userID, userSlug, hostSlug, proj); kvErr != nil {
			// Non-fatal: DB row was created; KV is best-effort.
			log.Warn().Err(kvErr).Str("projectId", id).Msg("project create: write KV failed (non-fatal)")
		}

		// GAP-CP-10: Broadcast project state change to SPA.
		publishProjectsUpdated(nc, userSlug)

		reply, _ := json.Marshal(map[string]string{"id": id})
		_ = msg.Respond(reply)
	})
	return err
}

// writeProjectKV writes a Project to the mclaude-projects JetStream KV bucket.
// userSlug and hostSlug are included in the value (ADR-0050) so the SPA can
// construct host-scoped NATS subjects. The key format ({userID}.{projectID})
// is unchanged — key migration to {uslug}.{hslug}.{pslug} is a separate ADR.
func writeProjectKV(nc *nats.Conn, userID, userSlug, hostSlug string, proj *Project) error {
	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	kv, err := ensureProjectsKV(js)
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
	_, err = kv.Put(userID+"."+proj.ID, val)
	return err
}

// ensureProjectsKV creates the mclaude-projects KV bucket if it doesn't exist.
func ensureProjectsKV(js nats.JetStreamContext) (nats.KeyValue, error) {
	kv, err := js.KeyValue("mclaude-projects")
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  "mclaude-projects",
		History: 1,
	})
}

// ensureJobQueueKV creates the mclaude-job-queue KV bucket if it doesn't exist.
func ensureJobQueueKV(js nats.JetStreamContext) (nats.KeyValue, error) {
	kv, err := js.KeyValue("mclaude-job-queue")
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  "mclaude-job-queue",
		History: 1,
	})
}

// ensureHostsKV creates the mclaude-hosts KV bucket if it doesn't exist (ADR-0046).
// The bucket stores host liveness state keyed by {uslug}.{hslug}.
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

// ensureSessionsKV creates the mclaude-sessions KV bucket if it doesn't exist.
// Per spec startup step 7, the control-plane ensures this bucket exists but
// does not write to it (session-agents own session state).
// History=64 allows the SPA to replay recent session state updates (ADR-0046).
func ensureSessionsKV(js nats.JetStreamContext) (nats.KeyValue, error) {
	kv, err := js.KeyValue("mclaude-sessions")
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  "mclaude-sessions",
		History: 64,
	})
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
func publishProjectsUpdateToHost(nc *nats.Conn, userSlug, hostSlug string) {
	if nc == nil || userSlug == "" || hostSlug == "" {
		return
	}
	subject := subj.UserHostAPIProjectsUpdate(slug.UserSlug(userSlug), slug.HostSlug(hostSlug))
	payload, _ := json.Marshal(map[string]string{"event": "credentials_changed"})
	if err := nc.Publish(subject, payload); err != nil {
		log.Warn().Err(err).Str("subject", subject).Msg("publish projects.update failed (non-fatal)")
	}
}

// publishProjectsDeleteToHost publishes a NATS message to
// mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete so the controller
// tears down per-project resources (GAP-CP-04).
func publishProjectsDeleteToHost(nc *nats.Conn, userSlug, hostSlug, projectID string) {
	if nc == nil || userSlug == "" || hostSlug == "" {
		return
	}
	subject := subj.UserHostAPIProjectsDelete(slug.UserSlug(userSlug), slug.HostSlug(hostSlug))
	payload, _ := json.Marshal(map[string]string{"projectId": projectID, "event": "deleted"})
	if err := nc.Publish(subject, payload); err != nil {
		log.Warn().Err(err).Str("subject", subject).Str("projectId", projectID).Msg("publish projects.delete failed (non-fatal)")
	}
}
