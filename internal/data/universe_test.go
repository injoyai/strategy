package data

import (
	"context"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

func dayTime(value string) time.Time {
	if len(value) == 10 {
		return mustTime(value + "T00:00:00Z")
	}
	return mustTime(value)
}

func mustInterval(from, to string) *domain.Interval {
	return &domain.Interval{From: dayTime(from), To: dayTime(to)}
}

func membershipRow(inst, event, from, to, available, revision string) domain.Observation {
	at := mustTime(available)
	return domain.Observation{
		InstrumentID: idPtr(inst),
		Dataset:      "index_membership",
		EventTime:    dayTime(event),
		Effective:    mustInterval(from, to),
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "membership-" + inst + "-" + event,
			RevisionID:     revision,
			AvailableAt:    at,
			IngestedAt:     at,
			PublishedAt:    timePtr(at),
		},
	}
}

func appendRows(t *testing.T, s *Store, job, dataset, frequency string, obs ...domain.Observation) domain.IngestReceipt {
	t.Helper()
	receipt, err := s.Append(context.Background(), ports.BatchInput{
		JobID:        domain.ID(job),
		Dataset:      dataset,
		Frequency:    frequency,
		Observations: obs,
	})
	if err != nil {
		t.Fatalf("append %s batch: %v", dataset, err)
	}
	return receipt
}

func assertMembers(t *testing.T, got []domain.ID, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("members = %v, want %v", got, want)
		}
	}
}

func staticDefinition(members ...string) domain.UniverseDefinition {
	ids := make([]domain.ID, 0, len(members))
	for _, member := range members {
		ids = append(ids, domain.ID(member))
	}
	return domain.UniverseDefinition{Kind: domain.UniverseStatic, Members: ids}
}

func staticRequest(name string, snapshotID domain.ID, members ...string) domain.UniverseVersionRequest {
	return domain.UniverseVersionRequest{
		Name:       name,
		SnapshotID: snapshotID,
		Definition: staticDefinition(members...),
	}
}

func membershipFixture(t *testing.T) (*Store, domain.Snapshot) {
	t.Helper()
	s, _ := newTestStore(t)
	ctx := context.Background()

	ep1 := membershipRow("inst-stable", "2026-01-01", "2026-01-01", "2026-01-15", "2025-12-31T15:00:00Z", "rev-001")
	ep1.Values = map[string]domain.Value{"code": {Kind: domain.ValueString, Encoded: "OLD001"}}
	ep2 := membershipRow("inst-stable", "2026-01-15", "2026-01-15", "2026-02-01", "2026-01-14T15:00:00Z", "rev-001")
	ep2.Values = map[string]domain.Value{"code": {Kind: domain.ValueString, Encoded: "NEW002"}}

	reinstated := membershipRow("idx-reinstated", "2026-01-02", "2026-01-02", "2026-01-06", "2026-01-03T15:00:00Z", "rev-001")
	correction := membershipRow("idx-reinstated", "2026-01-02", "2026-01-02", "2026-01-20", "2026-01-10T15:00:00Z", "rev-002")
	correction.Provenance.SupersedesRevisionID = "rev-001"

	corp := domain.Observation{
		EntityID:  idPtr("corp-1"),
		Dataset:   "index_membership",
		EventTime: mustTime("2026-01-01T15:00:00Z"),
		Effective: mustInterval("2026-01-01", "2026-02-01"),
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "membership-corp-1-2026-01-01",
			RevisionID:     "rev-001",
			AvailableAt:    mustTime("2025-12-31T15:00:00Z"),
			IngestedAt:     mustTime("2025-12-31T15:00:00Z"),
			PublishedAt:    timePtr(mustTime("2025-12-31T15:00:00Z")),
		},
	}

	membership := appendRows(t, s, "membership-job", "index_membership", "daily",
		membershipRow("idx-join", "2026-01-10", "2026-01-10", "2026-02-01", "2026-01-09T15:00:00Z", "rev-001"),
		membershipRow("idx-leave", "2026-01-01", "2026-01-01", "2026-01-15", "2025-12-31T15:00:00Z", "rev-001"),
		membershipRow("idx-delisted", "2026-01-01", "2026-01-01", "2026-01-20", "2025-12-31T15:00:00Z", "rev-001"),
		membershipRow("idx-suspended", "2026-01-01", "2026-01-01", "2026-02-01", "2025-12-31T15:00:00Z", "rev-001"),
		ep1, ep2, reinstated, correction, corp,
	)

	status := domain.Observation{
		InstrumentID: idPtr("idx-suspended"),
		Dataset:      "trading_status",
		EventTime:    mustTime("2026-01-10T15:00:00Z"),
		Effective:    mustInterval("2026-01-01", "2026-02-01"),
		Values:       map[string]domain.Value{"status": {Kind: domain.ValueString, Encoded: "suspended"}},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "status-idx-suspended-2026-01-10",
			RevisionID:     "rev-001",
			AvailableAt:    mustTime("2026-01-09T15:00:00Z"),
			IngestedAt:     mustTime("2026-01-09T15:00:00Z"),
			PublishedAt:    timePtr(mustTime("2026-01-09T15:00:00Z")),
		},
	}
	statusBatch := appendRows(t, s, "status-job", "trading_status", "daily", status)

	snap, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{
		Name:     "membership-snapshot",
		BatchIDs: []domain.ID{membership.BatchID, statusBatch.BatchID},
	})
	if err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	return s, snap
}

