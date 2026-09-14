package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// SC-AC-03: changing what happens after the decision time never changes a past
// selection. Two mechanisms have to hold, and they are different:
//
//   - a run is pinned to the snapshot it was submitted with, so re-running the
//     same request reproduces the same rows, ranks and explanations even after
//     later data landed;
//   - a view never shows a row that became available after the decision time,
//     so a run on a newer snapshot still cannot see tomorrow's bar.
//
// The test writes the "later" data straight into the data plane, because a late
// correction is exactly what a provider replays: a backdated revision whose
// availability sits inside the decision time, plus a row that only becomes
// available after it.

// lateBar is one bar written outside the provider pipeline, so the test controls
// its revision and availability exactly. The value map carries just the close,
// which is all a field binding reads.
func lateBar(inst, eventTime, close string, availableAt time.Time, revision string) domain.Observation {
	instrument := domain.ID(inst)
	published := availableAt
	return domain.Observation{
		InstrumentID: &instrument,
		Dataset:      "bar",
		EventTime:    pitTime(eventTime),
		Values:       map[string]domain.Value{"close": {Kind: domain.ValueDecimal, Encoded: close}},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "bar-" + inst + "-" + eventTime,
			RevisionID:     revision,
			AvailableAt:    availableAt,
			IngestedAt:     availableAt,
			PublishedAt:    &published,
		},
	}
}

// pitTime parses one fixture timestamp; a malformed literal is a broken test,
// not a case to report.
func pitTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic("pit acceptance: parse " + value + ": " + err.Error())
	}
	return parsed
}

