package main

import (
	"encoding/json"
	"testing"

	"github.com/nats-io/nats.go"
)

// ---- parseManageSubject ----

func TestParseManageSubject_Valid(t *testing.T) {
	cases := []struct {
		subject   string
		wantUslug string
		wantHslug string
	}{
		{
			"mclaude.users.alice.hosts.laptop-a.manage.grant",
			"alice", "laptop-a",
		},
		{
			"mclaude.users.bob-gmail.hosts.us-east.manage.revoke-access",
			"bob-gmail", "us-east",
		},
		{
			"mclaude.users.dev.hosts.local.manage.deregister",
			"dev", "local",
		},
	}
	for _, tc := range cases {
		uslug, hslug := parseManageSubject(tc.subject)
		if uslug != tc.wantUslug {
			t.Errorf("parseManageSubject(%q): uslug=%q; want %q", tc.subject, uslug, tc.wantUslug)
		}
		if hslug != tc.wantHslug {
			t.Errorf("parseManageSubject(%q): hslug=%q; want %q", tc.subject, hslug, tc.wantHslug)
		}
	}
}

func TestParseManageSubject_TooShort(t *testing.T) {
	uslug, hslug := parseManageSubject("mclaude.users.alice")
	if uslug != "" || hslug != "" {
		t.Errorf("parseManageSubject(too short) = (%q, %q); want ('', '')", uslug, hslug)
	}
}

func TestParseManageSubject_Empty(t *testing.T) {
	uslug, hslug := parseManageSubject("")
	if uslug != "" || hslug != "" {
		t.Errorf("parseManageSubject('') = (%q, %q); want ('', '')", uslug, hslug)
	}
}

// ---- parseImportSubject ----

func TestParseImportSubject_Valid(t *testing.T) {
	cases := []struct {
		subject   string
		wantUslug string
		wantHslug string
		wantPslug string
	}{
		{
			"mclaude.users.alice.hosts.laptop-a.projects.myapp.import.request",
			"alice", "laptop-a", "myapp",
		},
		{
			"mclaude.users.bob.hosts.local.projects.billing.attachments.upload",
			"bob", "local", "billing",
		},
		{
			"mclaude.users.dev.hosts.us-east.projects.web.import.confirm",
			"dev", "us-east", "web",
		},
	}
	for _, tc := range cases {
		uslug, hslug, pslug, err := parseImportSubject(tc.subject)
		if err != nil {
			t.Errorf("parseImportSubject(%q): unexpected error: %v", tc.subject, err)
			continue
		}
		if uslug != tc.wantUslug {
			t.Errorf("parseImportSubject(%q): uslug=%q; want %q", tc.subject, uslug, tc.wantUslug)
		}
		if hslug != tc.wantHslug {
			t.Errorf("parseImportSubject(%q): hslug=%q; want %q", tc.subject, hslug, tc.wantHslug)
		}
		if pslug != tc.wantPslug {
			t.Errorf("parseImportSubject(%q): pslug=%q; want %q", tc.subject, pslug, tc.wantPslug)
		}
	}
}

func TestParseImportSubject_TooShort(t *testing.T) {
	// "mclaude.users.alice.hosts.laptop" has only 5 parts — too short.
	_, _, _, err := parseImportSubject("mclaude.users.alice.hosts.laptop")
	if err == nil {
		t.Error("expected error for too-short subject")
	}
	// Also test with fewer parts.
	_, _, _, err2 := parseImportSubject("mclaude.users")
	if err2 == nil {
		t.Error("expected error for very short subject")
	}
}

// ---- NATS reply helpers ----

func TestReplyNATSOK_NoReply(t *testing.T) {
	// replyNATSOK with a message that has no reply subject — should not panic.
	msg := &nats.Msg{Subject: "test.subject", Reply: ""}
	replyNATSOK(msg) // should not panic
}

func TestReplyNATSError_NoReply(t *testing.T) {
	msg := &nats.Msg{Subject: "test.subject", Reply: ""}
	replyNATSError(msg, "some error") // should not panic
}

func TestReplyNATSForbidden_NoReply(t *testing.T) {
	msg := &nats.Msg{Subject: "test.subject", Reply: ""}
	replyNATSForbidden(msg, "forbidden error") // should not panic
}

