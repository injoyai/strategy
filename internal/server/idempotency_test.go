package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

func TestMemoryStoreClaimCommitReplay(t *testing.T) {
	store := NewMemoryIdempotencyStore()

	rec, claimed, err := store.Begin("scope", "key-1", "hash-a", testBase)
	if err != nil || !claimed || rec != nil {
		t.Fatalf("first Begin: claimed=%v rec=%v err=%v, want fresh claim", claimed, rec, err)
	}

	// Second Begin before commit observes the in-flight claim.
	rec, claimed, err = store.Begin("scope", "key-1", "hash-a", testBase)
	if err != nil || claimed || rec == nil || !rec.InFlight() {
		t.Fatalf("second Begin: claimed=%v rec=%v err=%v, want in-flight record", claimed, rec, err)
	}

	done := IdempotencyRecord{
		Scope:       "scope",
		Key:         "key-1",
		RequestHash: "hash-a",
		Status:      http.StatusOK,
		Body:        []byte(`{"ok":1}`),
	}
	if err := store.Commit(done); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	rec, claimed, err = store.Begin("scope", "key-1", "hash-a", testBase)
	if err != nil || claimed {
		t.Fatalf("Begin after commit: claimed=%v err=%v, want replay record", claimed, err)
	}
	if rec.Status != http.StatusOK || string(rec.Body) != `{"ok":1}` {
		t.Errorf("replayed record = %+v, want committed response", rec)
	}
	if rec.InFlight() {
		t.Error("committed record must not be in flight")
	}
}

func TestMemoryStoreScopeIsolation(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	for _, scope := range []string{"a|default|POST|/x", "b|default|POST|/x", "a|default|GET|/x"} {
		if _, claimed, err := store.Begin(scope, "shared-key", "h", testBase); err != nil || !claimed {
			t.Errorf("scope %q: claimed=%v err=%v, want a fresh claim per scope", scope, claimed, err)
		}
	}
}

func TestMemoryStoreExpiryReclaim(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	if _, claimed, _ := store.Begin("s", "k", "h", testBase); !claimed {
		t.Fatal("expected initial claim")
	}
	// Within the window the claim survives.
	if rec, claimed, _ := store.Begin("s", "k", "h", testBase.Add(IdempotencyReplayWindow-time.Minute)); claimed || rec == nil {
		t.Fatalf("claim expired too early: claimed=%v rec=%v", claimed, rec)
	}
	// Past the window the key is reclaimable as a new request.
	rec, claimed, err := store.Begin("s", "k", "h", testBase.Add(IdempotencyReplayWindow+time.Minute))
	if err != nil || !claimed || rec != nil {
		t.Fatalf("expired claim: claimed=%v rec=%v err=%v, want reclaim", claimed, rec, err)
	}
}

func TestMemoryStoreCommitHashGuard(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	if _, claimed, _ := store.Begin("s", "k", "hash-original", testBase); !claimed {
		t.Fatal("expected initial claim")
	}
	// A commit carrying a different hash must not overwrite the claim, so an
	// expired-then-reclaimed slot cannot be resurrected with stale output.
	err := store.Commit(IdempotencyRecord{Scope: "s", Key: "k", RequestHash: "hash-other"})
	if err == nil {
		t.Fatal("expected conflict error for mismatched hash")
	}
	if domain.ErrorCode(err) != domain.CodeIdempotencyConflict {
		t.Errorf("code = %q, want idempotency.conflict", domain.ErrorCode(err))
	}
}

func TestCanonicalRequestHash(t *testing.T) {
	// Object keys are sorted, so key order never changes the hash.
	h1 := canonicalRequestHash([]byte(`{"b":1,"a":2}`))
	h2 := canonicalRequestHash([]byte(`{"a":2,"b":1}`))
	if h1 != h2 {
		t.Errorf("key order changed the hash: %s vs %s", h1, h2)
	}

	// Number literals are preserved: 1.0 and 1.00 hash differently by design.
	if canonicalRequestHash([]byte(`{"a":1.0}`)) == canonicalRequestHash([]byte(`{"a":1.00}`)) {
		t.Error("distinct numeric literals must hash differently")
	}

	// Whitespace between tokens is irrelevant.
	if canonicalRequestHash([]byte(`{ "a" : 2 , "b" : 1 }`)) != h2 {
		t.Error("whitespace should not change the canonical hash")
	}

	// Non-JSON bodies (multipart uploads) hash as raw bytes.
	raw := canonicalRequestHash([]byte("\x00\x01not json"))
	if raw == canonicalRequestHash([]byte("\x00\x01not json!")) {
		t.Error("distinct raw bodies must hash differently")
	}
}

func TestIdempotencyScope(t *testing.T) {
	got := idempotencyScope(Caller{ID: "local"}, http.MethodPost, "/api/v1/imports")
	want := "local|default|POST|/api/v1/imports"
	if got != want {
		t.Errorf("scope = %q, want %q", got, want)
	}
	if idempotencyScope(Caller{}, "POST", "/x") == got {
		t.Error("anonymous caller must not share scope with a named caller")
	}
}