// TestM1SPointInTimeImmutabilityAcceptance freezes one run, lands later data and
// proves the frozen result is unchanged while the newer snapshot behaves as the
// new data says.
func TestM1SPointInTimeImmutabilityAcceptance(t *testing.T) {
	s := bootAcceptance(t)
	snapshot := s.ingestSnapshot(t, "pit-snapshot-one")
	pool := s.savePool(t, "pit-pool", snapshot.ID, "pit-pool", "INST_A")
	// The rule is a bare field comparison: no factor window, so the only input
	// that matters is the latest close the decision time can see.
	screener := s.saveScreener(t, "pit-cheap", "pit-screener", map[string]any{
		"input_bindings": []map[string]any{
			{"binding_id": "px", "kind": "field", "dataset": "bar", "field": "close"},
		},
		"condition_tree":  compareCondition("cheap", "px", "lt", "15"),
		"ranking":         map[string]any{"mode": "sort", "fields": []map[string]any{{"input": map[string]any{"binding_id": "px"}, "direction": "desc"}}},
		"selection":       map[string]any{"mode": "all"},
		"display_columns": []string{"px"},
	})
	request := s.runRequest(screener, snapshot, pool)

	// The decision time is 2026-01-15; the newest bar it can see is 2026-01-09
	// (close 11.40), so INST_A passes the condition and is selected.
	before := s.submitScreenRun(t, request, "pit-run-before")
	beforeRows := s.screenRows(t, before.ID, "")
	if before.Summary == nil || before.Summary.Selected != 1 {
		t.Fatalf("summary = %+v, want the member selected", before.Summary)
	}
	if len(beforeRows.Items) != 1 || beforeRows.Items[0].Values["px"].Value != "11.40" {
		t.Fatalf("rows = %+v, want the close visible at the decision time", beforeRows.Items)
	}
	beforeExplanation := s.screenExplanation(t, before.ID, "INST_A")

	// Later data lands: a backdated revision of that same 2026-01-09 bar, and a
	// February bar that only becomes available after the decision time. Both are
	// appended as one new batch and published as a second snapshot that also
	// carries the original batch — so the correction is resolved by the
	// latest-revision rule inside one snapshot, not by swapping snapshots.
	receipt, err := s.data.Append(context.Background(), ports.BatchInput{
		JobID:     "late-job",
		Dataset:   "bar",
		Frequency: "daily",
		Observations: []domain.Observation{
			lateBar("INST_A", "2026-01-09T15:00:00Z", "20.00", pitTime("2026-01-09T15:30:00Z"), "rev-002"),
			lateBar("INST_A", "2026-02-01T15:00:00Z", "99.00", pitTime("2026-02-01T15:30:00Z"), "rev-002"),
		},
	})
	if err != nil {
		t.Fatalf("append late batch: %v", err)
	}
	later := s.publishSnapshot(t, "pit-snapshot-two", append([]string{snapshot.BatchIDs[0]}, receipt.BatchID.String()), "pit-snapshot-two")
	if later.ManifestHash == snapshot.ManifestHash {
		t.Fatal("the second snapshot must be a different manifest")
	}

	// 1. The frozen run is unchanged: same rows, same explanation, same artifact.
	replayed := s.submitScreenRun(t, request, "pit-run-replayed")
	if replayed.SnapshotHash != before.SnapshotHash {
		t.Fatalf("snapshot hash = %q, want the pinned %q", replayed.SnapshotHash, before.SnapshotHash)
	}
	replayedRows := s.screenRows(t, replayed.ID, "")
	if len(replayedRows.Items) != len(beforeRows.Items) {
		t.Fatalf("rows = %+v, want the frozen result", replayedRows.Items)
	}
	for i := range beforeRows.Items {
		original, again := beforeRows.Items[i], replayedRows.Items[i]
		if original.InstrumentID != again.InstrumentID || original.Selected != again.Selected || original.Reason != again.Reason {
			t.Fatalf("row %d = %+v then %+v, want an identical classification", i, original, again)
		}
		if original.Values["px"].Value != again.Values["px"].Value {
			t.Fatalf("row %d close = %v then %v, want the frozen value", i, original.Values["px"].Value, again.Values["px"].Value)
		}
		if (original.Rank == nil) != (again.Rank == nil) || (original.Rank != nil && *original.Rank != *again.Rank) {
			t.Fatalf("row %d rank = %v then %v, want the frozen rank", i, original.Rank, again.Rank)
		}
	}
	replayedExplanation := s.screenExplanation(t, replayed.ID, "INST_A")
	if replayedExplanation.Stage != beforeExplanation.Stage ||
		replayedExplanation.Nodes.Truth != beforeExplanation.Nodes.Truth ||
		replayedExplanation.Nodes.NodeID != beforeExplanation.Nodes.NodeID {
		t.Fatalf("explanation = %+v, want the frozen evidence %+v", replayedExplanation, beforeExplanation)
	}
	// The first run itself is untouched by everything that happened after it.
	if reloaded := s.screenRun(t, before.ID); reloaded.Summary == nil || reloaded.Summary.Selected != 1 {
		t.Fatalf("first run = %+v, want its published summary unchanged", reloaded.Summary)
	}
	if row := s.screenRows(t, before.ID, "").Items[0]; row.Values["px"].Value != "11.40" {
		t.Fatalf("first run row = %+v, want the value it published", row)
	}

	// 2. The newer snapshot is a different input, not a different answer for the
	// old one. A pool is bound to the snapshot it was saved against, so the
	// second run needs its own pool version.
	laterPool := s.savePool(t, "pit-pool-two", later.ID, "pit-pool-two", "INST_A")
	onLater := s.submitScreenRun(t, s.runRequest(screener, later, laterPool), "pit-run-later")
	if onLater.SnapshotHash != later.ManifestHash {
		t.Fatalf("snapshot hash = %q, want the second snapshot %q", onLater.SnapshotHash, later.ManifestHash)
	}
	if onLater.Summary == nil || onLater.Summary.Selected != 0 || onLater.Summary.ConditionFalse != 1 {
		t.Fatalf("summary = %+v, want the corrected close to fail the condition", onLater.Summary)
	}
	laterRows := s.screenRows(t, onLater.ID, "")
	if len(laterRows.Items) != 1 || laterRows.Items[0].Values["px"].Value != "20.00" {
		t.Fatalf("rows = %+v, want the corrected close and not the later bar", laterRows.Items)
	}
	if laterRows.Items[0].Reason != "condition_false" {
		t.Fatalf("row = %+v, want the corrected value to have failed the condition", laterRows.Items[0])
	}
}

// publishSnapshot freezes a set of batches through the API and returns the
// snapshot.
func (s *acceptanceStack) publishSnapshot(t *testing.T, name string, batchIDs []string, idem string) acceptanceSnapshot {
	t.Helper()
	code, _, raw := s.call(http.MethodPost, "/snapshots", map[string]any{
		"name":       name,
		"batch_ids":  batchIDs,
		"strict_pit": false,
	}, idem)
	if code != http.StatusAccepted {
		t.Fatalf("POST /snapshots: status %d body %s", code, raw)
	}
	job := s.waitJob(decodeBody[acceptanceJob](t, "snapshot job", raw).ID, stateSucceeded)
	if len(job.ResultRefs) != 1 || job.ResultRefs[0].Kind != "snapshot" {
		t.Fatalf("snapshot result refs = %+v, want the published snapshot", job.ResultRefs)
	}
	code, _, raw = s.call(http.MethodGet, "/snapshots/"+job.ResultRefs[0].ID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /snapshots/{id}: status %d body %s", code, raw)
	}
	return decodeBody[acceptanceSnapshot](t, "snapshot", raw)
}
