package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/synthetic"
)

const (
	KindConnectionCheck = "connection.check"
	KindSnapshotPublish = "snapshot.publish"
	KindIngestionRun    = "ingestion.run"
)

const (
	resultKindBatch    = "batch"
	resultKindSnapshot = "snapshot"
)

type Handlers struct {
	Jobs      *jobs.Store
	Data      *data.Store
	Factories map[domain.ID]ports.ProviderFactory
	Clock     ports.Clock
	Artifacts *artifacts.Store
}

func (h *Handlers) clock() ports.Clock {
	if h.Clock == nil {
		return ports.SystemClock{}
	}
	return h.Clock
}

func (h *Handlers) Map() map[string]jobs.HandlerFunc {
	return map[string]jobs.HandlerFunc{
		KindConnectionCheck: h.ConnectionCheck,
		KindSnapshotPublish: h.SnapshotPublish,
		KindIngestionRun:    h.IngestionRun,
		KindImportValidate:  h.ImportValidate,
	}
}

type connectionCheckConfig struct {
	ConnectionID      domain.ID `json:"connection_id"`
	ConnectionVersion string    `json:"connection_version"`
}

type pageEvidence struct {
	Dataset          string    `json:"dataset"`
	ContentType      string    `json:"content_type"`
	Checksum         string    `json:"checksum"`
	Bytes            int       `json:"bytes"`
	FetchedAt        time.Time `json:"fetched_at"`
	NextCursor       string    `json:"next_cursor,omitempty"`
	ProviderRevision string    `json:"provider_revision,omitempty"`
}

type collected struct {
	observations []domain.Observation
	issues       []domain.Issue
	pages        []pageEvidence
}

func (c *collected) manifestBytes() json.RawMessage {
	raw, err := json.Marshal(c.pages)
	if err != nil {
		return nil
	}
	return raw
}

func decodeConfig(config []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(config))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return domain.Wrap(err, domain.CodeInternalError, "pipeline: decode job config")
	}
	return nil
}

func wrapUpstream(err error, format string, args ...any) error {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return err
	}
	return domain.Wrap(err, domain.CodeInternalError, format, args...)
}

func (h *Handlers) openProvider(ctx context.Context, cfg domain.ConnectionConfig) (ports.DataProvider, error) {
	factory, ok := h.Factories[cfg.Provider.ID]
	if !ok {
		return nil, domain.NewError(domain.CodeInternalError,
			"pipeline: no factory registered for provider %s", cfg.Provider.ID)
	}
	provider, err := factory.Open(ctx, cfg)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError,
			"pipeline: open provider %s", cfg.Provider.ID)
	}
	return provider, nil
}

func capabilityOf(desc domain.ProviderDescriptor, dataset string) (*domain.Capability, error) {
	for i := range desc.Capabilities {
		if desc.Capabilities[i].Dataset == dataset {
			return &desc.Capabilities[i], nil
		}
	}
	return nil, domain.NewError(domain.CodeValidationInvalid,
		"pipeline: provider %s does not serve dataset %s", desc.Ref.ID, dataset)
}

func requireFrequency(capability *domain.Capability, frequency string) error {
	for _, f := range capability.Frequencies {
		if f == frequency {
			return nil
		}
	}
	return domain.NewError(domain.CodeValidationInvalid,
		"pipeline: dataset %s does not support frequency %s", capability.Dataset, frequency)
}

func fieldNames(fields []domain.Field) []string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	return names
}

func pageLimit(capability *domain.Capability) int {
	if capability.MaxPageSize > 0 {
		return capability.MaxPageSize
	}
	return synthetic.MaxPageSize
}

func (h *Handlers) collect(ctx context.Context, task *jobs.Task, provider ports.DataProvider, base domain.FetchRequest) (*collected, error) {
	out := &collected{}
	normalizer := synthetic.NewNormalizer(base.Dataset)
	for {
		if task.Cancelled() {
			return nil, domain.NewError(domain.CodeResourceConflict,
				"pipeline: %s fetch cancelled", base.Dataset)
		}
		page, err := provider.Fetch(ctx, base)
		if err != nil {
			return nil, wrapUpstream(err, "pipeline: fetch %s page %d", base.Dataset, len(out.pages)+1)
		}
		observations, issues, err := normalizer.Normalize(ctx, page)
		if err != nil {
			return nil, wrapUpstream(err, "pipeline: normalize %s page %d", base.Dataset, len(out.pages)+1)
		}
		out.observations = append(out.observations, observations...)
		out.issues = append(out.issues, issues...)
		out.pages = append(out.pages, pageEvidence{
			Dataset:          base.Dataset,
			ContentType:      page.ContentType,
			Checksum:         page.Checksum,
			Bytes:            len(page.Payload),
			FetchedAt:        page.FetchedAt,
			NextCursor:       page.NextCursor,
			ProviderRevision: page.ProviderRevision,
		})
		if err := task.Progress(base.Dataset, int64(len(out.pages)), nil); err != nil {
			return nil, err
		}
		if page.NextCursor == "" {
			return out, nil
		}
		base.Page.Cursor = page.NextCursor
	}
}

