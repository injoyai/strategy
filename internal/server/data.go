package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

const (
	jobKindConnectionCheck = "connection.check"
	jobKindSnapshotPublish = "snapshot.publish"
	jobKindIngestionRun    = "ingestion.run"
)

type ProviderRegistration struct {
	Provider ports.DataProvider
	Factory  ports.ProviderFactory
}

func describeProviders(regs []ProviderRegistration) ([]domain.ProviderDescriptor, map[domain.ID]ports.ProviderFactory) {
	descriptors := make([]domain.ProviderDescriptor, 0, len(regs))
	factories := make(map[domain.ID]ports.ProviderFactory, len(regs))
	seen := make(map[domain.ID]struct{}, len(regs))
	for _, reg := range regs {
		if reg.Provider == nil {
			panic("server: provider registration with nil provider")
		}
		if reg.Factory == nil {
			panic("server: provider registration with nil factory")
		}
		desc, err := reg.Provider.Describe(context.Background())
		if err != nil {
			panic("server: describe provider: " + err.Error())
		}
		if desc.Ref.ID == "" {
			panic("server: provider descriptor with empty id")
		}
		if desc.Ref.Version == "" {
			panic("server: provider descriptor with empty version")
		}
		if desc.ConfigSchema == nil {
			panic("server: provider descriptor " + desc.Ref.ID.String() + " without config schema")
		}
		if _, dup := seen[desc.Ref.ID]; dup {
			panic("server: duplicate provider id " + desc.Ref.ID.String())
		}
		seen[desc.Ref.ID] = struct{}{}
		factories[desc.Ref.ID] = reg.Factory
		descriptors = append(descriptors, desc)
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Ref.ID < descriptors[j].Ref.ID })
	return descriptors, factories
}

func (a *API) findProvider(id domain.ID) (domain.ProviderDescriptor, bool) {
	for _, p := range a.providers {
		if p.Ref.ID == id {
			return p, true
		}
	}
	return domain.ProviderDescriptor{}, false
}

