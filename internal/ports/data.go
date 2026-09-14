package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/screening"
)

// DataProvider is a bound, versioned connection to one upstream data source.
// Implementations are constructed by a ProviderFactory and live for the
// lifetime of one job or request; they must not cache mutable state across
// calls because revisions and availability windows change between fetches.
//
// Contract (docs/interfaces.md §2):
//   - Describe reports honest capabilities; an unsupported dataset returns an
//     explicit error, never a silent fallback to a substitute dataset.
//   - Check performs a lightweight connectivity probe and returns issues
//     surfaced by the upstream; it does not write or normalize data.
//   - Fetch returns one bounded page of raw content. Force mode is the
//     caller's responsibility (passing IngestForce through the request);
//     the provider must still record a new request/batch even when content
//     is byte-identical to a cached page.
type DataProvider interface {
	Describe(context.Context) (domain.ProviderDescriptor, error)
	Check(context.Context) ([]domain.Issue, error)
	Fetch(context.Context, domain.FetchRequest) (domain.RawPage, error)
}

// ProviderFactory builds a DataProvider bound to a versioned connection. The
// factory owns the connection's JSON Schema (ConfigSchema); Open validates
// settings against it before the provider is returned. A failing Open must
// not leak secrets in its error.
type ProviderFactory interface {
	ConfigSchema() json.RawMessage
	Open(context.Context, domain.ConnectionConfig) (DataProvider, error)
}

// Normalizer turns one raw page into the platform's Observation model. It
// owns the dataset's field schema (units, types, natural key) and surfaces
// issues without silently fixing them: a duplicate natural key, an OHLC
// violation, a negative volume, an unknown unit, a future available_at, or
// a superseded revision is reported as an Issue, not corrected in place.
type Normalizer interface {
	Schema() []domain.Field // the dataset's declared field list
	Dataset() string
	Normalize(context.Context, domain.RawPage) ([]domain.Observation, []domain.Issue, error)
}

// BatchInput is one ingest unit handed to DataStore.Append: the normalized
// rows plus the quality issues the pipeline derived for them, and the dataset
// declaration the ingestion made (field units and the availability policy).
// The store persists evidence and derives only aggregate labels (worst
// severity); it never re-derives, repairs or drops individual findings.
// Declaration is optional so an import that declares no mapping can still
// append, and a nil declaration simply leaves the dataset catalog without a
// declared unit for those fields.
type BatchInput struct {
	JobID        domain.ID
	Dataset      string
	Frequency    string
	Observations []domain.Observation
	Issues       []domain.Issue
	RawManifest  json.RawMessage
	Declaration  *domain.DatasetDeclaration
}

// DatasetFilter is the list query for the dataset catalog, with the same
// Sort / AfterID / Limit semantics as BatchFilter. Q is a substring filter on
// the dataset name.
type DatasetFilter struct {
	Q       string
	Sort    string
	AfterID string
	Limit   int
}

// BatchFilter is the list query for batches. Sort accepts "id" (ascending)
// or "-id" (descending, the default); AfterID is the exclusive keyset resume
// point — the PageResult.NextCursor of the previous page ("" starts the
// listing); Limit is clamped by the implementation.
type BatchFilter struct {
	JobID   domain.ID
	Dataset string
	Q       string
	Sort    string
	AfterID string
	Limit   int
}

// SnapshotFilter is the list query for snapshots, with the same Sort /
// AfterID / Limit semantics as BatchFilter.
type SnapshotFilter struct {
	Q       string
	Sort    string
	AfterID string
	Limit   int
}

// UniverseFilter is the list query for universe versions, with the same
// Sort / AfterID / Limit semantics as BatchFilter. Q is a substring filter
// on the universe name.
type UniverseFilter struct {
	Q       string
	Sort    string
	AfterID string
	Limit   int
}

// DataStore is the durable write/read boundary over observations and
// snapshots. Append persists a new batch of observations under one batch ID
// (issued by the store); byte-identical rows inside one input are collapsed,
// so overlapping provider pages never duplicate rows inside a batch —
// cross-request retries are the idempotency layer's concern, not Append's.
// PublishSnapshot freezes a set of batches into an immutable,
// content-addressed snapshot; the manifest hash is taken over the canonical
// (order-independent) manifest, so republishing the same batch set under the
// same policy returns the existing snapshot. The manifest is written
// atomically with the snapshot row so a crash never leaves a half-published
// snapshot. OpenView returns a DataView bound to one snapshot and one
// decision time; queries through it never see records whose available_at
// exceeds the as_of. CreateUniverseVersion saves an immutable
// member-selection version bound to one snapshot; creation is not
// idempotent — every save is a new version, mirroring screening's
// save semantics.
type DataStore interface {
	Append(context.Context, BatchInput) (domain.IngestReceipt, error)
	GetBatch(context.Context, domain.ID) (domain.Batch, error)
	ListBatches(context.Context, BatchFilter) (domain.PageResult[domain.Batch], error)
	PublishSnapshot(context.Context, domain.SnapshotRequest) (domain.Snapshot, error)
	GetSnapshot(context.Context, domain.ID) (domain.Snapshot, error)
	ListSnapshots(context.Context, SnapshotFilter) (domain.PageResult[domain.Snapshot], error)
	OpenView(context.Context, domain.ID, time.Time) (DataView, error)
	CreateUniverseVersion(context.Context, domain.UniverseVersionRequest) (domain.UniverseVersion, error)
	GetUniverseVersion(context.Context, domain.ID) (domain.UniverseVersion, error)
	ListUniverseVersions(context.Context, UniverseFilter) (domain.PageResult[domain.UniverseVersion], error)
	// The dataset catalog: the declared schema, observed coverage and recorded
	// quality findings per dataset name.
	GetDataset(context.Context, domain.ID) (domain.Dataset, error)
	ListDatasets(context.Context, DatasetFilter) (domain.PageResult[domain.Dataset], error)
	// Screener versions follow the same immutable-save semantics: every save
	// mints a new revision, and (id, version) addresses exactly one frozen
	// rule set.
	CreateScreenerVersion(context.Context, screening.VersionRequest) (screening.Version, error)
	GetScreenerVersion(context.Context, domain.ID, domain.ID) (screening.Version, error)
	ListScreenerVersions(context.Context, ScreenerFilter) (domain.PageResult[screening.Version], error)
}

// DataView is an immutable, point-in-time read bound to a snapshot and an
// as_of decision time. Query applies the PIT filters (available_at <= as_of
// and the effective window covering as_of) and the latest-revision-within-
// snapshot rule; a query-level replay_time further narrows visibility to
// rows ingested at or before that moment. DatasetInstruments lists the
// distinct instruments of one dataset visible at the as_of under the same
// PIT filters — the primitive historical_rule universes resolve through.
// RecentEventTimes lists the most recent distinct event times of one dataset
// and frequency that fall strictly before as_of, newest first — the primitive
// that derives the window a factor needs from the data itself instead of
// converting a declared period count into an unverified time span.
// It never returns mutable buffers or future-visible records.
type DataView interface {
	SnapshotID() domain.ID
	AsOf() time.Time
	Query(context.Context, domain.DataQuery) (domain.PageResult[domain.Observation], error)
	DatasetInstruments(context.Context, string, string) ([]domain.ID, error)
	RecentEventTimes(context.Context, string, string, []domain.ID, int) ([]time.Time, error)
	// LatestValues resolves one field's most recent visible value per
	// instrument — the primitive a screening run reads its field inputs with.
	LatestValues(context.Context, string, string, string, []domain.ID) (map[domain.ID]domain.Value, error)
}
