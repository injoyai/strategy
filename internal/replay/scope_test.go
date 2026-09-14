package replay

import (
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// scopeSession builds one session with a frozen config and one committed
// advance, so its decision point is later than its configured start.
func scopeSession(t *testing.T) (Session, Config) {
	t.Helper()
	cfg := baseConfig(t)
	s, err := Init(domain.ID("rlp-1"), cfg, decisionClock())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	advancing, _, err := OpenAdvance(s, cmd(s, CmdAdvance, s.Revision), decisionClock())
	if err != nil {
		t.Fatalf("open advance: %v", err)
	}
	s, err = CommitAdvance(advancing, cmd(advancing, CmdAdvance, advancing.Revision), decisionClock().Points[1])
	if err != nil {
		t.Fatalf("commit advance: %v", err)
	}
	return s, cfg
}

func scopeQuery(scope ReadScope) DataQuery {
	return DataQuery{
		SessionID:     scope.SessionID,
		Dataset:       "bar",
		Frequency:     "daily",
		InstrumentIDs: []domain.ID{"INST_A"},
		Fields:        []string{"close"},
	}
}

func scopeOf(t *testing.T, s Session, cfg Config) ReadScope {
	t.Helper()
	scope, err := ScopeOf(s, cfg, "hash-1")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	return scope
}

func TestScopeOfRequiresTheSessionsOwnFrozenConfig(t *testing.T) {
	s, cfg := scopeSession(t)
	if _, err := ScopeOf(s, cfg, "hash-1"); err != nil {
		t.Fatalf("scope of matching config: %v", err)
	}
	// A session paired with another session's config would silently read the
	// wrong universe, so the pairing is checked rather than assumed.
	other := baseConfig(t)
	other.Name = "replay-2"
	if _, err := ScopeOf(s, other, "hash-1"); codeOf(t, err) != codeSessionMismatch {
		t.Fatalf("foreign config: code = %q, want %q", codeOf(t, err), codeSessionMismatch)
	}
	if _, err := ScopeOf(s, cfg, "  "); codeOf(t, err) != codeConditionsMissing {
		t.Fatalf("blank snapshot hash: code = %q, want %q", codeOf(t, err), codeConditionsMissing)
	}
	stateless := s
	stateless.CurrentAsOf = time.Time{}
	if _, err := ScopeOf(stateless, cfg, "hash-1"); codeOf(t, err) != codeConditionsMissing {
		t.Fatalf("session without a decision point: code = %q, want %q", codeOf(t, err), codeConditionsMissing)
	}
}

func TestReadWithoutARangeSpansTheSessionHistoryUpToTheDecisionPoint(t *testing.T) {
	s, cfg := scopeSession(t)
	scope := scopeOf(t, s, cfg)
	iv, err := scope.Resolve(scopeQuery(scope))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !iv.From.Equal(cfg.From.UTC()) || !iv.To.Equal(s.CurrentAsOf.UTC()) {
		t.Fatalf("interval = [%s,%s), want the session history up to the decision point [%s,%s)",
			iv.From, iv.To, cfg.From.UTC(), s.CurrentAsOf.UTC())
	}
}

func TestReadEndingAtTheDecisionPointIsLegalAndBeyondItIsRefused(t *testing.T) {
	s, cfg := scopeSession(t)
	scope := scopeOf(t, s, cfg)
	// The bound is exclusive, so a range that ends exactly at the decision point
	// reads what was knowable then.
	atDecision := scopeQuery(scope)
	atDecision.Range = &domain.Interval{From: s.CurrentAsOf.AddDate(0, 0, -3), To: s.CurrentAsOf}
	if _, err := scope.Resolve(atDecision); err != nil {
		t.Fatalf("range ending at the decision point: %v", err)
	}
	// One nanosecond past it is a read of the future: refused, not clipped.
	beyond := scopeQuery(scope)
	beyond.Range = &domain.Interval{From: s.CurrentAsOf.AddDate(0, 0, -3), To: s.CurrentAsOf.Add(time.Nanosecond)}
	if _, err := scope.Resolve(beyond); codeOf(t, err) != codeFutureRead {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeFutureRead)
	}
	// A window lying entirely after the decision point is the same refusal.
	after := scopeQuery(scope)
	after.Range = &domain.Interval{From: s.CurrentAsOf.AddDate(0, 0, 1), To: s.CurrentAsOf.AddDate(0, 0, 2)}
	if _, err := scope.Resolve(after); codeOf(t, err) != codeFutureRead {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeFutureRead)
	}
}

func TestReadRefusesAMalformedOrForeignQuery(t *testing.T) {
	s, cfg := scopeSession(t)
	scope := scopeOf(t, s, cfg)
	cases := []struct {
		name string
		edit func(q *DataQuery)
		code string
	}{
		{"other session", func(q *DataQuery) { q.SessionID = "rlp-2" }, codeSessionMismatch},
		{"no dataset", func(q *DataQuery) { q.Dataset = " " }, codeQueryInvalid},
		{"no frequency", func(q *DataQuery) { q.Frequency = "" }, codeQueryInvalid},
		{"no instruments", func(q *DataQuery) { q.InstrumentIDs = nil }, codeQueryInvalid},
		{"no fields", func(q *DataQuery) { q.Fields = nil }, codeQueryInvalid},
		{"blank field", func(q *DataQuery) { q.Fields = []string{" "} }, codeQueryInvalid},
		{"inverted range", func(q *DataQuery) {
			q.Range = &domain.Interval{From: s.CurrentAsOf, To: s.CurrentAsOf.AddDate(0, 0, -1)}
		}, domain.CodeValidationInterval},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := scopeQuery(scope)
			c.edit(&q)
			if _, err := scope.Resolve(q); codeOf(t, err) != c.code {
				t.Fatalf("code = %q, want %q", codeOf(t, err), c.code)
			}
		})
	}
}

