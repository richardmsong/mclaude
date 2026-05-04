## Run: 2026-05-03T00:00:00Z

**Gaps found: 4**

1. **Missing function signature specification for `attestHostDirect`** — The design describes `attestHostDirect` as being called by both `seedDev` and `handleHostRegister`, but the exact signature is incomplete. The doc says it returns `(jwt string, seed []byte, error)` and takes `(ctx, db, accountKP, userID, slug, name)`, but it is not clear how it differs from `IssueHostJWTLegacy` which already generates an NKey pair and returns seed.
   - **Doc**: Section "Component Changes" → "`hosts.go` — extract shared attestation function", function signature shows `attestHostDirect(ctx context.Context, db *DB, accountKP nkeys.KeyPair, userID, slug, name string)` with no explanation of parameter `name` beyond "name" in the signature.
   - **Code**: `IssueHostJWT` (line 297 in nkeys.go) takes only `(publicKey, hslug, accountKP)` — it does NOT generate a keypair. `IssueHostJWTLegacy` (line 397) generates the keypair and returns seed. The design conflates these two approaches.

2. **Inconsistency in `IssueHostJWT` vs `IssueHostJWTLegacy` usage in `attestHostDirect`** — The design says `attestHostDirect` should call `IssueHostJWT(pubKey, slug, accountKP)`, but the doc also says it generates the keypair. `IssueHostJWT` requires a client-provided public key; it does not generate one. The code shows `IssueHostJWTLegacy` generates the keypair and seed. The design does not clarify whether `attestHostDirect` wraps `IssueHostJWTLegacy` or uses `GenerateUserNKey()` + `IssueHostJWT()`.
   - **Doc**: "attestHostDirect function ... Calls `GenerateUserNKey()` → `(pubKey, seed, err)` ... Calls `IssueHostJWT(pubKey, slug, accountKP)` → JWT"
   - **Code**: `IssueHostJWT` signature is `func IssueHostJWT(publicKey string, hslug string, accountKP nkeys.KeyPair) (string, error)` — returns only JWT, not seed. It does not generate keypairs.

3. **`handleHostRegister` interaction with `attestHostDirect` is ambiguous** — The design says "`handleHostRegister` continues to accept a client-provided public key ... it calls `IssueHostJWT` directly with that key. `attestHostDirect` is only for programmatic server-side registration." However, looking at the code, `handleHostRegister` is a device-code redemption flow (POST /api/hosts/register) that already calls `IssueHostJWTLegacy`. The design does not explain how to refactor `handleHostRegister` to keep calling `IssueHostJWT` with a client-provided public key when the spec (spec-cli.md line 66-97) says "`mclaude host register` ... Generates an NKey pair locally — the private seed never leaves the machine" — meaning the client generates the keypair, not the server.
   - **Doc**: "handleHostRegister continues to accept a client-provided public key ... calls IssueHostJWT directly with that key"
   - **Code**: Current `handleHostRegister` calls `IssueHostJWTLegacy` which generates the keypair on the server. The spec in spec-cli.md contradicts the design — it says the client generates the keypair locally.

4. **Database column addition timing and recovery** — The design says to add a nullable `seed TEXT` column to hosts via ALTER TABLE. However, there is no recovery plan if `attestHostDirect` fails partway through (after issuing JWT but before upserting the hosts row with seed). If the upsert fails, the seed is lost and `mclaude-controller-local` cannot retrieve it. The design does not address atomicity or error recovery.
   - **Doc**: "seedDev... call `attestHostDirect` for the `local` host... After `attestHostDirect`, write the host to NATS KV". Section "Error Handling" says "log error and continue (non-fatal, same pattern as existing `ensureUserResources` failure handling)" but does not explain what state the database is left in.
   - **Code**: `seedDev` in main.go (line 197) does not currently call any attestation function. The design requires a new function that must be atomic with respect to database writes.

