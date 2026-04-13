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
	ID        string `json:"id"`
	Name      string `json:"name"`
	GitURL    string `json:"gitUrl"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// StartProjectsSubscriber connects to NATS, ensures the mclaude-projects KV
// bucket exists, and subscribes to mclaude.*.api.projects.create.
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
			Name   string `json:"name"`
			GitURL string `json:"gitUrl"`
		}
		if err := json.Unmarshal(msg.Data, &req); err != nil || req.Name == "" {
			replyError(msg, "name required")
			return
		}

		if s.db == nil {
			replyError(msg, "service unavailable")
			return
		}

		id := uuid.NewString()
		proj, err := s.db.CreateProject(context.Background(), id, userID, req.Name, req.GitURL)
		if err != nil {
			replyError(msg, "failed to create project")
			return
		}

		// Create MCProject CR to trigger the reconciler to provision K8s resources.
		// When k8sClient is available (running in cluster), we use the reconciler-driven
		// path. Falls back to direct provisioning via K8sProvisioner for backward
		// compatibility when the Manager is not running (e.g., local dev without cluster).
		// Non-fatal — project record and KV entry are always created.
		if s.k8sClient != nil {
			if err := CreateMCProject(context.Background(), s.k8sClient, s.controlPlaneNs, userID, id, req.GitURL); err != nil {
				log.Error().Err(err).
					Str("userId", userID).
					Str("projectId", id).
					Msg("create MCProject CR failed — session-agent pod will not start")
			} else {
				log.Info().
					Str("userId", userID).
					Str("projectId", id).
					Msg("MCProject CR created — reconciler will provision K8s resources")
			}
		} else if s.k8sProvisioner != nil {
			if err := s.k8sProvisioner.ProvisionProject(context.Background(), userID, id, req.GitURL); err != nil {
				log.Error().Err(err).
					Str("userId", userID).
					Str("projectId", id).
					Msg("k8s provisioning failed — session-agent pod will not start")
			} else {
				log.Info().
					Str("userId", userID).
					Str("projectId", id).
					Msg("k8s resources provisioned for project")
			}
		}

		// Write to KV so session-store watchers pick it up immediately.
		state := ProjectKVState{
			ID:        proj.ID,
			Name:      proj.Name,
			GitURL:    proj.GitURL,
			Status:    proj.Status,
			CreatedAt: proj.CreatedAt.UTC().Format(time.RFC3339),
		}
		val, _ := json.Marshal(state)
		if _, err := kv.Put(userID+"."+id, val); err != nil {
			// Non-fatal: DB row was created; KV is best-effort and will be
			// back-filled if the control-plane reconnects.
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
		ID:        proj.ID,
		Name:      proj.Name,
		GitURL:    proj.GitURL,
		Status:    proj.Status,
		CreatedAt: proj.CreatedAt.UTC().Format(time.RFC3339),
	}
	val, _ := json.Marshal(state)
	// Key uses "." as separator — NATS uses "." as the token separator for
	// wildcard matching (">" and "*"). Using "/" would break kvWatch patterns.
	_, err = kv.Put(userID+"."+proj.ID, val)
	return err
}

// ensureProjectsKV creates the mclaude-projects KV bucket if it doesn't exist.
func ensureProjectsKV(js nats.JetStreamContext) (nats.KeyValue, error) {
	kv, err := js.KeyValue("mclaude-projects")
	if err == nil {
		return kv, nil
	}
	// Bucket doesn't exist — create it.
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  "mclaude-projects",
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