func TestSessionWithoutHistoryBeforeItsDecisionPointCannotReadARangelessWindow(t *testing.T) {
	// The first decision point equals the configured start, so there is no
	// history to derive: the caller must name a range (which may reach before the
	// session start) instead of receiving an empty chart with no reason.
	cfg := baseConfig(t)
	s, err := Init(domain.ID("rlp-1"), cfg, decisionClock())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	scope := scopeOf(t, s, cfg)
	if _, err := scope.Resolve(scopeQuery(scope)); codeOf(t, err) != codeRangeInvalid {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeRangeInvalid)
	}
	explicit := scopeQuery(scope)
	explicit.Range = &domain.Interval{From: cfg.From.AddDate(0, 0, -5), To: s.CurrentAsOf}
	if _, err := scope.Resolve(explicit); err != nil {
		t.Fatalf("explicit range: %v", err)
	}
}

func TestCacheKeySeparatesSessionRevisionWindowAndComputation(t *testing.T) {
	s, cfg := scopeSession(t)
	scope := scopeOf(t, s, cfg)
	base, err := scope.CacheKey(scopeQuery(scope), "derived/1")
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}

	widened := scopeQuery(scope)
	widened.InstrumentIDs = []domain.ID{"INST_A", "INST_B"}
	widened.Fields = []string{"close", "volume"}
	sameSet, err := scope.CacheKey(widened, "derived/1")
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	if base == sameSet {
		t.Fatal("a different instrument/field set must not share a key")
	}
	// The same set listed in another order is the same read, so it shares a key.
	reordered := scopeQuery(scope)
	reordered.InstrumentIDs = []domain.ID{"INST_B", "INST_A"}
	reordered.Fields = []string{"volume", "close"}
	again, err := scope.CacheKey(reordered, "derived/1")
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	if again != sameSet {
		t.Fatal("the same set listed in another order must share a key")
	}

	revised := scope
	revised.Revision++
	if key, err := revised.CacheKey(scopeQuery(scope), "derived/1"); err != nil || key == base {
		t.Fatalf("key = %q (err %v), want a different key for another revision", key, err)
	}
	later := scope
	later.AsOf = scope.AsOf.AddDate(0, 0, 1)
	if key, err := later.CacheKey(scopeQuery(scope), "derived/1"); err != nil || key == base {
		t.Fatalf("key = %q (err %v), want a different key for another decision point", key, err)
	}
	if key, err := scope.CacheKey(scopeQuery(scope), "derived/2"); err != nil || key == base {
		t.Fatalf("key = %q (err %v), want a different key for another computation version", key, err)
	}

	// A key can never be minted for a query the scope refuses, and the version is
	// required: a cache that ignored it would serve yesterday's derivation.
	future := scopeQuery(scope)
	future.Range = &domain.Interval{From: scope.AsOf, To: scope.AsOf.Add(time.Hour)}
	if _, err := scope.CacheKey(future, "derived/1"); codeOf(t, err) != codeFutureRead {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeFutureRead)
	}
	if _, err := scope.CacheKey(scopeQuery(scope), ""); codeOf(t, err) != codeQueryInvalid {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeQueryInvalid)
	}
}

func TestScreenRunInputsComeOnlyFromTheSession(t *testing.T) {
	s, cfg := scopeSession(t)
	screener := domain.VersionRef{ID: "scr-1", Version: "v2"}
	inputs, err := ScreenRunInputsOf(s, cfg, cmd(s, CmdScreen, s.Revision), screener)
	if err != nil {
		t.Fatalf("screen inputs: %v", err)
	}
	if inputs.SnapshotID != cfg.SnapshotID || !inputs.Universe.Match(cfg.UniverseRef) ||
		!inputs.AsOf.Equal(s.CurrentAsOf) || inputs.Timezone != cfg.Timezone || inputs.StrictPIT != cfg.StrictPIT {
		t.Fatalf("inputs = %+v, want every bound derived from the session and its config", inputs)
	}
	if !inputs.Screener.Match(screener) || inputs.CommandID != "cmd-1" {
		t.Fatalf("inputs = %+v, want the named screener and command carried through", inputs)
	}

	if _, err := ScreenRunInputsOf(s, cfg, cmd(s, CmdAdvance, s.Revision), screener); codeOf(t, err) != codeCommandKind {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeCommandKind)
	}
	if _, err := ScreenRunInputsOf(s, cfg, cmd(s, CmdScreen, s.Revision+1), screener); codeOf(t, err) != codeRevisionConflict {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeRevisionConflict)
	}
	advancing := s
	advancing.State = StateAdvancing
	if _, err := ScreenRunInputsOf(advancing, cfg, cmd(advancing, CmdScreen, advancing.Revision), screener); codeOf(t, err) != codeStateInvalid {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeStateInvalid)
	}
	if _, err := ScreenRunInputsOf(s, cfg, cmd(s, CmdScreen, s.Revision), domain.VersionRef{ID: "scr-1"}); codeOf(t, err) != codeQueryInvalid {
		t.Fatalf("code = %q, want %q", codeOf(t, err), codeQueryInvalid)
	}
}
