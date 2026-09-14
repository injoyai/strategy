package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/analysis"
	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/store"
)

// researchBase is day 0 of the fixtures; bars sit at 15:00 each calendar
// day and become available at 15:30, published at 15:20 — the M1-07
// provenance shape.
var researchBase = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustDay(offset int) time.Time { return researchBase.AddDate(0, 0, offset) }

func timePtr(v time.Time) *time.Time { return &v }

func idPtr(v string) *domain.ID {
	id := domain.ID(v)
	return &id
}

// barRow builds one published daily bar with only the close field.
func barRow(inst string, offset int, close string) domain.Observation {
	event := mustDay(offset).Add(15 * time.Hour)
	return domain.Observation{
		InstrumentID: idPtr(inst),
		Dataset:      "bar",
		EventTime:    event,
		Values: map[string]domain.Value{
			"close": {Kind: domain.ValueDecimal, Encoded: close},
		},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: fmt.Sprintf("bar-%s-%03d", inst, offset),
			RevisionID:     "rev-001",
			AvailableAt:    event.Add(30 * time.Minute),
			IngestedAt:     event.Add(30 * time.Minute),
			PublishedAt:    timePtr(event.Add(-10 * time.Minute)),
		},
	}
}

// standardBars: 40 calendar days of closes for three instruments with
// distinct dynamics (linear, wiggled linear, geometric) — enough history
// for momentum's default n=20 plus warmup, and cross-sectionally
// non-proportional prices so factor values actually vary.
func standardBars() []domain.Observation {
	obs := make([]domain.Observation, 0, 120)
	for i := 0; i < 40; i++ {
		obs = append(obs,
			barRow("INST_A", i, strconv.Itoa(100+i)),
			barRow("INST_B", i, strconv.Itoa(200+3*i+i%7)),
			barRow("INST_C", i, fmt.Sprintf("%.2f", 60*math.Pow(1.02, float64(i)))),
		)
	}
	return obs
}

type testStack struct {
	service   *Service
	data      *data.Store
	artifacts *artifacts.Store
	registry  *factor.Registry
}

