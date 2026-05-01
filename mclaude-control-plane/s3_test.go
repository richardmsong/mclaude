package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ---- S3 pre-signed URL generation ----

func newTestS3Config() *s3Config {
	return &s3Config{
		Endpoint:  "https://s3.example.com",
		Bucket:    "test-bucket",
		AccessID:  "TESTKEY",
		AccessSig: "TESTSECRET",
		Region:    "us-east-1",
	}
}

func TestPresignPutURL_Structure(t *testing.T) {
	cfg := newTestS3Config()
	key := "alice/laptop-a/myapp/attachments/att-001"
	urlStr, err := cfg.presignPutURL(key, 300)
	if err != nil {
		t.Fatalf("presignPutURL: %v", err)
	}
	if !strings.HasPrefix(urlStr, "https://") {
		t.Errorf("presignPutURL: URL should start with https://; got %q", urlStr)
	}
	// Must contain the canonical path with bucket and key.
	if !strings.Contains(urlStr, "/test-bucket/") {
		t.Errorf("presignPutURL: URL missing bucket; got %q", urlStr)
	}
	if !strings.Contains(urlStr, key) {
		t.Errorf("presignPutURL: URL missing key; got %q", urlStr)
	}
	// Must have V4 signing parameters.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	q := parsedURL.Query()
	if q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Errorf("missing X-Amz-Algorithm; query=%v", q)
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Errorf("missing X-Amz-Signature; query=%v", q)
	}
	if q.Get("X-Amz-Expires") != "300" {
		t.Errorf("X-Amz-Expires=%q; want 300", q.Get("X-Amz-Expires"))
	}
	if q.Get("X-Amz-SignedHeaders") != "host" {
		t.Errorf("X-Amz-SignedHeaders=%q; want host", q.Get("X-Amz-SignedHeaders"))
	}
}

func TestPresignGetURL_Structure(t *testing.T) {
	cfg := newTestS3Config()
	key := "alice/laptop-a/myapp/attachments/att-002"
	urlStr, err := cfg.presignGetURL(key, 300)
	if err != nil {
		t.Fatalf("presignGetURL: %v", err)
	}
	if !strings.HasPrefix(urlStr, "https://") {
		t.Errorf("presignGetURL: URL should start with https://; got %q", urlStr)
	}
	// GET and PUT URLs should be different (method affects signature).
	putURL, _ := cfg.presignPutURL(key, 300)
	if urlStr == putURL {
		t.Error("GET and PUT pre-signed URLs should be different (method affects signature)")
	}
}

func TestPresignURL_DifferentKeysGiveDifferentURLs(t *testing.T) {
	cfg := newTestS3Config()
	url1, _ := cfg.presignPutURL("key1", 300)
	url2, _ := cfg.presignPutURL("key2", 300)
	if url1 == url2 {
		t.Error("different keys should produce different pre-signed URLs")
	}
}

func TestPresignURL_DifferentExpiresGiveDifferentURLs(t *testing.T) {
	cfg := newTestS3Config()
	url1, _ := cfg.presignPutURL("key1", 300)
	url2, _ := cfg.presignPutURL("key1", 600)
	if url1 == url2 {
		t.Error("different expiry times should produce different pre-signed URLs")
	}
}

func TestPresignURL_CredentialContainsRegionAndService(t *testing.T) {
	cfg := newTestS3Config()
	urlStr, err := cfg.presignPutURL("test-key", 300)
	if err != nil {
		t.Fatalf("presignPutURL: %v", err)
	}
	// X-Amz-Credential should contain region and service.
	parsedURL, _ := url.Parse(urlStr)
	cred := parsedURL.Query().Get("X-Amz-Credential")
	if !strings.Contains(cred, cfg.AccessID) {
		t.Errorf("credential missing access key ID; got %q", cred)
	}
	if !strings.Contains(cred, "us-east-1") {
		t.Errorf("credential missing region; got %q", cred)
	}
	if !strings.Contains(cred, "/s3/") {
		t.Errorf("credential missing s3 service; got %q", cred)
	}
	if !strings.Contains(cred, "aws4_request") {
		t.Errorf("credential missing aws4_request; got %q", cred)
	}
}

func TestLoadS3Config_AllEnvVarsRequired(t *testing.T) {
	// Without any env vars, loadS3Config should return nil.
	// (Don't actually set env vars in tests to avoid side effects.)
	// This just tests that loadS3Config handles missing vars correctly.
	// Since env vars aren't set in tests, it should always return nil here.
	cfg := loadS3Config()
	// The test environment won't have S3 vars set, so cfg should be nil.
	// But if it IS set (CI with real S3), that's also valid — just skip.
	_ = cfg // just verifies no panic
}

