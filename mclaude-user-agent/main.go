package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// AgentConfig holds environment-sourced configuration for the user-agent.
type AgentConfig struct {
	UserSlug        string
	HostSlug        string
	Namespace       string
	NATSURL         string
	ControlPlaneURL string
}

func envRequired(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatal().Str("env", key).Msg("required environment variable not set")
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	level, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stderr).With().
		Str("component", "user-agent").
		Timestamp().
		Logger()

	cfg := &AgentConfig{
		UserSlug:        envRequired("USER_SLUG"),
		HostSlug:        envRequired("HOST_SLUG"),
		Namespace:       envOr("NAMESPACE", "mclaude-"+os.Getenv("USER_SLUG")),
		NATSURL:         envRequired("NATS_URL"),
		ControlPlaneURL: envRequired("CONTROL_PLANE_URL"),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatal().Err(err).Msg("user-agent exiting with error")
	}
}

func run(ctx context.Context, cfg *AgentConfig) error {
	// Step 1: Read nkey-seed from /secrets/agent/nkey-seed. Fatal if missing.
	seedBytes, err := os.ReadFile("/secrets/agent/nkey-seed")
	if err != nil {
		return fmt.Errorf("read nkey-seed: %w (controller did not provision correctly)", err)
	}

	// Step 2: Build auth client.
	agentAuth, err := NewUserAgentAuth(seedBytes, cfg.ControlPlaneURL, log.Logger)
	if err != nil {
		return fmt.Errorf("create user-agent auth: %w", err)
	}

	// Step 3: Initialize k8s client.
	k8sClient, err := newK8sClient()
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Step 4: Load or generate X25519 keypair (Boot step 2).
	keypair, err := LoadOrGenerateX25519Keypair(ctx, k8sClient, cfg.Namespace, "user-agent-key")
	if err != nil {
		return fmt.Errorf("load/generate X25519 keypair: %w", err)
	}
	agentAuth.SetX25519Pub(keypair.Public[:])
	log.Info().
		Str("x25519_pub", fmt.Sprintf("%x", keypair.Public)).
		Msg("X25519 keypair ready")

	// Step 5: Authenticate with CP (Boot step 3) with exponential backoff.
	_, err = authenticateWithBackoff(ctx, agentAuth)
	if err != nil {
		return fmt.Errorf("authenticate with CP: %w", err)
	}
	log.Info().Msg("authenticated with CP")

	// Step 6: Create subsystem objects.
	tokenMgr := NewTokenManager(k8sClient, cfg.Namespace, cfg.UserSlug, keypair, log.Logger)
	provisioner := NewProvisioner(k8sClient, cfg.Namespace, cfg, log.Logger)

	// Step 7: Start JWT refresh loop. The refresh loop re-authenticates before TTL expiry
	// so the NATS JWTFunc always returns a valid JWT for reconnects.
	agentAuth.StartRefreshLoop(ctx, func(newJWT string) {
		log.Debug().Msg("JWT refreshed — NATS will use new JWT on next reconnect")
		_ = newJWT
	})

	// Step 8: NATS connect + subscribe loop.
	// On activation: drain connection, reconnect with full JWT, re-subscribe.
	return runNATSLoop(ctx, cfg, agentAuth, tokenMgr, provisioner, connectNATS)
}

// natsConnectFn is a function that creates a NATS connection. Injected for testing.
type natsConnectFn func(natsURL string, auth *UserAgentAuth) (*nats.Conn, error)

// runNATSLoop connects to NATS, subscribes to agent subjects, and loops on reconnect
// signals (triggered by activate). Each iteration:
//  1. Connect with current JWT (bootstrap or full).
//  2. Subscribe based on JWT scope (bootstrap → activate-only; full → all subjects).
//  3. Wait for ctx done or an activate-triggered reconnect signal.
//  4. Drain and reconnect with the updated full JWT.
//
// The connectFn parameter is injected for testing; production code passes connectNATS.
func runNATSLoop(
	ctx context.Context,
	cfg *AgentConfig,
	agentAuth *UserAgentAuth,
	tokenMgr *TokenManager,
	provisioner *Provisioner,
	connectFn natsConnectFn,
) error {
	uslug := cfg.UserSlug
	hslug := cfg.HostSlug

	activateSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", uslug, hslug)
	tokenDeliverySubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-delivery", uslug, hslug)
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
			if _, authErr := authenticateWithBackoff(ctx, agentAuth); authErr != nil {
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
		}); err != nil {
			log.Warn().Err(err).Str("subject", tokenDeliverySubj).Msg("subscribe token-delivery failed (bootstrap JWT — will re-subscribe after activate)")
		}

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

// authenticateWithBackoff runs challenge-response with CP using exponential backoff.
// Retries until success or context cancellation.
func authenticateWithBackoff(ctx context.Context, auth *UserAgentAuth) (string, error) {
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

// connectNATS creates a NATS connection using the user-agent's JWT + NKey signing.
func connectNATS(natsURL string, auth *UserAgentAuth) (*nats.Conn, error) {
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

// newK8sClient creates an in-cluster Kubernetes client.
func newK8sClient() (kubernetes.Interface, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("create k8s client: %w", err)
	}
	return client, nil
}

// ensure metav1 is used (imported for Secret patches).
var _ = metav1.GetOptions{}
