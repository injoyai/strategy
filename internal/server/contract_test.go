package server

// Contract tests: the OpenAPI document in docs/api/openapi.json is the single
// source of truth. Every operation is registered against a transport-only
// stub and then exercised through the full middleware chain, so the shell
// (auth, request IDs, idempotency, pagination, error mapping) is verified
// against the same document the TypeScript client is generated from.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

const contractToken = "contract-token-0123456789"

type contractSpec struct {
	Paths map[string]contractPathItem `json:"paths"`
}

type contractPathItem struct {
	Get  *contractOperation `json:"get"`
	Post *contractOperation `json:"post"`
}

type contractOperation struct {
	OperationID string                     `json:"operationId"`
	Parameters  []contractParameter        `json:"parameters"`
	RequestBody *contractRequestBodyField  `json:"requestBody"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

type contractParameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
}

type contractRequestBodyField struct {
	Required bool `json:"required"`
}

type contractCase struct {
	OperationID string
	Method      string
	Path        string
	Op          *contractOperation
	Idem        bool
	HasBody     bool
	HasLimit    bool
}

func (op *contractOperation) requiresIdempotency() bool {
	for _, p := range op.Parameters {
		if p.In == "header" && p.Name == "Idempotency-Key" && p.Required {
			return true
		}
	}
	return false
}

func (op *contractOperation) hasQueryLimit() bool {
	for _, p := range op.Parameters {
		if p.In == "query" && p.Name == "limit" {
			return true
		}
	}
	return false
}

func (op *contractOperation) successStatus(t *testing.T, opID string) int {
	t.Helper()
	for _, code := range []string{"200", "201", "202", "203", "204"} {
		if _, ok := op.Responses[code]; ok {
			var n int
			if _, err := fmt.Sscanf(code, "%d", &n); err != nil {
				t.Fatalf("operation %s: bad success status %q", opID, code)
			}
			return n
		}
	}
	t.Fatalf("operation %s declares no 2xx response", opID)
	return 0
}

// newContractAPI loads docs/api/openapi.json, registers a transport stub for
// every operation and returns the API, the wrapped handler and the sorted
// case list.
func newContractAPI(t *testing.T) (*API, http.Handler, []contractCase) {
	t.Helper()
	raw, err := os.ReadFile("../../docs/api/openapi.json")
	if err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}
	var spec contractSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse openapi.json: %v", err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("openapi.json declares no paths")
	}

	api := NewAPI(Options{Log: silentLogger(), Auth: NewBearerAuth(contractToken)})
	var cases []contractCase
	for path, item := range spec.Paths {
		for method, op := range map[string]*contractOperation{"get": item.Get, "post": item.Post} {
			if op == nil {
				continue
			}
			if op.OperationID == "" {
				t.Fatalf("%s %s: missing operationId", method, path)
			}
			c := contractCase{
				OperationID: op.OperationID,
				Method:      strings.ToUpper(method),
				Path:        path,
				Op:          op,
				Idem:        op.requiresIdempotency(),
				HasBody:     op.RequestBody != nil,
				HasLimit:    op.hasQueryLimit(),
			}
			success := op.successStatus(t, c.OperationID)
			api.Handle(c.Method, path, func(w http.ResponseWriter, r *http.Request) {
				if c.HasLimit {
					if _, ok := ParsePage(w, r); !ok {
						return
					}
				}
				if c.HasBody {
					var body struct {
						Name string `json:"name"`
					}
					if !DecodeJSON(w, r, &body) {
						return
					}
				}
				if success == http.StatusAccepted {
					WriteAccepted(w, r, "job_1", map[string]any{"id": "job_1"})
					return
				}
				WriteJSON(w, success, map[string]any{"ok": true})
			}, RouteOptions{IdempotencyRequired: c.Idem})
			cases = append(cases, c)
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].OperationID < cases[j].OperationID })
	return api, api.Handler(), cases
}

var pathParamPattern = regexp.MustCompile(`\{[^}]+\}`)

func concretePath(path string) string {
	return pathParamPattern.ReplaceAllString(path, "t0")
}

// contractKey builds a per-operation idempotency key within the 8..128 bound.
func contractKey(prefix, opID string) string {
	key := prefix + "-" + strings.ReplaceAll(opID, "_", "-")
	if len(key) < 8 {
		key += "-xxxx"
	}
	if len(key) > 128 {
		key = key[:128]
	}
	return key
}

func contractHeaders(c contractCase, key string) map[string]string {
	headers := map[string]string{"Authorization": "Bearer " + contractToken}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	if c.HasBody {
		headers["Content-Type"] = "application/json"
	}
	return headers
}

func contractBody(c contractCase) []byte {
	if c.HasBody {
		return []byte(`{"name":"x"}`)
	}
	return nil
}

// TestContractAllOperationsSucceed walks every operation in the OpenAPI
// document and expects the declared success status through the full chain.
func TestContractAllOperationsSucceed(t *testing.T) {
	_, h, cases := newContractAPI(t)
	if len(cases) == 0 {
		t.Fatal("no operations discovered")
	}
	for _, c := range cases {
		t.Run(c.OperationID, func(t *testing.T) {
			want := c.Op.successStatus(t, c.OperationID)
			key := ""
			if c.Idem {
				key = contractKey("ck", c.OperationID)
			}
			target := "/api/v1" + concretePath(c.Path)
			rec := doRequest(t, h, c.Method, target, contractHeaders(c, key), contractBody(c))
			if rec.Code != want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, want, rec.Body.String())
			}
			if want == http.StatusAccepted {
				if got := rec.Header().Get("Location"); got != "/api/v1/jobs/job_1" {
					t.Errorf("202 Location = %q, want the job resource URL", got)
				}
			}
			if rec.Header().Get("X-Request-ID") == "" {
				t.Error("every response must carry X-Request-ID")
			}
		})
	}
}

// TestContractAuthRequiredEverywhere proves no /api/v1 operation is reachable
// without credentials, whatever the auth mode returns.
func TestContractAuthRequiredEverywhere(t *testing.T) {
	_, h, cases := newContractAPI(t)
	for _, c := range cases {
		t.Run(c.OperationID, func(t *testing.T) {
			headers := map[string]string{}
			if c.HasBody {
				headers["Content-Type"] = "application/json"
				headers["Idempotency-Key"] = contractKey("an", c.OperationID)
			}
			target := "/api/v1" + concretePath(c.Path)
			rec := doRequest(t, h, c.Method, target, headers, contractBody(c))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="researchd"` {
				t.Errorf("WWW-Authenticate = %q, want the bearer challenge", got)
			}
			if env := decodeWireError(t, rec); env.Code != domain.CodeAuthUnauthorized {
				t.Errorf("code = %q, want auth.unauthorized", env.Code)
			}
		})
	}
}