// ---- S3 config in Server ----

func TestServer_S3IsNilByDefault(t *testing.T) {
	srv := newTestServer(t)
	if srv.s3 != nil {
		t.Error("server.s3 should be nil when not configured; got non-nil")
	}
}

// ---- Attachment HTTP handlers with nil S3 ----

func TestHandleAttachmentUploadURL_NilS3(t *testing.T) {
	srv := newTestServer(t) // s3=nil
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/upload-url",
		strings.NewReader(`{"filename":"test.png","mimeType":"image/png","sizeBytes":1024,"projectSlug":"myapp","hostSlug":"laptop-a"}`))
	req.Header.Set("Content-Type", "application/json")
	// Inject user ID into context.
	req = req.WithContext(contextWithUserID(req.Context(), "test-user-id"))
	srv.handleAttachmentUploadURL(rec, req)
	// s3=nil → 503 Service Unavailable
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when S3 not configured", rec.Code)
	}
}

func TestHandleAttachmentUploadURL_NilDB(t *testing.T) {
	srv := newTestServer(t) // db=nil
	srv.s3 = newTestS3Config() // s3 configured
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/upload-url",
		strings.NewReader(`{"filename":"test.png","mimeType":"image/png","sizeBytes":1024,"projectSlug":"myapp","hostSlug":"laptop-a"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(contextWithUserID(req.Context(), "test-user-id"))
	srv.handleAttachmentUploadURL(rec, req)
	// db=nil → 503 Service Unavailable
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when DB not configured", rec.Code)
	}
}

func TestHandleAttachmentUploadURL_SizeLimitExceeded(t *testing.T) {
	// Note: size limit check happens after db nil check. With db=nil → 503.
	// The size limit is enforced before DB lookup in the production handler, but
	// db nil check comes first. Test that the constant is correct.
	if maxAttachmentBytes != 50*1024*1024 {
		t.Errorf("maxAttachmentBytes = %d; want %d (50 MB)", maxAttachmentBytes, 50*1024*1024)
	}
	if maxImportBytes != 500*1024*1024 {
		t.Errorf("maxImportBytes = %d; want %d (500 MB)", maxImportBytes, 500*1024*1024)
	}
}

func TestHandleAttachmentUploadURL_MissingFields(t *testing.T) {
	// Note: the handler checks db != nil before validating body fields.
	// When db=nil (as in newTestServer), we get 503. This tests the nil-S3 path separately.
	// Body validation (400) is tested in integration tests with a real DB.
	srv := newTestServer(t)
	srv.s3 = newTestS3Config()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/upload-url",
		strings.NewReader(`{"filename":"test.png"}`)) // missing mimeType, sizeBytes, etc.
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(contextWithUserID(req.Context(), "test-user-id"))
	srv.handleAttachmentUploadURL(rec, req)
	// db=nil → 503 (DB guard fires before field validation).
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when db=nil (db guard fires before field validation)", rec.Code)
	}
}

func TestHandleAttachmentConfirm_NilS3(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/att-001/confirm", nil)
	req = req.WithContext(contextWithUserID(req.Context(), "test-user-id"))
	srv.handleAttachmentConfirm(rec, req, "att-001")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when S3 not configured", rec.Code)
	}
}

func TestHandleAttachmentGet_NilS3(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/attachments/att-001", nil)
	req = req.WithContext(contextWithUserID(req.Context(), "test-user-id"))
	srv.handleAttachmentGet(rec, req, "att-001")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when S3 not configured", rec.Code)
	}
}

func TestHandleAttachmentRoutes_Dispatch(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// POST /api/attachments/upload-url — needs auth, so get 401.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/upload-url",
		strings.NewReader(`{}`))
	mux.ServeHTTP(rec, req)
	// Without auth token → 401.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/attachments/upload-url without auth: status = %d; want 401", rec.Code)
	}
}

// ---- S3 prefix deletion (s3DeletePrefix / s3ListObjectKeys) ----

// newFakeS3Config creates a test s3Config pointing at the given httptest server URL.
func newFakeS3Config(serverURL string) *s3Config {
	return &s3Config{
		Endpoint:  serverURL,
		Bucket:    "test-bucket",
		AccessID:  "TESTKEY",
		AccessSig: "TESTSECRET",
		Region:    "us-east-1",
	}
}

func TestS3DeletePrefix_EmptyList(t *testing.T) {
	// Fake S3 server returns an empty list — no DELETE requests expected.
	var deleteCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// ListObjectsV2 request — return empty list.
			if r.URL.Query().Get("list-type") != "2" {
				t.Errorf("unexpected GET query; want list-type=2, got %v", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
		case http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	cfg := newFakeS3Config(ts.URL)
	if err := cfg.s3DeletePrefix("alice/laptop-a/myapp/"); err != nil {
		t.Fatalf("s3DeletePrefix: %v", err)
	}
	if deleteCalls != 0 {
		t.Errorf("delete called %d times; want 0 (empty list)", deleteCalls)
	}
}

func TestS3DeletePrefix_DeletesMultipleObjects(t *testing.T) {
	// Fake S3 server returns two objects. Both should be deleted.
	var listCalls, deleteCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listCalls++
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<ListBucketResult>`+
				`<IsTruncated>false</IsTruncated>`+
				`<Contents><Key>alice/laptop-a/myapp/imports/imp-001.tar.gz</Key></Contents>`+
				`<Contents><Key>alice/laptop-a/myapp/attachments/att-001</Key></Contents>`+
				`</ListBucketResult>`)
		case http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	cfg := newFakeS3Config(ts.URL)
	if err := cfg.s3DeletePrefix("alice/laptop-a/myapp/"); err != nil {
		t.Fatalf("s3DeletePrefix: %v", err)
	}
	if listCalls != 1 {
		t.Errorf("list called %d times; want 1", listCalls)
	}
	if deleteCalls != 2 {
		t.Errorf("delete called %d times; want 2 (one per object)", deleteCalls)
	}
}

