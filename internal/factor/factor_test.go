package factor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/store"
)

// factorAsOf is the decision time every test view is pinned at — far
// enough after the fixtures that staleness checks behave deterministically.
var factorAsOf = mustTime("2026-02-10T00:00:00Z")

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(fmt.Sprintf("parse time %q: %v", value, err))
	}
	return parsed
}

func timePtr(value time.Time) *time.Time { return &value }

func idPtr(value string) *domain.ID {
	id := domain.ID(value)
	return &id
}

func assertErr(t *testing.T, err error, code, contains string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != code {
		t.Fatalf("code = %q, want %q (err: %v)", domErr.Code, code, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}

// barRow builds one published daily bar observation with only the close
// field — the minimal price input momentum needs.
func barRow(inst, date, close string) domain.Observation {
	return domain.Observation{
		InstrumentID: idPtr(inst),
		Dataset:      "bar",
		EventTime:    mustTime(date + "T15:00:00Z"),
		Values: map[string]domain.Value{
			"close": {Kind: domain.ValueDecimal, Encoded: close},
		},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "bar-" + inst + "-" + date,
			RevisionID:     "rev-001",
			AvailableAt:    mustTime(date + "T15:30:00Z"),
			IngestedAt:     mustTime(date + "T15:30:00Z"),
			PublishedAt:    timePtr(mustTime(date + "T15:20:00Z")),
		},
	}
}

// valuationRow builds one valuation observation carrying an arbitrary
// field, for PE-style factors.
func valuationRow(inst, date, field, value string, published *time.Time) domain.Observation {
	return domain.Observation{
		InstrumentID: idPtr(inst),
		Dataset:      "valuation",
		EventTime:    mustTime(date + "T15:00:00Z"),
		Values: map[string]domain.Value{
			field: {Kind: domain.ValueDecimal, Encoded: value},
		},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "val-" + inst + "-" + date,
			RevisionID:     "rev-001",
			AvailableAt:    mustTime(date + "T16:00:00Z"),
			IngestedAt:     mustTime(date + "T16:00:00Z"),
			PublishedAt:    published,
		},
	}
}

// appendRows appends observations grouped by dataset (one batch each) and
// returns the batch ids for snapshot publishing.
func appendRows(t *testing.T, s *data.Store, obs []domain.Observation) []domain.ID {
	t.Helper()
	byDataset := make(map[string][]domain.Observation)
	order := []string{}
	for _, o := range obs {
		if _, ok := byDataset[o.Dataset]; !ok {
			order = append(order, o.Dataset)
		}
		byDataset[o.Dataset] = append(byDataset[o.Dataset], o)
	}
	ctx := context.Background()
	batchIDs := make([]domain.ID, 0, len(order))
	for _, dataset := range order {
		receipt, err := s.Append(ctx, ports.BatchInput{
			JobID:        "job-factor",
			Dataset:      dataset,
			Frequency:    "daily",
			Observations: byDataset[dataset],
		})
		if err != nil {
			t.Fatalf("append %s: %v", dataset, err)
		}
		batchIDs = append(batchIDs, receipt.BatchID)
	}
	return batchIDs
}

// newTestEngine builds the full stack: migrated store, observations
// published into one snapshot, a view pinned at factorAsOf, a registry
// with the default factors and a fresh cache.
func newTestEngine(t *testing.T, obs ...domain.Observation) (*Engine, *Registry, *Cache) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx, db, silentLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := data.New(db, ports.NewFixedClock(factorAsOf))
	batchIDs := appendRows(t, s, obs)
	snap, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-factor", BatchIDs: batchIDs})
	if err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	view, err := s.OpenView(ctx, snap.ID, factorAsOf)
	if err != nil {
		t.Fatalf("open view: %v", err)
	}
	registry, err := NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := RegisterDefaults(registry); err != nil {
		t.Fatalf("register defaults: %v", err)
	}
	cache := NewCache()
	engine, err := NewEngine(registry, view, cache)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return engine, registry, cache
}

