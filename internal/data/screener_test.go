package data

import (
	"context"
	"strings"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// screenerRequest is a minimal valid rule set: one field binding, one compare
// condition, a sort ranking and top_n selection.
func screenerRequest(name string) screening.VersionRequest {
	return screening.VersionRequest{
		Name: name,
		Definition: screening.Definition{
			Name:          name,
			InputBindings: []screening.InputBinding{{BindingID: "px", Kind: screening.BindingField, Dataset: "bar", Field: "close"}},
			ConditionTree: screening.Compare{
				NodeID:   "gt",
				Input:    screening.Input{BindingID: "px"},
				Operator: screening.OpGt,
				Value:    domain.Value{Kind: domain.ValueDecimal, Encoded: "10"},
			},
			Ranking:        screening.Ranking{Mode: screening.RankingSort, Fields: []screening.RankField{{Input: screening.Input{BindingID: "px"}, Direction: screening.DirectionDesc}}},
			Selection:      screening.Selection{Mode: screening.SelectionTopN, N: 5},
			DisplayColumns: []domain.ID{"px"},
		},
	}
}

func TestCreateScreenerVersionStartsLineage(t *testing.T) {
	store, clock := newTestStore(t)
	saved, err := store.CreateScreenerVersion(context.Background(), screenerRequest("quality-momentum"))
	if err != nil {
		t.Fatalf("create screener version: %v", err)
	}
	if !strings.HasPrefix(saved.ID.String(), "scr_") {
		t.Fatalf("screener id = %q, want a scr_ prefix", saved.ID)
	}
	if saved.Version != "v1" {
		t.Fatalf("first version = %q, want v1", saved.Version)
	}
	if saved.RuleSchemaVersion != screening.RuleSchemaVersion {
		t.Fatalf("rule schema version = %q, want %q", saved.RuleSchemaVersion, screening.RuleSchemaVersion)
	}
	if saved.DefinitionHash == "" {
		t.Fatal("definition hash must be recorded")
	}
	if !saved.CreatedAt.Equal(clock.Now()) {
		t.Fatalf("created_at = %s, want the injected clock %s", saved.CreatedAt, clock.Now())
	}
	def, err := saved.DefinitionOf()
	if err != nil {
		t.Fatalf("rebuild definition: %v", err)
	}
	if def.Name != "quality-momentum" || def.Ranking.Mode != screening.RankingSort || def.Selection.N != 5 {
		t.Fatalf("rebuilt definition lost data: %+v", def)
	}
}

// TestCreateScreenerVersionAppendsRevision pins the save semantics: a request
// carrying parent_id records a new immutable revision of that identity instead
// of minting a new screener.
func TestCreateScreenerVersionAppendsRevision(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	first, err := store.CreateScreenerVersion(ctx, screenerRequest("quality-momentum"))
	if err != nil {
		t.Fatalf("create first revision: %v", err)
	}

	second := screenerRequest("quality-momentum")
	second.ParentID = first.ID
	second.Definition.ParentID = first.ID
	second.Definition.Selection = screening.Selection{Mode: screening.SelectionAll}
	stored, err := store.CreateScreenerVersion(ctx, second)
	if err != nil {
		t.Fatalf("create second revision: %v", err)
	}
	if stored.ID != first.ID {
		t.Fatalf("revision id = %q, want the parent id %q", stored.ID, first.ID)
	}
	if stored.Version != "v2" {
		t.Fatalf("second version = %q, want v2", stored.Version)
	}
	if stored.ParentID != first.ID {
		t.Fatalf("parent_id = %q, want %q", stored.ParentID, first.ID)
	}

	// The earlier revision stays addressable and unchanged.
	original, err := store.GetScreenerVersion(ctx, first.ID, first.Version)
	if err != nil {
		t.Fatalf("get first revision: %v", err)
	}
	if original.Definition.Selection.Mode != screening.SelectionTopN {
		t.Fatal("saving a revision mutated the earlier one")
	}

	orphan := screenerRequest("orphan")
	orphan.ParentID = "scr_missing"
	if _, err := store.CreateScreenerVersion(ctx, orphan); err == nil {
		t.Fatal("unknown parent_id must be rejected")
	} else if code := domain.ErrorCode(err); code != domain.CodeResourceNotFound {
		t.Fatalf("unknown parent code = %q, want %q", code, domain.CodeResourceNotFound)
	}
}

func TestGetScreenerVersionRequiresBothParts(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	saved, err := store.CreateScreenerVersion(ctx, screenerRequest("quality-momentum"))
	if err != nil {
		t.Fatalf("create screener version: %v", err)
	}

	if _, err := store.GetScreenerVersion(ctx, saved.ID, saved.Version); err != nil {
		t.Fatalf("get exact revision: %v", err)
	}
	_, err = store.GetScreenerVersion(ctx, saved.ID, "v9")
	if code := domain.ErrorCode(err); code != domain.CodeResourceNotFound {
		t.Fatalf("missing revision code = %q, want %q", code, domain.CodeResourceNotFound)
	}
	_, err = store.GetScreenerVersion(ctx, saved.ID, "")
	if code := domain.ErrorCode(err); code != domain.CodeValidationInvalid {
		t.Fatalf("empty version code = %q, want %q", code, domain.CodeValidationInvalid)
	}
}

// TestListScreenerVersionsPagesByIDAndVersion proves the cursor keeps the
// (id, version) order across pages: revisions of one identity must stay
// contiguous and in order instead of interleaving with another identity.
func TestListScreenerVersionsPagesByIDAndVersion(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	first, err := store.CreateScreenerVersion(ctx, screenerRequest("pool-a"))
	if err != nil {
		t.Fatalf("create pool-a: %v", err)
	}
	for i := 0; i < 2; i++ {
		next := screenerRequest("pool-a")
		next.ParentID = first.ID
		next.Definition.ParentID = first.ID
		if _, err := store.CreateScreenerVersion(ctx, next); err != nil {
			t.Fatalf("append revision: %v", err)
		}
	}
	second, err := store.CreateScreenerVersion(ctx, screenerRequest("pool-b"))
	if err != nil {
		t.Fatalf("create pool-b: %v", err)
	}

	var seen []string
	cursor := ""
	for page := 0; page < 10; page++ {
		res, err := store.ListScreenerVersions(ctx, ports.ScreenerFilter{Sort: "id", AfterID: cursor, Limit: 1})
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		for _, v := range res.Items {
			seen = append(seen, screenerKey(v.ID.String(), v.Version.String()))
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(seen) != 4 {
		t.Fatalf("paged %d revisions, want 4 (%v)", len(seen), seen)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatalf("cursor order broken at %d: %v", i, seen)
		}
	}
	// Identifiers are generated, so either identity may sort first; what must
	// hold is that both identities' revisions stay contiguous and in order.
	for _, identity := range []domain.ID{first.ID, second.ID} {
		prefix := identity.String() + "@"
		var revisions []string
		var indices []int
		for i, key := range seen {
			if strings.HasPrefix(key, prefix) {
				revisions = append(revisions, key)
				indices = append(indices, i)
			}
		}
		want := []string{prefix + "v1", prefix + "v2", prefix + "v3"}
		if identity == second.ID {
			want = []string{prefix + "v1"}
		}
		if len(revisions) != len(want) {
			t.Fatalf("identity %s paged %v, want %v", identity, revisions, want)
		}
		for i := range want {
			if revisions[i] != want[i] {
				t.Fatalf("identity %s revisions = %v, want %v", identity, revisions, want)
			}
			if i > 0 && indices[i] != indices[i-1]+1 {
				t.Fatalf("identity %s revisions are not contiguous: %v", identity, seen)
			}
		}
	}
}

// TestScreenerDefinitionHashCoversRuleOnly mirrors the universe-version rule:
// the hash identifies the rule contract, not the label it was saved under.
func TestScreenerDefinitionHashCoversRuleOnly(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	left, err := store.CreateScreenerVersion(ctx, screenerRequest("alpha"))
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	right, err := store.CreateScreenerVersion(ctx, screenerRequest("beta"))
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if left.DefinitionHash != right.DefinitionHash {
		t.Fatalf("identical rules hashed differently: %s vs %s", left.DefinitionHash, right.DefinitionHash)
	}

	changed := screenerRequest("alpha")
	changed.Definition.Selection = screening.Selection{Mode: screening.SelectionAll}
	other, err := store.CreateScreenerVersion(ctx, changed)
	if err != nil {
		t.Fatalf("create changed rule: %v", err)
	}
	if other.DefinitionHash == left.DefinitionHash {
		t.Fatal("a different rule set must hash differently")
	}
}

func TestCreateScreenerVersionRejectsInvalidDefinition(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.CreateScreenerVersion(context.Background(), screenerRequest(" "))
	if err == nil {
		t.Fatal("an unnamed screener must be rejected")
	}
	if code := domain.ErrorCode(err); code != "screening.definition_invalid" {
		t.Fatalf("invalid definition code = %q, want screening.definition_invalid", code)
	}
}
