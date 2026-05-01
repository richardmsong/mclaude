package main

import (
	"fmt"
	"strings"
	"time"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// NATSPermissions holds the pub/sub allow-lists for a NATS user JWT.
type NATSPermissions struct {
	PubAllow []string
	SubAllow []string
}

// userSessionsBucket returns the per-user sessions KV bucket name.
func userSessionsBucket(uslug string) string {
	return "mclaude-sessions-" + uslug
}

// userProjectsBucket returns the per-user projects KV bucket name.
func userProjectsBucket(uslug string) string {
	return "mclaude-projects-" + uslug
}

// userSessionsStream returns the per-user sessions JetStream stream name.
func userSessionsStream(uslug string) string {
	return "MCLAUDE_SESSIONS_" + uslug
}

// kvStreamName returns the JetStream stream name for a KV bucket.
// KV bucket "foo" is backed by stream "KV_foo".
func kvStreamName(bucket string) string {
	return "KV_" + bucket
}

// UserSubjectPermissions returns the NATS pub/sub permissions for a user (SPA/CLI).
// Per ADR-0054: explicit, per-user-resource scoped allow-lists replacing broad wildcards.
// hostSlugs is the list of host slugs the user has access to (owned + granted).
func UserSubjectPermissions(uslug string, hostSlugs []string) NATSPermissions {
	sessBucket := userSessionsBucket(uslug)
	projBucket := userProjectsBucket(uslug)
	sessStream := userSessionsStream(uslug)
	sessStreamName := kvStreamName(sessBucket)
	projStreamName := kvStreamName(projBucket)

	pub := []string{
		// Core user subjects
		"mclaude.users." + uslug + ".hosts.*.>",
		"_INBOX.>",
		// KV direct-get (any key in own buckets)
		"$JS.API.DIRECT.GET." + sessStreamName + ".>",
		"$JS.API.DIRECT.GET." + projStreamName + ".>",
		// KV watch (consumer create) on own buckets
		"$JS.API.CONSUMER.CREATE." + sessStreamName + ".>",
		"$JS.API.CONSUMER.CREATE." + projStreamName + ".>",
		// Session stream consumer
		"$JS.API.CONSUMER.CREATE." + sessStream + ".>",
		// Stream info for KV init (NATS client requires this)
		"$JS.API.STREAM.INFO." + sessStreamName,
		"$JS.API.STREAM.INFO." + projStreamName,
		"$JS.API.STREAM.INFO." + sessStream,
		// Consumer info
		"$JS.API.CONSUMER.INFO." + sessStreamName + ".*",
		"$JS.API.CONSUMER.INFO." + projStreamName + ".*",
		"$JS.API.CONSUMER.INFO.KV_mclaude-hosts.*",
		"$JS.API.CONSUMER.INFO." + sessStream + ".*",
		// Ack consumed messages
		"$JS.ACK." + sessStreamName + ".>",
		"$JS.ACK." + projStreamName + ".>",
		"$JS.ACK.KV_mclaude-hosts.>",
		"$JS.ACK." + sessStream + ".>",
		// Flow control
		"$JS.FC." + sessStreamName + ".>",
		"$JS.FC." + projStreamName + ".>",
		"$JS.FC.KV_mclaude-hosts.>",
		"$JS.FC." + sessStream + ".>",
	}

	sub := []string{
		// Core user subjects
		"mclaude.users." + uslug + ".hosts.*.>",
		"_INBOX.>",
		// KV watch push delivery
		"$KV." + sessBucket + ".hosts.>",
		"$KV." + projBucket + ".hosts.>",
		// Flow control
		"$JS.FC." + sessStreamName + ".>",
		"$JS.FC." + projStreamName + ".>",
		"$JS.FC.KV_mclaude-hosts.>",
		"$JS.FC." + sessStream + ".>",
	}

	// Per-host entries for the shared mclaude-hosts KV bucket.
	// One entry per accessible host slug (read access is scoped per-host in JWT).
	for _, hslug := range hostSlugs {
		// Direct-get: subject-form with full $KV.mclaude-hosts.{hslug} path
		pub = append(pub,
			"$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts."+hslug,
			"$JS.API.CONSUMER.CREATE.KV_mclaude-hosts.*.$KV.mclaude-hosts."+hslug,
		)
		// KV watch push delivery (per-host)
		sub = append(sub, "$KV.mclaude-hosts."+hslug)
	}

	return NATSPermissions{PubAllow: pub, SubAllow: sub}
}

// SessionAgentSubjectPermissions returns permissions for a session-agent.
// Per ADR-0054: per-project scoped — agent can only access its own user's
// resources, scoped to one specific host and project.
//
// uslug is the owning user's slug.
// hslug is the host slug where the agent runs.
// pslug is the project slug the agent manages.
func SessionAgentSubjectPermissions(uslug, hslug, pslug string) NATSPermissions {
	sessBucket := userSessionsBucket(uslug)
	projBucket := userProjectsBucket(uslug)
	sessStream := userSessionsStream(uslug)
	sessStreamName := kvStreamName(sessBucket)
	projStreamName := kvStreamName(projBucket)

	// KV key prefixes for this project
	sessKeyPrefix := "$KV." + sessBucket + ".hosts." + hslug + ".projects." + pslug + ".sessions."
	projKey := "$KV." + projBucket + ".hosts." + hslug + ".projects." + pslug

	pub := []string{
		// Project-scoped command subjects
		"mclaude.users." + uslug + ".hosts." + hslug + ".projects." + pslug + ".>",
		"_INBOX.>",
		// KV write: this project's sessions
		sessKeyPrefix + ">",
		// KV write: this project's state
		projKey,
		// KV direct-get (subject-form, per-project)
		"$JS.API.DIRECT.GET." + sessStreamName + ".$KV." + sessBucket + ".hosts." + hslug + ".projects." + pslug + ".sessions.>",
		"$JS.API.DIRECT.GET." + projStreamName + ".$KV." + projBucket + ".hosts." + hslug + ".projects." + pslug,
		"$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts." + hslug,
		// KV watch (consumer create, filtered)
		"$JS.API.CONSUMER.CREATE." + sessStreamName + ".*.$KV." + sessBucket + ".hosts." + hslug + ".projects." + pslug + ".sessions.>",
		"$JS.API.CONSUMER.CREATE." + projStreamName + ".*.$KV." + projBucket + ".hosts." + hslug + ".projects." + pslug,
		"$JS.API.CONSUMER.CREATE.KV_mclaude-hosts.*.$KV.mclaude-hosts." + hslug,
		// Session stream (filtered to this project)
		"$JS.API.CONSUMER.CREATE." + sessStream + ".*.mclaude.users." + uslug + ".hosts." + hslug + ".projects." + pslug + ".sessions.>",
		// Stream info (per-user stream, required for KV init — residual R4 accepted)
		"$JS.API.STREAM.INFO." + sessStreamName,
		"$JS.API.STREAM.INFO." + projStreamName,
		"$JS.API.STREAM.INFO." + sessStream,
		// Consumer info
		"$JS.API.CONSUMER.INFO." + sessStreamName + ".*",
		"$JS.API.CONSUMER.INFO." + projStreamName + ".*",
		"$JS.API.CONSUMER.INFO.KV_mclaude-hosts.*",
		"$JS.API.CONSUMER.INFO." + sessStream + ".*",
		// Ack consumed messages
		"$JS.ACK." + sessStreamName + ".>",
		"$JS.ACK." + projStreamName + ".>",
		"$JS.ACK.KV_mclaude-hosts.>",
		"$JS.ACK." + sessStream + ".>",
		// Flow control
		"$JS.FC." + sessStreamName + ".>",
		"$JS.FC." + projStreamName + ".>",
		"$JS.FC.KV_mclaude-hosts.>",
		"$JS.FC." + sessStream + ".>",
		// Quota publish (ADR-0044)
		"mclaude.users." + uslug + ".quota",
	}

	sub := []string{
		// Project-scoped subjects only
		"mclaude.users." + uslug + ".hosts." + hslug + ".projects." + pslug + ".>",
		"mclaude.users." + uslug + ".quota",
		"_INBOX.>",
		// KV watch push delivery (per-project)
		sessKeyPrefix + ">",
		projKey,
		"$KV.mclaude-hosts." + hslug,
		// Flow control
		"$JS.FC." + sessStreamName + ".>",
		"$JS.FC." + projStreamName + ".>",
		"$JS.FC.KV_mclaude-hosts.>",
		"$JS.FC." + sessStream + ".>",
	}

	return NATSPermissions{PubAllow: pub, SubAllow: sub}
}

// HostSubjectPermissions returns NATS pub/sub permissions for a host controller.
// Per ADR-0054: host-scoped subjects only, zero JetStream access.
// This JWT is constant-size regardless of how many users share the host.
func HostSubjectPermissions(hslug string) NATSPermissions {
	prefix := "mclaude.hosts." + hslug + ".>"
	return NATSPermissions{
		PubAllow: []string{prefix, "_INBOX.>"},
		SubAllow: []string{
			prefix,
			"_INBOX.>",
			"$SYS.ACCOUNT.*.CONNECT",
			"$SYS.ACCOUNT.*.DISCONNECT",
		},
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
// NOTE: Per ADR-0054, the CP no longer generates NKey pairs for clients.
// Clients generate their own NKey pairs. This function is retained for
// generating the CP's own service connection credentials only.
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

// issueJWT is the internal helper that signs a JWT for the given public key.
// All external public keys are NKey user-level keys (prefix "U").
func issueJWT(publicKey string, claimName string, accountKP nkeys.KeyPair, expirySecs int64, perms NATSPermissions) (string, error) {
	claims := natsjwt.NewUserClaims(publicKey)
	claims.Name = claimName
	if expirySecs > 0 {
		claims.Expires = time.Now().Unix() + expirySecs
	}
	claims.IssuerAccount, _ = accountKP.PublicKey()
	claims.Permissions.Pub.Allow = perms.PubAllow
	claims.Permissions.Sub.Allow = perms.SubAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", fmt.Errorf("encode jwt: %w", err)
	}
	return encoded, nil
}

// IssueUserJWT issues a NATS user JWT for a user identified by their NKey public key.
// Per ADR-0054: the client generates the NKey pair and sends only the public key.
// CP receives the public key, issues a scoped JWT, and returns it (no seed).
//
// publicKey: the user's NKey public key (generated by SPA/CLI, stored in users.nkey_public).
// userID: the user's UUID, stored in claims.Name for auth middleware lookup (GetUserByID).
// userSlug: the user's URL-safe slug (used in subject patterns and resource names).
// hostSlugs: slugs of hosts the user has access to (owned + granted via host_access).
func IssueUserJWT(publicKey string, userID string, userSlug string, hostSlugs []string, accountKP nkeys.KeyPair, expirySecs int64) (string, error) {
	perms := UserSubjectPermissions(userSlug, hostSlugs)
	// claims.Name = userID (UUID) so the auth middleware can call GetUserByID.
	return issueJWT(publicKey, userID, accountKP, expirySecs, perms)
}

// IssueHostJWT issues a NATS user JWT for a host controller.
// Per ADR-0054: host-scoped subjects only, zero JetStream access, 5-min TTL.
//
// publicKey: the host controller's NKey public key (generated at registration,
//   stored in hosts.public_key).
// hslug: the host's slug.
func IssueHostJWT(publicKey string, hslug string, accountKP nkeys.KeyPair) (string, error) {
	perms := HostSubjectPermissions(hslug)
	const hostTTLSecs = 5 * 60 // 5-minute TTL (ADR-0054)
	return issueJWT(publicKey, "host-"+hslug, accountKP, hostTTLSecs, perms)
}

// IssueSessionAgentJWT issues a NATS user JWT for a session-agent.
// Per ADR-0054: per-project scoped, 5-min TTL.
//
// publicKey: the agent's NKey public key (generated at startup,
//   registered via mclaude.hosts.{hslug}.api.agents.register).
// uslug: the owning user's slug.
// hslug: the host slug where the agent runs.
// pslug: the project slug the agent manages.
func IssueSessionAgentJWT(publicKey string, uslug, hslug, pslug string, accountKP nkeys.KeyPair) (string, error) {
	perms := SessionAgentSubjectPermissions(uslug, hslug, pslug)
	const agentTTLSecs = 5 * 60 // 5-minute TTL (ADR-0054)
	return issueJWT(publicKey, "agent-"+uslug+"-"+hslug+"-"+pslug, accountKP, agentTTLSecs, perms)
}

// IssueUserJWTLegacy is a backward-compatible wrapper that generates an NKey pair
// and issues a user JWT with the OLD broad-wildcard permissions PLUS per-user KV
// bucket permissions required by ADR-0061.
// DEPRECATED: Use IssueUserJWT with a client-provided public key.
// Retained for the old /auth/login and /auth/refresh flows during migration.
func IssueUserJWTLegacy(userID string, userSlug string, accountKP nkeys.KeyPair, expirySecs int64) (jwt string, seed []byte, err error) {
	userKP, userSeed, err := GenerateUserNKey()
	if err != nil {
		return "", nil, fmt.Errorf("generate user nkey: %w", err)
	}

	// Legacy broad permissions (pre-ADR-0054) — used only when client has not
	// provided an NKey public key (old login protocol).
	prefix := fmt.Sprintf("mclaude.%s.>", userID)
	kvProjects := fmt.Sprintf("$KV.mclaude-projects.%s.>", userID)
	kvSessions := fmt.Sprintf("$KV.mclaude-sessions.%s.>", userID)
	kvHosts := fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)
	hostsPrefix := fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)

	// ADR-0061: per-user KV bucket subjects required for the SPA to open the
	// new per-user KV buckets (mclaude-sessions-{uslug}, mclaude-projects-{uslug}).
	// The old shared-bucket $KV entries (kvSessions, kvProjects) used userID as a
	// prefix; the new per-user buckets use userSlug in the bucket name itself.
	sessBucket := userSessionsBucket(userSlug)
	projBucket := userProjectsBucket(userSlug)
	sessStreamName := kvStreamName(sessBucket)
	projStreamName := kvStreamName(projBucket)
	kvPerUserSessions := "$KV." + sessBucket + ".>"
	kvPerUserProjects := "$KV." + projBucket + ".>"

	perms := NATSPermissions{
		PubAllow: []string{
			prefix,
			"_INBOX.>",
			"$JS.API.>",
			hostsPrefix,
			// ADR-0061: stream info needed by NATS client for KV bucket init on per-user buckets.
			"$JS.API.STREAM.INFO." + sessStreamName,
			"$JS.API.STREAM.INFO." + projStreamName,
			// ADR-0061: consumer create for KV watch on per-user buckets.
			"$JS.API.CONSUMER.CREATE." + sessStreamName + ".>",
			"$JS.API.CONSUMER.CREATE." + projStreamName + ".>",
			// ADR-0061: KV put on per-user buckets (pub side of $KV subjects).
			kvPerUserSessions,
			kvPerUserProjects,
		},
		SubAllow: []string{
			prefix,
			"_INBOX.>",
			kvProjects,
			kvSessions,
			kvHosts,
			"$JS.API.>",
			"$JS.API.DIRECT.GET.>",
			hostsPrefix,
			// ADR-0061: KV watch push delivery on per-user buckets (sub side of $KV subjects).
			kvPerUserSessions,
			kvPerUserProjects,
		},
	}

	claims := natsjwt.NewUserClaims(userKP.PublicKey)
	claims.Name = userID
	if expirySecs > 0 {
		claims.Expires = time.Now().Unix() + expirySecs
	}
	claims.IssuerAccount, _ = accountKP.PublicKey()
	claims.Permissions.Pub.Allow = perms.PubAllow
	claims.Permissions.Sub.Allow = perms.SubAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode user jwt: %w", err)
	}

	return encoded, userSeed, nil
}