func TestUniverseHistoricalRuleResolvesPITMembership(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	uv, err := s.CreateUniverseVersion(ctx, domain.UniverseVersionRequest{
		Name:       "membership-at-decision-time",
		SnapshotID: snap.ID,
		Definition: domain.UniverseDefinition{
			Kind: domain.UniverseHistoricalRule,
			Rule: &domain.UniverseRule{Dataset: "index_membership", Frequency: "daily"},
		},
	})
	if err != nil {
		t.Fatalf("create universe version: %v", err)
	}

	cases := []struct {
		name string
		asOf string
		want []string
	}{
		{"delisted and suspended members stay in the sample", "2026-01-05T00:00:00Z",
			[]string{"idx-delisted", "idx-leave", "idx-reinstated", "idx-suspended", "inst-stable"}},
		{"reinstatement knowledge gap", "2026-01-08T00:00:00Z",
			[]string{"idx-delisted", "idx-leave", "idx-suspended", "inst-stable"}},
		{"announced but not yet effective join is excluded", "2026-01-09T16:00:00Z",
			[]string{"idx-delisted", "idx-leave", "idx-suspended", "inst-stable"}},
		{"correction lands and join becomes effective", "2026-01-12T00:00:00Z",
			[]string{"idx-delisted", "idx-join", "idx-leave", "idx-reinstated", "idx-suspended", "inst-stable"}},
		{"half-open removal boundary and code change episode", "2026-01-15T00:00:00Z",
			[]string{"idx-delisted", "idx-join", "idx-reinstated", "idx-suspended", "inst-stable"}},
		{"windows ending the same day drop both members", "2026-01-20T00:00:00Z",
			[]string{"idx-join", "idx-suspended", "inst-stable"}},
		{"empty membership after all windows close", "2026-02-05T00:00:00Z",
			[]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view, err := s.OpenView(ctx, snap.ID, mustTime(tc.asOf))
			if err != nil {
				t.Fatalf("open view: %v", err)
			}
			members, err := ResolveUniverse(ctx, uv, view)
			if err != nil {
				t.Fatalf("resolve universe: %v", err)
			}
			assertMembers(t, members, tc.want...)
		})
	}
}

