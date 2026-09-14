package screenrun

import (
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

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
		Params: []factor.Param{{
			Name: "n", Type: factor.ParamInteger, Required: true,
		}},
		Inputs: []factor.Input{{
			Name: "close", Dataset: "bar", Field: "close", Frequency: "daily",
			Lookback: 1, Unit: "cny", PIT: true,
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

func mixedDefinition() screening.Definition {
	return screening.Definition{
		Name: "mixed",
		InputBindings: []screening.InputBinding{
			{BindingID: "px", Kind: screening.BindingField, Dataset: "bar", Field: "close"},
			{BindingID: "mom", Kind: screening.BindingFactor, FactorRef: domain.VersionRef{ID: "momentum", Version: "1.0.0"}, Params: screening.Params{"n": 20}},
		},
		ConditionTree: screening.Compare{
			NodeID:   "gt",
			Input:    screening.Input{BindingID: "px"},
			Operator: screening.OpGt,
			Value:    domain.Value{Kind: domain.ValueDecimal, Encoded: "10"},
		},
		Ranking:   screening.Ranking{Mode: screening.RankingSort, Fields: []screening.RankField{{Input: screening.Input{BindingID: "px"}, Direction: screening.DirectionDesc}}},
		Selection: screening.Selection{Mode: screening.SelectionAll},
	}
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

// TestCoverageResolvesFactorsAndReportsFieldGaps pins today's honest split:
// a registered factor binding with canonical parameters is available, a field
// binding is not resolvable yet, and the verdict still passes because the
// field gap is reported as a warning rather than a silent assumption.
func TestCoverageResolvesFactorsAndReportsFieldGaps(t *testing.T) {
	svc := &Service{registry: testRegistry(t)}
	var coverage []Coverage
	issues := svc.coverage(mixedDefinition(), &coverage)

	byBinding := map[domain.ID]Coverage{}
	for _, c := range coverage {
		byBinding[c.BindingID] = c
	}
	if len(coverage) != 2 {
		t.Fatalf("coverage = %+v, want one entry per binding", coverage)
	}
	if got := byBinding["mom"]; !got.Available || got.Reason != nil {
		t.Fatalf("factor binding coverage = %+v, want available with no reason", got)
	}
	field := byBinding["px"]
	if field.Available {
		t.Fatal("a field binding must not be reported as available while no input catalog exists")
	}
	if field.Reason == nil || *field.Reason != codeCatalogUnavailable {
		t.Fatalf("field binding reason = %v, want %q", field.Reason, codeCatalogUnavailable)
	}

	// The gap is a warning: nothing about the request is wrong, it just cannot
	// be checked yet — so the run stays preflightable.
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			t.Fatalf("unexpected error-severity issue: %+v", issue)
		}
	}
	if !hasCode(issues, codeCatalogUnavailable) || !hasCode(issues, codeDataAvailabilityUnchecked) {
		t.Fatalf("issues = %+v, want both the catalog gap and the availability caveat", issues)
	}
}

func TestCoverageFailsClosedOnUnknownFactor(t *testing.T) {
	svc := &Service{registry: testRegistry(t)}
	def := mixedDefinition()
	def.InputBindings[1].FactorRef = domain.VersionRef{ID: "momentum", Version: "9.9.9"}
	var coverage []Coverage
	issues := svc.coverage(def, &coverage)

	var found *domain.Issue
	for i := range issues {
		if issues[i].Severity == domain.SeverityError {
			found = &issues[i]
			break
		}
	}
	if found == nil {
		t.Fatal("an unknown factor version must be an error, not a warning")
	}
	if found.Code != "factor.not_registered" {
		t.Fatalf("issue code = %q, want factor.not_registered", found.Code)
	}
	// The per-binding reason and the issue must agree, so a caller reading only
	// coverage still sees the right class.
	for _, c := range coverage {
		if c.BindingID == "mom" {
			if c.Available || c.Reason == nil || *c.Reason != found.Code {
				t.Fatalf("coverage = %+v, want unavailable with reason %q", c, found.Code)
			}
		}
	}
}

func TestCoverageFailsClosedOnInvalidFactorParams(t *testing.T) {
	svc := &Service{registry: testRegistry(t)}
	def := mixedDefinition()
	def.InputBindings[1].Params = nil // n is required
	var coverage []Coverage
	issues := svc.coverage(def, &coverage)

	if !hasError(issues) {
		t.Fatalf("missing required parameters must be an error: %+v", issues)
	}
	for _, c := range coverage {
		if c.BindingID == "mom" && c.Available {
			t.Fatal("a binding with invalid parameters must not be available")
		}
	}
}

func hasCode(issues []domain.Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
