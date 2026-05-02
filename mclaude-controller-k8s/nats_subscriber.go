// nats_subscriber.go implements the NATS subscription for project provisioning
// requests from the control-plane. Per ADR-0063 (aligning with ADR-0054), the
// controller subscribes to the single host-scoped pattern:
//
//	mclaude.hosts.{hslug}.>
//
// The legacy dual subscription ("during ADR-0054 migration") is dropped.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProvisionRequest is the NATS request payload from control-plane (ADR-0035, ADR-0050).
type ProvisionRequest struct {
	UserID        string `json:"userId"`
	ProjectID     string `json:"projectId"`
	UserSlug      string `json:"userSlug"`
	HostSlug      string `json:"hostSlug"`
	ProjectSlug   string `json:"projectSlug"`
	GitURL        string `json:"gitUrl,omitempty"`
	GitIdentityID string `json:"gitIdentityId,omitempty"`
}

// ProvisionReply is the NATS reply to the control-plane.
type ProvisionReply struct {
	OK          bool   `json:"ok"`
	ProjectSlug string `json:"projectSlug,omitempty"`
	Error       string `json:"error,omitempty"`
	Code        string `json:"code,omitempty"`
}

// NATSProvisioner subscribes to provisioning subjects and creates MCProject CRs.
type NATSProvisioner struct {
	nc              *nats.Conn
	k8sClient       client.Client
	controlPlaneNs  string
	clusterSlug     string
	reconciler      *MCProjectReconciler
	logger          zerolog.Logger
}

// StartNATSSubscriber subscribes to the single host-scoped provisioning subject
// pattern (ADR-0063, aligning with ADR-0054):
//
//	mclaude.hosts.{hslug}.>
//
// CP fan-out provisioning events arrive on this subject; the handler reads
// userSlug/projectSlug/projectID from the JSON payload.
func (p *NATSProvisioner) StartNATSSubscriber() error {
	hostSubject := fmt.Sprintf("mclaude.hosts.%s.>", p.clusterSlug)
	p.logger.Info().Str("subject", hostSubject).Msg("subscribing to host-scoped provisioning requests (ADR-0054/ADR-0063)")
	if _, err := p.nc.Subscribe(hostSubject, func(msg *nats.Msg) {
		p.handleProvisionRequest(msg)
	}); err != nil {
		return fmt.Errorf("subscribe host-scoped subject: %w", err)
	}
	return nil
}

// handleProvisionRequest processes a single provisioning NATS message.
func (p *NATSProvisioner) handleProvisionRequest(msg *nats.Msg) {
	var req ProvisionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		p.replyError(msg, "invalid request payload", "parse_error")
		return
	}

	p.logger.Info().
		Str("userSlug", req.UserSlug).
		Str("hostSlug", req.HostSlug).
		Str("projectSlug", req.ProjectSlug).
		Msg("received provisioning request")

	ctx := context.Background()

	// Determine the operation from the subject suffix.
	// Subject shape: mclaude.users.{uslug}.hosts.{hslug}.api.projects.{operation}
	// We need to extract the operation from the subject.
	operation := extractOperation(msg.Subject)

	switch operation {
	case "create":
		err := p.handleCreate(ctx, req)
		if err != nil {
			p.logger.Error().Err(err).
				Str("projectSlug", req.ProjectSlug).
				Msg("provisioning failed")
			p.replyError(msg, err.Error(), "provision_failed")
			return
		}
		p.replyOK(msg, req.ProjectSlug)

	case "update":
		err := p.handleUpdate(ctx, req)
		if err != nil {
			p.logger.Error().Err(err).
				Str("projectSlug", req.ProjectSlug).
				Msg("update failed")
			p.replyError(msg, err.Error(), "update_failed")
			return
		}
		p.replyOK(msg, req.ProjectSlug)

	case "delete":
		err := p.handleDelete(ctx, req)
		if err != nil {
			p.logger.Error().Err(err).
				Str("projectSlug", req.ProjectSlug).
				Msg("delete failed")
			p.replyError(msg, err.Error(), "delete_failed")
			return
		}
		p.replyOK(msg, req.ProjectSlug)

	default:
		p.logger.Warn().
			Str("subject", msg.Subject).
			Str("operation", operation).
			Msg("unknown subject suffix, ignoring")
	}
}