func TestReplyNATSNotFound_NoReply(t *testing.T) {
	msg := &nats.Msg{Subject: "test.subject", Reply: ""}
	replyNATSNotFound(msg, "not found error") // should not panic
}

// ---- handleAgentRegister validation ----

func TestHandleAgentRegister_NilDB(t *testing.T) {
	srv := newTestServer(t) // db=nil
	var gotReply []byte
	msg := &nats.Msg{
		Subject: "mclaude.hosts.laptop-a.api.agents.register",
		Reply:   "_INBOX.test.reply",
		Data:    []byte(`{"user_slug":"alice","project_slug":"myapp","nkey_public":"UTEST"}`),
	}
	// Intercept the response — since we can't use a real NATS server in unit tests,
	// we wrap the respond call. The easiest is to check it doesn't panic.
	// When Reply is set but nc is nil, msg.Respond will panic — use no-reply form.
	msg.Reply = "" // no reply subject → just check no panic
	srv.handleAgentRegister(msg)
	_ = gotReply // suppresses unused warning
}

func TestHandleAgentRegister_MalformedSubject(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.hosts", // too short
		Reply:   "",
		Data:    []byte(`{}`),
	}
	srv.handleAgentRegister(msg) // should not panic
}

func TestHandleAgentRegister_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.hosts.laptop-a.api.agents.register",
		Reply:   "",
		Data:    []byte("not json"),
	}
	srv.handleAgentRegister(msg) // should not panic
}

func TestHandleAgentRegister_MissingFields(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.hosts.laptop-a.api.agents.register",
		Reply:   "",
		Data:    []byte(`{"user_slug":"alice"}`), // missing project_slug and nkey_public
	}
	srv.handleAgentRegister(msg) // should not panic (returns error response)
}

// ---- handleNATSHostRegister validation ----

func TestHandleNATSHostRegister_NilDB(t *testing.T) {
	srv := newTestServer(t) // db=nil
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts._.register",
		Reply:   "",
		Data:    []byte(`{"name":"My Host","nkey_public":"UABC"}`),
	}
	srv.handleNATSHostRegister(msg) // should not panic
}

func TestHandleNATSHostRegister_MissingFields(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts._.register",
		Reply:   "",
		Data:    []byte(`{"name":"My Host"}`), // missing nkey_public
	}
	srv.handleNATSHostRegister(msg) // should not panic
}

func TestHandleNATSHostRegister_MalformedSubject(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users", // too short
		Reply:   "",
		Data:    []byte(`{}`),
	}
	srv.handleNATSHostRegister(msg) // should not panic
}

// ---- handleManageGrant validation ----

func TestHandleManageGrant_NilDB(t *testing.T) {
	srv := newTestServer(t) // db=nil
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.grant",
		Reply:   "",
		Data:    []byte(`{"userSlug":"bob"}`),
	}
	srv.handleManageGrant(msg) // should not panic
}

func TestHandleManageGrant_MissingUserSlug(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.grant",
		Reply:   "",
		Data:    []byte(`{}`), // missing userSlug
	}
	srv.handleManageGrant(msg) // should not panic
}

func TestHandleManageGrant_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.grant",
		Reply:   "",
		Data:    []byte("not json"),
	}
	srv.handleManageGrant(msg) // should not panic
}

// ---- handleManageRevokeAccess validation ----

func TestHandleManageRevokeAccess_NilDB(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.revoke-access",
		Reply:   "",
		Data:    []byte(`{"userSlug":"bob"}`),
	}
	srv.handleManageRevokeAccess(msg) // should not panic
}

// ---- handleManageDeregister validation ----

func TestHandleManageDeregister_NilDB(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.deregister",
		Reply:   "",
		Data:    []byte(`{}`),
	}
	srv.handleManageDeregister(msg) // should not panic
}

// ---- handleManageRevoke validation ----

func TestHandleManageRevoke_NilDB(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.revoke",
		Reply:   "",
		Data:    []byte(`{}`),
	}
	srv.handleManageRevoke(msg) // should not panic
}

// ---- handleManageRekey validation ----

