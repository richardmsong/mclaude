package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ---------------------------------------------------------------------------
// In-memory KeyValue mock for HTTP handler unit tests.
// Implements jetstream.KeyValue (v1.38.0) — only the methods called by the
// HTTP handlers are functional; all others panic to catch unexpected calls.
// ---------------------------------------------------------------------------

type memKVStore struct {
	mu      sync.RWMutex
	entries map[string][]byte
}

func newMemKV() *memKVStore { return &memKVStore{entries: make(map[string][]byte)} }

// Ensure memKVStore satisfies jetstream.KeyValue at compile time.
var _ jetstream.KeyValue = (*memKVStore)(nil)

func (m *memKVStore) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	m.mu.RLock()
	v, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return &memKVEntry{key: key, val: v}, nil
}

func (m *memKVStore) GetRevision(_ context.Context, _ string, _ uint64) (jetstream.KeyValueEntry, error) {
	panic("memKVStore.GetRevision not implemented")
}

func (m *memKVStore) Put(_ context.Context, key string, val []byte) (uint64, error) {
	m.mu.Lock()
	cp := make([]byte, len(val))
	copy(cp, val)
	m.entries[key] = cp
	m.mu.Unlock()
	return 1, nil
}

func (m *memKVStore) PutString(_ context.Context, _ string, _ string) (uint64, error) {
	panic("memKVStore.PutString not implemented")
}

func (m *memKVStore) Create(_ context.Context, _ string, _ []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	panic("memKVStore.Create not implemented")
}

func (m *memKVStore) Update(_ context.Context, _ string, _ []byte, _ uint64) (uint64, error) {
	panic("memKVStore.Update not implemented")
}

func (m *memKVStore) Delete(_ context.Context, _ string, _ ...jetstream.KVDeleteOpt) error {
	panic("memKVStore.Delete not implemented")
}

func (m *memKVStore) Purge(_ context.Context, _ string, _ ...jetstream.KVDeleteOpt) error {
	panic("memKVStore.Purge not implemented")
}

func (m *memKVStore) Watch(_ context.Context, _ string, _ ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	panic("memKVStore.Watch not implemented")
}

func (m *memKVStore) WatchAll(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	m.mu.RLock()
	snapshot := make([]memKVEntry, 0, len(m.entries))
	for k, v := range m.entries {
		cp := make([]byte, len(v))
		copy(cp, v)
		snapshot = append(snapshot, memKVEntry{key: k, val: cp})
	}
	m.mu.RUnlock()

	ch := make(chan jetstream.KeyValueEntry, len(snapshot)+1)
	for i := range snapshot {
		ch <- &snapshot[i]
	}
	ch <- nil // sentinel: end of initial values
	return &memKVWatcher{ch: ch}, nil
}

func (m *memKVStore) WatchFiltered(_ context.Context, _ []string, _ ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	panic("memKVStore.WatchFiltered not implemented")
}

func (m *memKVStore) Keys(_ context.Context, _ ...jetstream.WatchOpt) ([]string, error) {
	panic("memKVStore.Keys not implemented")
}

func (m *memKVStore) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	panic("memKVStore.ListKeys not implemented")
}

func (m *memKVStore) ListKeysFiltered(_ context.Context, _ ...string) (jetstream.KeyLister, error) {
	panic("memKVStore.ListKeysFiltered not implemented")
}

func (m *memKVStore) History(_ context.Context, _ string, _ ...jetstream.WatchOpt) ([]jetstream.KeyValueEntry, error) {
	panic("memKVStore.History not implemented")
}

func (m *memKVStore) Bucket() string { return "mem-kv" }

func (m *memKVStore) Status(_ context.Context) (jetstream.KeyValueStatus, error) {
	panic("memKVStore.Status not implemented")
}

func (m *memKVStore) PurgeDeletes(_ context.Context, _ ...jetstream.KVPurgeOpt) error {
	panic("memKVStore.PurgeDeletes not implemented")
}

// memKVEntry implements jetstream.KeyValueEntry.
type memKVEntry struct {
	key string
	val []byte
}