// TestContractIdempotencyKeyRequired checks that exactly the operations whose
// OpenAPI definition carries the required Idempotency-Key header reject
// requests without one - and that reads do not demand it.
func TestContractIdempotencyKeyRequired(t *testing.T) {
	_, h, cases := newContractAPI(t)
	for _, c := range cases {
		t.Run(c.OperationID, func(t *testing.T) {
			headers := contractHeaders(c, "")
			target := "/api/v1" + concretePath(c.Path)
			rec := doRequest(t, h, c.Method, target, headers, contractBody(c))
			if c.Idem {
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 for a missing Idempotency-Key", rec.Code)
				}
				if env := decodeWireError(t, rec); env.Code != domain.CodeValidationInvalid {
					t.Errorf("code = %q, want validation.invalid", env.Code)
				}
				return
			}
			if rec.Code >= 400 && rec.Code < 500 {
				t.Errorf("operation without the header requirement failed with %d; the stub must not demand a key", rec.Code)
			}
		})
	}
}

// TestContractUnknownFieldRejected proves every operation with a request body
// rejects fields the contract does not define.
func TestContractUnknownFieldRejected(t *testing.T) {
	_, h, cases := newContractAPI(t)
	for _, c := range cases {
		if !c.HasBody {
			continue
		}
		t.Run(c.OperationID, func(t *testing.T) {
			headers := contractHeaders(c, contractKey("uk", c.OperationID))
			body := []byte(`{"name":"x","totally_unknown_field":1}`)
			target := "/api/v1" + concretePath(c.Path)
			rec := doRequest(t, h, c.Method, target, headers, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if env := decodeWireError(t, rec); env.Code != domain.CodeValidationUnknownField {
				t.Errorf("code = %q, want validation.unknown_field", env.Code)
			}
		})
	}
}

// TestContractIdempotentReplayAndConflict exercises the retry semantics on a
// real idempotent route through the whole chain.
func TestContractIdempotentReplayAndConflict(t *testing.T) {
	_, h, cases := newContractAPI(t)
	var c contractCase
	for _, tc := range cases {
		if tc.Idem {
			c = tc
			break
		}
	}
	if c.OperationID == "" {
		t.Fatal("no idempotent operation found in the contract")
	}
	target := "/api/v1" + concretePath(c.Path)
	key := contractKey("rp", c.OperationID)
	headers := contractHeaders(c, key)

	first := doRequest(t, h, c.Method, target, headers, contractBody(c))
	if first.Code != c.Op.successStatus(t, c.OperationID) {
		t.Fatalf("first request: status = %d", first.Code)
	}

	replay := doRequest(t, h, c.Method, target, headers, contractBody(c))
	if replay.Code != first.Code {
		t.Fatalf("replay status = %d, want %d", replay.Code, first.Code)
	}
	if replay.Header().Get("Idempotent-Replay") != "true" {
		t.Error("replay must carry Idempotent-Replay: true")
	}
	if replay.Body.String() != first.Body.String() {
		t.Errorf("replay body %s differs from first %s", replay.Body.String(), first.Body.String())
	}
	if loc := first.Header().Get("Location"); loc != "" && replay.Header().Get("Location") != loc {
		t.Errorf("replayed Location %q, want %q", replay.Header().Get("Location"), loc)
	}

	headers["Idempotency-Key"] = key
	conflictBody := []byte(`{"name":"different"}`)
	if !c.HasBody {
		conflictBody = nil
	}
	other := doRequest(t, h, c.Method, target, headers, conflictBody)
	if c.HasBody && other.Code != http.StatusConflict {
		t.Fatalf("different payload: status = %d, want 409", other.Code)
	}
	if c.HasBody {
		if env := decodeWireError(t, other); env.Code != domain.CodeIdempotencyConflict {
			t.Errorf("code = %q, want idempotency.conflict", env.Code)
		}
	}
}

// TestContractPaginationLimitValidation exercises the shared paging
// parameters on every operation that declares them.
func TestContractPaginationLimitValidation(t *testing.T) {
	_, h, cases := newContractAPI(t)
	checked := 0
	for _, c := range cases {
		if !c.HasLimit {
			continue
		}
		checked++
		t.Run(c.OperationID, func(t *testing.T) {
			target := "/api/v1" + concretePath(c.Path)
			key := ""
			if c.Idem {
				key = contractKey("pg", c.OperationID)
			}
			headers := contractHeaders(c, key)

			rec := doRequest(t, h, c.Method, target+"?limit=abc", headers, contractBody(c))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("limit=abc: status = %d, want 400", rec.Code)
			}
			if env := decodeWireError(t, rec); env.Code != domain.CodeValidationInvalid {
				t.Errorf("limit=abc: code = %q", env.Code)
			}

			rec = doRequest(t, h, c.Method, target+"?limit=0", headers, contractBody(c))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("limit=0: status = %d, want 400", rec.Code)
			}

			rec = doRequest(t, h, c.Method, target+"?limit=200", headers, contractBody(c))
			want := c.Op.successStatus(t, c.OperationID)
			if rec.Code != want {
				t.Fatalf("limit=200: status = %d, want %d", rec.Code, want)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no operation declares the limit parameter")
	}
}

// TestContractRequestIDPropagation checks client-supplied IDs travel through
// the chain and that malformed ones fail closed with a minted envelope ID.
func TestContractRequestIDPropagation(t *testing.T) {
	_, h, cases := newContractAPI(t)
	c := cases[0]
	target := "/api/v1" + concretePath(c.Path)
	headers := contractHeaders(c, "")
	headers["X-Request-ID"] = "contract-rid-0001"

	rec := doRequest(t, h, c.Method, target, headers, contractBody(c))
	if got := rec.Header().Get("X-Request-ID"); got != "contract-rid-0001" {
		t.Errorf("X-Request-ID = %q, want the client value", got)
	}

	bad := contractHeaders(c, "")
	bad["X-Request-ID"] = "spaces not allowed"
	rec = doRequest(t, h, c.Method, target, bad, contractBody(c))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed X-Request-ID: status = %d, want 400", rec.Code)
	}
	if env := decodeWireError(t, rec); env.RequestID == "spaces not allowed" {
		t.Error("malformed client ID must not be echoed into the envelope")
	}
}

// TestContractUnknownRouteEnvelope pins the envelope shape for routing
// misses under /api/v1.
func TestContractUnknownRouteEnvelope(t *testing.T) {
	_, h, _ := newContractAPI(t)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/definitely/not/registered",
		map[string]string{"Authorization": "Bearer " + contractToken}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if env := decodeWireError(t, rec); env.Code != domain.CodeResourceNotFound {
		t.Errorf("code = %q, want resource.not_found", env.Code)
	}
}

// TestContractHealthAndRoot covers the process-local endpoints outside the
// authenticated API surface.
func TestContractHealthAndRoot(t *testing.T) {
	api, _, _ := newContractAPI(t)
	root := RootMux(silentLogger(), api)

	rec := doRequest(t, root, http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("healthz: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, root, http.MethodPost, "/healthz", nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("healthz POST: status = %d, want 405", rec.Code)
	}
	rec = doRequest(t, root, http.MethodGet, "/readyz", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ready") {
		t.Errorf("readyz: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, root, http.MethodGet, "/", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("root: status = %d, want 404", rec.Code)
	}
}
