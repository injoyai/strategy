package synthetic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// pageRow is one row of the wire payload the synthetic provider emits (see
// provider.go Fetch). It carries the full observation identity — instrument,
// event time and provenance — so the normalizer rebuilds domain Observations
// losslessly instead of guessing identity from value shapes.
type pageRow struct {
	InstrumentID *domain.ID              `json:"instrument_id,omitempty"`
	EventTime    time.Time               `json:"event_time"`
	Provenance   domain.Provenance       `json:"provenance"`
	Values       map[string]domain.Value `json:"values"`
}

// pagePayload is the envelope produced by provider.go Fetch. It is the
// contract between the provider and the normalizer; a future provider that
// returns CSV would get a different normalizer, not a different shape here.
type pagePayload struct {
	Dataset string    `json:"dataset"`
	Rows    []pageRow `json:"rows"`
}

// Normalizer parses a RawPage payload back into Observation rows. The payload
// is the JSON the provider emitted, so the round-trip is lossless: the
// normalizer does not invent or repair values, and it surfaces schema
// violations as Issues rather than dropping rows silently.
//
// Quality checks (duplicate keys, OHLC, volume, unit, available_at) are owned
// by QualityEngine, not the normalizer. The split keeps one responsibility
// per type and lets a future provider share the same quality engine.
type Normalizer struct {
	dataset string
	fields  []domain.Field
}

// NewNormalizer builds a normalizer for one dataset. The fields list is the
// provider's declared schema for that dataset; rows whose values reference
// fields outside the schema are reported as issues (the normalizer does not
// silently drop unknown fields, but it also does not invent them).
func NewNormalizer(dataset string) *Normalizer {
	return &Normalizer{dataset: dataset, fields: datasetFields(dataset)}
}

// Schema returns the declared field list for this dataset.
func (n *Normalizer) Schema() []domain.Field { return n.fields }

// Dataset returns the dataset name this normalizer handles.
func (n *Normalizer) Dataset() string { return n.dataset }

// Normalize decodes the payload into observations. Each row already carries
// its identity (instrument, event time) and provenance, so the rebuild is a
// straight lift; the only value the normalizer contributes is IngestedAt —
// ingestion genuinely happens here — and only when the upstream did not
// provide one. Any other repair would be silent data fabrication.
func (n *Normalizer) Normalize(ctx context.Context, page domain.RawPage) ([]domain.Observation, []domain.Issue, error) {
	if page.ContentType != "application/json" {
		return nil, nil, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: normalizer expects application/json, got %q", page.ContentType)
	}
	var payload pagePayload
	if err := json.Unmarshal(page.Payload, &payload); err != nil {
		return nil, nil, domain.Wrap(err, domain.CodeInternalError,
			"synthetic: decode payload: %v", err)
	}
	if payload.Dataset != n.dataset {
		return nil, nil, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: payload dataset %q does not match normalizer %q",
			payload.Dataset, n.dataset)
	}

	obs := make([]domain.Observation, 0, len(payload.Rows))
	var issues []domain.Issue
	for i, row := range payload.Rows {
		// Field-level schema validation: a value referencing a field outside
		// the declared schema is reported, never silently dropped.
		for name := range row.Values {
			if !fieldInSchema(name, n.fields) {
				issues = append(issues, domain.Issue{
					Code:     domain.CodeValidationUnknownField,
					Path:     fmt.Sprintf("row[%d].%s", i, name),
					Severity: domain.SeverityWarning,
					Message:  fmt.Sprintf("field %q is not in dataset %q schema", name, n.dataset),
				})
			}
		}
		o := domain.Observation{
			InstrumentID: row.InstrumentID,
			Dataset:      n.dataset,
			EventTime:    row.EventTime,
			Values:       row.Values,
			Provenance:   row.Provenance,
		}
		if o.Provenance.IngestedAt.IsZero() {
			o.Provenance.IngestedAt = page.FetchedAt
		}
		obs = append(obs, o)
	}
	return obs, issues, nil
}

// fieldInSchema reports whether name appears in the declared schema.
func fieldInSchema(name string, fields []domain.Field) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
