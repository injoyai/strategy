// Shared pagination primitives for every list endpoint: limit clamping,
// opaque time-bound cursors and the fixed sort vocabulary.
//
// Stability contract: list handlers always order by (sort key, id) so a
// cursor can resume from the last (key, id) pair without duplicates or
// gaps. Cursors are opaque to clients, carry their creation time and the
// sort they were minted for, and expire (422 pagination.cursor_expired)
// once the TTL passes - a client must restart from the first page.
package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

const (
	// DefaultPageLimit and MaxPageLimit mirror the OpenAPI paging parameters.
	DefaultPageLimit = 50
	MaxPageLimit     = 200
	// CursorTTL bounds how long a client may walk one listing; the value
	// matches the idempotency replay window (24h) so both reuse the same
	// "long enough for interactive use" rationale.
	CursorTTL = 24 * time.Hour
	// SortAsc/SortDesc are the only sort values in the contract.
	SortAsc  = "id"
	SortDesc = "-id"
)

// Page is the parsed paging request shared by list handlers.
type Page struct {
	Limit  int
	Cursor string
	Sort   string
}

// ParsePage reads limit, cursor and sort from the query string. Invalid
// values write the mapped 400 response and return false.
func ParsePage(w http.ResponseWriter, r *http.Request) (Page, bool) {
	page := Page{Limit: DefaultPageLimit, Sort: SortAsc}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > MaxPageLimit {
			writeBoundaryError(w, r, domain.CodeValidationInvalid,
				"limit must be an integer between 1 and 200")
			return Page{}, false
		}
		page.Limit = n
	}
	page.Cursor = r.URL.Query().Get("cursor")
	if raw := r.URL.Query().Get("sort"); raw != "" {
		if raw != SortAsc && raw != SortDesc {
			writeBoundaryError(w, r, domain.CodeValidationInvalid,
				"sort must be one of: id, -id")
			return Page{}, false
		}
		page.Sort = raw
	}
	return page, true
}

// cursorPayload is the wire format of an opaque cursor. Versioned so an
// incompatible format change forces clients to restart paging instead of
// decoding garbage.
type cursorPayload struct {
	Version   int       `json:"v"`
	CreatedAt time.Time `json:"at"`
	Sort      string    `json:"sort"`
	LastID    string    `json:"last_id"`
}

// EncodeCursor serializes a resume point into its opaque form.
func EncodeCursor(sort, lastID string, createdAt time.Time) string {
	raw, err := json.Marshal(cursorPayload{Version: 1, CreatedAt: createdAt.UTC(), Sort: sort, LastID: lastID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ParseCursor decodes an opaque cursor and validates it against the sort
// the handler is using now. Expired cursors fail with the dedicated
// pagination code; malformed or foreign cursors fail as invalid input.
func ParseCursor(w http.ResponseWriter, r *http.Request, encoded, sort string, now time.Time) (Cursor, bool) {
	if encoded == "" {
		return Cursor{Sort: sort}, true
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		writeBoundaryError(w, r, domain.CodeValidationInvalid, "cursor is malformed")
		return Cursor{}, false
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Version != 1 {
		writeBoundaryError(w, r, domain.CodeValidationInvalid, "cursor is malformed")
		return Cursor{}, false
	}
	if p.Sort != sort {
		writeBoundaryError(w, r, domain.CodeValidationInvalid, "cursor does not match the requested sort")
		return Cursor{}, false
	}
	if now.Sub(p.CreatedAt) > CursorTTL {
		writeBoundaryError(w, r, domain.CodePaginationCursorExpired,
			"cursor expired; restart listing from the first page")
		return Cursor{}, false
	}
	return Cursor{Sort: p.Sort, LastID: p.LastID}, true
}

// Cursor is the decoded resume point handed to list handlers.
type Cursor struct {
	Sort   string
	LastID string
}
