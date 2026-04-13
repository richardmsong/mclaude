package main

import (
	"fmt"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// NATSPermissions holds the pub/sub allow-lists for a NATS user JWT.
type NATSPermissions struct {
	PubAllow []string
	SubAllow []string
}

// UserSubjectPermissions returns the NATS pub/sub permissions for a user.
// Clients may operate on their own mclaude.{userId}.> namespace, the
// _INBOX.> namespace (required for request/reply), and the KV buckets
// scoped to their user ID ($KV.mclaude-projects.{userId}.> and
// $KV.mclaude-sessions.{userId}.>).
func UserSubjectPermissions(userID string) NATSPermissions {
	prefix := fmt.Sprintf("mclaude.%s.>", userID)
	kvProjects := fmt.Sprintf("$KV.mclaude-projects.%s.>", userID)
	kvSessions := fmt.Sprintf("$KV.mclaude-sessions.%s.>", userID)
	return NATSPermissions{
		PubAllow: []string{prefix, "_INBOX.>"},
		SubAllow: []string{prefix, "_INBOX.>", kvProjects, kvSessions},
	}
}

// SessionAgentSubjectPermissions returns permissions for a session agent.
// Session agents only need access to their user's namespace (no _INBOX since
// they don't use request/reply).
func SessionAgentSubjectPermissions(userID string) NATSPermissions {
	prefix := fmt.Sprintf("mclaude.%s.>", userID)
	return NATSPermissions{
		PubAllow: []string{prefix},
		SubAllow: []string{prefix},
	}
}

// NKeyPair wraps an nkeys.KeyPair with its encoded public key.
type NKeyPair struct {
	KeyPair   nkeys.KeyPair
	PublicKey string
}

// GenerateOperatorNKey generates a new NATS operator-level NKey pair.
func GenerateOperatorNKey() (*NKeyPair, error) {
	kp, err := nkeys.CreateOperator()
	if err != nil {
		return nil, fmt.Errorf("create operator nkey: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("operator public key: %w", err)
	}
	return &NKeyPair{KeyPair: kp, PublicKey: pub}, nil
}

// GenerateAccountNKey generates a new NATS account-level NKey pair.
func GenerateAccountNKey() (*NKeyPair, error) {
	kp, err := nkeys.CreateAccount()
	if err != nil {
		return nil, fmt.Errorf("create account nkey: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("account public key: %w", err)
	}
	return &NKeyPair{KeyPair: kp, PublicKey: pub}, nil
}

// GenerateUserNKey generates a new NATS user-level NKey pair.
// Returns the key pair and the seed (private key) to return to the client.
func GenerateUserNKey() (*NKeyPair, []byte, error) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		return nil, nil, fmt.Errorf("create user nkey: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, nil, fmt.Errorf("user public key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, nil, fmt.Errorf("user seed: %w", err)
	}
	return &NKeyPair{KeyPair: kp, PublicKey: pub}, seed, nil
}

// IssueUserJWT issues a NATS user JWT scoped to mclaude.{userID}.>
// signed by the given account key pair.
//
// Returns the encoded JWT string and the user's NKey seed.
// The seed must be returned to the client alongside the JWT — the client
// needs the seed to sign NATS connection nonce challenges.
func IssueUserJWT(userID string, accountKP nkeys.KeyPair, expirySecs int64) (jwt string, seed []byte, err error) {
	userKP, userSeed, err := GenerateUserNKey()
	if err != nil {
		return "", nil, fmt.Errorf("generate user nkey: %w", err)
	}

	perms := UserSubjectPermissions(userID)

	claims := natsjwt.NewUserClaims(userKP.PublicKey)
	claims.Name = userID
	claims.Expires = expirySecs
	claims.Permissions.Pub.Allow = perms.PubAllow
	claims.Permissions.Sub.Allow = perms.SubAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode user jwt: %w", err)
	}

	return encoded, userSeed, nil
}

// IssueSessionAgentJWT issues a long-lived NATS user JWT for a session-agent,
// scoped to mclaude.{userID}.> with no _INBOX.> (session-agents don't use
// request/reply). No expiry — these are service credentials.
//
// Returns the encoded JWT string and the NKey seed. Both are written into the
// K8s user-secrets Secret as a NATS credentials file.
func IssueSessionAgentJWT(userID string, accountKP nkeys.KeyPair) (jwt string, seed []byte, err error) {
	userKP, userSeed, err := GenerateUserNKey()
	if err != nil {
		return "", nil, fmt.Errorf("generate session-agent nkey: %w", err)
	}

	perms := SessionAgentSubjectPermissions(userID)

	claims := natsjwt.NewUserClaims(userKP.PublicKey)
	claims.Name = "session-agent-" + userID
	// Expires = 0 means no expiry for session-agent service credentials.
	claims.Permissions.Pub.Allow = perms.PubAllow
	claims.Permissions.Sub.Allow = perms.SubAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode session-agent jwt: %w", err)
	}

	return encoded, userSeed, nil
}

// FormatNATSCredentials formats a NATS credentials file from a JWT and NKey seed.
// The format is the standard NATS .creds file format understood by nats.UserCredentials().
func FormatNATSCredentials(jwt string, seed []byte) []byte {
	return []byte("-----BEGIN NATS USER JWT-----\n" +
		jwt + "\n" +
		"------END NATS USER JWT------\n" +
		"\n" +
		"************************* IMPORTANT *************************\n" +
		"NKEY Seed printed below can be used to sign and prove identity.\n" +
		"NKEYs are sensitive and should be treated as secrets.\n" +
		"\n" +
		"-----BEGIN USER NKEY SEED-----\n" +
		string(seed) + "\n" +
		"------END USER NKEY SEED------\n" +
		"\n" +
		"*************************************************************\n")
}

// DecodeUserJWT decodes and validates a NATS user JWT.
// Returns the claims if the JWT was issued by accountPubKey and is not expired.
//
// Verification is two-layered:
//  1. The Issuer field inside the JWT payload must match accountPubKey.
//  2. The claims must pass NATS structural validation (expiry, type, etc.).
//
// Note: the NATS broker performs full NKey cryptographic signature
// verification. The control-plane uses this function to validate its own
// issued tokens for refresh flows — the broker is authoritative for pub/sub.
func DecodeUserJWT(token string, accountPubKey string) (*natsjwt.UserClaims, error) {
	claims, err := natsjwt.DecodeUserClaims(token)
	if err != nil {
		return nil, fmt.Errorf("decode user jwt: %w", err)
	}
	// Verify the JWT was issued by the expected account key.
	// The Issuer field is the NKey public key of the signing account.
	if accountPubKey != "" && claims.Issuer != accountPubKey {
		return nil, fmt.Errorf("jwt issuer %q does not match account key %q", claims.Issuer, accountPubKey)
	}
	vr := natsjwt.CreateValidationResults()
	claims.Validate(vr)
	if vr.IsBlocking(true) {
		return nil, fmt.Errorf("invalid user jwt: %v", vr.Errors())
	}
	return claims, nil
}
