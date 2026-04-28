// nats_subscriber.go implements the NATS subscription for project provisioning
// requests from the control-plane. Per ADR-0035, the cluster controller subscribes
// to mclaude.users.*.hosts.{cluster-slug}.api.projects.> (wildcard at user level).
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	logger          zerolog.Logger
}

// StartNATSSubscriber subscribes to the provisioning subjects for this cluster.
// Subject pattern: mclaude.users.*.hosts.{clusterSlug}.api.projects.>
func (p *NATSProvisioner) StartNATSSubscriber() error {
	subject := fmt.Sprintf("mclaude.users.*.hosts.%s.api.projects.>", p.clusterSlug)
	p.logger.Info().Str("subject", subject).Msg("subscribing to provisioning requests")

	_, err := p.nc.Subscribe(subject, func(msg *nats.Msg) {
		p.handleProvisionRequest(msg)
	})
	return err
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
	case "create", "provision":
		err := p.handleCreate(ctx, req)
		if err != nil {
			p.logger.Error().Err(err).
				Str("projectSlug", req.ProjectSlug).
				Msg("provisioning failed")
			p.replyError(msg, err.Error(), "provision_failed")
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
		p.replyError(msg, fmt.Sprintf("unknown operation: %s", operation), "unknown_operation")
	}
}

// handleCreate creates an MCProject CR for the provisioning request.
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
		if k8serrors.IsAlreadyExists(err) {
			return nil // idempotent
		}
		return fmt.Errorf("create MCProject CR: %w", err)
	}

	p.logger.Info().
		Str("cr", crName).
		Str("userSlug", req.UserSlug).
		Str("projectSlug", req.ProjectSlug).
		Msg("MCProject CR created — reconciler will provision resources")

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