func (a *API) RegisterData() {
	a.Handle(http.MethodGet, "/providers", a.listProviders, RouteOptions{})
	a.Handle(http.MethodGet, "/providers/{id}", a.getProvider, RouteOptions{})
	a.Handle(http.MethodPost, "/connections", a.createConnection, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/connections", a.listConnections, RouteOptions{})
	a.Handle(http.MethodGet, "/connections/{id}", a.getConnection, RouteOptions{})
	a.Handle(http.MethodPost, "/connections/{id}/check", a.checkConnection, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/batches", a.listBatches, RouteOptions{})
	a.Handle(http.MethodGet, "/batches/{id}", a.getBatch, RouteOptions{})
	a.Handle(http.MethodGet, "/snapshots", a.listSnapshots, RouteOptions{})
	a.Handle(http.MethodPost, "/snapshots", a.startSnapshot, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/snapshots/{id}", a.getSnapshot, RouteOptions{})
	a.Handle(http.MethodPost, "/ingestions", a.startIngestion, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodPost, "/data/query", a.queryData, RouteOptions{})
}

func (a *API) requireDataJobs(w http.ResponseWriter, r *http.Request) bool {
	if a.jobs != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "jobs are not mounted on this API"))
	return false
}

type wireInterval struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type wireCapability struct {
	Dataset     string         `json:"dataset"`
	Fields      []domain.Field `json:"fields"`
	Frequencies []string       `json:"frequencies"`
	Coverage    *wireInterval  `json:"coverage"`
	PITLevel    string         `json:"pit_level"`
	MaxPageSize int            `json:"max_page_size"`
}

type providerWire struct {
	ID           string           `json:"id"`
	Version      string           `json:"version"`
	Name         string           `json:"name"`
	ConfigSchema any              `json:"config_schema"`
	Capabilities []wireCapability `json:"capabilities"`
}

type wireValue struct {
	Kind          string  `json:"kind"`
	Value         any     `json:"value"`
	MissingReason *string `json:"missing_reason"`
}

type wireProvenance struct {
	SourceID             string     `json:"source_id"`
	SourceRecordID       string     `json:"source_record_id"`
	RevisionID           string     `json:"revision_id"`
	SupersedesRevisionID *string    `json:"supersedes_revision_id"`
	PublishedAt          *time.Time `json:"published_at"`
	AvailableAt          time.Time  `json:"available_at"`
	IngestedAt           time.Time  `json:"ingested_at"`
	SchemaVersion        string     `json:"schema_version"`
	PolicyVersion        string     `json:"policy_version"`
	QualityFlags         []string   `json:"quality_flags"`
}

type wireObservation struct {
	InstrumentID *string              `json:"instrument_id"`
	EntityID     *string              `json:"entity_id"`
	Dataset      string               `json:"dataset"`
	EventTime    time.Time            `json:"event_time"`
	PeriodEnd    *string              `json:"period_end,omitempty"`
	Effective    *wireInterval        `json:"effective,omitempty"`
	Values       map[string]wireValue `json:"values"`
	Provenance   wireProvenance       `json:"provenance"`
}

func providerWireOf(d domain.ProviderDescriptor) providerWire {
	caps := make([]wireCapability, 0, len(d.Capabilities))
	for _, c := range d.Capabilities {
		caps = append(caps, wireCapabilityOf(c))
	}
	return providerWire{
		ID:           d.Ref.ID.String(),
		Version:      d.Ref.Version,
		Name:         d.Name,
		ConfigSchema: d.ConfigSchema,
		Capabilities: caps,
	}
}

func wireCapabilityOf(c domain.Capability) wireCapability {
	fields := c.Fields
	if fields == nil {
		fields = []domain.Field{}
	}
	frequencies := c.Frequencies
	if frequencies == nil {
		frequencies = []string{}
	}
	out := wireCapability{
		Dataset:     c.Dataset,
		Fields:      fields,
		Frequencies: frequencies,
		PITLevel:    string(c.PITLevel),
		MaxPageSize: c.MaxPageSize,
	}
	if c.Coverage != nil {
		out.Coverage = &wireInterval{From: c.Coverage.From, To: c.Coverage.To}
	}
	return out
}

func wireValueOf(v domain.Value) (wireValue, error) {
	if v.MissingReason != "" {
		reason := v.MissingReason
		return wireValue{Kind: string(v.Kind), Value: nil, MissingReason: &reason}, nil
	}
	switch v.Kind {
	case domain.ValueDecimal, domain.ValueString, domain.ValueTimestamp:
		if v.Encoded == "" {
			return wireValue{}, domain.NewError(domain.CodeInternalError, "server: %s value carries no encoding", v.Kind)
		}
		return wireValue{Kind: string(v.Kind), Value: v.Encoded}, nil
	case domain.ValueNumber:
		n := json.Number(v.Encoded)
		if _, err := n.Float64(); err != nil {
			return wireValue{}, domain.Wrap(err, domain.CodeInternalError, "server: invalid number encoding %q", v.Encoded)
		}
		return wireValue{Kind: string(v.Kind), Value: n}, nil
	case domain.ValueBoolean:
		b, err := strconv.ParseBool(v.Encoded)
		if err != nil {
			return wireValue{}, domain.Wrap(err, domain.CodeInternalError, "server: invalid boolean encoding %q", v.Encoded)
		}
		return wireValue{Kind: string(v.Kind), Value: b}, nil
	default:
		return wireValue{}, domain.NewError(domain.CodeInternalError, "server: unknown value kind %q", v.Kind)
	}
}

func wireProvenanceOf(p domain.Provenance) wireProvenance {
	out := wireProvenance{
		SourceID:       p.SourceID.String(),
		SourceRecordID: p.SourceRecordID,
		RevisionID:     p.RevisionID,
		PublishedAt:    p.PublishedAt,
		AvailableAt:    p.AvailableAt,
		IngestedAt:     p.IngestedAt,
		SchemaVersion:  p.SchemaVersion,
		PolicyVersion:  p.PolicyVersion,
		QualityFlags:   p.QualityFlags,
	}
	if out.QualityFlags == nil {
		out.QualityFlags = []string{}
	}
	if p.SupersedesRevisionID != "" {
		s := p.SupersedesRevisionID
		out.SupersedesRevisionID = &s
	}
	return out
}

func wireObservationOf(obs domain.Observation) (wireObservation, error) {
	out := wireObservation{
		Dataset:    obs.Dataset,
		EventTime:  obs.EventTime,
		Values:     make(map[string]wireValue, len(obs.Values)),
		Provenance: wireProvenanceOf(obs.Provenance),
	}
	if obs.InstrumentID != nil {
		s := obs.InstrumentID.String()
		out.InstrumentID = &s
	}
	if obs.EntityID != nil {
		s := obs.EntityID.String()
		out.EntityID = &s
	}
	out.PeriodEnd = obs.PeriodEnd
	if obs.Effective != nil {
		out.Effective = &wireInterval{From: obs.Effective.From, To: obs.Effective.To}
	}
	for k, v := range obs.Values {
		wv, err := wireValueOf(v)
		if err != nil {
			return wireObservation{}, err
		}
		out.Values[k] = wv
	}
	return out, nil
}

type providerPage struct {
	Items      []providerWire `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type connectionPage struct {
	Items      []domain.Connection `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

type batchPage struct {
	Items      []domain.Batch `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type snapshotPage struct {
	Items      []domain.Snapshot `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type observationPage struct {
	Items      []wireObservation `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	cursor, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
	if !ok {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	matched := make([]domain.ProviderDescriptor, 0, len(a.providers))
	for _, p := range a.providers {
		if q != "" &&
			!strings.Contains(strings.ToLower(p.Ref.ID.String()), q) &&
			!strings.Contains(strings.ToLower(p.Name), q) {
			continue
		}
		matched = append(matched, p)
	}
	if page.Sort == SortDesc {
		sort.Slice(matched, func(i, j int) bool { return matched[i].Ref.ID > matched[j].Ref.ID })
	}

	items := make([]providerWire, 0, page.Limit+1)
	next := ""
	for _, p := range matched {
		id := p.Ref.ID.String()
		if cursor.LastID != "" {
			if page.Sort == SortDesc {
				if id >= cursor.LastID {
					continue
				}
			} else if id <= cursor.LastID {
				continue
			}
		}
		items = append(items, providerWireOf(p))
		if len(items) > page.Limit {
			items = items[:page.Limit]
			next = EncodeCursor(page.Sort, items[page.Limit-1].ID, a.clock.Now())
			break
		}
	}
	out := providerPage{Items: items}
	if next != "" {
		out.NextCursor = &next
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) getProvider(w http.ResponseWriter, r *http.Request) {
	desc, ok := a.findProvider(domain.ID(r.PathValue("id")))
	if !ok {
		a.writeError(w, r, domain.NewError(domain.CodeResourceNotFound, "no such provider %q", r.PathValue("id")))
		return
	}
	WriteJSON(w, http.StatusOK, providerWireOf(desc))
}

func (a *API) createConnection(w http.ResponseWriter, r *http.Request) {
	var cfg domain.ConnectionConfig
	if !DecodeJSON(w, r, &cfg) {
		return
	}
	if err := cfg.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	desc, ok := a.findProvider(cfg.Provider.ID)
	if !ok {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "unknown provider %q", cfg.Provider.ID))
		return
	}
	if desc.Ref.Version != cfg.Provider.Version {
		a.writeError(w, r, domain.NewError(domain.CodeResourceVersionMismatch,
			"provider %s is at version %s, request pinned %s", desc.Ref.ID, desc.Ref.Version, cfg.Provider.Version))
		return
	}
	if _, err := a.factories[cfg.Provider.ID].Open(r.Context(), cfg); err != nil {
		a.writeError(w, r, err)
		return
	}
	conn, err := a.data.CreateConnection(r.Context(), cfg)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, conn)
}

func (a *API) getConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := a.data.GetConnection(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, conn)
}

func (a *API) listConnections(w http.ResponseWriter, r *http.Request) {
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	cursor, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
	if !ok {
		return
	}
	res, err := a.data.ListConnections(r.Context(), r.URL.Query().Get("q"), page.Sort, cursor.LastID, page.Limit)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := connectionPage{Items: res.Items}
	if res.NextCursor != "" {
		cur := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &cur
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) checkConnection(w http.ResponseWriter, r *http.Request) {
	if !a.requireDataJobs(w, r) {
		return
	}
	var cmd struct{}
	if !DecodeJSON(w, r, &cmd) {
		return
	}
	conn, err := a.data.GetConnection(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	cfg, err := json.Marshal(map[string]string{
		"connection_id":      conn.ID.String(),
		"connection_version": conn.Version,
	})
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "server: encode connection check config"))
		return
	}
	job, err := a.jobs.Create(r.Context(), jobKindConnectionCheck, cfg, "")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteAccepted(w, r, job.ID, job)
}

func (a *API) listBatches(w http.ResponseWriter, r *http.Request) {
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	cursor, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
	if !ok {
		return
	}
	query := r.URL.Query()
	res, err := a.data.ListBatches(r.Context(), ports.BatchFilter{
		JobID:   domain.ID(query.Get("job_id")),
		Dataset: query.Get("dataset_id"),
		Q:       query.Get("q"),
		Sort:    page.Sort,
		AfterID: cursor.LastID,
		Limit:   page.Limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := batchPage{Items: res.Items}
	if res.NextCursor != "" {
		cur := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &cur
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) getBatch(w http.ResponseWriter, r *http.Request) {
	batch, err := a.data.GetBatch(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, batch)
}

func (a *API) listSnapshots(w http.ResponseWriter, r *http.Request) {
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	cursor, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
	if !ok {
		return
	}
	res, err := a.data.ListSnapshots(r.Context(), ports.SnapshotFilter{
		Q:       r.URL.Query().Get("q"),
		Sort:    page.Sort,
		AfterID: cursor.LastID,
		Limit:   page.Limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := snapshotPage{Items: res.Items}
	if res.NextCursor != "" {
		cur := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &cur
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) startSnapshot(w http.ResponseWriter, r *http.Request) {
	if !a.requireDataJobs(w, r) {
		return
	}
	var req domain.SnapshotRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	cfg, err := json.Marshal(req)
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "server: encode snapshot config"))
		return
	}
	job, err := a.jobs.Create(r.Context(), jobKindSnapshotPublish, cfg, "")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteAccepted(w, r, job.ID, job)
}

func (a *API) getSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.data.GetSnapshot(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, snapshot)
}

func (a *API) startIngestion(w http.ResponseWriter, r *http.Request) {
	if !a.requireDataJobs(w, r) {
		return
	}
	var req domain.IngestionRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	if req.ImportID != "" {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "ingestion: import_id is not supported yet"))
		return
	}
	if req.ConnectionRef == nil {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "ingestion: connection_ref is required"))
		return
	}
	conn, err := a.data.GetConnection(r.Context(), req.ConnectionRef.ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if conn.Version != req.ConnectionRef.Version {
		a.writeError(w, r, domain.NewError(domain.CodeResourceVersionMismatch,
			"connection %s is at version %s, request pinned %s", req.ConnectionRef.ID, conn.Version, req.ConnectionRef.Version))
		return
	}
	cfg, err := json.Marshal(req)
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "server: encode ingestion config"))
		return
	}
	job, err := a.jobs.Create(r.Context(), jobKindIngestionRun, cfg, "")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteAccepted(w, r, job.ID, job)
}

func (a *API) queryData(w http.ResponseWriter, r *http.Request) {
	var q domain.DataQuery
	if !DecodeJSON(w, r, &q) {
		return
	}
	if err := q.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	view, err := a.data.OpenView(r.Context(), q.SnapshotID, q.AsOf)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	res, err := view.Query(r.Context(), q)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items := make([]wireObservation, 0, len(res.Items))
	for _, obs := range res.Items {
		wo, err := wireObservationOf(obs)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		items = append(items, wo)
	}
	out := observationPage{Items: items}
	if res.NextCursor != "" {
		cur := res.NextCursor
		out.NextCursor = &cur
	}
	WriteJSON(w, http.StatusOK, out)
}
