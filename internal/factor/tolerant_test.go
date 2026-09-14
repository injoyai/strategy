package factor

import (
	"context"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

// A tolerant request is the opt-in screening mode: a member the engine cannot
// compute at this decision time carries a missing reason instead of failing the
// run. The strict default is unchanged, and the two modes never share a cache
// entry, because a tolerant frame contains members a strict caller would have
// refused.

func TestTolerantRunTurnsMemberGapsIntoMissingValues(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	ctx := context.Background()
	// n=5 needs 6 points; the fixtures carry 3, so every member is short.
	strict := reqFor(momentumRef(), stdMembers, map[string]any{"n": 5})

	if _, err := engine.Run(ctx, strict); err == nil {
		t.Fatal("a strict run must refuse a member it cannot compute")
	} else {
		assertErr(t, err, codePreflightFailed, "insufficient_history")
	}

	tolerant := strict
	tolerant.TolerateMemberGaps = true
	frame, err := engine.Run(ctx, tolerant)
	if err != nil {
		t.Fatalf("tolerant run: %v", err)
	}
	if len(frame.Values) != 0 {
		t.Fatalf("values = %+v, want no computed value", frame.Values)
	}
	if len(frame.Missing) != len(stdMembers) {
		t.Fatalf("missing = %+v, want every short member to carry a reason", frame.Missing)
	}
	for _, member := range stdMembers {
		if reason := frame.Missing[member]; reason != ReasonInsufficientHistory {
			t.Fatalf("member %s reason = %q, want %q", member, reason, ReasonInsufficientHistory)
		}
	}

	// The tolerant frame is cached under its own key: a strict request with the
	// same inputs must still be refused rather than served what it rejected.
	if _, err := engine.Run(ctx, strict); err == nil {
		t.Fatal("a strict run was served the tolerant result")
	} else {
		assertErr(t, err, codePreflightFailed, "insufficient_history")
	}
}

// TestTolerantRunStillRefusesEverythingElse keeps the mode narrow: only a
// member's data shortfall is accepted, so a request-level problem keeps failing
// even when tolerance is on.
func TestTolerantRunStillRefusesEverythingElse(t *testing.T) {
	engine, _, _ := newTestEngine(t, standardBars()...)
	req := reqFor(momentumRef(), nil, map[string]any{"n": 1})
	req.TolerateMemberGaps = true
	if _, err := engine.Run(context.Background(), req); err == nil {
		t.Fatal("an empty universe must fail even for a tolerant run")
	} else {
		assertErr(t, err, codePreflightFailed, ProblemUniverseEmpty)
	}
}

// TestRunRequestToleratesIsTheSingleDefinition pins the predicate callers use to
// classify findings: screening must not grow its own list of what a tolerant run
// accepts, or a run could be accepted and then fail at compute time.
func TestRunRequestToleratesIsTheSingleDefinition(t *testing.T) {
	strict := RunRequest{}
	if strict.Tolerates(ProblemInsufficientHistory) {
		t.Fatal("a strict request must not tolerate a member gap")
	}
	tolerant := RunRequest{TolerateMemberGaps: true}
	if !tolerant.Tolerates(ProblemInsufficientHistory) {
		t.Fatal("a tolerant request must tolerate a member gap")
	}
	for _, code := range []string{ProblemDatasetMissing, ProblemFieldMissing, ProblemPITUnverified, ProblemParamInvalid, ProblemUniverseEmpty} {
		if tolerant.Tolerates(code) {
			t.Fatalf("code %q must not be tolerated", code)
		}
	}
}

// TestCacheKeySeparatesTheTwoModes checks the identity directly, so a future
// change to the key cannot silently make the modes share a frame.
func TestCacheKeySeparatesTheTwoModes(t *testing.T) {
	_, registry, _ := newTestEngine(t, standardBars()...)
	base := CacheKeyRequest{
		SnapshotHash:       "sh-1",
		UniverseID:         "uni-1",
		UniverseHash:       "uh-1",
		Ref:                momentumRef(),
		Params:             map[string]any{"n": float64(5)},
		Range:              domain.Interval{From: mustTime("2026-01-01T00:00:00Z"), To: factorAsOf},
		AvailabilityPolicy: "policy-1",
	}
	strictKey, err := registry.CacheKey(base)
	if err != nil {
		t.Fatalf("strict key: %v", err)
	}
	base.TolerateMemberGaps = true
	tolerantKey, err := registry.CacheKey(base)
	if err != nil {
		t.Fatalf("tolerant key: %v", err)
	}
	if strictKey == tolerantKey {
		t.Fatal("both modes share a cache key")
	}
}
