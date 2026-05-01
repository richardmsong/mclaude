package nats

import (
	"fmt"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// OperatorAccount holds the bootstrap key material for the NATS 3-tier
// trust chain (operator → system account → application account → user).
// Generated once by the mclaude-cp init-keys Helm Job and persisted to a K8s Secret.
type OperatorAccount struct {
	OperatorSeed      []byte
	OperatorPublicKey string
	AccountSeed       []byte
	AccountPublicKey  string
	OperatorJWT       string
	AccountJWT        string
	// SysAccountSeed is the system account NKey seed. Used by the control-plane
	// to connect with system account credentials and publish
	// $SYS.REQ.CLAIMS.UPDATE for runtime JWT revocation (ADR-0054).
	// The full NATS resolver (resolver: nats) must be configured on the hub
	// for revocation to take effect immediately.
	SysAccountSeed      []byte
	SysAccountPublicKey string
	SysAccountJWT       string
}

// GenerateOperatorAccount generates a fresh operator + account NKey pair
// and the corresponding JWTs for the NATS auth trust chain.
//
// The operator JWT is self-signed; the account JWT is signed by the operator.
// operatorName and accountName are embedded in the JWT claims for
// identification (e.g. "mclaude-operator", "mclaude-account").
func GenerateOperatorAccount(operatorName, accountName string) (*OperatorAccount, error) {
	// 1. Generate operator NKey pair.
	opKP, err := nkeys.CreateOperator()
	if err != nil {
		return nil, fmt.Errorf("create operator nkey: %w", err)
	}
	opPub, err := opKP.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("operator public key: %w", err)
	}
	opSeed, err := opKP.Seed()
	if err != nil {
		return nil, fmt.Errorf("operator seed: %w", err)
	}

	// 2. Generate account NKey pair.
	acctKP, err := nkeys.CreateAccount()
	if err != nil {
		return nil, fmt.Errorf("create account nkey: %w", err)
	}
	acctPub, err := acctKP.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("account public key: %w", err)
	}
	acctSeed, err := acctKP.Seed()
	if err != nil {
		return nil, fmt.Errorf("account seed: %w", err)
	}

	// 3. Generate system account NKey pair (dedicated, no JetStream).
	sysKP, err := nkeys.CreateAccount()
	if err != nil {
		return nil, fmt.Errorf("create sys account nkey: %w", err)
	}
	sysPub, err := sysKP.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("sys account public key: %w", err)
	}
	sysSeed, err := sysKP.Seed()
	if err != nil {
		return nil, fmt.Errorf("sys account seed: %w", err)
	}

	// 4. Issue self-signed operator JWT with system account.
	opClaims := natsjwt.NewOperatorClaims(opPub)
	opClaims.Name = operatorName
	opClaims.SystemAccount = sysPub
	opJWT, err := opClaims.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("encode operator jwt: %w", err)
	}

	// 5. Issue system account JWT (no JetStream).
	sysClaims := natsjwt.NewAccountClaims(sysPub)
	sysClaims.Name = "SYS"
	sysJWT, err := sysClaims.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("encode sys account jwt: %w", err)
	}

	// 6. Issue application account JWT with JetStream enabled.
	acctClaims := natsjwt.NewAccountClaims(acctPub)
	acctClaims.Name = accountName
	acctClaims.Limits.JetStreamLimits = natsjwt.JetStreamLimits{
		MemoryStorage: -1,
		DiskStorage:   -1,
		Streams:       -1,
		Consumer:      -1,
	}
	acctJWT, err := acctClaims.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("encode account jwt: %w", err)
	}

	return &OperatorAccount{
		OperatorSeed:        opSeed,
		OperatorPublicKey:   opPub,
		AccountSeed:         acctSeed,
		AccountPublicKey:    acctPub,
		OperatorJWT:         opJWT,
		AccountJWT:          acctJWT,
		SysAccountSeed:      sysSeed,
		SysAccountPublicKey: sysPub,
		SysAccountJWT:       sysJWT,
	}, nil
}
