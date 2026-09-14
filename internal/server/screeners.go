package server

import (
	"net/http"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// The M1S screener surface: immutable screening rule versions. Saving is not
// idempotent — every save mints a new revision — so the Idempotency-Key
// guards an HTTP replay, not the revision count. (id, version) addresses
// exactly one frozen rule set, and a version is never addressed by id alone:
// guessing the latest revision would make a run unreproducible.

func (a *API) RegisterScreeners() {
	a.Handle(http.MethodGet, "/screeners", a.listScreeners, RouteOptions{})
	a.Handle(http.MethodPost, "/screeners", a.createScreener, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/screeners/{id}", a.getScreener, RouteOptions{})
}

// requireScreenerStore guards the surface: without the data store there is
// nowhere to persist immutable versions.
func (a *API) requireScreenerStore(w http.ResponseWriter, r *http.Request) bool {
	if a.data != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "screener store is not mounted on this API"))
	return false
}

type screenerPage struct {
	Items      []screening.WireScreener `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

func (a *API) listScreeners(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenerStore(w, r) {
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
	res, err := a.data.ListScreenerVersions(r.Context(), ports.ScreenerFilter{
		Q:       r.URL.Query().Get("q"),
		Sort:    page.Sort,
		AfterID: cursor.LastID,
		Limit:   page.Limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := screenerPage{Items: make([]screening.WireScreener, 0, len(res.Items))}
	for _, v := range res.Items {
		out.Items = append(out.Items, screening.WireScreenerOf(v))
	}
	if res.NextCursor != "" {
		next := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &next
	}
	WriteJSON(w, http.StatusOK, out)
}

// screenerCreateWire is the ScreenerCreate request body. Absent fields stay
// absent so the definition view decides what is invalid, rather than this
// decoder inventing defaults.
type screenerCreateWire struct {
	Name           string                       `json:"name"`
	Description    string                       `json:"description"`
	InputBindings  []screening.WireInputBinding `json:"input_bindings"`
	ConditionTree  screening.WireCondition      `json:"condition_tree"`
	Ranking        screening.WireRanking        `json:"ranking"`
	Selection      screening.WireSelection      `json:"selection"`
	DisplayColumns []domain.ID                  `json:"display_columns"`
	ParentID       domain.ID                    `json:"parent_id"`
}

func (a *API) createScreener(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenerStore(w, r) {
		return
	}
	var req screenerCreateWire
	if !DecodeJSON(w, r, &req) {
		return
	}
	def, err := screening.DefinitionOfWire(screening.WireDefinition{
		InputBindings:  req.InputBindings,
		ConditionTree:  req.ConditionTree,
		Ranking:        req.Ranking,
		Selection:      req.Selection,
		DisplayColumns: req.DisplayColumns,
	}, req.Name, req.Description, req.ParentID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	version, err := a.data.CreateScreenerVersion(r.Context(), screening.VersionRequest{
		Name:        req.Name,
		Description: req.Description,
		ParentID:    req.ParentID,
		Definition:  def,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, screening.WireScreenerOf(version))
}

func (a *API) getScreener(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenerStore(w, r) {
		return
	}
	// version is a required query parameter in the contract: an id alone does
	// not name a frozen rule set.
	version := r.URL.Query().Get("version")
	if version == "" {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "screener: version query parameter is required"))
		return
	}
	stored, err := a.data.GetScreenerVersion(r.Context(), domain.ID(r.PathValue("id")), domain.ID(version))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, screening.WireScreenerOf(stored))
}
