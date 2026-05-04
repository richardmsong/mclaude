package main

// ADR-0063: The K8s controller no longer mints session-agent JWTs.
// Session-agent pods self-bootstrap via HTTP challenge-response against
// CONTROL_PLANE_URL (ADR-0054). All JWT-issuance code has been removed.
//
// This file is retained as a placeholder; shared NKey/JWT helpers used by
// both controllers live in mclaude.io/common/pkg/hostauth.
