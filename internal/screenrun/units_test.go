package screenrun

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
	"github.com/injoyai/strategy/internal/store"
)

// SC-AC-01's unit half: a factor declares the unit each of its inputs carries,
// and the field it is bound to declares the unit its values are stored in.
// Preflight compares them by equality — the same rule the expression unit
// checker applies — and blocks a mismatch or an undeclared unit instead of
// assuming the values are compatible.

// unitSpec is a factor reading one bar field in one unit.
func unitSpec(unit string) *factor.Spec {
	return &factor.Spec{
		ID:      "unit-probe",
		Version: "1.0.0",
		Title:   "Unit probe",
		Kind:    factor.KindBuiltin,
		Inputs: []factor.Input{{
			Name: "close", Dataset: "bar", Field: "close", Frequency: "daily",
			Lookback: 1, Unit: unit, PIT: true,
		}},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal("1"), "", nil
		},
	}
}

// catalogStore seeds one bar batch with the given declaration (nil declares
// nothing, which is how a field ends up with no unit at all).
func catalogStore(t *testing.T, declaration *domain.DatasetDeclaration) *data.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, discardLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dataStore := data.New(db, ports.NewFixedClock(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)))
	at := time.Date(2026, 1, 5, 15, 30, 0, 0, time.UTC)
	if _, err := dataStore.Append(ctx, ports.BatchInput{
		JobID:        "job-1",
		Dataset:      "bar",
		Frequency:    "daily",
		Observations: []domain.Observation{barObservation("INST_A", "10.40", at)},
		Declaration:  declaration,
	}); err != nil {
		t.Fatalf("append batch: %v", err)
	}
	return dataStore
}

func declaredUnits(unit string) *domain.DatasetDeclaration {
	return &domain.DatasetDeclaration{
		Frequency:             "daily",
		AvailabilityPolicyRef: domain.VersionRef{ID: "availability", Version: "v1"},
		Fields:                []domain.DatasetField{{Name: "close", Unit: unit}},
	}
}

func unitService(t *testing.T, store *data.Store) *Service {
	t.Helper()
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	service, err := New(store, registry, factor.NewCache())
	if err != nil {
		t.Fatalf("build service: %v", err)
	}
	return service
}

func TestFactorInputUnitsMustBeDeclaredAndMatch(t *testing.T) {
	cases := []struct {
		name        string
		declaration *domain.DatasetDeclaration
		spec        *factor.Spec
		wantCode    string
	}{
		{
			name:        "declared unit equals the factor's contract",
			declaration: declaredUnits("price"),
			spec:        unitSpec("price"),
		},
		{
			name:        "declared unit differs from the factor's contract",
			declaration: declaredUnits("shares"),
			spec:        unitSpec("price"),
			wantCode:    codeUnitMismatch,
		},
		{
			name:        "the dataset never declared the field's unit",
			declaration: nil,
			spec:        unitSpec("price"),
			wantCode:    codeUnitUndeclared,
		},
		{
			name:        "the dataset exists but has no such field",
			declaration: declaredUnits("price"),
			spec: &factor.Spec{
				ID: "unit-probe", Version: "1.0.0", Title: "Unit probe", Kind: factor.KindBuiltin,
				Inputs: []factor.Input{{
					Name: "close", Dataset: "bar", Field: "turnover", Frequency: "daily",
					Lookback: 1, Unit: "price", PIT: true,
				}},
				OutputUnit: "ratio", AssetClasses: []string{"equity"},
				Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) { return domain.Decimal("1"), "", nil },
			},
			wantCode: codeFieldUnknown,
		},
		{
			name:        "the dataset does not exist",
			declaration: declaredUnits("price"),
			spec: &factor.Spec{
				ID: "unit-probe", Version: "1.0.0", Title: "Unit probe", Kind: factor.KindBuiltin,
				Inputs: []factor.Input{{
					Name: "close", Dataset: "quotes", Field: "close", Frequency: "daily",
					Lookback: 1, Unit: "price", PIT: true,
				}},
				OutputUnit: "ratio", AssetClasses: []string{"equity"},
				Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) { return domain.Decimal("1"), "", nil },
			},
			wantCode: codeDatasetUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := unitService(t, catalogStore(t, tc.declaration))
			issues, err := service.checkFactorInputUnits(context.Background(), "mom", tc.spec)
			if err != nil {
				t.Fatalf("check units: %v", err)
			}
			if tc.wantCode == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %+v, want none", issues)
				}
				return
			}
			if len(issues) != 1 {
				t.Fatalf("issues = %+v, want exactly one", issues)
			}
			if issues[0].Code != tc.wantCode || issues[0].Severity != domain.SeverityError {
				t.Fatalf("issue = %+v, want an error-severity %s", issues[0], tc.wantCode)
			}
			// The finding has to point at the binding a caller can fix.
			if issues[0].Path != "input_bindings[mom]" {
				t.Fatalf("path = %q, want the bound input", issues[0].Path)
			}
		})
	}
}

