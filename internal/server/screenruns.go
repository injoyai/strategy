package server

import (
	"net/http"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/screenrun"
)

// The M1S screening-run surface. Preflight is a read-only check that persists
// nothing, so it carries /data/query's read-only POST treatment: no
// Idempotency-Key, and replaying it can never change an answer.

func (a *API) RegisterScreenRuns() {
	a.Handle(http.MethodPost, "/screen-runs/preflight", a.preflightScreenRun, RouteOptions{})
}

// requireScreenRuns guards the surface: it is only mounted when the screening
// run service is supplied.
func (a *API) requireScreenRuns(w http.ResponseWriter, r *http.Request) bool {
	if a.screenRuns != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "screening runs are not mounted on this API"))
	return false
}

type screenCoverageWire struct {
	BindingID string  `json:"binding_id"`
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
}

type screenPreflightWire struct {
	Valid             bool                 `json:"valid"`
	Issues            []domain.Issue       `json:"issues"`
	Coverage          []screenCoverageWire `json:"coverage"`
	EstimatedScanRows *int                 `json:"estimated_scan_rows"`
	EstimatedRows     *int                 `json:"estimated_rows"`
}

func (a *API) preflightScreenRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) {
		return
	}
	var req screenrun.Request
	if !DecodeJSON(w, r, &req) {
		return
	}
	result, err := a.screenRuns.Preflight(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := screenPreflightWire{
		Valid:             result.Valid,
		Issues:            result.Issues,
		Coverage:          make([]screenCoverageWire, 0, len(result.Coverage)),
		EstimatedScanRows: result.EstimatedScanRows,
		EstimatedRows:     result.EstimatedRows,
	}
	if out.Issues == nil {
		out.Issues = []domain.Issue{}
	}
	for _, coverage := range result.Coverage {
		out.Coverage = append(out.Coverage, screenCoverageWire{
			BindingID: coverage.BindingID.String(),
			Available: coverage.Available,
			Reason:    coverage.Reason,
		})
	}
	WriteJSON(w, http.StatusOK, out)
}
