package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	agentauth "mclaude-user-agent/auth"
)

// NATSConnectFn is a function that creates a NATS connection. Injected for testing.
type NATSConnectFn func(natsURL string, auth *agentauth.UserAgentAuth) (*nats.Conn, error)

// RunNATSLoop connects to NATS, subscribes to agent subjects, and loops on reconnect
// signals (triggered by activate). Each iteration:
//  1. Connect with current JWT (bootstrap or full).
//  2. Subscribe based on JWT scope (bootstrap → activate-only; full → all subjects).
//  3. Wait for ctx done or an activate-triggered reconnect signal.
//  4. Drain and reconnect with the updated full JWT.
//
// The connectFn parameter is injected for testing; production code passes ConnectNATS.
func RunNATSLoop(
	ctx context.Context,
	cfg *Config,
	agentAuth *agentauth.UserAgentAuth,
	tokenMgr *TokenManager,
	provisioner *Provisioner,
	connectFn NATSConnectFn,
) error {
	uslug := cfg.UserSlug
	hslug := cfg.HostSlug

	activateSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", uslug, hslug)
	tokenDeliverySubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-delivery", uslug, hslug)
	tokenRevokeSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-revoke", uslug, hslug)
	agentStatusSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.status", uslug, hslug)
	provisionSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.projects.provision", uslug, hslug)
	deleteSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.projects.delete", uslug, hslug)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Connect with current JWT.
		nc, connErr := connectFn(cfg.NATSURL, agentAuth)
		if connErr != nil {
			return fmt.Errorf("connect to NATS: %w", connErr)
		}
		log.Info().Str("url", cfg.NATSURL).Msg("connected to NATS")

		// reconnectCh is signalled by the activate handler to trigger reconnect
		// after re-auth with the full JWT.
		reconnectCh := make(chan struct{}, 1)

		// Subscribe to activate — needed in both bootstrap and full mode.
		_, subErr := nc.Subscribe(activateSubj, func(msg *nats.Msg) {
			log.Info().Msg("received agent.activate signal — re-authenticating with CP")
			if _, authErr := AuthenticateWithBackoff(ctx, agentAuth); authErr != nil {
				log.Error().Err(authErr).Msg("re-auth after activate failed")
				return
			}
			log.Info().Msg("re-auth after activate succeeded — triggering NATS reconnect")
			// Signal the loop to drain and reconnect with the new full JWT.
			select {
			case reconnectCh <- struct{}{}:
			default:
				// Already a reconnect pending; skip.
			}
		})
		if subErr != nil {
			nc.Close()
			return fmt.Errorf("subscribe activate: %w", subErr)
		}

		// Subscribe to token-delivery (request/reply).
		// NATS RBAC will reject this subscription if we have a bootstrap JWT; the
		// error is logged and the agent continues in activate-only mode until the
		// reconnect loop upgrades to a full JWT.
		if _, err := nc.Subscribe(tokenDeliverySubj, func(msg *nats.Msg) {
			reply := tokenMgr.HandleDelivery(ctx, msg.Data)
			if msg.Reply != "" {
				if pubErr := nc.Publish(msg.Reply, reply); pubErr != nil {
					log.Warn().Err(pubErr).Msg("token-delivery: publish reply failed")
				}
			}
			// ADR-0092: publish unsolicited status after token change (delivery success or failure).
			// Publish regardless of ok — even a failed delivery may change perceived state.
			publishUnsolicitedStatus(ctx, nc, agentStatusSubj, tokenMgr)
		}); err != nil {
			log.Warn().Err(err).Str("subject", tokenDeliverySubj).Msg("subscribe token-delivery failed (bootstrap JWT — will re-subscribe after activate)")
		}

		// Subscribe to token-revoke (request/reply) — ADR-0092.
		// NATS RBAC rejects if bootstrap JWT; logged as warning.
		if _, err := nc.Subscribe(tokenRevokeSubj, func(msg *nats.Msg) {
			reply := tokenMgr.HandleRevoke(ctx, msg.Data)
			if msg.Reply != "" {
				if pubErr := nc.Publish(msg.Reply, reply); pubErr != nil {
					log.Warn().Err(pubErr).Msg("token-revoke: publish reply failed")
				}
			}
			// Publish unsolicited status after revoke (token removed — hasToken may change).
			publishUnsolicitedStatus(ctx, nc, agentStatusSubj, tokenMgr)
		}); err != nil {
			log.Warn().Err(err).Str("subject", tokenRevokeSubj).Msg("subscribe token-revoke failed (bootstrap JWT — will re-subscribe after activate)")
		}

		// Subscribe to agent.status (request/reply) — ADR-0092.
		// NATS RBAC rejects if bootstrap JWT; logged as warning.
		if _, err := nc.Subscribe(agentStatusSubj, func(msg *nats.Msg) {
			reply := tokenMgr.HandleStatus(ctx, msg.Data)
			if msg.Reply != "" {
				if pubErr := nc.Publish(msg.Reply, reply); pubErr != nil {
					log.Warn().Err(pubErr).Msg("agent.status: publish reply failed")
				}
			}
		}); err != nil {
			log.Warn().Err(err).Str("subject", agentStatusSubj).Msg("subscribe agent.status failed (bootstrap JWT — will re-subscribe after activate)")
		}

		// ADR-0092: publish unsolicited status on startup so any watching SPA gets current state.
		publishUnsolicitedStatus(ctx, nc, agentStatusSubj, tokenMgr)

		// Subscribe to projects.provision (request/reply).
		if _, err := nc.Subscribe(provisionSubj, func(msg *nats.Msg) {
			var req ProvisionRequest
			if jsonErr := json.Unmarshal(msg.Data, &req); jsonErr != nil {
				log.Warn().Err(jsonErr).Msg("provision: bad payload")
				reply := ProvisionReply{Error: "bad_request"}
				replyData, _ := json.Marshal(reply)
				if msg.Reply != "" {
					_ = nc.Publish(msg.Reply, replyData)
				}
				return
			}
			reply := provisioner.Provision(ctx, req)
			if msg.Reply != "" {
				replyData, _ := json.Marshal(reply)
				if pubErr := nc.Publish(msg.Reply, replyData); pubErr != nil {
					log.Warn().Err(pubErr).Msg("provision: publish reply failed")
				}
			}
		}); err != nil {
			log.Warn().Err(err).Str("subject", provisionSubj).Msg("subscribe projects.provision failed (bootstrap JWT — will re-subscribe after activate)")
		}

		// Subscribe to projects.delete (no reply expected).
		if _, err := nc.Subscribe(deleteSubj, func(msg *nats.Msg) {
			var req DeleteRequest
			if jsonErr := json.Unmarshal(msg.Data, &req); jsonErr != nil {
				log.Warn().Err(jsonErr).Msg("delete: bad payload")
				return
			}
			provisioner.Delete(ctx, req)
		}); err != nil {
			log.Warn().Err(err).Str("subject", deleteSubj).Msg("subscribe projects.delete failed (bootstrap JWT — will re-subscribe after activate)")
		}

		// Wait: either context done (shutdown) or activate-triggered reconnect.
		select {
		case <-ctx.Done():
			log.Info().Msg("shutting down")
			_ = nc.Drain()
			return nil
		case <-reconnectCh:
			// Drain current connection cleanly so in-flight messages complete,
			// then reconnect with the new full JWT on the next loop iteration.
			log.Info().Msg("draining NATS connection before reconnect with full JWT")
			if drainErr := nc.Drain(); drainErr != nil {
				log.Warn().Err(drainErr).Msg("drain failed; closing instead")
				nc.Close()
			}
			log.Info().Msg("NATS connection drained — reconnecting with full JWT")
			// Loop continues: next iteration calls connectFn with the updated JWT.
		}
	}
}

