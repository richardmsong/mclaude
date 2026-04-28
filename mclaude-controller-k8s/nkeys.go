package main

import (
	"fmt"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// SessionAgentSubjectPermissions returns permissions for a session agent.
// Per ADR-0050: both UUID-prefixed and host-scoped slug subjects, plus JetStream and inbox.
func SessionAgentSubjectPermissions(userID, userSlug string) (pubAllow, subAllow []string) {
	perms := []string{
		fmt.Sprintf("mclaude.%s.>", userID),
		fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug),
		"_INBOX.>",
		"$JS.API.>",
		"$JS.*.API.>",
	}
	return perms, perms
}

// IssueSessionAgentJWT issues a long-lived NATS user JWT for a session-agent.
func IssueSessionAgentJWT(userID, userSlug string, accountKP nkeys.KeyPair) (jwt string, seed []byte, err error) {
	userKP, err := nkeys.CreateUser()
	if err != nil {
		return "", nil, fmt.Errorf("create user nkey: %w", err)
	}
	pub, err := userKP.PublicKey()
	if err != nil {
		return "", nil, fmt.Errorf("user public key: %w", err)
	}
	userSeed, err := userKP.Seed()
	if err != nil {
		return "", nil, fmt.Errorf("user seed: %w", err)
	}

	pubAllow, subAllow := SessionAgentSubjectPermissions(userID, userSlug)

	claims := natsjwt.NewUserClaims(pub)
	claims.Name = "session-agent-" + userID
	claims.Permissions.Pub.Allow = pubAllow
	claims.Permissions.Sub.Allow = subAllow

	encoded, err := claims.Encode(accountKP)
	if err != nil {
		return "", nil, fmt.Errorf("encode session-agent jwt: %w", err)
	}

	return encoded, userSeed, nil
}
