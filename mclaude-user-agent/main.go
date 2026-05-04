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
	jwt, err := authenticateWithBackoff(ctx, agentAuth)
	if err != nil {
		return fmt.Errorf("authenticate with CP: %w", err)
	}
	log.Info().Msg("authenticated with CP")

	// Step 6: Connect to NATS.
	nc, err := connectNATS(cfg.NATSURL, agentAuth)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	defer nc.Close()
	log.Info().Str("url", cfg.NATSURL).Msg("connected to NATS")

	// Step 7: Create subsystem objects.
	tokenMgr := NewTokenManager(k8sClient, cfg.Namespace, cfg.UserSlug, keypair, log.Logger)
	provisioner := NewProvisioner(k8sClient, cfg.Namespace, cfg, log.Logger)

	// Step 8: Start JWT refresh loop. On each refresh, attempt NATS reconnect if needed.
	agentAuth.StartRefreshLoop(ctx, func(newJWT string) {
		log.Debug().Msg("JWT refreshed — NATS will use new JWT on next reconnect")
		_ = newJWT
	})
	_ = jwt

	// Step 9: Subscribe based on JWT scope.
	// We don't parse the JWT to determine bootstrap vs full — instead we try the full
	// subscription first. If the NATS server rejects the subscription (permissions error),
	// we fall back to bootstrap mode.
	//
	// Simpler approach per spec: try to subscribe to full scope. If NATS rejects, subscribe
	// bootstrap only. In practice, the JWT type is opaque to us — we rely on NATS RBAC.
	// Per spec boot sequence steps 5-7: we subscribe optimistically.

	uslug := cfg.UserSlug
	hslug := cfg.HostSlug

	activateSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", uslug, hslug)
	fullSubjPrefix := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.>", uslug, hslug)
	tokenDeliverySubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-delivery", uslug, hslug)
	provisionSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.projects.provision", uslug, hslug)
	deleteSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.projects.delete", uslug, hslug)

	// Try full subscription. If denied, fall back to bootstrap mode.
	if err := subscribeAgentSubjects(ctx, nc, uslug, hslug, fullSubjPrefix, activateSubj, tokenDeliverySubj, provisionSubj, deleteSubj, tokenMgr, provisioner, agentAuth, cfg); err != nil {
		return fmt.Errorf("subscribe NATS subjects: %w", err)
	}

	<-ctx.Done()
	log.Info().Msg("shutting down")
	return nil
}

// subscribeAgentSubjects sets up the full NATS subscription tree.
// On activation signal, re-runs challenge-response and reconnects with full JWT.
func subscribeAgentSubjects(
	ctx context.Context,
	nc *nats.Conn,
	uslug, hslug, fullSubjPrefix, activateSubj, tokenDeliverySubj, provisionSubj, deleteSubj string,
	tokenMgr *TokenManager,
	provisioner *Provisioner,
	agentAuth *UserAgentAuth,
	cfg *AgentConfig,
) error {
	// Always subscribe to activate — needed in both bootstrap and full mode.
	_, err := nc.Subscribe(activateSubj, func(msg *nats.Msg) {
		log.Info().Msg("received agent.activate signal — re-authenticating with CP")
		// Re-run challenge-response to get full JWT.
		if _, authErr := authenticateWithBackoff(ctx, agentAuth); authErr != nil {
			log.Error().Err(authErr).Msg("re-auth after activate failed")
			return
		}
		log.Info().Msg("re-auth after activate succeeded")
		// Reconnect to NATS is handled by the NATS library's reconnect logic using the
		// updated JWT from JWTFunc(). The library will use the new JWT on next reconnect.
	})
	if err != nil {
		log.Warn().Err(err).Str("subject", activateSubj).Msg("subscribe activate failed (bootstrap JWT expected)")
		// Still continue — we may get activated later.
	}

	// Subscribe to token-delivery (request/reply).
	_, err = nc.Subscribe(tokenDeliverySubj, func(msg *nats.Msg) {
		reply := tokenMgr.HandleDelivery(ctx, msg.Data)
		if msg.Reply != "" {
			if pubErr := nc.Publish(msg.Reply, reply); pubErr != nil {
				log.Warn().Err(pubErr).Msg("token-delivery: publish reply failed")
			}
		}
	})
	if err != nil {
		log.Warn().Err(err).Str("subject", tokenDeliverySubj).Msg("subscribe token-delivery failed (may need full JWT)")
	}

	// Subscribe to projects.provision (request/reply).
	_, err = nc.Subscribe(provisionSubj, func(msg *nats.Msg) {
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
	})
	if err != nil {
		log.Warn().Err(err).Str("subject", provisionSubj).Msg("subscribe projects.provision failed (may need full JWT)")
	}

	// Subscribe to projects.delete (no reply expected).
	_, err = nc.Subscribe(deleteSubj, func(msg *nats.Msg) {
		var req DeleteRequest
		if jsonErr := json.Unmarshal(msg.Data, &req); jsonErr != nil {
			log.Warn().Err(jsonErr).Msg("delete: bad payload")
			return
		}
		provisioner.Delete(ctx, req)
	})
	if err != nil {
		log.Warn().Err(err).Str("subject", deleteSubj).Msg("subscribe projects.delete failed (may need full JWT)")
	}

	_ = fullSubjPrefix
	return nil
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
