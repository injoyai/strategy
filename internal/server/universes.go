package server

import (
	"net/http"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// The M1-09 universe surface: immutable member-selection versions bound
// to one snapshot, resolved strictly through a view pinned to that
// snapshot — never through current tables. Creation is not idempotent:
// every save mints a new version, so the Idempotency-Key guards the
// HTTP replay, not the version count.

func (a *API) RegisterUniverses() {
	a.Handle(http.MethodGet, "/universes", a.listUniverses, RouteOptions{})
	a.Handle(http.MethodPost, "/universes", a.createUniverse, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/universes/{id}", a.getUniverse, RouteOptions{})
	a.Handle(http.MethodPost, "/universes/{id}/resolve", a.resolveUniverse, RouteOptions{})
}

func (a *API) requireResearch(w http.ResponseWriter, r *http.Request) bool {
	if a.research != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "research surface is not mounted on this API"))
	return false
}

type universePage struct {
	Items      []domain.UniverseVersion `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

func (a *API) listUniverses(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	cursor, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
	if !ok {
		return
	}
	res, err := a.data.ListUniverseVersions(r.Context(), ports.UniverseFilter{
		Q:       r.URL.Query().Get("q"),
		Sort:    page.Sort,
		AfterID: cursor.LastID,
		Limit:   page.Limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := universePage{Items: res.Items}
	if res.NextCursor != "" {
		cur := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &cur
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) createUniverse(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	var req domain.UniverseVersionRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	universe, err := a.data.CreateUniverseVersion(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, universe)
}

func (a *API) getUniverse(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	universe, err := a.data.GetUniverseVersion(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, universe)
}

type universeResolveRequest struct {
	AsOf time.Time `json:"as_of"`
}

type universeMembersWire struct {
	UniverseID    string    `json:"universe_id"`
	AsOf          time.Time `json:"as_of"`
	InstrumentIDs []string  `json:"instrument_ids"`
	Count         int       `json:"count"`
}

// resolveUniverse previews the membership at an explicit decision time.
// The snapshot comes from the version's binding, and resolution goes
// through a view pinned to it — the resolver structurally cannot mix
// membership from one snapshot with decision data from another.
func (a *API) resolveUniverse(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	var req universeResolveRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if req.AsOf.IsZero() {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "universe: as_of is required"))
		return
	}
	universe, err := a.data.GetUniverseVersion(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	view, err := a.data.OpenView(r.Context(), universe.SnapshotID, req.AsOf)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	members, err := data.ResolveUniverse(r.Context(), universe, view)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.String())
	}
	WriteJSON(w, http.StatusOK, universeMembersWire{
		UniverseID:    universe.ID.String(),
		AsOf:          view.AsOf(),
		InstrumentIDs: ids,
		Count:         len(ids),
	})
}