func TestIdempotencyMiddlewareContract(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	calls := 0
	api.Handle("POST", "/things", func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Name string `json:"name"`
		}
		if !DecodeJSON(w, r, &body) {
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"name": body.Name})
	}, RouteOptions{IdempotencyRequired: true})

	const key = "test-key-0001"
	headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": key}

	// Missing / too-short keys are rejected before the handler runs.
	for _, bad := range []string{"", "short"} {
		hs := map[string]string{"Content-Type": "application/json"}
		if bad != "" {
			hs["Idempotency-Key"] = bad
		}
		rec := doRequest(t, h, http.MethodPost, "/api/v1/things", hs, []byte(`{"name":"a"}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("key %q: status = %d, want 400", bad, rec.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("handler ran %d times for invalid keys", calls)
	}

	// First request executes and stores the response.
	first := doRequest(t, h, http.MethodPost, "/api/v1/things", headers, []byte(`{"name":"a"}`))
	if first.Code != http.StatusOK || calls != 1 {
		t.Fatalf("first request: status = %d calls = %d", first.Code, calls)
	}
	if rec := first.Header().Get("Idempotent-Replay"); rec != "" {
		t.Errorf("first response must not carry Idempotent-Replay, got %q", rec)
	}

	// Identical retry replays the stored response without executing.
	replay := doRequest(t, h, http.MethodPost, "/api/v1/things", headers, []byte(`{"name":"a"}`))
	if replay.Code != http.StatusOK || calls != 1 {
		t.Fatalf("replay: status = %d calls = %d, want cached 200 with no extra call", replay.Code, calls)
	}
	if replay.Header().Get("Idempotent-Replay") != "true" {
		t.Error("replayed response must carry Idempotent-Replay: true")
	}
	if replay.Body.String() != first.Body.String() {
		t.Errorf("replay body = %s, want %s", replay.Body.String(), first.Body.String())
	}

	// Same key with a different payload conflicts.
	conflict := doRequest(t, h, http.MethodPost, "/api/v1/things", headers, []byte(`{"name":"b"}`))
	if conflict.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("conflict: status = %d calls = %d, want 409 with no extra call", conflict.Code, calls)
	}
	if env := decodeWireError(t, conflict); env.Code != domain.CodeIdempotencyConflict {
		t.Errorf("code = %q, want idempotency.conflict", env.Code)
	}
}

func TestIdempotencyInFlightConflict(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	api := NewAPI(Options{Log: silentLogger(), Auth: LocalAuth{}, Clock: fixedClock(), Idempotency: store})
	h := api.Handler()
	api.Handle("POST", "/things", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{})
	}, RouteOptions{IdempotencyRequired: true})

	// Simulate a concurrent request that claimed the key but has not committed.
	scope := idempotencyScope(Caller{ID: "local"}, http.MethodPost, "/api/v1/things")
	if _, claimed, err := store.Begin(scope, "inflight-key", "hash-x", testBase); err != nil || !claimed {
		t.Fatalf("pre-claim: claimed=%v err=%v", claimed, err)
	}

	rec := doRequest(t, h, http.MethodPost, "/api/v1/things",
		map[string]string{"Content-Type": "application/json", "Idempotency-Key": "inflight-key"},
		[]byte(`{"name":"a"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while the first request is in flight", rec.Code)
	}
	if env := decodeWireError(t, rec); env.Code != domain.CodeIdempotencyConflict {
		t.Errorf("code = %q, want idempotency.conflict", env.Code)
	}
}

func TestIdempotencyReplayAfterAccepted(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("POST", "/jobs", func(w http.ResponseWriter, r *http.Request) {
		WriteAccepted(w, r, "job_7", map[string]string{"id": "job_7"})
	}, RouteOptions{IdempotencyRequired: true})

	const key = "accept-key-1"
	headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": key}
	first := doRequest(t, h, http.MethodPost, "/api/v1/jobs", headers, []byte(`{}`))
	if first.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", first.Code)
	}
	replay := doRequest(t, h, http.MethodPost, "/api/v1/jobs", headers, []byte(`{}`))
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202", replay.Code)
	}
	if got := replay.Header().Get("Location"); got != "/api/v1/jobs/job_7" {
		t.Errorf("replayed Location = %q, want the stored job URL", got)
	}
}

func TestIdempotencyExpiryReexecutes(t *testing.T) {
	clock := fixedClock()
	store := NewMemoryIdempotencyStore()
	api := NewAPI(Options{Log: silentLogger(), Auth: LocalAuth{}, Clock: clock, Idempotency: store})
	h := api.Handler()
	calls := 0
	api.Handle("POST", "/things", func(w http.ResponseWriter, r *http.Request) {
		calls++
		WriteJSON(w, http.StatusOK, map[string]int{"call": calls})
	}, RouteOptions{IdempotencyRequired: true})

	headers := map[string]string{"Idempotency-Key": "expire-key-1"}
	first := doRequest(t, h, http.MethodPost, "/api/v1/things", headers, nil)
	if first.Code != http.StatusOK || calls != 1 {
		t.Fatalf("first: status=%d calls=%d", first.Code, calls)
	}

	// Past the replay window the same key executes again as a new request.
	clock.Advance(IdempotencyReplayWindow + time.Minute)
	again := doRequest(t, h, http.MethodPost, "/api/v1/things", headers, nil)
	if again.Code != http.StatusOK || calls != 2 {
		t.Fatalf("after expiry: status=%d calls=%d, want re-execution", again.Code, calls)
	}
	if again.Header().Get("Idempotent-Replay") != "" {
		t.Error("a re-executed request must not be flagged as a replay")
	}
	var body map[string]int
	if err := json.Unmarshal(again.Body.Bytes(), &body); err != nil || body["call"] != 2 {
		t.Errorf("body = %s, want the second execution result", again.Body.String())
	}
}

func TestIdempotencyMiddlewareLeavesBodyIntact(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("POST", "/echo", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if !DecodeJSON(w, r, &body) {
			return
		}
		WriteJSON(w, http.StatusOK, body)
	}, RouteOptions{IdempotencyRequired: true})

	rec := doRequest(t, h, http.MethodPost, "/api/v1/echo",
		map[string]string{"Content-Type": "application/json", "Idempotency-Key": "intact-key1"},
		[]byte(`{"name":"ok"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("handler saw a broken body: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
