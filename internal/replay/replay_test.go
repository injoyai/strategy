package replay

import (
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

func newMoney(t *testing.T, amount, currency string) domain.Money {
	t.Helper()
	d, err := domain.ParseDecimal(amount)
	if err != nil {
		t.Fatalf("parse decimal: %v", err)
	}
	m, err := domain.NewMoney(d, currency)
	if err != nil {
		t.Fatalf("new money: %v", err)
	}
	return m
}

func ref(t *testing.T) domain.VersionRef {
	t.Helper()
	r := domain.VersionRef{ID: domain.ID("model-fill-v1"), Version: "1"}
	if err := r.Validate(); err != nil {
		t.Fatalf("invalid ref: %v", err)
	}
	return r
}

func baseConfig(t *testing.T) Config {
	t.Helper()
	from := time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC)
	return Config{
		Name:            "replay-1",
		SnapshotID:      domain.ID("snap-1"),
		UniverseRef:     domain.VersionRef{ID: domain.ID("univ-1"), Version: "3"},
		From:            from,
		To:              from.AddDate(0, 0, 5),
		Timezone:        "Asia/Shanghai",
		DayEndPolicy:    "close_of_bar",
		CashPolicy:      "settle_on_fill",
		EndPolicy:       "cancel_remainder_keep_positions",
		OrderTypes:      []string{"market", "limit"},
		WarmupDays:      5,
		StrictPIT:       true,
		InitialCash:     newMoney(t, "1000000", "USD"),
		MarketRules:     ModelRef{Ref: ref(t)},
		FillModel:       ModelRef{Ref: ref(t)},
		CostModel:       ModelRef{Ref: ref(t)},
		ValuationPolicy: ModelRef{Ref: ref(t)},
		MetricsPolicy:   ModelRef{Ref: ref(t)},
		BenchmarkRef:    domain.VersionRef{ID: domain.ID("bench-1"), Version: "1"},
	}
}

func decisionClock() DecisionClock {
	base := time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC)
	return DecisionClock{Points: []time.Time{
		base,
		base.AddDate(0, 0, 1),
		base.AddDate(0, 0, 2),
		base.AddDate(0, 0, 3),
	}}
}

func cmd(s Session, kind CommandKind, expected int) Command {
	return Command{SessionID: s.ID, CommandID: domain.ID("cmd-1"), ExpectedRevision: expected, Kind: kind}
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("expected non-nil error")
	}
	return domain.ErrorCode(err)
}