func TestHandleManageRekey_NilDB(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.rekey",
		Reply:   "",
		Data:    []byte(`{"nkeyPublic":"UNEWKEY"}`),
	}
	srv.handleManageRekey(msg) // should not panic
}

func TestHandleManageRekey_MissingNKey(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.rekey",
		Reply:   "",
		Data:    []byte(`{}`), // missing nkeyPublic
	}
	srv.handleManageRekey(msg) // should not panic
}

// ---- handleManageUpdate validation ----

func TestHandleManageUpdate_NilDB(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.update",
		Reply:   "",
		Data:    []byte(`{"name":"New Name"}`),
	}
	srv.handleManageUpdate(msg) // should not panic
}

func TestHandleManageUpdate_NoFields(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.manage.update",
		Reply:   "",
		Data:    []byte(`{}`), // no name or type
	}
	srv.handleManageUpdate(msg) // should not panic
}

// ---- handleCheckSlug validation ----

func TestHandleCheckSlug_NilDB(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.projects.myapp.check-slug",
		Reply:   "",
		Data:    []byte(`{"slug":"my-project"}`),
	}
	srv.handleCheckSlug(msg) // should not panic
}

func TestHandleCheckSlug_MissingSlug(t *testing.T) {
	srv := newTestServer(t)
	msg := &nats.Msg{
		Subject: "mclaude.users.alice.hosts.laptop-a.projects.myapp.check-slug",
		Reply:   "",
		Data:    []byte(`{}`),
	}
	srv.handleCheckSlug(msg) // should not panic
}

// ---- revokeNKeyJWT ----

func TestRevokeNKeyJWT_NilNC(t *testing.T) {
	srv := newTestServer(t) // nc=nil
	err := srv.revokeNKeyJWT(newRequestContext(), "UTEST")
	if err != nil {
		t.Errorf("revokeNKeyJWT with nil nc: unexpected error: %v", err)
	}
}

func TestRevokeNKeyJWT_EmptyNKey(t *testing.T) {
	srv := newTestServer(t)
	err := srv.revokeNKeyJWT(newRequestContext(), "")
	if err != nil {
		t.Errorf("revokeNKeyJWT with empty nkey: unexpected error: %v", err)
	}
}

func TestRevokeNKeyJWT_NoCredentials(t *testing.T) {
	srv := newTestServer(t)
	// nc is not nil but operatorSeed/sysAccountSeed are empty → warning, no error.
	// We can't easily set nc without a real NATS server, so just test that the
	// nil NC guard is in place.
	err := srv.revokeNKeyJWT(newRequestContext(), "UTEST")
	if err != nil {
		t.Errorf("revokeNKeyJWT with no creds/nil nc: unexpected error: %v", err)
	}
}

// ---- AgentRegisterRequest struct ----

func TestAgentRegisterRequest_JSONRoundTrip(t *testing.T) {
	req := AgentRegisterRequest{
		UserSlug:    "alice",
		ProjectSlug: "myapp",
		NKeyPublic:  "UABC123",
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got AgentRegisterRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.UserSlug != req.UserSlug || got.ProjectSlug != req.ProjectSlug || got.NKeyPublic != req.NKeyPublic {
		t.Errorf("round-trip failed: got %+v; want %+v", got, req)
	}
}

// ---- ManageGrantRequest ----

func TestManageGrantRequest_JSONRoundTrip(t *testing.T) {
	req := ManageGrantRequest{UserSlug: "bob"}
	b, _ := json.Marshal(req)
	var got ManageGrantRequest
	json.Unmarshal(b, &got) //nolint:errcheck
	if got.UserSlug != "bob" {
		t.Errorf("round-trip: userSlug=%q; want bob", got.UserSlug)
	}
}

// ---- ManageRekeyRequest ----

func TestManageRekeyRequest_JSONRoundTrip(t *testing.T) {
	req := ManageRekeyRequest{NKeyPublic: "UNEWKEY"}
	b, _ := json.Marshal(req)
	var got ManageRekeyRequest
	json.Unmarshal(b, &got) //nolint:errcheck
	if got.NKeyPublic != "UNEWKEY" {
		t.Errorf("round-trip: NKeyPublic=%q; want UNEWKEY", got.NKeyPublic)
	}
}
