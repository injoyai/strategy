package screenrun

import (
	"context"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// fakeView serves canned event times so the window derivation can be tested
// without a database; Query and DatasetInstruments are unused by these tests.
type fakeView struct {
	asOf  time.Time
	times []time.Time // newest first, as the real view returns them
	calls []string
}

func (f *fakeView) SnapshotID() domain.ID { return "snap_1" }
func (f *fakeView) AsOf() time.Time       { return f.asOf }

func (f *fakeView) Query(context.Context, domain.DataQuery) (domain.PageResult[domain.Observation], error) {
	return domain.PageResult[domain.Observation]{}, nil
}

func (f *fakeView) DatasetInstruments(context.Context, string, string) ([]domain.ID, error) {
	return nil, nil
}

func (f *fakeView) LatestValues(context.Context, string, string, string, []domain.ID) (map[domain.ID]domain.Value, error) {
	return map[domain.ID]domain.Value{}, nil
}

func (f *fakeView) RecentEventTimes(_ context.Context, dataset, frequency string, _ []domain.ID, limit int) ([]time.Time, error) {
	f.calls = append(f.calls, dataset+"/"+frequency)
	if limit >= len(f.times) {
		return f.times, nil
	}
	return f.times[:limit], nil
}

var _ ports.DataView = (*fakeView)(nil)

func testRegistry(t *testing.T) *factor.Registry {
	t.Helper()
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	spec := &factor.Spec{
		ID:      "momentum",
		Version: "1.0.0",
		Title:   "Momentum",
		Kind:    factor.KindBuiltin,
		Params: []factor.Param{
			{Name: "n", Type: factor.ParamInteger, Required: true},
		},
		Inputs: []factor.Input{{
			Name: "close", Dataset: "bar", Field: "close", Frequency: "daily",
			LookbackFor: func(params map[string]any) int { return 2 },
			Unit:        "cny", PIT: true,
		}},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal("1"), "", nil
		},
	}
	if err := registry.Register(spec); err != nil {
		t.Fatalf("register momentum: %v", err)
	}
	return registry
}

func days(t *testing.T, values ...string) []time.Time {
	t.Helper()
	out := make([]time.Time, 0, len(values))
	for _, value := range values {
		at, err := time.Parse(time.RFC3339, value+"T15:00:00Z")
		if err != nil {
			t.Fatalf("parse %s: %v", value, err)
		}
		out = append(out, at.UTC())
	}
	return out
}

// TestRequestValidate pins the boundary between "cannot be evaluated" (an
// error, answered as an HTTP failure) and findings (reported in the payload).
func TestRequestValidate(t *testing.T) {
	valid := Request{
		ScreenerRef:         domain.VersionRef{ID: "scr_1", Version: "v1"},
		SnapshotID:          "snap_1",
		UniverseRef:         domain.VersionRef{ID: "univ_1", Version: "hash_1"},
		AsOf:                time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		DecisionTimezone:    "Asia/Shanghai",
		RequiredValuePolicy: string(screening.PolicyExcludeInstrument),
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"missing screener id", func(r *Request) { r.ScreenerRef.ID = "" }},
		{"floating screener version", func(r *Request) { r.ScreenerRef.Version = domain.LatestVersion }},
		{"missing universe ref", func(r *Request) { r.UniverseRef.ID = "" }},
		{"missing snapshot", func(r *Request) { r.SnapshotID = "" }},
		{"missing as_of", func(r *Request) { r.AsOf = time.Time{} }},
		{"missing timezone", func(r *Request) { r.DecisionTimezone = "  " }},
		{"unknown timezone", func(r *Request) { r.DecisionTimezone = "Mars/Olympus" }},
		{"unknown policy", func(r *Request) { r.RequiredValuePolicy = "zero_fill" }},
		{"empty policy", func(r *Request) { r.RequiredValuePolicy = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			tc.mutate(&req)
			err := req.validate()
			if err == nil {
				t.Fatal("invalid request accepted")
			}
			if code := domain.ErrorCode(err); code != domain.CodeValidationInvalid {
				t.Fatalf("code = %q, want %q", code, domain.CodeValidationInvalid)
			}
		})
	}
}