func (h *Handlers) ConnectionCheck(ctx context.Context, task *jobs.Task) error {
	config, err := h.Jobs.Config(ctx, task.JobID())
	if err != nil {
		return err
	}
	var cfg connectionCheckConfig
	if err := decodeConfig(config, &cfg); err != nil {
		return err
	}
	conn, err := h.Data.GetConnection(ctx, cfg.ConnectionID)
	if err != nil {
		return err
	}
	provider, err := h.openProvider(ctx, conn.ToConfig())
	if err != nil {
		return err
	}
	issues, err := provider.Check(ctx)
	if err != nil {
		return wrapUpstream(err, "pipeline: check provider %s", conn.Provider.ID)
	}
	if err := task.Progress("checked", int64(len(issues)), nil); err != nil {
		return err
	}
	var blocking []string
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			blocking = append(blocking, issue.Code+": "+issue.Message)
		}
	}
	if len(blocking) > 0 {
		return domain.NewError(domain.CodeResourceConflict,
			"connection check reported %d blocking issue(s): %s",
			len(blocking), strings.Join(blocking, "; "))
	}
	return nil
}

func (h *Handlers) SnapshotPublish(ctx context.Context, task *jobs.Task) error {
	config, err := h.Jobs.Config(ctx, task.JobID())
	if err != nil {
		return err
	}
	var req domain.SnapshotRequest
	if err := decodeConfig(config, &req); err != nil {
		return err
	}
	snapshot, err := h.Data.PublishSnapshot(ctx, req)
	if err != nil {
		return err
	}
	if err := task.Progress("published", int64(len(snapshot.BatchIDs)), nil); err != nil {
		return err
	}
	task.SetResultRefs([]jobs.ResultRef{{Kind: resultKindSnapshot, ID: snapshot.ID.String()}})
	return nil
}

func applyAvailabilityPolicy(observations []domain.Observation) {
	for i := range observations {
		if observations[i].Provenance.PublishedAt != nil {
			continue
		}
		available := observations[i].Provenance.AvailableAt
		observations[i].Provenance.PublishedAt = &available
	}
}

func (h *Handlers) IngestionRun(ctx context.Context, task *jobs.Task) error {
	config, err := h.Jobs.Config(ctx, task.JobID())
	if err != nil {
		return err
	}
	var req domain.IngestionRequest
	if err := decodeConfig(config, &req); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	if req.ImportID != "" {
		return domain.NewError(domain.CodeValidationInvalid,
			"pipeline: file import is not supported; ingest from a connection_ref instead")
	}
	if req.ConnectionRef == nil {
		return domain.NewError(domain.CodeInternalError,
			"pipeline: ingestion request carries no connection ref")
	}
	conn, err := h.Data.GetConnection(ctx, req.ConnectionRef.ID)
	if err != nil {
		return err
	}
	if conn.Version != req.ConnectionRef.Version {
		return domain.NewError(domain.CodeResourceVersionMismatch,
			"connection %s is at version %s, job pinned %s",
			conn.ID, conn.Version, req.ConnectionRef.Version)
	}
	provider, err := h.openProvider(ctx, conn.ToConfig())
	if err != nil {
		return err
	}
	desc, err := provider.Describe(ctx)
	if err != nil {
		return wrapUpstream(err, "pipeline: describe provider %s", conn.Provider.ID)
	}
	capability, err := capabilityOf(desc, req.Dataset)
	if err != nil {
		return err
	}
	if err := requireFrequency(capability, req.Frequency); err != nil {
		return err
	}

	var calendar []domain.Observation
	if req.Dataset == synthetic.DatasetBar {
		calendar, err = h.collectCalendar(ctx, task, provider, desc, req)
		if err != nil {
			return err
		}
	}

	base := domain.FetchRequest{
		Dataset:       req.Dataset,
		Frequency:     req.Frequency,
		InstrumentIDs: req.InstrumentIDs,
		Fields:        fieldNames(capability.Fields),
		Range:         req.Range,
		Page:          domain.Page{Limit: pageLimit(capability)},
	}
	main, err := h.collect(ctx, task, provider, base)
	if err != nil {
		return err
	}
	applyAvailabilityPolicy(main.observations)
	qualityIssues := synthetic.NewQualityEngine(calendar, h.clock().Now()).Check(main.observations)
	issues := append(main.issues, qualityIssues...)

	receipt, err := h.Data.Append(ctx, ports.BatchInput{
		JobID:        domain.ID(task.JobID()),
		Dataset:      req.Dataset,
		Frequency:    req.Frequency,
		Observations: main.observations,
		Issues:       issues,
		RawManifest:  main.manifestBytes(),
	})
	if err != nil {
		return err
	}
	if err := task.Progress("appended", receipt.Rows, nil); err != nil {
		return err
	}
	task.SetResultRefs([]jobs.ResultRef{{Kind: resultKindBatch, ID: receipt.BatchID.String()}})
	return nil
}

func (h *Handlers) collectCalendar(ctx context.Context, task *jobs.Task, provider ports.DataProvider, desc domain.ProviderDescriptor, req domain.IngestionRequest) ([]domain.Observation, error) {
	capability, err := capabilityOf(desc, synthetic.DatasetCalendar)
	if err != nil {
		return nil, err
	}
	frequency := req.Frequency
	if len(capability.Frequencies) > 0 {
		frequency = capability.Frequencies[0]
	}
	collected, err := h.collect(ctx, task, provider, domain.FetchRequest{
		Dataset:       synthetic.DatasetCalendar,
		Frequency:     frequency,
		InstrumentIDs: req.InstrumentIDs,
		Fields:        fieldNames(capability.Fields),
		Range:         req.Range,
		Page:          domain.Page{Limit: pageLimit(capability)},
	})
	if err != nil {
		return nil, err
	}
	return collected.observations, nil
}
