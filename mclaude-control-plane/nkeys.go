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
// $KV.mclaude-sessions.{userId}.>) and their slug
// ($KV.mclaude-hosts.{userSlug}.> per ADR-0004).
func UserSubjectPermissions(userID string, userSlug string) NATSPermissions {
	prefix := fmt.Sprintf("mclaude.%s.>", userID)
	kvProjects := fmt.Sprintf("$KV.mclaude-projects.%s.>", userID)
	kvSessions := fmt.Sprintf("$KV.mclaude-sessions.%s.>", userID)
	kvHosts := fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)
	return NATSPermissions{
		PubAllow: []string{prefix, "_INBOX.>", "$JS.API.>"},
		SubAllow: []string{prefix, "_INBOX.>", kvProjects, kvSessions, kvHosts, "$JS.API.>", "$JS.API.DIRECT.GET.>"},
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

// HostSubjectPermissions returns the NATS pub/sub permissions for a per-host
// user JWT (ADR-0035). uslug may be "*" for cluster controllers (wildcard at
// the user level).
func HostSubjectPermissions(uslug, hslug string) NATSPermissions {
	prefix := fmt.Sprintf("mclaude.users.%s.hosts.%s.>", uslug, hslug)
	return NATSPermissions{
		PubAllow: []string{prefix, "_INBOX.>", "$JS.*.API.>", "$SYS.ACCOUNT.*.CONNECT", "$SYS.ACCOUNT.*.DISCONNECT"},
		SubAllow: []string{prefix, "_INBOX.>", "$JS.*.API.>"},
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
// userID is the user's UUID; it is stored in claims.Name so that
// authMiddleware can pass it to db.GetUserByID for authenticated API calls.
// userSlug is the user's URL-safe slug used to scope $KV.mclaude-hosts.{userSlug}.>
// per ADR-0004. LoginResponse carries UserSlug separately (ADR-0046).
//
// Returns the encoded JWT string and the user's NKey seed.
// The seed must be returned to the client alongside the JWT — the client
// needs the seed to sign NATS connection nonce challenges.
func IssueUserJWT(userID string, userSlug string, accountKP nkeys.KeyPair, expirySecs int64) (jwt string, seed []byte, err error) {
	userKP, userSeed, err := GenerateUserNKey()
	if err != nil {
		return "", nil, fmt.Errorf("generate user nkey: %w", err)
	}

	perms := UserSubjectPermissions(userID, userSlug)

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

// IssueHostJWT issues a per-host NATS user JWT scoped to
// mclaude.users.{uslug}.hosts.{hslug}.> (ADR-0035).
//
// For machine hosts: uslug is the user's slug, hslug is the host slug.
// For cluster controllers: uslug is "*" (wildcard), hslug is the cluster slug.
//
// Returns the encoded JWT string and the NKey seed.
func IssueHostJWT(uslug, hslug string, accountKP nkeys.KeyPair) (jwt string, seed []byte, err error) {
	userKP, userSeed, err := GenerateUserNKey()
	if err != nil {
		return "", nil, fmt.Errorf("generate host nkey: %w", err)
	}

	perms := HostSubjectPermissions(uslug, hslug)

	claims := natsjwt.NewUserClaims(userKP.PublicKey)
	claims.Name = fmt.Sprintf("host-%s-%s", uslug, hslug)
	// No expiry for host credentials (service credentials).
	claims.Permissions.Pub.Allow = perms.PubAllow
	claims.Permissions.Sub.Allow = perms.SubAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode host jwt: %w", err)
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

// DecodeUserJWT decodes and validates a NATS user JWT.
// Returns the claims if the JWT was issued by accountPubKey and is not expired.
func DecodeUserJWT(token string, accountPubKey string) (*natsjwt.UserClaims, error) {
	claims, err := natsjwt.DecodeUserClaims(token)
	if err != nil {
		return nil, fmt.Errorf("decode user jwt: %w", err)
	}
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