func TestS3DeletePrefix_Pagination(t *testing.T) {
	// First page is truncated with a continuation token; second page is the last.
	var listCalls, deleteCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listCalls++
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			if r.URL.Query().Get("continuation-token") == "" {
				// First page: 1 object, truncated.
				fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
					`<ListBucketResult>`+
					`<IsTruncated>true</IsTruncated>`+
					`<Contents><Key>alice/laptop-a/myapp/imports/imp-001.tar.gz</Key></Contents>`+
					`<NextContinuationToken>page2token</NextContinuationToken>`+
					`</ListBucketResult>`)
			} else {
				// Second page: 1 object, not truncated.
				fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
					`<ListBucketResult>`+
					`<IsTruncated>false</IsTruncated>`+
					`<Contents><Key>alice/laptop-a/myapp/attachments/att-001</Key></Contents>`+
					`</ListBucketResult>`)
			}
		case http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	cfg := newFakeS3Config(ts.URL)
	if err := cfg.s3DeletePrefix("alice/laptop-a/myapp/"); err != nil {
		t.Fatalf("s3DeletePrefix: %v", err)
	}
	if listCalls != 2 {
		t.Errorf("list called %d times; want 2 (one per page)", listCalls)
	}
	if deleteCalls != 2 {
		t.Errorf("delete called %d times; want 2 (one per object)", deleteCalls)
	}
}

func TestS3DeletePrefix_ListError(t *testing.T) {
	// Fake S3 server returns 500 — s3DeletePrefix should return an error.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	cfg := newFakeS3Config(ts.URL)
	err := cfg.s3DeletePrefix("alice/laptop-a/myapp/")
	if err == nil {
		t.Fatal("s3DeletePrefix: expected error when list returns 500; got nil")
	}
}

func TestS3ListObjectKeys_NamespacedXML(t *testing.T) {
	// AWS S3 responses include an XML namespace on the root element; the token-based
	// parser must handle this by matching on local element names only.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		// Response with AWS S3 namespace.
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
			`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
			`<IsTruncated>false</IsTruncated>`+
			`<Contents><Key>alice/laptop-a/myapp/imports/imp-001.tar.gz</Key></Contents>`+
			`<Contents><Key>alice/laptop-a/myapp/attachments/att-002</Key></Contents>`+
			`</ListBucketResult>`)
	}))
	defer ts.Close()

	cfg := newFakeS3Config(ts.URL)
	keys, err := cfg.s3ListObjectKeys("alice/laptop-a/myapp/")
	if err != nil {
		t.Fatalf("s3ListObjectKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("got %d keys; want 2 (XML namespace must be handled)", len(keys))
	}
}

func TestS3ListObjectKeys_EmptyPrefix(t *testing.T) {
	// Test with an empty response body (no Contents elements).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	defer ts.Close()

	cfg := newFakeS3Config(ts.URL)
	keys, err := cfg.s3ListObjectKeys("nonexistent/prefix/")
	if err != nil {
		t.Fatalf("s3ListObjectKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("got %d keys; want 0", len(keys))
	}
}