func TestUniverseStaticResolution(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	uv, err := s.CreateUniverseVersion(ctx, staticRequest("static-research", snap.ID, "inst-b", "inst-a", "inst-b", "inst-c"))
	if err != nil {
		t.Fatalf("create universe version: %v", err)
	}
	if len(uv.Definition.Members) != 3 ||
		uv.Definition.Members[0].String() != "inst-a" ||
		uv.Definition.Members[1].String() != "inst-b" ||
		uv.Definition.Members[2].String() != "inst-c" {
		t.Fatalf("canonical members = %v, want [inst-a inst-b inst-c]", uv.Definition.Members)
	}

	for _, asOf := range []string{"2026-01-05T00:00:00Z", "2026-02-05T00:00:00Z"} {
		view, err := s.OpenView(ctx, snap.ID, mustTime(asOf))
		if err != nil {
			t.Fatalf("open view at %s: %v", asOf, err)
		}
		members, err := ResolveUniverse(ctx, uv, view)
		if err != nil {
			t.Fatalf("resolve universe at %s: %v", asOf, err)
		}
		assertMembers(t, members, "inst-a", "inst-b", "inst-c")
	}

	reordered, err := s.CreateUniverseVersion(ctx, staticRequest("static-reordered", snap.ID, "inst-c", "inst-b", "inst-a"))
	if err != nil {
		t.Fatalf("create reordered version: %v", err)
	}
	if reordered.DefinitionHash != uv.DefinitionHash {
		t.Fatalf("reordered definition hash = %q, want %q", reordered.DefinitionHash, uv.DefinitionHash)
	}

	shrunk, err := s.CreateUniverseVersion(ctx, staticRequest("static-shrunk", snap.ID, "inst-a", "inst-b"))
	if err != nil {
		t.Fatalf("create shrunk version: %v", err)
	}
	if shrunk.DefinitionHash == uv.DefinitionHash {
		t.Fatal("different member list must hash differently")
	}
}

func TestUniverseCreateIsNotIdempotent(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	req := staticRequest("frozen-list", snap.ID, "inst-a", "inst-b")
	first, err := s.CreateUniverseVersion(ctx, req)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := s.CreateUniverseVersion(ctx, req)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("identical requests must record distinct versions, both %s", first.ID)
	}
	if first.DefinitionHash != second.DefinitionHash {
		t.Fatal("identical definitions must share the definition hash")
	}
	for _, uv := range []domain.UniverseVersion{first, second} {
		got, err := s.GetUniverseVersion(ctx, uv.ID)
		if err != nil {
			t.Fatalf("get %s: %v", uv.ID, err)
		}
		if got.Name != "frozen-list" || got.SnapshotID != snap.ID || got.DefinitionHash != uv.DefinitionHash {
			t.Fatalf("round trip mismatch: %#v", got)
		}
		if got.CreatedAt.IsZero() {
			t.Fatal("created_at must be set")
		}
	}
}