// TestDeriveWindowUsesSnapshotHistory pins the whole point of the derivation:
// the window start is the lookback-th most recent event time in the snapshot,
// so a period count is never converted into an unverified time span.
func TestDeriveWindowUsesSnapshotHistory(t *testing.T) {
	registry := testRegistry(t)
	spec, err := registry.Lookup(factor.FactorRef{ID: "momentum", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("lookup momentum: %v", err)
	}
	view := &fakeView{
		asOf:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		times: days(t, "2026-01-14", "2026-01-13", "2026-01-12", "2026-01-09"),
	}
	from, dates, problem, err := deriveWindow(context.Background(), view, []domain.ID{"INST_A"}, spec, map[string]any{"n": 20})
	if err != nil {
		t.Fatalf("derive window: %v", err)
	}
	if problem != nil {
		t.Fatalf("unexpected finding: %+v", problem)
	}
	// The spec requires two points, so the window opens on the second newest.
	if want := days(t, "2026-01-13")[0]; !from.Equal(want) {
		t.Fatalf("window start = %s, want %s", from, want)
	}
	// The window opens on the second newest date and therefore holds exactly the
	// required number of dates — the estimate counts the window, not every date
	// the snapshot happens to carry.
	if dates != 2 {
		t.Fatalf("dates = %d, want 2 (the window's own date count)", dates)
	}
	if len(view.calls) != 1 || view.calls[0] != "bar/daily" {
		t.Fatalf("view calls = %v, want one bar/daily lookup", view.calls)
	}
}

// TestDeriveWindowReportsInsufficientHistory proves a short snapshot is a
// finding rather than a silently shortened window.
func TestDeriveWindowReportsInsufficientHistory(t *testing.T) {
	registry := testRegistry(t)
	spec, err := registry.Lookup(factor.FactorRef{ID: "momentum", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("lookup momentum: %v", err)
	}
	view := &fakeView{
		asOf:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		times: days(t, "2026-01-14"),
	}
	from, dates, problem, err := deriveWindow(context.Background(), view, []domain.ID{"INST_A"}, spec, nil)
	if err != nil {
		t.Fatalf("derive window: %v", err)
	}
	if problem == nil {
		t.Fatal("one available point against a two-point lookback must be reported")
	}
	if problem.Code != factor.ProblemInsufficientHistory {
		t.Fatalf("code = %q, want %q", problem.Code, factor.ProblemInsufficientHistory)
	}
	if problem.Severity != domain.SeverityError {
		t.Fatalf("severity = %q, want error", problem.Severity)
	}
	if !from.IsZero() {
		t.Fatalf("window start = %s, want zero when the history is insufficient", from)
	}
	if dates != 1 {
		t.Fatalf("dates = %d, want the 1 available date", dates)
	}
}

// TestDeriveWindowTakesTheWidestRequirement pins the multi-input rule: the
// window has to cover every input, not just the first one.
func TestDeriveWindowTakesTheWidestRequirement(t *testing.T) {
	spec := &factor.Spec{
		ID:      "composite",
		Version: "1.0.0",
		Inputs: []factor.Input{
			{Name: "close", Dataset: "bar", Field: "close", Frequency: "daily", Lookback: 1},
			{Name: "eps", Dataset: "valuation", Field: "eps", Frequency: "quarterly", Lookback: 2},
		},
	}
	view := &fakeView{
		asOf:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		times: days(t, "2026-01-14", "2026-01-13"),
	}
	from, dates, problem, err := deriveWindow(context.Background(), view, []domain.ID{"INST_A"}, spec, nil)
	if err != nil {
		t.Fatalf("derive window: %v", err)
	}
	if problem != nil {
		t.Fatalf("unexpected finding: %+v", problem)
	}
	// Both inputs answer the same canned list, so the shallow one would open on
	// the newest date while the deep one needs the older of the two: the widest
	// requirement has to win.
	if dates != 2 {
		t.Fatalf("dates = %d, want 2", dates)
	}
	if len(view.calls) != 2 {
		t.Fatalf("view calls = %v, want one per input", view.calls)
	}
	if want := days(t, "2026-01-13")[0]; !from.Equal(want) {
		t.Fatalf("window start = %s, want %s", from, want)
	}
}

// TestDeriveWindowLatestValueStillNeedsOnePoint covers the zero-lookback case:
// a latest-value input has to have its newest point inside the window.
func TestDeriveWindowLatestValueStillNeedsOnePoint(t *testing.T) {
	spec := &factor.Spec{
		ID:      "latest",
		Version: "1.0.0",
		Inputs:  []factor.Input{{Name: "close", Dataset: "bar", Field: "close", Frequency: "daily"}},
	}
	view := &fakeView{asOf: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	if _, _, problem, err := deriveWindow(context.Background(), view, []domain.ID{"INST_A"}, spec, nil); err != nil {
		t.Fatalf("derive window: %v", err)
	} else if problem == nil {
		t.Fatal("a latest-value input with no data at all must be reported")
	}

	view.times = days(t, "2026-01-14")
	from, _, problem, err := deriveWindow(context.Background(), view, []domain.ID{"INST_A"}, spec, nil)
	if err != nil {
		t.Fatalf("derive window: %v", err)
	}
	if problem != nil {
		t.Fatalf("unexpected finding: %+v", problem)
	}
	if want := days(t, "2026-01-14")[0]; !from.Equal(want) {
		t.Fatalf("window start = %s, want %s", from, want)
	}
}

// TestDeriveWindowWithoutInputsIsEmpty documents that a factor with no declared
// inputs has no data dependency to derive a window from.
func TestDeriveWindowWithoutInputsIsEmpty(t *testing.T) {
	spec := &factor.Spec{ID: "constant", Version: "1.0.0"}
	view := &fakeView{asOf: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	from, dates, problem, err := deriveWindow(context.Background(), view, []domain.ID{"INST_A"}, spec, nil)
	if err != nil {
		t.Fatalf("derive window: %v", err)
	}
	if problem != nil || !from.IsZero() || dates != 0 {
		t.Fatalf("derived (%s, %d, %+v), want an empty window and no finding", from, dates, problem)
	}
}
