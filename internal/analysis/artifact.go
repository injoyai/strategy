package analysis

import (
	"encoding/json"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
)

// artifactSchemaVersion identifies the artifact JSON shape; bump on any
// breaking change so consumers can route old artifacts.
const artifactSchemaVersion = "analysis-artifact/1"

// Artifact is D6's complete analysis product: the full per-date series
// (full fidelity — chart downsampling happens at render time in M1-09
// and must never mutate this file), the page summary and the fixed
// configuration it was computed under. Checksum seals the canonical
// JSON of everything below it.
type Artifact struct {
	SchemaVersion string         `json:"schema_version"`
	Config        ArtifactConfig `json:"config"`
	Series        []DailyStats   `json:"series"`
	Summary       Summary        `json:"summary"`
	EvidenceNote  string         `json:"evidence_note"`
	Checksum      string         `json:"checksum"`
}

// ArtifactConfig records the fixed analysis configuration (D6): the
// factor identity with canonical parameters and definition hash, the
// label economics, grouping, correlation method and segmentation. The
// label economics fields are evidence of how the caller built labels;
// label values themselves never appear in the artifact — labels are an
// input, not a stored dataset (§6.2 isolation).
type ArtifactConfig struct {
	Ref            factor.FactorRef `json:"ref"`
	DefinitionHash string           `json:"definition_hash"`
	Params         map[string]any   `json:"params"`
	LabelPrice     string           `json:"label_price"`
	LabelEntry     string           `json:"label_entry"`
	LabelCost      string           `json:"label_cost"`
	Horizons       []int            `json:"horizons"`
	Method         string           `json:"method"`
	Groups         int              `json:"groups"`
	MinSamples     int              `json:"min_samples"`
	Range          domain.Interval  `json:"range"`
	Segments       []Segment        `json:"segments,omitempty"`
}

// coreJSON encodes every artifact field except Checksum — exactly the
// bytes the digest is taken over. Deterministic by construction: struct
// field order is fixed, slices are ordered, and maps marshal with
// sorted keys.
func (a *Artifact) coreJSON() ([]byte, error) {
	core := struct {
		SchemaVersion string         `json:"schema_version"`
		Config        ArtifactConfig `json:"config"`
		Series        []DailyStats   `json:"series"`
		Summary       Summary        `json:"summary"`
		EvidenceNote  string         `json:"evidence_note"`
	}{a.SchemaVersion, a.Config, a.Series, a.Summary, a.EvidenceNote}
	encoded, err := json.Marshal(core)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "analysis: encode artifact")
	}
	return encoded, nil
}

// seal computes and stores the checksum over the canonical core.
func (a *Artifact) seal(checksummer ports.Checksummer) error {
	encoded, err := a.coreJSON()
	if err != nil {
		return err
	}
	a.Checksum = checksummer.Checksum(encoded)
	return nil
}

// VerifyChecksum recomputes the digest over the canonical core and
// reports whether it matches the stored one — the artifact leg of M1's
// checksum evidence chain.
func (a *Artifact) VerifyChecksum(checksummer ports.Checksummer) (bool, error) {
	encoded, err := a.coreJSON()
	if err != nil {
		return false, err
	}
	return checksummer.Checksum(encoded) == a.Checksum, nil
}