func TestStateMachineTransitions(t *testing.T) {
	cases := []struct {
		from, to SessionState
		ok       bool
	}{
		{StateInitializing, StateAwaitingAction, true},
		{StateInitializing, StateFailed, true},
		{StateAwaitingAction, StateAdvancing, true},
		{StateAwaitingAction, StateClosing, true},
		{StateAwaitingAction, StateFailed, true},
		{StateAdvancing, StateAwaitingAction, true},
		{StateAdvancing, StateFailed, true},
		{StateAdvancing, StateCompleted, false},
		{StateClosing, StateCompleted, true},
		{StateCompleted, StateAwaitingAction, false},
		{StateCompleted, StateFailed, false},
		{StateCompleted, StateFailed, false},
		{StateFailed, StateAwaitingAction, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("CanTransition(%s,%s)=%v want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestInitAssignsServerDecisionPoint(t *testing.T) {
	s, err := Init(domain.ID("rlp-1"), baseConfig(t), decisionClock())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if s.State != StateAwaitingAction {
		t.Fatalf("state=%s want awaiting_action", s.State)
	}
	if s.Revision != 0 {
		t.Fatalf("revision=%d want 0", s.Revision)
	}
	base := time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC)
	if got := s.DecisionTime(); !got.Equal(base) {
		t.Fatalf("decision time=%v want %v (must come from server clock, not caller)", got, base)
	}
}

func TestInitRejectsEndOfRange(t *testing.T) {
	clk := DecisionClock{Points: []time.Time{time.Date(2026, 3, 9, 15, 30, 0, 0, time.UTC)}}
	if _, err := Init(domain.ID("rlp-2"), baseConfig(t), clk); codeOf(t, err) != codeEndOfRange {
		t.Fatalf("want end_of_range, got %v", err)
	}
}

func TestApplyOrderIsClientTimeFreeAndAdvancesRevision(t *testing.T) {
	s, err := Init(domain.ID("rlp-1"), baseConfig(t), decisionClock())
	if err != nil {
		t.Fatal(err)
	}
	starter := s
	out, err := ApplyOrder(s, cmd(s, CmdSubmitOrder, s.Revision))
	if err != nil {
		t.Fatalf("apply order: %v", err)
	}
	if out.Revision != 1 {
		t.Fatalf("revision=%d want 1", out.Revision)
	}
	if !out.DecisionTime().Equal(starter.DecisionTime()) {
		t.Fatalf("order must not move the decision time; got %v want %v", out.DecisionTime(), starter.DecisionTime())
	}
}

func TestTwoTabCompetitionConflicts(t *testing.T) {
	s, err := Init(domain.ID("rlp-1"), baseConfig(t), decisionClock())
	if err != nil {
		t.Fatal(err)
	}
	one := cmd(s, CmdSubmitOrder, s.Revision)
	two := cmd(s, CmdCancelOrder, s.Revision)
	fresh, err := ApplyOrder(s, one)
	if err != nil {
		t.Fatalf("first tab submit: %v", err)
	}
	// second tab still targets the old revision but must be re-checked against the
	// freshest committed session; the now-stale expected revision => conflict.
	if _, err := ApplyOrder(fresh, two); codeOf(t, err) != codeRevisionConflict {
		t.Fatalf("second tab must conflict; got %v", err)
	}
	// a retried advance with the stale expected revision also conflicts
	if _, _, err := OpenAdvance(fresh, cmd(fresh, CmdAdvance, s.Revision), decisionClock()); err == nil {
		t.Fatal("stale advance revision must conflict")
	}
}

func TestAdvanceMovesToServerNextDecision(t *testing.T) {
	s, err := Init(domain.ID("rlp-1"), baseConfig(t), decisionClock())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC)
	want := base.AddDate(0, 0, 1)

	adv, next, err := OpenAdvance(s, cmd(s, CmdAdvance, s.Revision), decisionClock())
	if err != nil {
		t.Fatalf("open advance: %v", err)
	}
	if adv.State != StateAdvancing {
		t.Fatalf("state=%s want advancing", adv.State)
	}
	if !next.Equal(want) {
		t.Fatalf("server next decision=%v want %v", next, want)
	}
	if !adv.DecisionTime().Equal(base) {
		t.Fatalf("open advance must not publish new as_of yet; got %v", adv.DecisionTime())
	}
	// orders rejected while advancing
	if _, err := ApplyOrder(adv, cmd(adv, CmdSubmitOrder, adv.Revision)); codeOf(t, err) != codeStateInvalid {
		t.Fatalf("order during advancing must be state_invalid; got %v", err)
	}
	com, err := CommitAdvance(adv, cmd(adv, CmdAdvance, adv.Revision), next)
	if err != nil {
		t.Fatalf("commit advance: %v", err)
	}
	if com.State != StateAwaitingAction {
		t.Fatalf("state=%s want awaiting_action", com.State)
	}
	if !com.DecisionTime().Equal(want) {
		t.Fatalf("committed decision time=%v want %v", com.DecisionTime(), want)
	}
	if com.Revision != 1 {
		t.Fatalf("revision=%d want 1", com.Revision)
	}
}

func TestAdvanceFailsWhenNoNextDay(t *testing.T) {
	s, err := Init(domain.ID("rlp-1"), baseConfig(t), DecisionClock{Points: []time.Time{
		time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenAdvance(s, cmd(s, CmdAdvance, s.Revision), DecisionClock{}); codeOf(t, err) != codeEndOfRange {
		t.Fatalf("want end_of_range, got %v", err)
	}
}

func TestCloseSequence(t *testing.T) {
	s, err := Init(domain.ID("rlp-1"), baseConfig(t), decisionClock())
	if err != nil {
		t.Fatal(err)
	}
	c, err := OpenClose(s, cmd(s, CmdClose, s.Revision))
	if err != nil {
		t.Fatalf("open close: %v", err)
	}
	if c.State != StateClosing {
		t.Fatalf("state=%s want closing", c.State)
	}
	if _, err := ApplyOrder(c, cmd(c, CmdSubmitOrder, c.Revision)); codeOf(t, err) != codeStateInvalid {
		t.Fatalf("order during closing must be rejected; got %v", err)
	}
	done, err := CommitClose(c, cmd(c, CmdClose, c.Revision), "user")
	if err != nil {
		t.Fatalf("commit close: %v", err)
	}
	if done.State != StateCompleted {
		t.Fatalf("state=%s want completed", done.State)
	}
	if done.Revision != 1 {
		t.Fatalf("revision=%d want 1", done.Revision)
	}
}

func TestFailLocksTerminal(t *testing.T) {
	if _, err := Fail(Session{ID: domain.ID("x"), State: StateCompleted}); codeOf(t, err) != codeStateInvalid {
		t.Fatalf("want terminal fail rejected, got %v", err)
	}
	f, err := Fail(Session{ID: domain.ID("x"), State: StateAdvancing})
	if err != nil || f.State != StateFailed {
		t.Fatalf("fail=%v state=%s", err, f.State)
	}
}

func TestSessionMismatch(t *testing.T) {
	s := Session{ID: domain.ID("expected"), State: StateAwaitingAction, Revision: 0}
	bad := cmd(s, CmdSubmitOrder, 0)
	bad.SessionID = domain.ID("other")
	if _, err := ApplyOrder(s, bad); codeOf(t, err) != codeSessionMismatch {
		t.Fatalf("want session_mismatch, got %v", err)
	}
}

func TestConfigHashDeterministic(t *testing.T) {
	a := baseConfig(t)
	b := baseConfig(t)
	if a.Hash() != b.Hash() {
		t.Fatal("equal configs must hash equal")
	}
	b.Name = "replay-2"
	if a.Hash() == b.Hash() {
		t.Fatal("changing config must change hash")
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := baseConfig(t)
	cfg.Name = " "
	if issues := cfg.Validate(); !containsCode(t, issues, codeNameEmpty) {
		t.Fatalf("want name_empty, got %+v", issues)
	}
	cfg = baseConfig(t)
	cfg.FillModel = ModelRef{}
	if issues := cfg.Validate(); !containsCode(t, issues, codeModelInvalid) {
		t.Fatalf("want model_invalid, got %+v", issues)
	}
}

// TestConfigRequiresTheContractsExplicitFields pins the four policy inputs the
// contract declares as explicit validated values. Each one decides something
// with money consequences (when a day ends, when cash settles, what happens to
// open orders at the end, which order types may be submitted), so an absent or
// degenerate value is refused instead of defaulted.
func TestConfigRequiresTheContractsExplicitFields(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Config)
		code string
	}{
		{"no day-end policy", func(cfg *Config) { cfg.DayEndPolicy = "" }, codePolicyInvalid},
		{"no cash policy", func(cfg *Config) { cfg.CashPolicy = " " }, codePolicyInvalid},
		{"no end policy", func(cfg *Config) { cfg.EndPolicy = "" }, codePolicyInvalid},
		{"no order types", func(cfg *Config) { cfg.OrderTypes = nil }, codePolicyInvalid},
		{"blank order type", func(cfg *Config) { cfg.OrderTypes = []string{"market", " "} }, codePolicyInvalid},
		{"duplicate order type", func(cfg *Config) { cfg.OrderTypes = []string{"market", "market"} }, codePolicyInvalid},
		{"negative warmup", func(cfg *Config) { cfg.WarmupDays = -1 }, codeRangeInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := baseConfig(t)
			c.edit(&cfg)
			if issues := cfg.Validate(); !containsCode(t, issues, c.code) {
				t.Fatalf("want %s, got %+v", c.code, issues)
			}
		})
	}
}

func TestConfigHashCoversThePolicySet(t *testing.T) {
	base := baseConfig(t)
	reordered := baseConfig(t)
	reordered.OrderTypes = []string{"limit", "market"}
	// Order types are a set of capabilities, so listing the same set differently
	// is the same session.
	if base.Hash() != reordered.Hash() {
		t.Fatal("order types must hash as a set")
	}
	for name, edit := range map[string]func(*Config){
		"cash policy": func(cfg *Config) { cfg.CashPolicy = "immediate" },
		"end policy":  func(cfg *Config) { cfg.EndPolicy = "liquidate_at_close" },
		"order types": func(cfg *Config) { cfg.OrderTypes = []string{"market"} },
	} {
		changed := baseConfig(t)
		edit(&changed)
		if base.Hash() == changed.Hash() {
			t.Fatalf("a different %s must change the config hash", name)
		}
	}
}

func containsCode(t *testing.T, issues []domain.Issue, code string) bool {
	t.Helper()
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