// IssueHostJWTLegacy is a backward-compatible wrapper that generates an NKey pair
// and issues a host JWT. DEPRECATED: Use IssueHostJWT with a client-provided public key.
func IssueHostJWTLegacy(uslug, hslug string, accountKP nkeys.KeyPair) (jwt string, seed []byte, err error) {
	userKP, userSeed, err := GenerateUserNKey()
	if err != nil {
		return "", nil, fmt.Errorf("generate host nkey: %w", err)
	}

	perms := HostSubjectPermissions(hslug)

	claims := natsjwt.NewUserClaims(userKP.PublicKey)
	claims.Name = fmt.Sprintf("host-%s-%s", uslug, hslug)
	// 5-minute TTL for host JWTs (ADR-0054)
	claims.Expires = time.Now().Unix() + 5*60
	claims.IssuerAccount, _ = accountKP.PublicKey()
	claims.Permissions.Pub.Allow = perms.PubAllow
	claims.Permissions.Sub.Allow = perms.SubAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode host jwt: %w", err)
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

// VerifyNKeySignature verifies that the signature was produced by the NKey
// identified by publicKey over the challenge nonce.
// Returns nil on success, error on failure.
func VerifyNKeySignature(publicKey string, challenge []byte, signature []byte) error {
	kp, err := nkeys.FromPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	if err := kp.Verify(challenge, signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

// UserSubjectPermissionsLegacy returns old pre-ADR-0054 permissions.
// Used in existing tests that verify the old permission structure.
// DEPRECATED — retained only for backward compat test reference.
func UserSubjectPermissionsLegacy(userID string, userSlug string) NATSPermissions {
	prefix := fmt.Sprintf("mclaude.%s.>", userID)
	kvProjects := fmt.Sprintf("$KV.mclaude-projects.%s.>", userID)
	kvSessions := fmt.Sprintf("$KV.mclaude-sessions.%s.>", userID)
	kvHosts := fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)
	hostsPrefix := fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)
	return NATSPermissions{
		PubAllow: []string{prefix, "_INBOX.>", "$JS.API.>", hostsPrefix},
		SubAllow: []string{prefix, "_INBOX.>", kvProjects, kvSessions, kvHosts, "$JS.API.>", "$JS.API.DIRECT.GET.>", hostsPrefix},
	}
}

// permContains checks if a permission list contains a given subject.
func permContains(perms []string, subject string) bool {
	for _, p := range perms {
		if p == subject {
			return true
		}
	}
	return false
}

// permHasPrefix checks if any permission in the list starts with the given prefix.
func permHasPrefix(perms []string, prefix string) bool {
	for _, p := range perms {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
