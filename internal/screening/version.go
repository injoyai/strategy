package screening

import (
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// Version is one immutable screener version as persisted: the canonical
// definition payload plus the labels it was saved under. ID is the screener
// identity and survives across revisions; Version is the immutable revision
// label, so (ID, Version) addresses exactly one frozen rule set. Name,
// Description and ParentID are deliberately outside the canonical payload —
// the definition hash identifies the rule contract itself.
type Version struct {
	ID                domain.ID
	Version           domain.ID
	Name              string
	Description       string
	ParentID          domain.ID
	RuleSchemaVersion string
	Definition        WireDefinition
	DefinitionHash    string
	CreatedAt         time.Time
}

// VersionRequest is the save input for one new immutable version.
type VersionRequest struct {
	Name        string
	Description string
	ParentID    domain.ID
	Definition  Definition
}

// Validate checks the save-time contract that does not need a snapshot.
// Structural rules (node ids, tree shape, operators, ranking/selection and
// display columns) are enforced here; binding types and units are checked at
// preflight, where the snapshot makes the input catalog knowable. Failures
// collapse into one error carrying the first issue, like screening.Run does.
func (r VersionRequest) Validate() error {
	return r.Definition.Validate()
}

// VersionOf projects a stored version back onto its definition. The stored
// payload is re-validated on load, so a row written by another writer cannot
// be executed as a definition this build would never have accepted.
func (v Version) DefinitionOf() (Definition, error) {
	def, err := DefinitionOfWire(v.Definition, v.Name, v.Description, v.ParentID)
	if err != nil {
		return Definition{}, err
	}
	if err := def.Validate(); err != nil {
		return Definition{}, err
	}
	return def, nil
}

// Validate is the defensive re-check used when loading a stored version.
func (d Definition) Validate() error {
	if issues := Validate(d, nil, DefaultLimits()); len(issues) > 0 {
		return domain.NewError(codeDefinitionInvalid, "screener definition is invalid: %s", issues[0].Message)
	}
	return nil
}

// WireScreenerOf renders the wire form of one version for the API.
func WireScreenerOf(v Version) WireScreener {
	return WireScreener{
		ID:                v.ID,
		Version:           v.Version,
		Name:              v.Name,
		Description:       v.Description,
		InputBindings:     v.Definition.InputBindings,
		ConditionTree:     v.Definition.ConditionTree,
		Ranking:           v.Definition.Ranking,
		Selection:         v.Definition.Selection,
		DisplayColumns:    v.Definition.DisplayColumns,
		ParentID:          v.ParentID,
		RuleSchemaVersion: v.RuleSchemaVersion,
		CreatedAt:         v.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}
