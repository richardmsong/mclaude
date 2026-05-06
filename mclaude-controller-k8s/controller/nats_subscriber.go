// nats_subscriber.go implements the NATS subscription for the K8s controller.
//
// Per ADR-0091, the controller subscribes to the single host-scoped pattern:
//
//	mclaude.hosts.{hslug}.>
//
// Dispatched subjects:
//
//	grants.create    — per-user provisioning (namespace, RBAC, user-agent Deployment)
//	users.*.projects.*.update — re-reconcile user-secrets for OAuth token rotation
//
// Removed per ADR-0091:
//
//	users.*.projects.*.create — delegated to user-agent via agent.projects.provision
//	users.*.projects.*.delete — delegated to user-agent via agent.projects.delete
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
)

// NATSSubscriber subscribes to host-scoped subjects and dispatches to the UserReconciler.
type NATSSubscriber struct {
	nc             *nats.Conn
	hslug          string
	userReconciler *UserReconciler
	logger         zerolog.Logger
}

// StartNATSSubscriber subscribes to the single host-scoped pattern
// mclaude.hosts.{hslug}.> and dispatches messages by subject suffix.
func (s *NATSSubscriber) StartNATSSubscriber() error {
	hostSubject := fmt.Sprintf("mclaude.hosts.%s.>", s.hslug)
	s.logger.Info().Str("subject", hostSubject).Msg("subscribing to host-scoped subjects (ADR-0091)")
	if _, err := s.nc.Subscribe(hostSubject, func(msg *nats.Msg) {
		s.dispatch(msg)
	}); err != nil {
		return fmt.Errorf("subscribe host-scoped subject: %w", err)
	}
	return nil
}

// dispatch routes a NATS message to the correct handler based on subject suffix.
func (s *NATSSubscriber) dispatch(msg *nats.Msg) {
	prefix := fmt.Sprintf("mclaude.hosts.%s.", s.hslug)
	suffix := strings.TrimPrefix(msg.Subject, prefix)

	switch {
	case suffix == "grants.create":
		s.handleGrantCreate(msg)

	case matchesProjectsUpdate(suffix):
		// users.{uslug}.projects.{pslug}.update — re-reconcile user-secrets.
		uslug := extractProjectsUSlug(suffix)
		if uslug == "" {
			s.logger.Warn().Str("subject", msg.Subject).Msg("could not parse uslug from projects.update subject")
			return
		}
		s.handleProjectsUpdate(msg, uslug)

	default:
		// Log unrecognized subjects at debug level — expected for subjects like
		// api.agents.user-agent.register which are handled by CP (not the controller).
		s.logger.Debug().
			Str("subject", msg.Subject).
			Str("suffix", suffix).
			Msg("unrecognized subject suffix — ignored")
	}
}

// handleGrantCreate processes mclaude.hosts.{hslug}.grants.create.
// Payload: {"uslug": "<user-slug>"}
// Triggers the full per-user provisioning reconcile.
func (s *NATSSubscriber) handleGrantCreate(msg *nats.Msg) {
	var payload GrantCreatePayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		s.logger.Error().Err(err).Str("subject", msg.Subject).Msg("parse grants.create payload")
		return
	}
	if payload.USlug == "" {
		s.logger.Error().Str("subject", msg.Subject).Msg("grants.create: missing uslug field")
		return
	}

	s.logger.Info().
		Str("uslug", payload.USlug).
		Msg("grants.create received — starting per-user provisioning")

	ctx := context.Background()
	if err := s.userReconciler.ReconcileUser(ctx, payload.USlug); err != nil {
		s.logger.Error().Err(err).Str("uslug", payload.USlug).Msg("per-user provisioning failed")
		return
	}

	s.logger.Info().
		Str("uslug", payload.USlug).
		Msg("per-user provisioning complete")
}

// handleProjectsUpdate processes users.*.projects.*.update (request/reply).
// Re-reconciles user-secrets for OAuth token rotation.
// Per spec: this update touches only the per-user OAuth credential helpers in user-secrets.
// Project pods themselves are owned by user-agent — this update does not touch them.
func (s *NATSSubscriber) handleProjectsUpdate(msg *nats.Msg, uslug string) {
	s.logger.Info().Str("uslug", uslug).Msg("projects.update received — re-reconciling user-secrets")

	ctx := context.Background()
	if err := s.userReconciler.ReconcileSecretsForUser(ctx, uslug); err != nil {
		s.logger.Error().Err(err).Str("uslug", uslug).Msg("reconcile user-secrets failed")
		replyError(msg, err.Error(), "reconcile_failed")
		return
	}

	replyOK(msg)
}

// matchesProjectsUpdate returns true if the subject suffix matches
// users.{uslug}.projects.{pslug}.update.
func matchesProjectsUpdate(suffix string) bool {
	parts := strings.Split(suffix, ".")
	// users.<uslug>.projects.<pslug>.update → 5 parts
	return len(parts) == 5 &&
		parts[0] == "users" &&
		parts[2] == "projects" &&
		parts[4] == "update"
}

// extractProjectsUSlug extracts the uslug from a subject suffix of the form
// users.{uslug}.projects.{pslug}.update.
func extractProjectsUSlug(suffix string) string {
	parts := strings.Split(suffix, ".")
	if len(parts) != 5 {
		return ""
	}
	return parts[1]
}

// replyOK sends an OK reply to a request/reply NATS message.
func replyOK(msg *nats.Msg) {
	if msg.Reply == "" {
		return
	}
	type okReply struct {
		OK bool `json:"ok"`
	}
	data, _ := json.Marshal(okReply{OK: true})
	_ = msg.Respond(data)
}

// replyError sends an error reply to a request/reply NATS message.
func replyError(msg *nats.Msg, errMsg, code string) {
	if msg.Reply == "" {
		return
	}
	type errReply struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Code  string `json:"code,omitempty"`
	}
	data, _ := json.Marshal(errReply{OK: false, Error: errMsg, Code: code})
	_ = msg.Respond(data)
}
