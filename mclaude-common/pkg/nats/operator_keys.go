package nats

import (
	"fmt"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// OperatorAccount holds the bootstrap key material for the NATS 3-tier
// trust chain (operator → account → user). Generated once by the
// mclaude-cp init-keys Helm Job and persisted to a K8s Secret.
type OperatorAccount struct {
	// OperatorSeed is the operator NKey seed (private key).
	OperatorSeed []byte
	// OperatorPublicKey is the operator NKey public key (O…).
	OperatorPublicKey string
	// AccountSeed is the account NKey seed (private key).
	AccountSeed []byte
	// AccountPublicKey is the account NKey public key (A…).
	AccountPublicKey string
	// OperatorJWT is the self-signed operator JWT.
	OperatorJWT string
	// AccountJWT is the account JWT signed by the operator.
	AccountJWT string
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

	// 3. Issue self-signed operator JWT.
	opClaims := natsjwt.NewOperatorClaims(opPub)
	opClaims.Name = operatorName
	opJWT, err := opClaims.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("encode operator jwt: %w", err)
	}

	// 4. Issue account JWT signed by the operator.
	acctClaims := natsjwt.NewAccountClaims(acctPub)
	acctClaims.Name = accountName
	acctJWT, err := acctClaims.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("encode account jwt: %w", err)
	}

	return &OperatorAccount{
		OperatorSeed:      opSeed,
		OperatorPublicKey: opPub,
		AccountSeed:       acctSeed,
		AccountPublicKey:  acctPub,
		OperatorJWT:       opJWT,
		AccountJWT:        acctJWT,
	}, nil
}