// AuthenticateWithBackoff runs challenge-response with CP using exponential backoff.
// Retries until success or context cancellation.
func AuthenticateWithBackoff(ctx context.Context, auth *agentauth.UserAgentAuth) (string, error) {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 2 * time.Second
	bo.MaxInterval = 60 * time.Second

	jwt, err := backoff.Retry(ctx, func() (string, error) {
		jwt, authErr := auth.Authenticate(ctx)
		if authErr != nil {
			log.Warn().Err(authErr).Msg("challenge-response failed; retrying")
			return "", authErr
		}
		return jwt, nil
	}, backoff.WithBackOff(bo))
	return jwt, err
}

// publishUnsolicitedStatus publishes the current agent status to agentStatusSubj
// without a reply-to address. Used on startup and after token changes (ADR-0092).
// Errors are logged and silently ignored — unsolicited publishes are best-effort.
func publishUnsolicitedStatus(ctx context.Context, nc *nats.Conn, subj string, tokenMgr *TokenManager) {
	data := tokenMgr.HandleStatus(ctx, nil)
	if pubErr := nc.Publish(subj, data); pubErr != nil {
		log.Warn().Err(pubErr).Str("subject", subj).Msg("agent.status: unsolicited publish failed")
	}
}

// ConnectNATS creates a NATS connection using the user-agent's JWT + NKey signing.
func ConnectNATS(natsURL string, auth *agentauth.UserAgentAuth) (*nats.Conn, error) {
	nc, err := nats.Connect(
		natsURL,
		nats.UserJWT(auth.JWTFunc(), auth.SignFunc()),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Name("mclaude-user-agent"),
	)
	if err != nil {
		return nil, fmt.Errorf("nats.Connect(%s): %w", natsURL, err)
	}
	return nc, nil
}