func TestUniverseCreateValidation(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		mutate   func(*domain.UniverseVersionRequest)
		code     string
		contains string
	}{
		{"empty name", func(r *domain.UniverseVersionRequest) { r.Name = "" },
			domain.CodeValidationInvalid, "universe: name is required"},
		{"empty snapshot", func(r *domain.UniverseVersionRequest) { r.SnapshotID = "" },
			domain.CodeValidationInvalid, "universe: snapshot_id is required"},
		{"unknown kind", func(r *domain.UniverseVersionRequest) { r.Definition.Kind = "dynamic" },
			domain.CodeValidationInvalid, "universe: kind must be one of: static, historical_rule"},
		{"static without members", func(r *domain.UniverseVersionRequest) { r.Definition.Members = nil },
			domain.CodeValidationInvalid, "universe: static definition requires members"},
		{"static with empty member id", func(r *domain.UniverseVersionRequest) {
			r.Definition.Members = []domain.ID{"inst-a", ""}
		}, domain.CodeValidationInvalid, "universe: members[1] is empty"},
		{"static carrying a rule", func(r *domain.UniverseVersionRequest) {
			r.Definition.Rule = &domain.UniverseRule{Dataset: "bar", Frequency: "daily"}
		}, domain.CodeValidationInvalid, "universe: static definition must not carry a rule"},
		{"historical rule without rule", func(r *domain.UniverseVersionRequest) {
			r.Definition = domain.UniverseDefinition{Kind: domain.UniverseHistoricalRule}
		}, domain.CodeValidationInvalid, "universe: historical_rule definition requires a rule"},
		{"historical rule carrying members", func(r *domain.UniverseVersionRequest) {
			r.Definition = domain.UniverseDefinition{
				Kind:    domain.UniverseHistoricalRule,
				Members: []domain.ID{"inst-a"},
				Rule:    &domain.UniverseRule{Dataset: "index_membership", Frequency: "daily"},
			}
		}, domain.CodeValidationInvalid, "universe: historical_rule definition must not carry members"},
		{"rule without dataset", func(r *domain.UniverseVersionRequest) {
			r.Definition = domain.UniverseDefinition{
				Kind: domain.UniverseHistoricalRule,
				Rule: &domain.UniverseRule{Frequency: "daily"},
			}
		}, domain.CodeValidationInvalid, "universe: rule invalid"},
		{"rule without frequency", func(r *domain.UniverseVersionRequest) {
			r.Definition = domain.UniverseDefinition{
				Kind: domain.UniverseHistoricalRule,
				Rule: &domain.UniverseRule{Dataset: "index_membership"},
			}
		}, domain.CodeValidationInvalid, "universe: rule invalid"},
		{"unknown snapshot", func(r *domain.UniverseVersionRequest) { r.SnapshotID = "snap_missing" },
			domain.CodeResourceNotFound, "snapshot snap_missing not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := staticRequest("valid-universe", snap.ID, "inst-a")
			tc.mutate(&req)
			_, err := s.CreateUniverseVersion(ctx, req)
			assertErr(t, err, tc.code, tc.contains)
		})
	}
}

