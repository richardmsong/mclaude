package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestVersionResponse_Defaults(t *testing.T) {
	t.Setenv("MIN_CLIENT_VERSION", "")
	t.Setenv("SERVER_VERSION", "")

	r := &fakeResponseWriter{}
	handleVersion(r, fakeRequest("GET", "/version"))
	if r.status != 0 && r.status != http.StatusOK {
		t.Errorf("status = %d; want 200", r.status)
	}
	if !strings.Contains(r.body, `"minClientVersion"`) {
		t.Errorf("body missing minClientVersion field: %q", r.body)
	}
}

func TestVersionResponse_ConfiguredVersion(t *testing.T) {
	t.Setenv("MIN_CLIENT_VERSION", "1.2.3")
	t.Setenv("SERVER_VERSION", "v2.0.0")

	r := &fakeResponseWriter{}
	handleVersion(r, fakeRequest("GET", "/version"))
	if !strings.Contains(r.body, "1.2.3") {
		t.Errorf("body missing minClientVersion 1.2.3: %q", r.body)
	}
}
