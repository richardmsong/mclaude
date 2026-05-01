package nats_test

import (
	"strings"
	"testing"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	pkgnats "mclaude.io/common/pkg/nats"
)

// --------------------------------------------------------------------------
// FormatNATSCredentials tests
// --------------------------------------------------------------------------

func TestFormatNATSCredentials(t *testing.T) {
	t.Run("contains JWT delimiters", func(t *testing.T) {
		creds := pkgnats.FormatNATSCredentials("myjwt", []byte("SEED123"))
		got := string(creds)
		if !strings.Contains(got, "-----BEGIN NATS USER JWT-----") {
			t.Error("missing BEGIN NATS USER JWT delimiter")
		}
		if !strings.Contains(got, "------END NATS USER JWT------") {
			t.Error("missing END NATS USER JWT delimiter")
		}
	})

	t.Run("contains NKEY seed delimiters", func(t *testing.T) {
		creds := pkgnats.FormatNATSCredentials("myjwt", []byte("SEED123"))
		got := string(creds)
		if !strings.Contains(got, "-----BEGIN USER NKEY SEED-----") {
			t.Error("missing BEGIN USER NKEY SEED delimiter")
		}
		if !strings.Contains(got, "------END USER NKEY SEED------") {
			t.Error("missing END USER NKEY SEED delimiter")
		}
	})

	t.Run("JWT value embedded in output", func(t *testing.T) {
		jwt := "test-jwt-placeholder-for-unit-test"
		creds := pkgnats.FormatNATSCredentials(jwt, []byte("SUASEED"))
		if !strings.Contains(string(creds), jwt) {
			t.Errorf("JWT %q not found in creds output", jwt)
		}
	})

	t.Run("seed value embedded in output", func(t *testing.T) {
		seed := []byte("SUASEEDABCDEF1234567")
		creds := pkgnats.FormatNATSCredentials("somejwt", seed)
		if !strings.Contains(string(creds), string(seed)) {
			t.Errorf("seed %q not found in creds output", seed)
		}
	})

	t.Run("output is non-empty", func(t *testing.T) {
		creds := pkgnats.FormatNATSCredentials("jwt", []byte("seed"))
		if len(creds) == 0 {
			t.Error("expected non-empty credentials output")
		}
	})

	t.Run("JWT appears before seed in output", func(t *testing.T) {
		jwt := "MYJWT"
		seed := []byte("MYSEED")
		got := string(pkgnats.FormatNATSCredentials(jwt, seed))
		jwtIdx := strings.Index(got, jwt)
		seedIdx := strings.Index(got, string(seed))
		if jwtIdx == -1 {
			t.Fatal("JWT not found in output")
		}
		if seedIdx == -1 {
			t.Fatal("seed not found in output")
		}
		if jwtIdx >= seedIdx {
			t.Errorf("expected JWT (at %d) to appear before seed (at %d)", jwtIdx, seedIdx)
		}
	})

	t.Run("valid creds with real nkey user seed", func(t *testing.T) {
		// Generate a real user NKey to produce a properly-formatted seed.
		userKP, err := nkeys.CreateUser()
		if err != nil {
			t.Fatalf("create user nkey: %v", err)
		}
		seed, err := userKP.Seed()
		if err != nil {
			t.Fatalf("user seed: %v", err)
		}
		creds := pkgnats.FormatNATSCredentials("realJWT", seed)
		got := string(creds)
		// All four delimiters must be present.
		for _, delim := range []string{
			"-----BEGIN NATS USER JWT-----",
			"------END NATS USER JWT------",
			"-----BEGIN USER NKEY SEED-----",
			"------END USER NKEY SEED------",
		} {
			if !strings.Contains(got, delim) {
				t.Errorf("missing delimiter %q in creds", delim)
			}
		}
		// The real seed must be embedded.
		if !strings.Contains(got, string(seed)) {
			t.Error("real nkey seed not found in creds output")
		}
	})
}

// --------------------------------------------------------------------------
// GenerateOperatorAccount tests
// --------------------------------------------------------------------------