func TestUniverseGetAndList(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	names := []string{"core-alpha", "tactical-beta", "overlay-gamma"}
	created := make([]domain.UniverseVersion, 0, len(names))
	for _, name := range names {
		uv, err := s.CreateUniverseVersion(ctx, staticRequest(name, snap.ID, "inst-a", "inst-b"))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		created = append(created, uv)
	}

	t.Run("get round trip", func(t *testing.T) {
		got, err := s.GetUniverseVersion(ctx, created[0].ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ID != created[0].ID || got.Name != "core-alpha" || got.SnapshotID != snap.ID {
			t.Fatalf("identity mismatch: %#v", got)
		}
		if got.Definition.Kind != domain.UniverseStatic {
			t.Fatalf("kind = %q, want static", got.Definition.Kind)
		}
		assertMembers(t, got.Definition.CanonicalMembers(), "inst-a", "inst-b")
		if got.DefinitionHash == "" || got.DefinitionHash != created[0].DefinitionHash {
			t.Fatalf("definition hash mismatch: %q vs %q", got.DefinitionHash, created[0].DefinitionHash)
		}
	})

	t.Run("get unknown id", func(t *testing.T) {
		_, err := s.GetUniverseVersion(ctx, "univ_missing")
		assertErr(t, err, domain.CodeResourceNotFound, "data: universe version univ_missing not found")
	})

	t.Run("list ascending with keyset pagination", func(t *testing.T) {
		page1, err := s.ListUniverseVersions(ctx, ports.UniverseFilter{Sort: "id", Limit: 2})
		if err != nil {
			t.Fatalf("list page 1: %v", err)
		}
		if len(page1.Items) != 2 || page1.NextCursor == "" {
			t.Fatalf("page 1 = %d items, cursor %q; want 2 items and a cursor", len(page1.Items), page1.NextCursor)
		}
		if page1.Items[0].ID >= page1.Items[1].ID {
			t.Fatalf("page 1 not ascending by id: %s, %s", page1.Items[0].ID, page1.Items[1].ID)
		}
		page2, err := s.ListUniverseVersions(ctx, ports.UniverseFilter{Sort: "id", Limit: 2, AfterID: page1.NextCursor})
		if err != nil {
			t.Fatalf("list page 2: %v", err)
		}
		if len(page2.Items) != 1 || page2.NextCursor != "" {
			t.Fatalf("page 2 = %d items, cursor %q; want 1 item and no cursor", len(page2.Items), page2.NextCursor)
		}
		if page2.Items[0].ID <= page1.Items[1].ID {
			t.Fatalf("page 2 id %s must follow cursor %s", page2.Items[0].ID, page1.Items[1].ID)
		}
	})

	t.Run("list defaults to descending", func(t *testing.T) {
		page, err := s.ListUniverseVersions(ctx, ports.UniverseFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Items) != 3 || page.NextCursor != "" {
			t.Fatalf("page = %d items, cursor %q; want 3 items and no cursor", len(page.Items), page.NextCursor)
		}
		for i := 1; i < len(page.Items); i++ {
			if page.Items[i-1].ID <= page.Items[i].ID {
				t.Fatalf("page not descending by id at %d: %s, %s", i, page.Items[i-1].ID, page.Items[i].ID)
			}
		}
	})

	t.Run("list filters by name substring", func(t *testing.T) {
		page, err := s.ListUniverseVersions(ctx, ports.UniverseFilter{Q: "tactical"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Items) != 1 || page.Items[0].Name != "tactical-beta" {
			t.Fatalf("filtered page = %#v, want exactly tactical-beta", page.Items)
		}
	})
}

func TestUniverseRejectsForeignSnapshot(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	barBatch := appendBatch(t, s, "foreign-bar", nil, barRow("INST_X", "2026-01-05", "11.00", nil))
	foreign, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{
		Name:     "foreign-snapshot",
		BatchIDs: []domain.ID{barBatch.BatchID},
	})
	if err != nil {
		t.Fatalf("publish foreign snapshot: %v", err)
	}
	if foreign.ID == snap.ID {
		t.Fatal("different batch sets must publish distinct snapshots")
	}

	uv, err := s.CreateUniverseVersion(ctx, staticRequest("bound-universe", snap.ID, "inst-a"))
	if err != nil {
		t.Fatalf("create universe version: %v", err)
	}

	view, err := s.OpenView(ctx, foreign.ID, mustTime("2026-01-12T00:00:00Z"))
	if err != nil {
		t.Fatalf("open foreign view: %v", err)
	}
	_, err = ResolveUniverse(ctx, uv, view)
	assertErr(t, err, domain.CodeValidationInvalid, "is bound to snapshot")
}

func TestViewDatasetInstruments(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	view, err := s.OpenView(ctx, snap.ID, mustTime("2026-01-12T00:00:00Z"))
	if err != nil {
		t.Fatalf("open view: %v", err)
	}

	members, err := view.DatasetInstruments(ctx, "index_membership", "daily")
	if err != nil {
		t.Fatalf("list instruments: %v", err)
	}
	assertMembers(t, members, "idx-delisted", "idx-join", "idx-leave", "idx-reinstated", "idx-suspended", "inst-stable")

	status, err := view.DatasetInstruments(ctx, "trading_status", "daily")
	if err != nil {
		t.Fatalf("list status instruments: %v", err)
	}
	assertMembers(t, status, "idx-suspended")

	unknown, err := view.DatasetInstruments(ctx, "no_such_dataset", "daily")
	if err != nil {
		t.Fatalf("unknown dataset: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown dataset returned %v, want empty", unknown)
	}

	_, err = view.DatasetInstruments(ctx, "", "daily")
	assertErr(t, err, domain.CodeValidationInvalid, "data query: dataset is required")
	_, err = view.DatasetInstruments(ctx, "index_membership", "")
	assertErr(t, err, domain.CodeValidationInvalid, "data query: frequency is required")
}

func TestUniverseCacheKey(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	uv, err := s.CreateUniverseVersion(ctx, staticRequest("cache-key-universe", snap.ID, "inst-a", "inst-b"))
	if err != nil {
		t.Fatalf("create universe version: %v", err)
	}
	asOf := mustTime("2026-01-12T00:00:00Z")
	base, err := s.UniverseCacheKey(ctx, uv, asOf)
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	if base == "" {
		t.Fatal("cache key must not be empty")
	}

	t.Run("deterministic for identical inputs", func(t *testing.T) {
		again, err := s.UniverseCacheKey(ctx, uv, asOf)
		if err != nil {
			t.Fatalf("cache key: %v", err)
		}
		if again != base {
			t.Fatalf("cache key = %q, want %q", again, base)
		}
	})

	t.Run("as_of changes the key", func(t *testing.T) {
		later, err := s.UniverseCacheKey(ctx, uv, asOf.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("cache key: %v", err)
		}
		if later == base {
			t.Fatal("different as_of must produce a different key")
		}
	})

	t.Run("definition hash changes the key", func(t *testing.T) {
		mutated := uv
		mutated.DefinitionHash = "hash-other"
		key, err := s.UniverseCacheKey(ctx, mutated, asOf)
		if err != nil {
			t.Fatalf("cache key: %v", err)
		}
		if key == base {
			t.Fatal("different definition hash must produce a different key")
		}
	})

	t.Run("universe id changes the key", func(t *testing.T) {
		mutated := uv
		mutated.ID = "univ_other"
		key, err := s.UniverseCacheKey(ctx, mutated, asOf)
		if err != nil {
			t.Fatalf("cache key: %v", err)
		}
		if key == base {
			t.Fatal("different universe id must produce a different key")
		}
	})

	t.Run("snapshot hash changes the key", func(t *testing.T) {
		barBatch := appendBatch(t, s, "cache-key-bar", nil, barRow("INST_X", "2026-01-05", "11.00", nil))
		foreign, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{
			Name:     "cache-key-foreign",
			BatchIDs: []domain.ID{barBatch.BatchID},
		})
		if err != nil {
			t.Fatalf("publish foreign snapshot: %v", err)
		}
		mutated := uv
		mutated.SnapshotID = foreign.ID
		key, err := s.UniverseCacheKey(ctx, mutated, asOf)
		if err != nil {
			t.Fatalf("cache key: %v", err)
		}
		if key == base {
			t.Fatal("different snapshot must produce a different key")
		}
	})

	t.Run("zero as_of rejected", func(t *testing.T) {
		_, err := s.UniverseCacheKey(ctx, uv, time.Time{})
		assertErr(t, err, domain.CodeValidationInvalid, "universe: cache key as_of is required")
	})
}