// reqFor builds a well-formed run request pinned to the test fixtures.
func reqFor(ref FactorRef, members []domain.ID, params map[string]any) RunRequest {
	return RunRequest{
		Ref:                ref,
		Params:             params,
		Members:            members,
		Range:              domain.Interval{From: mustTime("2026-01-01T00:00:00Z"), To: factorAsOf},
		UniverseID:         "uni-1",
		UniverseHash:       "uh-1",
		SnapshotHash:       "sh-1",
		AvailabilityPolicy: "policy-1",
	}
}

func momentumRef() FactorRef { return FactorRef{ID: "momentum", Version: "1.0.0"} }
func reversalRef() FactorRef { return FactorRef{ID: "reversal", Version: "1.0.0"} }
func peRef() FactorRef       { return FactorRef{ID: "pe", Version: "1.0.0"} }

// registerPESpec registers a valuation-dependent PE factor — the AC-05
// contrast factor that needs the dataset price factors do not.
func registerPESpec(t *testing.T, registry *Registry) {
	t.Helper()
	err := registry.Register(&Spec{
		ID:      "pe",
		Version: "1.0.0",
		Title:   "PE (TTM)",
		Kind:    KindExpression,
		Inputs: []Input{{
			Name: "pe", Dataset: "valuation", Field: "pe_ttm",
			Frequency: "daily", Lookback: 1, Unit: "ratio", PIT: true,
		}},
		Expression:   &Expression{Kind: ExprInput, Name: "pe"},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
	})
	if err != nil {
		t.Fatalf("register pe spec: %v", err)
	}
}

// standardBars is the default price fixture: two members, three daily
// closes each — momentum n=2 yields exactly 0.2 for both.
func standardBars() []domain.Observation {
	return []domain.Observation{
		barRow("INST_A", "2026-01-05", "10"),
		barRow("INST_A", "2026-01-06", "11"),
		barRow("INST_A", "2026-01-07", "12"),
		barRow("INST_B", "2026-01-05", "20"),
		barRow("INST_B", "2026-01-06", "22"),
		barRow("INST_B", "2026-01-07", "24"),
	}
}

var stdMembers = []domain.ID{"INST_A", "INST_B"}

