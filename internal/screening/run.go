package screening

import (
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// M1S S2 run evidence: the persisted form of one screening run. The rule
// engine above stays pure — this file only describes what a run freezes and
// publishes, so the data plane can store it without re-deriving anything.

// EngineVersion identifies the selection pipeline that produced a run's
// result. It is written on every run row so a future change in selection
// semantics (percentile rule, tie-breaking, stage classification) can never be
// confused with results an older engine published.
const EngineVersion = "screening-engine/1"

// CodeResultNotReady is the stable code reported when a caller reads a run's
// rows or explanations before the run has published them. It lives here
// because both the persistence layer and the HTTP mapping need it, and
// screening sits below both.
const CodeResultNotReady = "screenrun.result_not_ready"

// WireRunConfig is one frozen ScreenRunCreate payload: every input a run was
// submitted with, exactly as the contract spells it. It is stored verbatim so
// the configuration a published result was computed from is always readable,
// even after the referenced versions move on.
type WireRunConfig struct {
	ScreenerRef         domain.VersionRef `json:"screener_ref"`
	SnapshotID          domain.ID         `json:"snapshot_id"`
	UniverseRef         domain.VersionRef `json:"universe_ref"`
	AsOf                time.Time         `json:"as_of"`
	DecisionTimezone    string            `json:"decision_timezone"`
	StrictPIT           bool              `json:"strict_pit"`
	RequiredValuePolicy string            `json:"required_value_policy"`
	SourceRunID         domain.ID         `json:"source_run_id,omitempty"`
}

// RunRequest is the input to CreateRun: the frozen configuration plus the
// version labels that identify how the result was produced.
type RunRequest struct {
	JobID                domain.ID
	Config               WireRunConfig
	EngineVersion        string
	ScoringPolicyVersion string
	SnapshotHash         string
}

// Validate checks the run record contract: a run always belongs to a job, pins
// a snapshot and a decision time, and names the pipeline that produced it.
func (r RunRequest) Validate() error {
	if r.JobID == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run job_id is required")
	}
	if err := r.Config.ScreenerRef.Validate(); err != nil {
		return err
	}
	if err := r.Config.UniverseRef.Validate(); err != nil {
		return err
	}
	if r.Config.SnapshotID == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run snapshot_id is required")
	}
	if r.Config.AsOf.IsZero() {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run as_of is required")
	}
	if r.Config.DecisionTimezone == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run decision_timezone is required")
	}
	if r.Config.RequiredValuePolicy == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run required_value_policy is required")
	}
	if r.SnapshotHash == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run snapshot_hash is required")
	}
	if r.EngineVersion == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run engine_version is required")
	}
	if r.ScoringPolicyVersion == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screening: run scoring_policy_version is required")
	}
	return nil
}

// RunPublishRequest is everything one atomic publish writes: the frozen
// result, the display-column descriptors every page of the listing reports,
// the content checksum of the canonical result artifact and the artifacts the
// result was sealed into.
type RunPublishRequest struct {
	Summary     Summary
	Rows        []Row
	Columns     []domain.Field
	ResultHash  string
	ArtifactIDs []domain.ID
}

// RunRecord is one persisted screening run. Summary and the published fields
// stay unset until the run publishes; a caller reading them earlier must be
// told the result is not ready rather than shown an empty result.
type RunRecord struct {
	ID                   domain.ID
	JobID                domain.ID
	ScreenerID           domain.ID
	SourceRunID          domain.ID
	Config               WireRunConfig
	ConfigHash           string
	EngineVersion        string
	ScoringPolicyVersion string
	SnapshotHash         string
	Summary              *Summary
	ResultHash           string
	Columns              []domain.Field
	ArtifactIDs          []domain.ID
	CreatedAt            time.Time
	PublishedAt          *time.Time
}

// Published reports whether the run's immutable result is readable.
func (r RunRecord) Published() bool { return r.PublishedAt != nil }

// Check verifies the stage counts conserve against the rows they describe:
// every member lands in exactly one stage, and the population is exactly the
// number of result rows. Publishing is the moment a run's numbers become
// permanent evidence, so the invariant is re-checked there instead of being
// trusted to the engine alone.
func (s Summary) Check(rows int) error {
	if s.Population != rows {
		return domain.NewError(domain.CodeInternalError,
			"screening: summary population %d does not match %d result rows", s.Population, rows)
	}
	if s.Population != s.ConditionFalse+s.ConditionUnknown+s.ConditionTrue {
		return domain.NewError(domain.CodeInternalError,
			"screening: condition stages do not conserve (population %d)", s.Population)
	}
	if s.ConditionTrue != s.RankInsufficient+s.Rankable {
		return domain.NewError(domain.CodeInternalError,
			"screening: condition_true %d does not match rank stages", s.ConditionTrue)
	}
	if s.Rankable != s.Selected+s.NotSelected {
		return domain.NewError(domain.CodeInternalError,
			"screening: rankable %d does not match selection counts", s.Rankable)
	}
	return nil
}

// ResultColumns describes the display columns a result carries, derived from
// the frozen rows themselves: the type is the kind the column's values were
// observed with, and a column any row lacks is nullable. A column no row ever
// carried stays an explicit unknown rather than an assumed type — the same
// rule the dataset catalog follows.
func ResultColumns(displayColumns []domain.ID, rows []Row) []domain.Field {
	out := make([]domain.Field, 0, len(displayColumns))
	for _, column := range displayColumns {
		field := domain.Field{Name: column.String(), Type: domain.FieldUnknown}
		kind := domain.ValueKind("")
		observed, absent := false, false
		for i := range rows {
			value, ok := rows[i].Values[column]
			if !ok || value.MissingReason != "" || value.Kind == "" {
				absent = true
				continue
			}
			if !observed {
				kind, observed = value.Kind, true
				continue
			}
			if value.Kind != kind {
				// One column carrying two kinds has no single type; reporting
				// either one would be a guess.
				kind = ""
			}
		}
		field.Type = fieldTypeOf(kind)
		field.Nullable = absent
		out = append(out, field)
	}
	return out
}

// fieldTypeOf maps an observed value kind onto the catalog's field type. An
// empty or mixed kind has no field type, which is reported as unknown.
func fieldTypeOf(kind domain.ValueKind) domain.FieldType {
	switch kind {
	case domain.ValueDecimal:
		return domain.FieldDecimal
	case domain.ValueNumber:
		return domain.FieldNumber
	case domain.ValueString:
		return domain.FieldString
	case domain.ValueBoolean:
		return domain.FieldBoolean
	case domain.ValueTimestamp:
		return domain.FieldTimestamp
	default:
		return domain.FieldUnknown
	}
}

// ValueKindOfFieldType is the inverse of fieldTypeOf: the kind the rule engine
// compares a condition literal against. An unknown field type has no kind,
// which is how callers learn to skip a check instead of guessing.
func ValueKindOfFieldType(fieldType domain.FieldType) domain.ValueKind {
	switch fieldType {
	case domain.FieldDecimal:
		return domain.ValueDecimal
	case domain.FieldNumber:
		return domain.ValueNumber
	case domain.FieldString:
		return domain.ValueString
	case domain.FieldBoolean:
		return domain.ValueBoolean
	case domain.FieldTimestamp:
		return domain.ValueTimestamp
	default:
		return ""
	}
}