// TestPreflightBlocksAMismatchedUnit proves the check is wired into the run
// path: the finding makes the run invalid with the binding reported unavailable,
// so submission refuses to queue a run that would compute over wrongly typed
// values.
func TestPreflightBlocksAMismatchedUnit(t *testing.T) {
	stack := newExecuteStack(t, unitSpec("shares"))
	stack.saveScreener(t, screening.Definition{
		Name:          "unit-checked",
		InputBindings: []screening.InputBinding{{BindingID: "mom", Kind: screening.BindingFactor, FactorRef: domain.VersionRef{ID: "unit-probe", Version: "1.0.0"}}},
		ConditionTree: screening.Missing{NodeID: "present", Input: screening.Input{BindingID: "mom"}, IsPresent: true},
		Ranking:       screening.Ranking{Mode: screening.RankingSort, Fields: []screening.RankField{{Input: screening.Input{BindingID: "mom"}, Direction: screening.DirectionDesc}}},
		Selection:     screening.Selection{Mode: screening.SelectionAll},
	})
	version := stack.latestScreenerVersion(t)
	stack.request.ScreenerRef = domain.VersionRef{ID: version.ID, Version: string(version.Version)}

	preflight, err := stack.service.Preflight(context.Background(), stack.request)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if preflight.Valid {
		t.Fatalf("preflight = %+v, want invalid: bar/close is declared in price, not shares", preflight)
	}
	var found bool
	for _, coverage := range preflight.Coverage {
		if coverage.BindingID == "mom" {
			if coverage.Available || coverage.Reason == nil || *coverage.Reason != codeUnitMismatch {
				t.Fatalf("coverage = %+v, want unavailable with reason %s", coverage, codeUnitMismatch)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("coverage = %+v, want the factor binding reported", preflight.Coverage)
	}
	if !strings.Contains(SummarizeIssues(preflight.Issues), "price") {
		t.Fatalf("issues = %+v, want the declared unit named", preflight.Issues)
	}
	// And the run itself refuses to compute under the same finding.
	if _, err := stack.service.Execute(context.Background(), stack.request); domain.ErrorCode(err) != CodePreflightFailed {
		t.Fatalf("execute error = %v, want %s", err, CodePreflightFailed)
	}
}

// TestPreflightAcceptsAMatchingUnit is the other half of the same boundary: the
// check must not block a correctly declared dataset.
func TestPreflightAcceptsAMatchingUnit(t *testing.T) {
	stack := newExecuteStack(t, unitSpec("price"))
	stack.saveScreener(t, screening.Definition{
		Name:          "unit-checked-ok",
		InputBindings: []screening.InputBinding{{BindingID: "mom", Kind: screening.BindingFactor, FactorRef: domain.VersionRef{ID: "unit-probe", Version: "1.0.0"}}},
		ConditionTree: screening.Missing{NodeID: "present", Input: screening.Input{BindingID: "mom"}, IsPresent: true},
		Ranking:       screening.Ranking{Mode: screening.RankingSort, Fields: []screening.RankField{{Input: screening.Input{BindingID: "mom"}, Direction: screening.DirectionDesc}}},
		Selection:     screening.Selection{Mode: screening.SelectionAll},
	})
	version := stack.latestScreenerVersion(t)
	stack.request.ScreenerRef = domain.VersionRef{ID: version.ID, Version: string(version.Version)}

	preflight, err := stack.service.Preflight(context.Background(), stack.request)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !preflight.Valid {
		t.Fatalf("preflight = %+v, want valid", preflight)
	}
}

// TestFactorBindingWithNoInputsSkipsTheUnitCheck keeps the check honest about
// what it can verify: a factor that reads no dataset has no unit contract to
// compare, so preflight must not invent a finding for it.
func TestFactorBindingWithNoInputsSkipsTheUnitCheck(t *testing.T) {
	service := unitService(t, catalogStore(t, nil))
	spec := &factor.Spec{
		ID: "constant", Version: "1.0.0", Title: "Constant", Kind: factor.KindBuiltin,
		OutputUnit: "ratio", AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) { return domain.Decimal("1"), "", nil },
	}
	issues, err := service.checkFactorInputUnits(context.Background(), "const", spec)
	if err != nil {
		t.Fatalf("check units: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none for a factor that declares no inputs", issues)
	}
}
