package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

func TestParsePageDefaults(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	page, ok := ParsePage(rec, req)
	if !ok {
		t.Fatal("empty query must parse")
	}
	if page.Limit != DefaultPageLimit || page.Sort != SortAsc || page.Cursor != "" {
		t.Errorf("page = %+v, want defaults limit=50 sort=id", page)
	}
}

func TestParsePageLimits(t *testing.T) {
	cases := []struct {
		raw    string
		want   int
		wantOK bool
	}{
		{"1", 1, true},
		{"200", 200, true},
		{"75", 75, true},
		{"0", 0, false},
		{"201", 0, false},
		{"-5", 0, false},
		{"abc", 0, false},
		{"1.5", 0, false},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/things?limit="+tc.raw, nil)
		page, ok := ParsePage(rec, req)
		if ok != tc.wantOK {
			t.Errorf("limit %q: ok = %v, want %v (status %d)", tc.raw, ok, tc.wantOK, rec.Code)
			continue
		}
		if !ok {
			if rec.Code != http.StatusBadRequest {
				t.Errorf("limit %q: status = %d, want 400", tc.raw, rec.Code)
			}
			if env := decodeWireError(t, rec); env.Code != domain.CodeValidationInvalid {
				t.Errorf("limit %q: code = %q", tc.raw, env.Code)
			}
			continue
		}
		if page.Limit != tc.want {
			t.Errorf("limit %q: parsed %d, want %d", tc.raw, page.Limit, tc.want)
		}
	}
}

func TestParsePageSortWhitelist(t *testing.T) {
	for _, sort := range []string{SortAsc, SortDesc} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/things?sort="+sort, nil)
		page, ok := ParsePage(rec, req)
		if !ok || page.Sort != sort {
			t.Errorf("sort %q: ok=%v page=%+v, want accepted", sort, ok, page)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things?sort=name", nil)
	if _, ok := ParsePage(rec, req); ok || rec.Code != http.StatusBadRequest {
		t.Errorf("sort=name: ok=%v status=%d, want 400", ok, rec.Code)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	encoded := EncodeCursor(SortAsc, "job_00042", testBase)
	if encoded == "" {
		t.Fatal("EncodeCursor returned empty")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	cursor, ok := ParseCursor(rec, req, encoded, SortAsc, testBase.Add(time.Hour))
	if !ok {
		t.Fatalf("ParseCursor failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cursor.LastID != "job_00042" || cursor.Sort != SortAsc {
		t.Errorf("cursor = %+v, want last_id=job_00042 sort=id", cursor)
	}
}

func TestCursorExpired(t *testing.T) {
	encoded := EncodeCursor(SortAsc, "x", testBase)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	if _, ok := ParseCursor(rec, req, encoded, SortAsc, testBase.Add(CursorTTL+time.Minute)); ok {
		t.Fatal("expired cursor must fail")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if env := decodeWireError(t, rec); env.Code != domain.CodePaginationCursorExpired {
		t.Errorf("code = %q, want pagination.cursor_expired", env.Code)
	}
}

func TestCursorSortMismatch(t *testing.T) {
	encoded := EncodeCursor(SortAsc, "x", testBase)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	if _, ok := ParseCursor(rec, req, encoded, SortDesc, testBase); ok || rec.Code != http.StatusBadRequest {
		t.Errorf("mismatched sort: ok=%v status=%d, want 400", ok, rec.Code)
	}
}

func TestCursorMalformed(t *testing.T) {
	legacy, err := json.Marshal(cursorPayload{Version: 0, Sort: SortAsc, LastID: "x", CreatedAt: testBase})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"garbage":     "not-a-cursor!!",
		"legacy":      base64.RawURLEncoding.EncodeToString(legacy),
		"broken json": "eyJzb3J0Ijo",
	}
	for name, encoded := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
		if _, ok := ParseCursor(rec, req, encoded, SortAsc, testBase); ok || rec.Code != http.StatusBadRequest {
			t.Errorf("%s: ok=%v status=%d, want 400", name, ok, rec.Code)
		}
	}
}

func TestCursorEmptyIsFirstPage(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	cursor, ok := ParseCursor(rec, req, "", SortAsc, testBase)
	if !ok {
		t.Fatalf("empty cursor must be the first page: status=%d", rec.Code)
	}
	if cursor.Sort != SortAsc || cursor.LastID != "" {
		t.Fatalf("first-page cursor = %+v, want zero cursor with sort asc", cursor)
	}
}

func TestCursorNotExpiredAtTTLBoundary(t *testing.T) {
	encoded := EncodeCursor(SortAsc, "x", testBase)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	if _, ok := ParseCursor(rec, req, encoded, SortAsc, testBase.Add(CursorTTL)); !ok {
		t.Fatalf("cursor at exactly the TTL must still parse: status=%d", rec.Code)
	}
}
