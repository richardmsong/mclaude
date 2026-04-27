package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// ProjectKVState is the value written to the mclaude-projects JetStream KV bucket.
// Must match the TypeScript ProjectKVState in mclaude-web/src/types.ts.
type ProjectKVState struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	GitURL        string  `json:"gitUrl"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"createdAt"`
	GitIdentityID *string `json:"gitIdentityId,omitempty"`
}

// ProvisionRequest is the NATS request payload sent from control-plane
// to the appropriate controller (K8s or local) per ADR-0035.
type ProvisionRequest struct {
	UserSlug      string `json:"userSlug"`
	HostSlug      string `json:"hostSlug"`
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

// ProvisionTimeoutSeconds is the NATS request/reply timeout for project provisioning.
const ProvisionTimeoutSeconds = 10

// StartProjectsSubscriber connects to NATS, ensures the mclaude-projects and
// mclaude-job-queue KV buckets exist, and subscribes to
// mclaude.*.api.projects.create.
// The caller owns the *nats.Conn lifetime — Close() it on shutdown.
func (s *Server) StartProjectsSubscriber(nc *nats.Conn) error {
	js, err := nc.JetStream()
	if err != nil {
		return err
	}

	kv, err := ensureProjectsKV(js)
	if err != nil {
		return err
	}

	if _, err := ensureJobQueueKV(js); err != nil {
		return err
	}

	// subject pattern: mclaude.{userID}.api.projects.create
	_, err = nc.Subscribe("mclaude.*.api.projects.create", func(msg *nats.Msg) {
		// Extract userID from subject token index 1
		parts := strings.Split(msg.Subject, ".")
		if len(parts) < 2 {
			replyError(msg, "malformed subject")
			return
		}
		userID := parts[1]

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

		if s.db == nil {
			replyError(msg, "service unavailable")
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

		// ADR-0035: publish provisioning request to the host-scoped subject
		// so the appropriate controller (K8s or local) can provision resources.
		if req.HostSlug != "" {
			gitIdentityIDStr := ""
			if req.GitIdentityID != nil {
				gitIdentityIDStr = *req.GitIdentityID
			}
			provReq := ProvisionRequest{
				UserSlug:      userID,
				HostSlug:      req.HostSlug,
				ProjectSlug:   proj.Slug,
				GitURL:        req.GitURL,
				GitIdentityID: gitIdentityIDStr,
			}
			provData, _ := json.Marshal(provReq)
			provSubject := "mclaude.users." + userID + ".hosts." + req.HostSlug + ".api.projects.create"
			reply, err := nc.Request(provSubject, provData, ProvisionTimeoutSeconds*time.Second)
			if err != nil {
				log.Error().Err(err).
					Str("userId", userID).
					Str("hostSlug", req.HostSlug).
					Str("projectId", id).
					Msg("provisioning request timed out — host unreachable")
			} else {
				var provReply ProvisionReply
				if jsonErr := json.Unmarshal(reply.Data, &provReply); jsonErr == nil && !provReply.OK {
					log.Error().
						Str("userId", userID).
						Str("projectId", id).
						Str("error", provReply.Error).
						Msg("provisioning failed")
				}
			}
		}

		// Write to KV so session-store watchers pick it up immediately.
		state := ProjectKVState{
			ID:            proj.ID,
			Name:          proj.Name,
			GitURL:        proj.GitURL,
			Status:        proj.Status,
			CreatedAt:     proj.CreatedAt.UTC().Format(time.RFC3339),
			GitIdentityID: proj.GitIdentityID,
		}
		val, _ := json.Marshal(state)
		if _, err := kv.Put(userID+"."+id, val); err != nil {
			// Non-fatal: DB row was created; KV is best-effort.
			_ = err
		}

		reply, _ := json.Marshal(map[string]string{"id": id})
		_ = msg.Respond(reply)
	})
	return err
}

// writeProjectKV writes a Project to the mclaude-projects JetStream KV bucket.
func writeProjectKV(nc *nats.Conn, userID string, proj *Project) error {
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

// replyError sends a JSON error reply if the message has a reply subject.
func replyError(msg *nats.Msg, errMsg string) {
	if msg.Reply == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{"error": errMsg})
	_ = msg.Respond(b)
}
