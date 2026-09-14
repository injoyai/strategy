package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

type SessionState string

const (
	StateInitializing   SessionState = "initializing"
	StateAwaitingAction SessionState = "awaiting_action"
	StateAdvancing      SessionState = "advancing"
	StateClosing        SessionState = "closing"
	StateCompleted      SessionState = "completed"
	StateFailed         SessionState = "failed"
)

type CommandKind string

const (
	CmdAdvance     CommandKind = "advance"
	CmdSubmitOrder CommandKind = "submit_order"
	CmdCancelOrder CommandKind = "cancel_order"
	CmdClose       CommandKind = "close"
	// CmdScreen names an in-session screening run. It is a command like the
	// others because it consumes a revision and belongs to the session's
	// serialized log, but it changes no account or time fact.
	CmdScreen CommandKind = "screen"
)

// ModelRef pins one immutable model policy version plus explicit parameters.
// Market, fill, cost, valuation and metrics behaviour comes only from
// approved versioned models; missing or unresolvable refs fail closed and
// there are never implicit defaults.
type ModelRef struct {
	Ref    domain.VersionRef
	Params map[string]any
}

func (m ModelRef) Validate() error { return m.Ref.Validate() }

// Config is the frozen set of replay session inputs. The server derives every
// decision time from these inputs plus the current session; the browser never
// supplies a historical time.
type Config struct {
	Name            string
	SnapshotID      domain.ID
	UniverseRef     domain.VersionRef
	From            time.Time
	To              time.Time
	Timezone        string
	DayEndPolicy    string
	DecisionPolicy  string
	WarmupDays      int
	StrictPIT       bool
	InitialCash     domain.Money
	MarketRules     ModelRef
	FillModel       ModelRef
	CostModel       ModelRef
	ValuationPolicy ModelRef
	MetricsPolicy   ModelRef
	BenchmarkRef    domain.VersionRef
	ParentID        domain.ID
}

func (c Config) Validate() []domain.Issue {
	var issues []domain.Issue
	add := func(code, path, msg string) {
		issues = append(issues, domain.Issue{Code: code, Path: path, Message: msg, Severity: "error"})
	}
	if strings.TrimSpace(c.Name) == "" {
		add(codeNameEmpty, "name", "replay session name is required")
	}
	if c.SnapshotID == "" {
		add(codeConditionsMissing, "snapshot_id", "snapshot_id is required")
	}
	if err := c.UniverseRef.Validate(); err != nil {
		add(codeConditionsMissing, "universe_ref", err.Error())
	}
	if c.From.IsZero() || c.To.IsZero() || !c.To.After(c.From) {
		add(codeRangeInvalid, "range", "range must be a non-empty [from,to) interval")
	}
	if strings.TrimSpace(c.Timezone) == "" {
		add(codeTimezoneInvalid, "timezone", "decision timezone is required")
	}
	for _, p := range []struct {
		path string
		pol  string
	}{{"day_end_policy", c.DayEndPolicy}, {"decision_policy", c.DecisionPolicy}} {
		if strings.TrimSpace(p.pol) == "" {
			add(codePolicyInvalid, p.path, "policy is required")
		}
	}
	for _, r := range []struct {
		path string
		ref  ModelRef
	}{{"market_rules", c.MarketRules}, {"fill_model", c.FillModel}, {"cost_model", c.CostModel},
		{"valuation_policy", c.ValuationPolicy}, {"metrics_policy", c.MetricsPolicy}} {
		if err := r.ref.Validate(); err != nil {
			add(codeModelInvalid, r.path, err.Error())
		}
	}
	if c.InitialCash.Currency == "" {
		add(codeCashInvalid, "initial_cash", "initial cash currency is required")
	}
	return issues
}

// Hash returns a stable, canonically-serialized fingerprint of the frozen
// config. Equal configs hash equal; the fingerprint is part of the session.
func (c Config) Hash() string {
	h := sha256.New()
	field := func(v string) { _, _ = h.Write([]byte(v)); _, _ = h.Write([]byte{0}) }
	field(c.Name)
	field(c.SnapshotID.String())
	field(c.UniverseRef.ID.String() + "/" + c.UniverseRef.Version)
	field(c.From.UTC().Format(time.RFC3339Nano))
	field(c.To.UTC().Format(time.RFC3339Nano))
	field(c.Timezone)
	field(c.DayEndPolicy)
	field(c.DecisionPolicy)
	field(fmt.Sprint(c.WarmupDays))
	field(fmt.Sprint(c.StrictPIT))
	field(c.InitialCash.Currency)
	field(string(c.InitialCash.Amount))
	for _, m := range []ModelRef{c.MarketRules, c.FillModel, c.CostModel, c.ValuationPolicy, c.MetricsPolicy} {
		field(m.Ref.ID.String() + "/" + m.Ref.Version)
		keys := make([]string, 0, len(m.Params))
		for k := range m.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			field(k)
			field(fmt.Sprintf("%v", m.Params[k]))
		}
	}
	field(c.BenchmarkRef.ID.String() + "/" + c.BenchmarkRef.Version)
	field(c.ParentID.String())
	return hex.EncodeToString(h.Sum(nil))
}

// Session is the mutable replay aggregate. Configured immutable inputs are
// covered by ConfigHash; only the state, current decision point, revision and
// end reason evolve.
type Session struct {
	ID          domain.ID
	ConfigHash  string
	State       SessionState
	CurrentAsOf time.Time
	Revision    int
	EndReason   string
	ParentID    domain.ID
}

// DecisionTime is the historical time the server associates with the current
// awaited state. It is always derived from the session, never from a client.
func (s Session) DecisionTime() time.Time { return s.CurrentAsOf }

// Command is a server-serialized, optimistic-concurrency intent. It carries
// no historical decision time: the server derives decision_at from the
// session's current decision point. SubmittedAt for audit is a caller-supplied
// real timestamp and never influences the history clock.
type Command struct {
	SessionID        domain.ID
	CommandID        domain.ID
	ExpectedRevision int
	Kind             CommandKind
}

var transitions = map[SessionState]map[SessionState]bool{
	StateInitializing:   {StateAwaitingAction: true, StateFailed: true},
	StateAwaitingAction: {StateAdvancing: true, StateClosing: true, StateFailed: true},
	StateAdvancing:      {StateAwaitingAction: true, StateFailed: true},
	StateClosing:        {StateCompleted: true, StateFailed: true},
	StateCompleted:      {},
	StateFailed:         {},
}

func CanTransition(from, to SessionState) bool {
	return transitions[from][to]
}