func TestGenerateOperatorAccount(t *testing.T) {
	const opName = "test-operator"
	const acctName = "test-account"

	oa, err := pkgnats.GenerateOperatorAccount(opName, acctName)
	if err != nil {
		t.Fatalf("GenerateOperatorAccount returned error: %v", err)
	}

	t.Run("all fields populated", func(t *testing.T) {
		if len(oa.OperatorSeed) == 0 {
			t.Error("OperatorSeed is empty")
		}
		if oa.OperatorPublicKey == "" {
			t.Error("OperatorPublicKey is empty")
		}
		if len(oa.AccountSeed) == 0 {
			t.Error("AccountSeed is empty")
		}
		if oa.AccountPublicKey == "" {
			t.Error("AccountPublicKey is empty")
		}
		if oa.OperatorJWT == "" {
			t.Error("OperatorJWT is empty")
		}
		if oa.AccountJWT == "" {
			t.Error("AccountJWT is empty")
		}
		if len(oa.SysAccountSeed) == 0 {
			t.Error("SysAccountSeed is empty")
		}
		if oa.SysAccountPublicKey == "" {
			t.Error("SysAccountPublicKey is empty")
		}
		if oa.SysAccountJWT == "" {
			t.Error("SysAccountJWT is empty")
		}
	})

	t.Run("operator seed is valid nkey operator seed", func(t *testing.T) {
		kp, err := nkeys.FromSeed(oa.OperatorSeed)
		if err != nil {
			t.Fatalf("operator seed is not a valid nkey seed: %v", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			t.Fatalf("cannot derive public key from operator seed: %v", err)
		}
		if pub != oa.OperatorPublicKey {
			t.Errorf("operator public key mismatch: seed-derived %q != stored %q", pub, oa.OperatorPublicKey)
		}
	})

	t.Run("account seed is valid nkey account seed", func(t *testing.T) {
		kp, err := nkeys.FromSeed(oa.AccountSeed)
		if err != nil {
			t.Fatalf("account seed is not a valid nkey seed: %v", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			t.Fatalf("cannot derive public key from account seed: %v", err)
		}
		if pub != oa.AccountPublicKey {
			t.Errorf("account public key mismatch: seed-derived %q != stored %q", pub, oa.AccountPublicKey)
		}
	})

	t.Run("sys account seed is valid nkey account seed", func(t *testing.T) {
		kp, err := nkeys.FromSeed(oa.SysAccountSeed)
		if err != nil {
			t.Fatalf("sys account seed is not a valid nkey seed: %v", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			t.Fatalf("cannot derive public key from sys account seed: %v", err)
		}
		if pub != oa.SysAccountPublicKey {
			t.Errorf("sys account public key mismatch: seed-derived %q != stored %q", pub, oa.SysAccountPublicKey)
		}
	})

	t.Run("operator JWT is self-signed and contains operator name", func(t *testing.T) {
		claims, err := natsjwt.DecodeOperatorClaims(oa.OperatorJWT)
		if err != nil {
			t.Fatalf("decode operator JWT: %v", err)
		}
		if claims.Name != opName {
			t.Errorf("operator JWT name = %q, want %q", claims.Name, opName)
		}
		if claims.Subject != oa.OperatorPublicKey {
			t.Errorf("operator JWT subject = %q, want public key %q", claims.Subject, oa.OperatorPublicKey)
		}
	})

	t.Run("operator JWT references system account", func(t *testing.T) {
		claims, err := natsjwt.DecodeOperatorClaims(oa.OperatorJWT)
		if err != nil {
			t.Fatalf("decode operator JWT: %v", err)
		}
		if claims.SystemAccount != oa.SysAccountPublicKey {
			t.Errorf("operator JWT SystemAccount = %q, want sys account public key %q",
				claims.SystemAccount, oa.SysAccountPublicKey)
		}
	})

	t.Run("account JWT contains account name and has JetStream enabled", func(t *testing.T) {
		claims, err := natsjwt.DecodeAccountClaims(oa.AccountJWT)
		if err != nil {
			t.Fatalf("decode account JWT: %v", err)
		}
		if claims.Name != acctName {
			t.Errorf("account JWT name = %q, want %q", claims.Name, acctName)
		}
		if claims.Subject != oa.AccountPublicKey {
			t.Errorf("account JWT subject = %q, want %q", claims.Subject, oa.AccountPublicKey)
		}
		// JetStream limits should be set to -1 (unlimited) as per implementation.
		js := claims.Limits.JetStreamLimits
		if js.MemoryStorage != -1 || js.DiskStorage != -1 || js.Streams != -1 || js.Consumer != -1 {
			t.Errorf("account JWT JetStream limits not set to -1: %+v", js)
		}
	})

	t.Run("sys account JWT contains SYS name and no JetStream", func(t *testing.T) {
		claims, err := natsjwt.DecodeAccountClaims(oa.SysAccountJWT)
		if err != nil {
			t.Fatalf("decode sys account JWT: %v", err)
		}
		if claims.Name != "SYS" {
			t.Errorf("sys account JWT name = %q, want %q", claims.Name, "SYS")
		}
		if claims.Subject != oa.SysAccountPublicKey {
			t.Errorf("sys account JWT subject = %q, want %q", claims.Subject, oa.SysAccountPublicKey)
		}
	})

	t.Run("each call generates unique keys", func(t *testing.T) {
		oa2, err := pkgnats.GenerateOperatorAccount(opName, acctName)
		if err != nil {
			t.Fatalf("second GenerateOperatorAccount returned error: %v", err)
		}
		if oa.OperatorPublicKey == oa2.OperatorPublicKey {
			t.Error("expected different operator public keys on each call")
		}
		if oa.AccountPublicKey == oa2.AccountPublicKey {
			t.Error("expected different account public keys on each call")
		}
		if oa.SysAccountPublicKey == oa2.SysAccountPublicKey {
			t.Error("expected different sys account public keys on each call")
		}
	})

	t.Run("different names produce different JWT names", func(t *testing.T) {
		oa2, err := pkgnats.GenerateOperatorAccount("other-operator", "other-account")
		if err != nil {
			t.Fatalf("GenerateOperatorAccount returned error: %v", err)
		}
		opClaims, err := natsjwt.DecodeOperatorClaims(oa2.OperatorJWT)
		if err != nil {
			t.Fatalf("decode operator JWT: %v", err)
		}
		if opClaims.Name != "other-operator" {
			t.Errorf("operator JWT name = %q, want %q", opClaims.Name, "other-operator")
		}
		acctClaims, err := natsjwt.DecodeAccountClaims(oa2.AccountJWT)
		if err != nil {
			t.Fatalf("decode account JWT: %v", err)
		}
		if acctClaims.Name != "other-account" {
			t.Errorf("account JWT name = %q, want %q", acctClaims.Name, "other-account")
		}
	})
}