// hasProblem reports whether problems contains the code.
func hasProblem(problems []Problem, code string) bool {
	for _, p := range problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

func TestRegisterValidation(t *testing.T) {
	_, registry, _ := newTestEngine(t, standardBars()...)

	base := func() *Spec {
		spec := MomentumSpec()
		spec.ID = "test-momentum"
		return spec
	}

	cases := []struct {
		name     string
		spec     *Spec
		code     string
		contains string
	}{
		{
			name:     "duplicate registration",
			spec:     MomentumSpec(),
			code:     codeDuplicateFactor,
			contains: "already registered",
		},
		{
			name: "builtin without compute",
			spec: func() *Spec {
				s := base()
				s.Compute = nil
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "compute",
		},
		{
			name: "expression without ast",
			spec: func() *Spec {
				s := base()
				s.Kind = KindExpression
				s.Compute = nil
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "requires an expression",
		},
		{
			name: "input missing dataset",
			spec: func() *Spec {
				s := base()
				s.Inputs[0].Dataset = ""
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "dataset",
		},
		{
			name: "param default violates min",
			spec: func() *Spec {
				s := base()
				s.Params[0].Default = int64(0)
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "default",
		},
		{
			name: "expression references undeclared input",
			spec: func() *Spec {
				s := base()
				s.Kind = KindExpression
				s.Compute = nil
				s.Expression = &Expression{Kind: ExprInput, Name: "pe"}
				s.OutputUnit = "price"
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "undeclared input",
		},
		{
			name: "expression factor not in deps",
			spec: func() *Spec {
				s := base()
				s.Kind = KindExpression
				s.Compute = nil
				s.Deps = nil
				ref := momentumRef()
				s.Expression = &Expression{
					Kind: ExprUnary,
					Op:   "neg",
					Left: &Expression{Kind: ExprFactor, Ref: &ref},
				}
				s.OutputUnit = "ratio"
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "not declared in deps",
		},
		{
			name: "dep not registered",
			spec: func() *Spec {
				s := base()
				s.Deps = []FactorRef{{ID: "ghost", Version: "1.0.0"}}
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "not registered",
		},
		{
			name: "no asset classes",
			spec: func() *Spec {
				s := base()
				s.AssetClasses = nil
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "asset class",
		},
		{
			name: "unit mismatch",
			spec: func() *Spec {
				s := base()
				s.Kind = KindExpression
				s.Compute = nil
				s.Expression = &Expression{Kind: ExprInput, Name: "close"}
				s.OutputUnit = "ratio"
				return s
			}(),
			code:     codeSpecInvalid,
			contains: "unit",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertErr(t, registry.Register(c.spec), c.code, c.contains)
		})
	}

	// The registry is intact after all the rejected registrations.
	if _, err := registry.Lookup(reversalRef()); err != nil {
		t.Fatalf("lookup reversal after rejects: %v", err)
	}
}

func TestRegisterCycle(t *testing.T) {
	_, registry, _ := newTestEngine(t, standardBars()...)
	noop := func(*ComputeContext) (domain.Decimal, string, error) {
		return domain.Decimal(""), ReasonNotApplicable, nil
	}
	a := &Spec{
		ID: "cyc_a", Version: "1", Title: "a", Kind: KindBuiltin,
		OutputUnit: "ratio", AssetClasses: []string{"equity"}, Compute: noop,
		Deps: []FactorRef{{ID: "cyc_b", Version: "1"}},
	}
	b := &Spec{
		ID: "cyc_b", Version: "1", Title: "b", Kind: KindBuiltin,
		OutputUnit: "ratio", AssetClasses: []string{"equity"}, Compute: noop,
		Deps: []FactorRef{{ID: "cyc_a", Version: "1"}},
	}
	// The public API cannot build a cycle (deps must already be
	// registered), so seed the map directly to simulate a legacy loop and
	// prove ValidateGraph catches it.
	registry.specs[specKey("cyc_a", "1")] = a
	registry.specs[specKey("cyc_b", "1")] = b

	problems := registry.ValidateGraph(FactorRef{ID: "cyc_b", Version: "1"})
	if !hasProblem(problems, ProblemCycle) {
		t.Fatalf("expected a cycle problem, got %v", problems)
	}
}

// TestRunValuationFactorFailsPriceFactorRuns is AC-05: lacking the
// valuation data PE needs surfaces a field-dependency error, while the
// price factor runs independently on the same snapshot.
func TestRunValuationFactorFailsPriceFactorRuns(t *testing.T) {
	engine, registry, _ := newTestEngine(t, standardBars()...)
	registerPESpec(t, registry)
	ctx := context.Background()

	// Preflight reports the missing dataset once, naming valuation.
	problems, err := engine.Preflight(ctx, reqFor(peRef(), stdMembers, nil))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !hasProblem(problems, ProblemDatasetMissing) {
		t.Fatalf("expected dataset_missing, got %v", problems)
	}

	// Run fails closed with the problem list inlined.
	_, err = engine.Run(ctx, reqFor(peRef(), stdMembers, nil))
	assertErr(t, err, codePreflightFailed, "dataset_missing")
	assertErr(t, err, codePreflightFailed, "valuation")

	// The price factor runs on the same snapshot without the valuation
	// dataset (AC-05's positive half).
	frame, err := engine.Run(ctx, reqFor(momentumRef(), stdMembers, map[string]any{"n": 2}))
	if err != nil {
		t.Fatalf("momentum run: %v", err)
	}
	if got := frame.Values["INST_A"]; got != domain.Decimal("0.2") {
		t.Fatalf("momentum INST_A = %q, want 0.2", got)
	}
	if len(frame.Missing) != 0 {
		t.Fatalf("momentum missing = %v, want none", frame.Missing)
	}
}

func TestPreflightFieldMissing(t *testing.T) {
	obs := append(standardBars(),
		valuationRow("INST_A", "2026-01-07", "pe_annual", "15.5", timePtr(mustTime("2026-01-07T16:10:00Z"))),
	)
	engine, registry, _ := newTestEngine(t, obs...)
	registerPESpec(t, registry)

	problems, err := engine.Preflight(context.Background(), reqFor(peRef(), stdMembers, nil))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !hasProblem(problems, ProblemFieldMissing) {
		t.Fatalf("expected field_missing, got %v", problems)
	}
	if hasProblem(problems, ProblemDatasetMissing) {
		t.Fatal("valuation rows exist; dataset_missing would be wrong")
	}
}

func TestPreflightPITUnverified(t *testing.T) {
	obs := append(standardBars(),
		// No published_at: strict PIT consumers must not trust the row.
		valuationRow("INST_A", "2026-01-07", "pe_ttm", "15.5", nil),
	)
	engine, registry, _ := newTestEngine(t, obs...)
	registerPESpec(t, registry)
	ctx := context.Background()

	problems, err := engine.Preflight(ctx, reqFor(peRef(), stdMembers, nil))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !hasProblem(problems, ProblemPITUnverified) {
		t.Fatalf("expected pit_unverified, got %v", problems)
	}
	_, err = engine.Run(ctx, reqFor(peRef(), stdMembers, nil))
	assertErr(t, err, codePreflightFailed, "pit_unverified")
}

func TestPreflightInsufficientHistory(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	// n=5 needs 6 points; the fixtures carry 3.
	problems, err := engine.Preflight(context.Background(), reqFor(momentumRef(), stdMembers, map[string]any{"n": 5}))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !hasProblem(problems, ProblemInsufficientHistory) {
		t.Fatalf("expected insufficient_history, got %v", problems)
	}
	if !strings.Contains(problems[0].Message, "INST_A") {
		t.Fatalf("problem should name the member: %v", problems)
	}
	_, err = engine.Run(context.Background(), reqFor(momentumRef(), stdMembers, map[string]any{"n": 5}))
	assertErr(t, err, codePreflightFailed, "insufficient_history")
}

func TestPreflightAllProblemsAtOnce(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	// Every member — including one with no data at all — is short at once;
	// the whole list comes back in one round.
	members := []domain.ID{"INST_A", "INST_B", "INST_C"}
	problems, err := engine.Preflight(context.Background(), reqFor(momentumRef(), members, map[string]any{"n": 50}))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("want one aggregated problem, got %v", problems)
	}
	for _, member := range members {
		if !strings.Contains(problems[0].Message, string(member)) {
			t.Fatalf("problem should name %s: %v", member, problems)
		}
	}
}

func TestPreflightUniverseEmpty(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	ctx := context.Background()
	problems, err := engine.Preflight(ctx, reqFor(momentumRef(), nil, map[string]any{"n": 2}))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !hasProblem(problems, ProblemUniverseEmpty) {
		t.Fatalf("expected universe_empty, got %v", problems)
	}
	_, err = engine.Run(ctx, reqFor(momentumRef(), nil, map[string]any{"n": 2}))
	assertErr(t, err, codePreflightFailed, "universe_empty")
}

func TestPreflightFrequencyUnsupported(t *testing.T) {
	engine, registry, _ := newTestEngine(t, standardBars()...)
	err := registry.Register(&Spec{
		ID: "weekly_close", Version: "1.0.0", Title: "Weekly close",
		Kind: KindExpression,
		Inputs: []Input{{
			Name: "close", Dataset: "bar", Field: "close",
			Frequency: "weekly", Lookback: 1, Unit: "price",
		}},
		Expression:   &Expression{Kind: ExprInput, Name: "close"},
		OutputUnit:   "price",
		AssetClasses: []string{"equity"},
	})
	if err != nil {
		t.Fatalf("register weekly spec: %v", err)
	}
	problems, err := engine.Preflight(context.Background(), reqFor(FactorRef{ID: "weekly_close", Version: "1.0.0"}, stdMembers, nil))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !hasProblem(problems, ProblemFrequencyUnsupported) {
		t.Fatalf("expected frequency_unsupported, got %v", problems)
	}
}

func TestRunMomentumAndReversal(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	ctx := context.Background()

	frame, err := engine.Run(ctx, reqFor(momentumRef(), stdMembers, map[string]any{"n": 2}))
	if err != nil {
		t.Fatalf("momentum run: %v", err)
	}
	if !frame.AsOf.Equal(factorAsOf) {
		t.Fatalf("frame as_of = %v, want %v", frame.AsOf, factorAsOf)
	}
	if got := frame.Values["INST_A"]; got != domain.Decimal("0.2") {
		t.Fatalf("momentum INST_A = %q, want 0.2", got)
	}
	if got := frame.Values["INST_B"]; got != domain.Decimal("0.2") {
		t.Fatalf("momentum INST_B = %q, want 0.2", got)
	}
	if len(frame.Missing) != 0 {
		t.Fatalf("momentum missing = %v, want none", frame.Missing)
	}

	// Reversal composes momentum; the n parameter passes through by name.
	reversal, err := engine.Run(ctx, reqFor(reversalRef(), stdMembers, map[string]any{"n": 2}))
	if err != nil {
		t.Fatalf("reversal run: %v", err)
	}
	if got := reversal.Values["INST_A"]; got != domain.Decimal("-0.2") {
		t.Fatalf("reversal INST_A = %q, want -0.2", got)
	}
	if got := reversal.Values["INST_B"]; got != domain.Decimal("-0.2") {
		t.Fatalf("reversal INST_B = %q, want -0.2", got)
	}
}

func TestRunParamInvalid(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	ctx := context.Background()

	_, err := engine.Run(ctx, reqFor(momentumRef(), stdMembers, map[string]any{"bogus": 1}))
	assertErr(t, err, codePreflightFailed, "unknown parameter")

	_, err = engine.Run(ctx, reqFor(momentumRef(), stdMembers, map[string]any{"n": "abc"}))
	assertErr(t, err, codePreflightFailed, "must be a integer")
}

func TestRunRequestValidation(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	ctx := context.Background()
	req := reqFor(momentumRef(), stdMembers, map[string]any{"n": 2})

	bad := req
	bad.UniverseID = ""
	_, err := engine.Run(ctx, bad)
	assertErr(t, err, codeRequestInvalid, "universe_id")

	bad = req
	bad.Range = domain.Interval{From: mustTime("2026-01-01T00:00:00Z"), To: mustTime("2026-02-09T00:00:00Z")}
	_, err = engine.Run(ctx, bad)
	assertErr(t, err, codeRequestInvalid, "as_of")

	bad = req
	bad.Range = domain.Interval{From: mustTime("2026-02-10T00:00:00Z"), To: mustTime("2026-01-01T00:00:00Z")}
	_, err = engine.Run(ctx, bad)
	assertErr(t, err, codeRequestInvalid, "range")

	_, err = engine.Run(ctx, reqFor(FactorRef{ID: "ghost", Version: "1"}, stdMembers, nil))
	assertErr(t, err, codeNotRegistered, "not registered")
}

func TestRunMissingInput(t *testing.T) {
	engine, registry, _ := newTestEngine(t, standardBars()...)
	// Zero lookback: preflight does not demand history, so a member with
	// no rows at all becomes a per-member missing_input instead of a
	// failed preflight.
	err := registry.Register(&Spec{
		ID: "last_close", Version: "1.0.0", Title: "Latest close",
		Kind: KindExpression,
		Inputs: []Input{{
			Name: "close", Dataset: "bar", Field: "close",
			Frequency: "daily", Lookback: 0, Unit: "price",
		}},
		Expression:   &Expression{Kind: ExprInput, Name: "close"},
		OutputUnit:   "price",
		AssetClasses: []string{"equity"},
	})
	if err != nil {
		t.Fatalf("register last_close: %v", err)
	}

	frame, err := engine.Run(context.Background(), reqFor(FactorRef{ID: "last_close", Version: "1.0.0"}, []domain.ID{"INST_A", "INST_C"}, nil))
	if err != nil {
		t.Fatalf("last_close run: %v", err)
	}
	if got := frame.Values["INST_A"]; got != domain.Decimal("12") {
		t.Fatalf("last_close INST_A = %q, want 12", got)
	}
	if got := frame.Missing["INST_C"]; got != ReasonMissingInput {
		t.Fatalf("last_close INST_C missing = %q, want %q", got, ReasonMissingInput)
	}
}

func TestRunStalenessInput(t *testing.T) {
	engine, registry, _ := newTestEngine(t, standardBars()...)
	// The fixtures end 2026-01-07 while the view is pinned at 2026-02-10:
	// a 24h staleness bound must reject every member.
	err := registry.Register(&Spec{
		ID: "stale_close", Version: "1.0.0", Title: "Fresh close",
		Kind: KindExpression,
		Inputs: []Input{{
			Name: "close", Dataset: "bar", Field: "close",
			Frequency: "daily", Lookback: 1, Unit: "price",
			Staleness: 24 * time.Hour,
		}},
		Expression:   &Expression{Kind: ExprInput, Name: "close"},
		OutputUnit:   "price",
		AssetClasses: []string{"equity"},
	})
	if err != nil {
		t.Fatalf("register stale_close: %v", err)
	}

	frame, err := engine.Run(context.Background(), reqFor(FactorRef{ID: "stale_close", Version: "1.0.0"}, stdMembers, nil))
	if err != nil {
		t.Fatalf("stale_close run: %v", err)
	}
	if len(frame.Values) != 0 {
		t.Fatalf("stale values = %v, want none", frame.Values)
	}
	for _, m := range stdMembers {
		if got := frame.Missing[m]; got != ReasonStaleInput {
			t.Fatalf("stale_close %s missing = %q, want %q", m, got, ReasonStaleInput)
		}
	}
}

func TestRunInvalidDenominator(t *testing.T) {
	obs := []domain.Observation{
		barRow("INST_A", "2026-01-05", "0"), // zero base: 5/0
		barRow("INST_A", "2026-01-06", "5"),
		barRow("INST_B", "2026-01-05", "20"),
		barRow("INST_B", "2026-01-06", "22"),
	}
	engine, _, _ := newTestEngine(t, obs...)

	frame, err := engine.Run(context.Background(), reqFor(momentumRef(), stdMembers, map[string]any{"n": 1}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := frame.Missing["INST_A"]; got != ReasonInvalidDenominator {
		t.Fatalf("INST_A missing = %q, want %q", got, ReasonInvalidDenominator)
	}
	if got := frame.Values["INST_B"]; got != domain.Decimal("0.1") {
		t.Fatalf("INST_B = %q, want 0.1", got)
	}
}

func TestCacheKey(t *testing.T) {
	_, registry, _ := newTestEngine(t, standardBars()...)

	base := CacheKeyRequest{
		SnapshotHash:       "sh-1",
		UniverseID:         "uni-1",
		UniverseHash:       "uh-1",
		Ref:                momentumRef(),
		Params:             map[string]any{"n": 2},
		Range:              domain.Interval{From: mustTime("2026-01-01T00:00:00Z"), To: factorAsOf},
		AvailabilityPolicy: "policy-1",
	}
	key, err := registry.CacheKey(base)
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	again, err := registry.CacheKey(base)
	if err != nil {
		t.Fatalf("cache key again: %v", err)
	}
	if key != again {
		t.Fatal("identical requests must produce identical keys")
	}

	// Explicitly supplying the default equals omitting the parameter.
	nilParams := base
	nilParams.Params = nil
	withDefault, err := registry.CacheKey(withParams(base, map[string]any{"n": 20}))
	if err != nil {
		t.Fatalf("cache key default: %v", err)
	}
	nilKey, err := registry.CacheKey(nilParams)
	if err != nil {
		t.Fatalf("cache key nil params: %v", err)
	}
	if nilKey != withDefault {
		t.Fatal("explicit default and omitted parameter must hash identically")
	}

	// Every field of the request changes the key.
	clone := func(mutate func(*CacheKeyRequest)) CacheKeyRequest {
		v := base
		mutate(&v)
		return v
	}
	variants := []CacheKeyRequest{
		clone(func(v *CacheKeyRequest) { v.SnapshotHash = "sh-2" }),
		clone(func(v *CacheKeyRequest) { v.UniverseID = "uni-2" }),
		clone(func(v *CacheKeyRequest) { v.UniverseHash = "uh-2" }),
		clone(func(v *CacheKeyRequest) { v.Ref = reversalRef() }),
		clone(func(v *CacheKeyRequest) { v.Params = map[string]any{"n": 3} }),
		clone(func(v *CacheKeyRequest) { v.Range.To = mustTime("2026-02-11T00:00:00Z") }),
		clone(func(v *CacheKeyRequest) { v.AvailabilityPolicy = "policy-2" }),
	}
	for i, v := range variants {
		got, err := registry.CacheKey(v)
		if err != nil {
			t.Fatalf("variant %d: %v", i, err)
		}
		if got == key {
			t.Fatalf("variant %d must change the key", i)
		}
	}

	// Parameter key order must not matter (json.Marshal sorts map keys).
	twoParamRef := FactorRef{ID: "twoparam", Version: "1.0.0"}
	err = registry.Register(&Spec{
		ID: "twoparam", Version: "1.0.0", Title: "two params", Kind: KindBuiltin,
		Params: []Param{
			{Name: "x", Type: ParamInteger, Default: int64(1)},
			{Name: "y", Type: ParamInteger, Default: int64(2)},
		},
		Inputs: []Input{{
			Name: "close", Dataset: "bar", Field: "close",
			Frequency: "daily", Lookback: 1, Unit: "price",
		}},
		OutputUnit:   "price",
		AssetClasses: []string{"equity"},
		Compute: func(*ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal(""), ReasonNotApplicable, nil
		},
	})
	if err != nil {
		t.Fatalf("register twoparam: %v", err)
	}
	abKey, err := registry.CacheKey(clone(func(v *CacheKeyRequest) {
		v.Ref = twoParamRef
		v.Params = map[string]any{"x": 2, "y": 3}
	}))
	if err != nil {
		t.Fatalf("cache key ab: %v", err)
	}
	baKey, err := registry.CacheKey(clone(func(v *CacheKeyRequest) {
		v.Ref = twoParamRef
		v.Params = map[string]any{"y": 3, "x": 2}
	}))
	if err != nil {
		t.Fatalf("cache key ba: %v", err)
	}
	if abKey != baKey {
		t.Fatal("parameter key order must not affect the key")
	}

	// Missing pinned inputs are rejected.
	_, err = registry.CacheKey(clone(func(v *CacheKeyRequest) { v.UniverseHash = "" }))
	assertErr(t, err, codeRequestInvalid, "universe_hash")
}

func withParams(base CacheKeyRequest, params map[string]any) CacheKeyRequest {
	base.Params = params
	return base
}

func TestCacheLifecycle(t *testing.T) {
	cache := NewCache()
	frame := &Frame{
		Ref:    momentumRef(),
		AsOf:   factorAsOf,
		Values: map[domain.ID]domain.Decimal{"INST_A": "1"},
	}

	cache.Stage("key", frame)
	if _, ok := cache.Get("key"); ok {
		t.Fatal("staged frame must be invisible to Get")
	}
	if err := cache.Publish("key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, ok := cache.Get("key")
	if !ok || got != frame {
		t.Fatalf("Get after publish = %v %v, want the staged frame", got, ok)
	}

	// Publishing without a staged entry is a state error.
	err := cache.Publish("missing")
	if err == nil {
		t.Fatal("publish without stage should fail")
	}
	assertErr(t, err, codeCacheState, "no staged frame")

	// Staging again over a published key replaces it after publish.
	replacement := &Frame{
		Ref:    momentumRef(),
		AsOf:   factorAsOf,
		Values: map[domain.ID]domain.Decimal{"INST_A": "2"},
	}
	cache.Stage("key", replacement)
	if err := cache.Publish("key"); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if got, _ := cache.Get("key"); got != replacement {
		t.Fatal("republish must replace the published frame")
	}

	// Abort discards a staged entry — a cancelled run leaves no hit.
	cache.Stage("aborted", frame)
	cache.Abort("aborted")
	if err := cache.Publish("aborted"); err == nil {
		t.Fatal("publish after abort should fail")
	}
	if _, ok := cache.Get("aborted"); ok {
		t.Fatal("aborted frame must not be visible")
	}
}

func TestCanonicalParams(t *testing.T) {
	registry, err := NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterDefaults(registry); err != nil {
		t.Fatal(err)
	}

	// Omitted optional parameters fall back to the declared default.
	params, err := registry.CanonicalParams(momentumRef(), nil)
	if err != nil {
		t.Fatalf("canonical params: %v", err)
	}
	if got := params["n"]; got != int64(20) {
		t.Fatalf("default n = %v (%T), want int64(20)", got, got)
	}

	// JSON-decoded integral floats coerce to the canonical int64.
	params, err = registry.CanonicalParams(momentumRef(), map[string]any{"n": float64(5)})
	if err != nil {
		t.Fatalf("canonical params: %v", err)
	}
	if got := params["n"]; got != int64(5) {
		t.Fatalf("n = %v (%T), want int64(5)", got, got)
	}

	// Violations fail closed with the factor request code; every problem
	// is reported in one round — the contract analysis relies on when
	// freezing an artifact's parameters.
	_, err = registry.CanonicalParams(momentumRef(), map[string]any{"n": "soon", "ghost": 1})
	assertErr(t, err, codeRequestInvalid, "must be a integer")
	_, err = registry.CanonicalParams(momentumRef(), map[string]any{"n": 0})
	assertErr(t, err, codeRequestInvalid, "below minimum")

	// Unknown refs pass the factor error through untouched.
	_, err = registry.CanonicalParams(FactorRef{ID: "ghost", Version: "1.0.0"}, nil)
	assertErr(t, err, codeNotRegistered, "not registered")
}

func TestEngineCacheRoundTrip(t *testing.T) {
	engine, registry, cache := newTestEngine(t, standardBars()...)
	ctx := context.Background()
	req := reqFor(momentumRef(), stdMembers, map[string]any{"n": 2})

	first, err := engine.Run(ctx, req)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := engine.Run(ctx, req)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first != second {
		t.Fatal("identical requests must return the cached frame pointer")
	}

	// Sentry: pre-publish a frame under the exact key the next run will
	// compute — Run must hit it instead of recomputing.
	sentryReq := reqFor(momentumRef(), stdMembers, map[string]any{"n": 2})
	sentryReq.SnapshotHash = "sh-sentry"
	sentryKey, err := registry.CacheKey(CacheKeyRequest{
		SnapshotHash:       sentryReq.SnapshotHash,
		UniverseID:         sentryReq.UniverseID,
		UniverseHash:       sentryReq.UniverseHash,
		Ref:                sentryReq.Ref,
		Params:             sentryReq.Params,
		Range:              sentryReq.Range,
		AvailabilityPolicy: sentryReq.AvailabilityPolicy,
	})
	if err != nil {
		t.Fatalf("sentry key: %v", err)
	}
	sentry := &Frame{
		Ref:    momentumRef(),
		AsOf:   factorAsOf,
		Values: map[domain.ID]domain.Decimal{"INST_A": "999"},
	}
	cache.Stage(sentryKey, sentry)
	if err := cache.Publish(sentryKey); err != nil {
		t.Fatalf("publish sentry: %v", err)
	}

	got, err := engine.Run(ctx, sentryReq)
	if err != nil {
		t.Fatalf("sentry run: %v", err)
	}
	if got != sentry {
		t.Fatalf("run must hit the pre-published frame, got %v", got)
	}
}
