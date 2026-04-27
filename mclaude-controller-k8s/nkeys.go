package main

import (
	"fmt"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// SessionAgentSubjectPermissions returns permissions for a session agent.
func SessionAgentSubjectPermissions(userID string) (pubAllow, subAllow []string) {
	prefix := fmt.Sprintf("mclaude.%s.>", userID)
	return []string{prefix}, []string{prefix}
}

// IssueSessionAgentJWT issues a long-lived NATS user JWT for a session-agent.
func IssueSessionAgentJWT(userID string, accountKP nkeys.KeyPair) (jwt string, seed []byte, err error) {
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

	pubAllow, subAllow := SessionAgentSubjectPermissions(userID)

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
