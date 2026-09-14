package domain

import (
	"time"
)

// M1-06 universe domain types. A UniverseVersion is one immutable
// member-selection contract bound to the snapshot it resolves against: the
// snapshot binding is mandatory so a backtest can never mix membership from
// one snapshot with decision data from another, and resolution always goes
// through a DataView pinned to that snapshot — never through current tables.

// UniverseKind selects how a universe version produces its members.
// UniverseStatic is a frozen, explicit list (静态研究口径: the list is a
// research decision, valid exactly as saved); UniverseHistoricalRule
// resolves from a snapshot dataset at every decision time, which is what
// keeps delisted and later-removed instruments inside their historical
// samples without survivorship backfill.
type UniverseKind string

const (
	UniverseStatic         UniverseKind = "static"
	UniverseHistoricalRule UniverseKind = "historical_rule"
)

// UniverseRule names the dataset a historical_rule universe resolves from,
// at one frequency. The rule carries no free-form predicate: member
// semantics live in the dataset's effective windows, so the same rule
// resolves identically at any as_of inside the same snapshot.
type UniverseRule struct {
	Dataset   string `json:"dataset"`
	Frequency string `json:"frequency"`
}

// Validate requires a concrete dataset and frequency.
func (r UniverseRule) Validate() error {
	if r.Dataset == "" {
		return NewError(CodeValidationInvalid, "universe: rule dataset is required")
	}
	if r.Frequency == "" {
		return NewError(CodeValidationInvalid, "universe: rule frequency is required")
	}
	return nil
}

// UniverseDefinition is the member-selection definition itself. Exactly one
// form is present — members for static, rule for historical_rule — so the
// canonical hash input is unambiguous.
type UniverseDefinition struct {
	Kind    UniverseKind  `json:"kind"`
	Members []ID          `json:"members,omitempty"`
	Rule    *UniverseRule `json:"rule,omitempty"`
}

// CanonicalMembers returns the static member list sorted and deduplicated.
// The result is never nil, so hashing and comparisons never depend on the
// order or multiplicity the caller spelled the list with.
func (d UniverseDefinition) CanonicalMembers() []ID {
	seen := make(map[ID]struct{}, len(d.Members))
	out := make([]ID, 0, len(d.Members))
	for _, id := range d.Members {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Canonical returns the hash input form: static members sorted and
// deduplicated, the rule copied as declared. Name and snapshot binding are
// deliberately excluded — the hash identifies the member-selection contract,
// not the label it was saved under or the snapshot it was saved against.
func (d UniverseDefinition) Canonical() UniverseDefinition {
	out := UniverseDefinition{Kind: d.Kind}
	switch d.Kind {
	case UniverseStatic:
		out.Members = d.CanonicalMembers()
	case UniverseHistoricalRule:
		if d.Rule != nil {
			rule := *d.Rule
			out.Rule = &rule
		}
	}
	return out
}

// Validate checks the definition contract: a known kind, exactly one of
// members/rule present, no empty member ids, and a fully populated rule.
func (d UniverseDefinition) Validate() error {
	switch d.Kind {
	case UniverseStatic:
		if len(d.Members) == 0 {
			return NewError(CodeValidationInvalid, "universe: static definition requires members")
		}
		for i, id := range d.Members {
			if id == "" {
				return NewError(CodeValidationInvalid, "universe: members[%d] is empty", i)
			}
		}
		if d.Rule != nil {
			return NewError(CodeValidationInvalid, "universe: static definition must not carry a rule")
		}
	case UniverseHistoricalRule:
		if d.Rule == nil {
			return NewError(CodeValidationInvalid, "universe: historical_rule definition requires a rule")
		}
		if len(d.Members) > 0 {
			return NewError(CodeValidationInvalid, "universe: historical_rule definition must not carry members")
		}
		if err := d.Rule.Validate(); err != nil {
			return Wrap(err, CodeValidationInvalid, "universe: rule invalid")
		}
	default:
		return NewError(CodeValidationInvalid, "universe: kind must be one of: static, historical_rule")
	}
	return nil
}

// UniverseSource records where a static pool came from: the screening run that
// selected it, the decision time it was selected at, the data it was selected
// against and the quality limits that exploration carried. It is provenance,
// not part of the member-selection contract, so it sits outside definition_hash
// — but a consumer must honour it: a pool selected at t may not be used for a
// decision before t, because that would backfill a choice nobody could have
// made.
type UniverseSource struct {
	ScreenRunID   ID        `json:"screen_run_id"`
	AsOf          time.Time `json:"as_of"`
	SnapshotHash  string    `json:"snapshot_hash"`
	QualityLimits []string  `json:"quality_limits"`
}

// Validate checks the source is complete enough to be evidence: without the run,
// the decision time and the data hash there is nothing a consumer could refuse
// or trust. An empty quality limit list is valid — it means the exploration
// reported no caveats.
func (s UniverseSource) Validate() error {
	if s.ScreenRunID == "" {
		return NewError(CodeValidationInvalid, "universe: source screen_run_id is required")
	}
	if s.AsOf.IsZero() {
		return NewError(CodeValidationInvalid, "universe: source as_of is required")
	}
	if s.SnapshotHash == "" {
		return NewError(CodeValidationInvalid, "universe: source snapshot_hash is required")
	}
	return nil
}

// UniverseVersionRequest is the input to CreateUniverseVersion. Creation is
// not idempotent across requests: every save is a new version, mirroring
// screening's semantics where saving a list records a new named revision.
type UniverseVersionRequest struct {
	Name       string             `json:"name"`
	SnapshotID ID                 `json:"snapshot_id"`
	Definition UniverseDefinition `json:"definition"`
	// Source is set only when the pool is saved from a screening run.
	Source *UniverseSource `json:"-"`
}

// Validate checks the name, the snapshot binding, the definition and the
// optional source evidence.
func (r UniverseVersionRequest) Validate() error {
	if r.Name == "" {
		return NewError(CodeValidationInvalid, "universe: name is required")
	}
	if r.SnapshotID == "" {
		return NewError(CodeValidationInvalid, "universe: snapshot_id is required")
	}
	if r.Source != nil {
		if err := r.Source.Validate(); err != nil {
			return err
		}
	}
	return r.Definition.Validate()
}

// UniverseVersion is one immutable member-selection version. Definition is
// the canonical (sorted, deduplicated) form as persisted; DefinitionHash is
// the checksum over the canonical JSON of the definition alone.
type UniverseVersion struct {
	ID             ID                 `json:"id"`
	Name           string             `json:"name"`
	SnapshotID     ID                 `json:"snapshot_id"`
	Definition     UniverseDefinition `json:"definition"`
	DefinitionHash string             `json:"definition_hash"`
	Source         *UniverseSource    `json:"source"`
	CreatedAt      time.Time          `json:"created_at"`
}
