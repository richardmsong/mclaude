package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// permContains checks if a permission list contains a given subject.
// Test helper — not part of production code.
func permContains(perms []string, subject string) bool {
	for _, p := range perms {
		if p == subject {
			return true
		}
	}
	return false
}

// permHasPrefix checks if any permission in the list starts with the given prefix.
// Test helper — not part of production code.
func permHasPrefix(perms []string, prefix string) bool {
	for _, p := range perms {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// fakeResponseWriter captures status + body for simple handler tests that
// don't need the full httptest.ResponseRecorder machinery.
type fakeResponseWriter struct {
	header http.Header
	status int
	body   string
}

func (f *fakeResponseWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *fakeResponseWriter) Write(b []byte) (int, error) {
	f.body += string(b)
	return len(b), nil
}

func (f *fakeResponseWriter) WriteHeader(status int) {
	f.status = status
}

// fakeRequest builds a minimal *http.Request for unit tests.
func fakeRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	return req
}

// fakeRequestWithBody builds a *http.Request with a JSON body.
func fakeRequestWithBody(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// testLogger returns a zerolog.Logger that discards output — suitable for tests.
func testLogger(t *testing.T) zerolog.Logger {
	t.Helper()
	return zerolog.Nop()
}