func TestResolveUniverseGuards(t *testing.T) {
	s, snap := membershipFixture(t)
	ctx := context.Background()

	view, err := s.OpenView(ctx, snap.ID, mustTime("2026-01-12T00:00:00Z"))
	if err != nil {
		t.Fatalf("open view: %v", err)
	}

	t.Run("nil view", func(t *testing.T) {
		_, err := ResolveUniverse(ctx, domain.UniverseVersion{}, nil)
		assertErr(t, err, domain.CodeValidationInvalid, "universe: resolver requires a data view")
	})

	t.Run("invalid definition", func(t *testing.T) {
		bad := domain.UniverseVersion{
			SnapshotID: snap.ID,
			Definition: domain.UniverseDefinition{Kind: domain.UniverseStatic},
		}
		_, err := ResolveUniverse(ctx, bad, view)
		assertErr(t, err, domain.CodeValidationInvalid, "universe: static definition requires members")
	})

	t.Run("snapshot binding mismatch", func(t *testing.T) {
		foreign := domain.UniverseVersion{
			ID:         "univ_foreign",
			SnapshotID: "snap_foreign",
			Definition: staticDefinition("inst-a", "inst-b"),
		}
		_, err := ResolveUniverse(ctx, foreign, view)
		assertErr(t, err, domain.CodeValidationInvalid, "is bound to snapshot")
	})
}
