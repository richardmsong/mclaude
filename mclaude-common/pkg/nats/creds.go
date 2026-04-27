// Package nats provides NATS credential formatting and key management helpers
// shared across mclaude components. Moved from mclaude-control-plane per
// ADR-0035 so the CLI can reuse FormatNATSCredentials for BYOH bootstrap.
package nats

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