// handleCreate creates an MCProject CR for the provisioning request,
// then polls until the CR reaches Ready phase before replying success.
func (p *NATSProvisioner) handleCreate(ctx context.Context, req ProvisionRequest) error {
	// Use a deterministic name for the MCProject CR.
	crName := fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug)

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: p.controlPlaneNs,
		},
		Spec: MCProjectSpec{
			UserID:        req.UserID,
			ProjectID:     req.ProjectID,
			UserSlug:      req.UserSlug,
			ProjectSlug:   req.ProjectSlug,
			GitURL:        req.GitURL,
			GitIdentityID: req.GitIdentityID,
		},
	}

	if err := p.k8sClient.Create(ctx, mcp); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("create MCProject CR: %w", err)
		}
		// Already exists — fall through to poll for Ready.
	}

	p.logger.Info().
		Str("cr", crName).
		Str("userSlug", req.UserSlug).
		Str("projectSlug", req.ProjectSlug).
		Msg("MCProject CR created — waiting for Ready phase")

	// Gap 1: Poll the CR status until Ready, Failed, or timeout.
	return p.waitForReady(ctx, crName)
}

// waitForReady polls the MCProject CR until it reaches Ready or Failed phase, or times out.
func (p *NATSProvisioner) waitForReady(ctx context.Context, crName string) error {
	const (
		timeout  = 30 * time.Second
		interval = 500 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)
	key := types.NamespacedName{Name: crName, Namespace: p.controlPlaneNs}

	for time.Now().Before(deadline) {
		var current MCProject
		if err := p.k8sClient.Get(ctx, key, &current); err != nil {
			p.logger.Debug().Err(err).Str("cr", crName).Msg("polling MCProject — get error")
			time.Sleep(interval)
			continue
		}
		switch current.Status.Phase {
		case PhaseReady:
			p.logger.Info().Str("cr", crName).Msg("MCProject reached Ready phase")
			return nil
		case PhaseFailed:
			return fmt.Errorf("MCProject %s reached Failed phase", crName)
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out (30s) waiting for MCProject %s to reach Ready phase", crName)
}

// handleUpdate refreshes the user-secrets Secret for the project (Gap 5).
func (p *NATSProvisioner) handleUpdate(ctx context.Context, req ProvisionRequest) error {
	crName := fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug)
	key := types.NamespacedName{Name: crName, Namespace: p.controlPlaneNs}

	var mcp MCProject
	if err := p.k8sClient.Get(ctx, key, &mcp); err != nil {
		return fmt.Errorf("get MCProject CR: %w", err)
	}

	userNs := "mclaude-" + mcp.Spec.UserSlug // ADR-0062: use slug, not UUID
	if err := p.reconciler.reconcileSecrets(ctx, &mcp, userNs); err != nil {
		return fmt.Errorf("reconcile secrets: %w", err)
	}

	p.logger.Info().
		Str("cr", crName).
		Str("userNs", userNs).
		Msg("user-secrets refreshed via update handler")

	return nil
}

// handleDelete deletes the MCProject CR. Idempotent — returns success if not found.
func (p *NATSProvisioner) handleDelete(ctx context.Context, req ProvisionRequest) error {
	crName := fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug)

	mcp := &MCProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: p.controlPlaneNs,
		},
	}

	if err := p.k8sClient.Delete(ctx, mcp); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil // idempotent
		}
		return fmt.Errorf("delete MCProject CR: %w", err)
	}

	p.logger.Info().
		Str("cr", crName).
		Msg("MCProject CR deleted")

	return nil
}

func (p *NATSProvisioner) replyOK(msg *nats.Msg, projectSlug string) {
	if msg.Reply == "" {
		return
	}
	reply := ProvisionReply{OK: true, ProjectSlug: projectSlug}
	data, _ := json.Marshal(reply)
	_ = msg.Respond(data)
}

func (p *NATSProvisioner) replyError(msg *nats.Msg, errMsg, code string) {
	if msg.Reply == "" {
		return
	}
	reply := ProvisionReply{OK: false, Error: errMsg, Code: code}
	data, _ := json.Marshal(reply)
	_ = msg.Respond(data)
}

// extractOperation extracts the last token from the NATS subject.
// e.g., "mclaude.users.alice.hosts.us-east.api.projects.create" → "create"
func extractOperation(subject string) string {
	for i := len(subject) - 1; i >= 0; i-- {
		if subject[i] == '.' {
			return subject[i+1:]
		}
	}
	return subject
}
