package agent

import "github.com/nats-io/nats.go"

// NATSProbeConnect connects to NATS for one-shot health/readiness probes.
// Unlike NATSConnect, it does NOT retry on failure so probes fail fast.
func NATSProbeConnect(natsURL, credsFile string) (*nats.Conn, error) {
	opts := []nats.Option{}
	if credsFile != "" {
		opts = append(opts, nats.UserCredentials(credsFile))
	}
	return nats.Connect(natsURL, opts...)
}

// NATSConnect connects to NATS, using a credentials file if one is provided.
// Unlimited reconnects and retry-on-failed-connect ensure BYOH daemons never
// permanently lose the NATS connection (spec: GAP-SA-K15).
func NATSConnect(natsURL, credsFile string) (*nats.Conn, error) {
	return NATSConnectWithAuth(natsURL, credsFile, nil)
}

// NATSConnectWithAuth connects to NATS with optional HTTP challenge-response auth (ADR-0054).
// If agentAuth is non-nil, uses nats.UserJWT() with the AgentAuth callbacks.
// Otherwise falls back to nats.UserCredentials(credsFile) if credsFile is non-empty.
func NATSConnectWithAuth(natsURL, credsFile string, agentAuth *AgentAuth) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	}
	if agentAuth != nil {
		opts = append(opts, nats.UserJWT(agentAuth.JWTFunc(), agentAuth.SignFunc()))
	} else if credsFile != "" {
		opts = append(opts, nats.UserCredentials(credsFile))
	}
	return nats.Connect(natsURL, opts...)
}