func newTestStack(t *testing.T) *testStack {
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
	ds := data.New(db, ports.NewFixedClock(researchBase))
	art := artifacts.New(t.TempDir(), db)
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := factor.RegisterDefaults(registry); err != nil {
		t.Fatalf("register defaults: %v", err)
	}
	svc, err := New(ds, registry, factor.NewCache(), art, ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &testStack{service: svc, data: ds, artifacts: art, registry: registry}
}

// publish appends observations as one batch and freezes them into a
// snapshot.
func (s *testStack) publish(t *testing.T, obs []domain.Observation) domain.Snapshot {
	t.Helper()
	ctx := context.Background()
	receipt, err := s.data.Append(ctx, ports.BatchInput{
		JobID:        "job-research",
		Dataset:      obs[0].Dataset,
		Frequency:    "daily",
		Observations: obs,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	snap, err := s.data.PublishSnapshot(ctx, domain.SnapshotRequest{
		Name:     "snap-research",
		BatchIDs: []domain.ID{receipt.BatchID},
	})
	if err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	return snap
}

func (s *testStack) staticUniverse(t *testing.T, snap domain.Snapshot, members ...string) domain.UniverseVersion {
	t.Helper()
	ids := make([]domain.ID, 0, len(members))
	for _, m := range members {
		ids = append(ids, domain.ID(m))
	}
	uv, err := s.data.CreateUniverseVersion(context.Background(), domain.UniverseVersionRequest{
		Name:       "uni-" + members[0],
		SnapshotID: snap.ID,
		Definition: domain.UniverseDefinition{Kind: domain.UniverseStatic, Members: ids},
	})
	if err != nil {
		t.Fatalf("create universe: %v", err)
	}
	return uv
}

// registerValuationSpec registers a factor whose input lives in the
// valuation dataset — the AC-05 contrast that must fail preflight on a
// bar-only snapshot.
func registerValuationSpec(t *testing.T, registry *factor.Registry) {
	t.Helper()
	spec := &factor.Spec{
		ID:      "pe",
		Version: "1.0.0",
		Title:   "Price to earnings",
		Kind:    factor.KindBuiltin,
		Inputs: []factor.Input{{
			Name: "eps", Dataset: "valuation", Field: "eps", Frequency: "daily",
			Lookback: 1, Unit: "usd", PIT: true,
		}},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal("1"), "", nil
		},
	}
	if err := registry.Register(spec); err != nil {
		t.Fatalf("register pe: %v", err)
	}
}

func assertCode(t *testing.T, err error, code, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != code {
		t.Fatalf("code = %q, want %q (err: %v)", domErr.Code, code, err)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}

// TestRunFactor computes momentum over the static universe through the
// service: the frame carries a value for every member, and the value
// matches the hand-computed 20-period ratio of the fixture closes.
func TestRunFactor(t *testing.T) {
	stack := newTestStack(t)
	snap := stack.publish(t, standardBars())
	uv := stack.staticUniverse(t, snap, "INST_A", "INST_B", "INST_C")
	ctx := context.Background()

	req := FactorRunRequest{
		SnapshotID: snap.ID,
		UniverseID: uv.ID,
		FactorRef:  domain.VersionRef{ID: "momentum", Version: "1.0.0"},
		AsOf:       mustDay(30),
		WindowFrom: mustDay(0),
	}
	frame, err := stack.service.RunFactor(ctx, req)
	if err != nil {
		t.Fatalf("run factor: %v", err)
	}
	if frame.Ref.ID != "momentum" || !frame.AsOf.Equal(mustDay(30)) {
		t.Fatalf("frame ref/as_of = %s/%v", frame.Ref, frame.AsOf)
	}
	if len(frame.Values) != 3 || len(frame.Missing) != 0 {
		t.Fatalf("frame values/missing = %d/%d, want 3/0", len(frame.Values), len(frame.Missing))
	}
	// 30 prior points: momentum = P(29)/P(9) - 1 = 129/109 - 1.
	value, err := strconv.ParseFloat(string(frame.Values["INST_A"]), 64)
	if err != nil {
		t.Fatalf("parse INST_A value: %v", err)
	}
	if math.Abs(value-(129.0/109.0-1)) > 1e-9 {
		t.Fatalf("INST_A momentum = %v, want %v", value, 129.0/109.0-1)
	}

	// Preflight of the same request reports no problems.
	problems, err := stack.service.PreflightFactor(ctx, req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("preflight problems = %v, want none", problems)
	}
}

// TestRunFactorPreflightFailure: the valuation-dependent factor cannot
// run on a bar-only snapshot — preflight reports every problem at once
// and the run fails closed with factor.preflight_failed.
func TestRunFactorPreflightFailure(t *testing.T) {
	stack := newTestStack(t)
	registerValuationSpec(t, stack.registry)
	snap := stack.publish(t, standardBars())
	uv := stack.staticUniverse(t, snap, "INST_A")
	ctx := context.Background()

	req := FactorRunRequest{
		SnapshotID: snap.ID,
		UniverseID: uv.ID,
		FactorRef:  domain.VersionRef{ID: "pe", Version: "1.0.0"},
		AsOf:       mustDay(30),
		WindowFrom: mustDay(0),
	}
	problems, err := stack.service.PreflightFactor(ctx, req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	found := false
	for _, p := range problems {
		if p.Code == factor.ProblemDatasetMissing {
			found = true
		}
	}
	if !found {
		t.Fatalf("preflight problems = %v, want dataset_missing", problems)
	}
	_, err = stack.service.RunFactor(ctx, req)
	assertCode(t, err, "factor.preflight_failed", "dataset")
}

// TestRunAnalysis walks the full M1-09 loop: frames per decision date,
// labels from the as_of-pinned view, the sealed artifact and its
// content-addressed storage.
func TestRunAnalysis(t *testing.T) {
	stack := newTestStack(t)
	snap := stack.publish(t, standardBars())
	uv := stack.staticUniverse(t, snap, "INST_A", "INST_B", "INST_C")
	ctx := context.Background()

	req := AnalysisRequest{
		SnapshotID: snap.ID,
		UniverseID: uv.ID,
		FactorRef:  domain.VersionRef{ID: "momentum", Version: "1.0.0"},
		Range:      domain.Interval{From: mustDay(30), To: mustDay(38)},
		AsOf:       mustDay(40),
		Horizons:   []int{1, 2},
		Groups:     2,
		MinSamples: 2,
		Method:     analysis.MethodPearson,
		Segments: []analysis.Segment{
			{Kind: analysis.SegmentTrain, Range: domain.Interval{From: mustDay(30), To: mustDay(34)}},
			{Kind: analysis.SegmentValidation, Range: domain.Interval{From: mustDay(34), To: mustDay(38)}},
		},
	}
	res, err := stack.service.RunAnalysis(ctx, req)
	if err != nil {
		t.Fatalf("run analysis: %v", err)
	}

	if res.Artifact.Summary.Dates != 8 {
		t.Fatalf("summary dates = %d, want 8", res.Artifact.Summary.Dates)
	}
	if len(res.Artifact.Summary.Horizons) != 2 ||
		res.Artifact.Summary.Horizons[0].Horizon != 1 || res.Artifact.Summary.Horizons[1].Horizon != 2 {
		t.Fatalf("horizons = %+v, want [1 2]", res.Artifact.Summary.Horizons)
	}
	h1 := res.Artifact.Summary.Horizons[0]
	if h1.LabelDates != 8 || h1.MissingLabelDates != 0 || h1.Pairs != 24 {
		t.Fatalf("horizon 1 label dates/pairs = %d/%d/%d, want 8/0/24", h1.LabelDates, h1.MissingLabelDates, h1.Pairs)
	}
	if h1.IC.N != 8 {
		t.Fatalf("horizon 1 IC n = %d, want 8", h1.IC.N)
	}
	if res.Artifact.Summary.Coverage.Covered != 24 || res.Artifact.Summary.Coverage.Total != 24 {
		t.Fatalf("coverage = %d/%d, want 24/24", res.Artifact.Summary.Coverage.Covered, res.Artifact.Summary.Coverage.Total)
	}

	// Segment accounting: 4 train dates, 4 validation dates, 12 labels
	// each at horizon 1.
	if len(res.Artifact.Summary.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(res.Artifact.Summary.Segments))
	}
	train := res.Artifact.Summary.Segments[0]
	if train.Kind != "train" || train.Dates != 4 || train.Samples != 12 {
		t.Fatalf("train segment = %+v", train)
	}
	if len(train.Horizons) != 2 || train.Horizons[0].Horizon != 1 || train.Horizons[0].Labels != 12 {
		t.Fatalf("train horizons = %+v", train.Horizons)
	}

	// The artifact verifies against its own checksum.
	ok, err := res.Artifact.VerifyChecksum(ports.SHA256Checksummer{})
	if err != nil || !ok {
		t.Fatalf("verify checksum = %v %v, want true", ok, err)
	}
	if res.Artifact.EvidenceNote != analysis.EvidenceNote {
		t.Fatal("artifact must carry the evidence note")
	}

	// The stored copy is the canonical artifact: same bytes, json media
	// type, content-addressed (a second run resolves to the same storage).
	stored, err := stack.artifacts.Get(ctx, res.Stored.ID)
	if err != nil {
		t.Fatalf("get stored artifact: %v", err)
	}
	if stored.MediaType != "application/json" || stored.Checksum == "" {
		t.Fatalf("stored artifact = %+v", stored)
	}
	_, reader, err := stack.artifacts.Open(ctx, res.Stored.ID)
	if err != nil {
		t.Fatalf("open stored artifact: %v", err)
	}
	defer reader.Close()
	var reloaded analysis.Artifact
	if err := json.NewDecoder(reader).Decode(&reloaded); err != nil {
		t.Fatalf("decode stored artifact: %v", err)
	}
	if reloaded.Checksum != res.Artifact.Checksum {
		t.Fatalf("stored checksum = %s, want %s", reloaded.Checksum, res.Artifact.Checksum)
	}

	// Determinism: equal inputs yield the same analysis checksum and the
	// same content-addressed artifact id.
	second, err := stack.service.RunAnalysis(ctx, req)
	if err != nil {
		t.Fatalf("second analysis: %v", err)
	}
	if second.Artifact.Checksum != res.Artifact.Checksum {
		t.Fatalf("analysis checksum differs across equal runs: %s vs %s", res.Artifact.Checksum, second.Artifact.Checksum)
	}
	if second.Stored.ID != res.Stored.ID {
		t.Fatalf("stored artifact id differs across equal runs: %s vs %s", res.Stored.ID, second.Stored.ID)
	}
}

// TestRunAnalysisFailures covers the closed loop's fail-closed paths.
func TestRunAnalysisFailures(t *testing.T) {
	stack := newTestStack(t)
	snap := stack.publish(t, standardBars())
	uv := stack.staticUniverse(t, snap, "INST_A", "INST_B", "INST_C")
	ctx := context.Background()

	base := AnalysisRequest{
		SnapshotID: snap.ID,
		UniverseID: uv.ID,
		FactorRef:  domain.VersionRef{ID: "momentum", Version: "1.0.0"},
		Range:      domain.Interval{From: mustDay(30), To: mustDay(38)},
		AsOf:       mustDay(40),
		Horizons:   []int{1},
		Groups:     2,
		MinSamples: 2,
		Method:     analysis.MethodPearson,
	}

	t.Run("as_of precedes range.to", func(t *testing.T) {
		req := base
		req.AsOf = mustDay(31)
		_, err := stack.service.RunAnalysis(ctx, req)
		assertCode(t, err, domain.CodeValidationInvalid, "as_of must not precede range.to")
	})
	t.Run("empty horizons", func(t *testing.T) {
		req := base
		req.Horizons = nil
		_, err := stack.service.RunAnalysis(ctx, req)
		assertCode(t, err, domain.CodeValidationInvalid, "horizon")
	})
	t.Run("unknown universe", func(t *testing.T) {
		req := base
		req.UniverseID = "univ-ghost"
		_, err := stack.service.RunAnalysis(ctx, req)
		assertCode(t, err, domain.CodeResourceNotFound, "not found")
	})
	t.Run("unknown factor", func(t *testing.T) {
		req := base
		req.FactorRef = domain.VersionRef{ID: "ghost", Version: "1.0.0"}
		_, err := stack.service.RunAnalysis(ctx, req)
		assertCode(t, err, "factor.not_registered", "not registered")
	})
	t.Run("universe bound to another snapshot", func(t *testing.T) {
		other := stack.publish(t, standardBars()[:30])
		otherUV := stack.staticUniverse(t, other, "INST_A")
		req := base
		req.UniverseID = otherUV.ID
		_, err := stack.service.RunAnalysis(ctx, req)
		assertCode(t, err, domain.CodeValidationInvalid, "is bound to snapshot")
	})
	t.Run("no warmup history", func(t *testing.T) {
		// Bars exist only inside the analysis range: the derived warmup
		// window cannot hold momentum's 21 points, so the per-date run
		// fails preflight with the failing date on the wire.
		late := stack.publish(t, standardBars()[90:114]) // days 30..37 only
		lateUV := stack.staticUniverse(t, late, "INST_A")
		req := base
		req.SnapshotID = late.ID
		req.UniverseID = lateUV.ID
		_, err := stack.service.RunAnalysis(ctx, req)
		assertCode(t, err, "factor.preflight_failed", "factor run at")
	})
}

// TestRequiredLookback walks the dependency closure: reversal's
// dependency on momentum means the deepest lookback (n+1) is found even
// though reversal itself declares no inputs.
func TestRequiredLookback(t *testing.T) {
	stack := newTestStack(t)
	lookback, err := stack.service.requiredLookback(factor.FactorRef{ID: "reversal", Version: "1.0.0"}, nil)
	if err != nil {
		t.Fatalf("required lookback: %v", err)
	}
	if lookback != 21 {
		t.Fatalf("reversal lookback = %d, want 21 (momentum n=20 + 1)", lookback)
	}
}