func (e *memKVEntry) Bucket() string                  { return "mem-kv" }
func (e *memKVEntry) Key() string                     { return e.key }
func (e *memKVEntry) Value() []byte                   { return e.val }
func (e *memKVEntry) Revision() uint64                { return 1 }
func (e *memKVEntry) Created() time.Time              { return time.Time{} }
func (e *memKVEntry) Delta() uint64                   { return 0 }
func (e *memKVEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

// memKVWatcher implements jetstream.KeyWatcher.
type memKVWatcher struct {
	ch chan jetstream.KeyValueEntry
}

func (w *memKVWatcher) Updates() <-chan jetstream.KeyValueEntry { return w.ch }
func (w *memKVWatcher) Stop() error                            { return nil }

// ---------------------------------------------------------------------------
// Tests — HTTP handlers use d.cfg.UserID (no X-User-ID header required)
// Spec: plan-quota-aware-scheduling.md §Daemon Jobs HTTP Server:
//   "Since the server is loopback-only and the daemon already knows the userId
//    from DaemonConfig, no auth header is required."
// ---------------------------------------------------------------------------

// TestHandleJobsRouteUsesConfigUserID verifies that POST /jobs and GET /jobs
// work without an X-User-ID header and use d.cfg.UserID for scoping.
func TestHandleJobsRouteUsesConfigUserID(t *testing.T) {
	d := &Daemon{
		cfg: DaemonConfig{
			UserID: "test-user",
			Log:    testLogger(t),
		},
		jobQueueKV: newMemKV(),
	}

	// POST without X-User-ID header — must succeed.
	body := `{"specPath":"docs/plan-spa.md","priority":5,"threshold":75,"projectId":"proj-1"}`
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	d.handleJobsRoute(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /jobs without X-User-ID: got %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	jobID := resp["id"]
	if jobID == "" {
		t.Fatal("POST /jobs response missing id")
	}
	if resp["status"] != "queued" {
		t.Errorf("POST /jobs response status: got %q, want queued", resp["status"])
	}

	// GET /jobs without X-User-ID header — must succeed and return the created job.
	req2 := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	rr2 := httptest.NewRecorder()
	d.handleJobsRoute(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET /jobs without X-User-ID: got %d, want 200", rr2.Code)
	}
	var jobs []JobEntry
	if err := json.NewDecoder(rr2.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode GET /jobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("GET /jobs: expected at least one job")
	}
	if jobs[0].UserID != "test-user" {
		t.Errorf("job.UserID: got %q, want test-user", jobs[0].UserID)
	}
}

// TestHandleJobByIDUsesConfigUserID verifies GET /jobs/{id} and DELETE /jobs/{id}
// work without an X-User-ID header.
func TestHandleJobByIDUsesConfigUserID(t *testing.T) {
	d := &Daemon{
		cfg: DaemonConfig{
			UserID: "test-user",
			Log:    testLogger(t),
		},
		jobQueueKV: newMemKV(),
	}

	// Create a job via POST to get a valid ID.
	body := `{"specPath":"docs/plan-spa.md","priority":5,"threshold":75,"projectId":"proj-1"}`
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	d.handleJobsRoute(rr, req)
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
	jobID := resp["id"]
	if jobID == "" {
		t.Fatal("POST /jobs did not return a job ID")
	}

	// GET /jobs/{id} without X-User-ID header — must succeed.
	req2 := httptest.NewRequest(http.MethodGet, "/jobs/"+jobID, nil)
	rr2 := httptest.NewRecorder()
	d.handleJobByID(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET /jobs/{id} without X-User-ID: got %d, want 200. Body: %s", rr2.Code, rr2.Body.String())
	}
	var job JobEntry
	if err := json.NewDecoder(rr2.Body).Decode(&job); err != nil {
		t.Fatalf("decode GET /jobs/{id}: %v", err)
	}
	if job.UserID != "test-user" {
		t.Errorf("job.UserID: got %q, want test-user", job.UserID)
	}

	// DELETE /jobs/{id} without X-User-ID header — must succeed.
	req3 := httptest.NewRequest(http.MethodDelete, "/jobs/"+jobID, nil)
	rr3 := httptest.NewRecorder()
	d.handleJobByID(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("DELETE /jobs/{id} without X-User-ID: got %d, want 200. Body: %s", rr3.Code, rr3.Body.String())
	}
}

// TestHandleJobsProjectsUsesConfigUserID verifies GET /jobs/projects
// works without an X-User-ID header.
func TestHandleJobsProjectsUsesConfigUserID(t *testing.T) {
	d := &Daemon{
		cfg: DaemonConfig{
			UserID: "test-user",
			Log:    testLogger(t),
		},
		projectsKV: newMemKV(),
	}

	// GET /jobs/projects without X-User-ID header — must succeed (empty list is OK).
	req := httptest.NewRequest(http.MethodGet, "/jobs/projects", nil)
	rr := httptest.NewRecorder()
	d.handleJobsProjects(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /jobs/projects without X-User-ID: got %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}
	// Response must be a valid JSON array.
	var projects []interface{}
	if err := json.NewDecoder(rr.Body).Decode(&projects); err != nil {
		t.Fatalf("decode GET /jobs/projects: %v", err)
	}
}
