package server

import (
	"net/http"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// The dataset catalog surface: what each dataset declares, what its rows cover
// and which quality findings were recorded against it. It is read-only and
// derived entirely from persisted state.

func (a *API) RegisterDatasets() {
	a.Handle(http.MethodGet, "/datasets", a.listDatasets, RouteOptions{})
	a.Handle(http.MethodGet, "/datasets/{id}", a.getDataset, RouteOptions{})
}

// requireDatasets guards the catalog: it is derived entirely from the data
// store, so without one there is nothing to report.
func (a *API) requireDatasets(w http.ResponseWriter, r *http.Request) bool {
	if a.data != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "data store is not mounted on this API"))
	return false
}

type datasetPage struct {
	Items      []domain.Dataset `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

func (a *API) listDatasets(w http.ResponseWriter, r *http.Request) {
	if !a.requireDatasets(w, r) {
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
	res, err := a.data.ListDatasets(r.Context(), ports.DatasetFilter{
		Q:       r.URL.Query().Get("q"),
		Sort:    page.Sort,
		AfterID: cursor.LastID,
		Limit:   page.Limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := datasetPage{Items: res.Items}
	if out.Items == nil {
		out.Items = []domain.Dataset{}
	}
	if res.NextCursor != "" {
		next := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &next
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) getDataset(w http.ResponseWriter, r *http.Request) {
	if !a.requireDatasets(w, r) {
		return
	}
	dataset, err := a.data.GetDataset(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, dataset)
}
