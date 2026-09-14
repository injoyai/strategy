package tdxprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

const rawContentType = "application/vnd.injoyai.tdx+json"

type Normalizer struct {
	dataset string
	fields  []domain.Field
}

func NewNormalizer(dataset string) *Normalizer {
	return &Normalizer{dataset: dataset, fields: fieldsFor(dataset)}
}

func (n *Normalizer) Schema() []domain.Field { return n.fields }

func (n *Normalizer) Dataset() string { return n.dataset }

func (n *Normalizer) Normalize(ctx context.Context, page domain.RawPage) ([]domain.Observation, []domain.Issue, error) {
	if err := checkContext(ctx); err != nil {
		return nil, nil, err
	}
	if n.fields == nil {
		return nil, nil, domain.NewError(domain.CodeValidationInvalid,
			"tdx: no normalizer for dataset %q", n.dataset)
	}
	if page.ContentType != rawContentType {
		return nil, nil, domain.NewError(domain.CodeValidationInvalid,
			"tdx: normalizer expects %s, got %q", rawContentType, page.ContentType)
	}
	var payload rawPayload
	if err := json.Unmarshal(page.Payload, &payload); err != nil {
		return nil, nil, domain.Wrap(err, domain.CodeInternalError, "tdx: decode raw page")
	}
	if payload.Dataset != n.dataset {
		return nil, nil, domain.NewError(domain.CodeValidationInvalid,
			"tdx: payload dataset %q does not match normalizer %q", payload.Dataset, n.dataset)
	}
	observations := make([]domain.Observation, 0, len(payload.Rows))
	for i, row := range payload.Rows {
		observation, err := n.normalizeRow(page.FetchedAt, row)
		if err != nil {
			return nil, nil, domain.Wrap(err, domain.CodeValidationInvalid, "tdx: row[%d] invalid", i)
		}
		observations = append(observations, observation)
	}
	issues := []domain.Issue{{
		Code: domain.CodeQualityPITUnverified, Path: n.dataset, Severity: domain.SeverityWarning,
		Message: "TDX historical rows use the local first-seen time because the upstream publishes no authoritative availability or revision timestamps",
		Details: map[string]string{"policy_version": "tdx-first-seen/v1"},
	}}
	return observations, issues, nil
}

func (n *Normalizer) normalizeRow(fetchedAt time.Time, row rawRow) (domain.Observation, error) {
	eventTime, err := time.Parse(time.RFC3339Nano, row.EventTime)
	if err != nil {
		return domain.Observation{}, domain.Wrap(err, domain.CodeValidationInvalid, "tdx: event_time invalid")
	}
	var instrumentID *domain.ID
	if row.InstrumentID != "" {
		parsed, err := domain.ParseID(row.InstrumentID)
		if err != nil {
			return domain.Observation{}, err
		}
		instrumentID = &parsed
	}
	values := make(map[string]domain.Value, len(row.Values))
	for name, raw := range row.Values {
		field, ok := schemaField(n.fields, name)
		if !ok {
			return domain.Observation{}, domain.NewError(domain.CodeValidationUnknownField,
				"tdx: field %q is not in dataset %q schema", name, n.dataset)
		}
		value := domain.Value{Kind: valueKind(field.Type), Encoded: raw.Value, MissingReason: raw.MissingReason}
		if value.MissingReason != "" {
			value.Encoded = ""
		}
		values[name] = value
	}
	canonical, err := json.Marshal(row)
	if err != nil {
		return domain.Observation{}, domain.Wrap(err, domain.CodeInternalError, "tdx: hash raw row")
	}
	sum := sha256.Sum256(canonical)
	revision := hex.EncodeToString(sum[:])
	sourceRecord := fmt.Sprintf("%s/%s/%s", n.dataset, row.InstrumentID, eventTime.UTC().Format(time.RFC3339Nano))
	return domain.Observation{
		InstrumentID: instrumentID,
		Dataset:      n.dataset,
		EventTime:    eventTime,
		Values:       values,
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: sourceRecord,
			RevisionID:     revision,
			AvailableAt:    fetchedAt.UTC(),
			IngestedAt:     fetchedAt.UTC(),
			SchemaVersion:  "tdx-" + n.dataset + "/v1",
			PolicyVersion:  "tdx-first-seen/v1",
			QualityFlags:   []string{"pit_unverified"},
		},
	}, nil
}

func schemaField(fields []domain.Field, name string) (domain.Field, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return domain.Field{}, false
}

func valueKind(fieldType domain.FieldType) domain.ValueKind {
	switch fieldType {
	case domain.FieldDecimal:
		return domain.ValueDecimal
	case domain.FieldNumber:
		return domain.ValueNumber
	case domain.FieldBoolean:
		return domain.ValueBoolean
	case domain.FieldTimestamp:
		return domain.ValueTimestamp
	default:
		return domain.ValueString
	}
}
