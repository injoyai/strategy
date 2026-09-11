// Package contracts defines proposed extension ports, not a runnable backend.
// HTTP wire schemas are defined in ../api/openapi.json; these are domain ports.
package contracts

import (
	"context"
	"encoding/json"
	"time"
)

type ID string
type Decimal string // Parse and validate at boundaries; not a math implementation.
type VersionRef struct {
	ID      ID
	Version string
}
type Interval struct{ From, To time.Time } // [From, To)
type Page struct {
	Cursor string
	Limit  int
}
type PageResult[T any] struct {
	Items      []T
	NextCursor string
}
type Issue struct {
	Code, Path, Message, Severity string
	Details                       map[string]string
}
type Field struct {
	Name, Type, Unit, Description string
	Nullable                      bool
}
type DatasetSchema struct {
	Ref                       VersionRef
	Fields                    []Field
	NaturalKey                []string
	AvailabilityPolicyVersion string
}
type Capability struct {
	Dataset, PITLevel string
	Fields            []Field
	Frequencies       []string
	Coverage          Interval
	MaxPageSize       int
}
type ProviderDescriptor struct {
	Ref          VersionRef
	Capabilities []Capability
}
type ConnectionConfig struct {
	Ref       VersionRef
	Provider  VersionRef
	SecretRef string
	Settings  json.RawMessage // Validated by the provider's registered JSON schema.
}
type FetchRequest struct {
	Dataset, Frequency string
	InstrumentIDs      []ID
	Fields             []string
	Range              Interval
	Page               Page
}
type RawPage struct {
	Payload                    []byte
	ContentType, NextCursor    string
	ProviderRevision, Checksum string
	FetchedAt                  time.Time
}

// A provider instance is bound to a versioned connection by its factory.
// It reports honest capabilities and never silently substitutes datasets.
type DataProvider interface {
	Describe(context.Context) (ProviderDescriptor, error)
	Check(context.Context) ([]Issue, error)
	Fetch(context.Context, FetchRequest) (RawPage, error)
}
type ProviderFactory interface {
	ConfigSchema() json.RawMessage
	Open(context.Context, ConnectionConfig) (DataProvider, error)
}
type Provenance struct {
	SourceID                     ID
	SourceRecordID, RevisionID   string
	SupersedesRevisionID         string
	PublishedAt                  *time.Time
	AvailableAt, IngestedAt      time.Time
	SchemaVersion, PolicyVersion string
	QualityFlags                 []string
}
type Value struct {
	Kind          string // decimal, number, string, boolean, timestamp
	Encoded       string // Canonical scalar; empty only when MissingReason is set.
	MissingReason string
}
type Observation struct {
	InstrumentID ID
	EntityID     ID
	Dataset      string
	EventTime    time.Time
	PeriodEnd    *time.Time
	Effective    *Interval
	Values       map[string]Value
	Provenance   Provenance
}
type Normalizer interface {
	Schema() DatasetSchema
	Normalize(context.Context, RawPage) ([]Observation, []Issue, error)
}
type IngestReceipt struct {
	BatchID ID
	Rows    int64
	Issues  []Issue
}
type Snapshot struct {
	ID                          ID
	ManifestHash, QualityStatus string
	Schemas                     []VersionRef
	AvailabilityPolicyVersion   string
	CreatedAt                   time.Time
}
type SnapshotRequest struct {
	Name      string
	BatchIDs  []ID
	StrictPIT bool
}
type DataStore interface {
	Append(context.Context, []Observation) (IngestReceipt, error)
	PublishSnapshot(context.Context, SnapshotRequest) (Snapshot, error)
	GetSnapshot(context.Context, ID) (Snapshot, error)
	OpenView(context.Context, ID, time.Time) (DataView, error)
}
type DataQuery struct {
	Dataset, Frequency string
	InstrumentIDs      []ID
	Fields             []string
	Range              Interval
	Page               Page
}

// DataView is immutable and bound to snapshot + decision time.
// Query rejects future visibility; implementations return no mutable buffers.
type DataView interface {
	SnapshotID() ID
	AsOf() time.Time
	Query(context.Context, DataQuery) (PageResult[Observation], error)
}
type UniverseDefinition struct {
	Ref  VersionRef
	Kind string // static or historical_rule
	Rule json.RawMessage
}
type UniverseResolver interface {
	Resolve(context.Context, UniverseDefinition, DataView) ([]ID, error)
}
type InputRequirement struct {
	Dataset, Frequency string
	Fields             []string
	LookbackPeriods    int
	MaxStaleness       time.Duration
	RequiresStrictPIT  bool
}
type FactorSpec struct {
	Ref                       VersionRef
	Name, Description         string
	Implementation            VersionRef
	ParameterSchema           json.RawMessage
	Inputs                    []InputRequirement
	Dependencies              []VersionRef
	OutputUnit, MissingPolicy string
	AssetClasses              []string
}
type FactorRequest struct {
	Params        json.RawMessage
	InstrumentIDs []ID
	Data          DataView
	Dependencies  []FactorPoint
}
type FactorPoint struct {
	InstrumentID  ID
	Factor        VersionRef
	DecisionTime  time.Time
	Value         *float64
	MissingReason string
}
type Factor interface {
	Spec() FactorSpec
	Validate(json.RawMessage) []Issue
	Compute(context.Context, FactorRequest) ([]FactorPoint, error)
}
type FactorRegistry interface {
	List(context.Context, Page) (PageResult[FactorSpec], error)
	Resolve(context.Context, VersionRef) (Factor, error)
}
type FactorBinding struct {
	Factor VersionRef
	Params json.RawMessage
}
type FactorRunConfig struct {
	SnapshotID                  ID
	Universe                    VersionRef
	Factors                     []FactorBinding
	Range                       Interval
	Frequency, DecisionTimezone string
	AvailabilityPolicy          VersionRef
	StrictPIT                   bool
	Analysis                    json.RawMessage // Evaluation-only label config; validated schema.
}
type FactorEngine interface {
	Preflight(context.Context, FactorRunConfig) ([]Issue, error)
	Run(context.Context, FactorRunConfig, ProgressSink) (RunResult, error)
}
type Position struct {
	InstrumentID ID
	Quantity     Decimal
	Sellable     Decimal
}
type Money struct {
	Amount   Decimal
	Currency string
}
type Portfolio struct {
	Cash      []Money
	Equity    Money
	Positions []Position
}
type MarketEvent struct {
	ID       ID
	At       time.Time
	Sequence uint64
	Kind     string
	Data     []Observation
}
type StrategyContext struct {
	Data      DataView
	Portfolio Portfolio // Read-only copy.
	Factors   []FactorPoint
}
type TargetWeight struct {
	InstrumentID ID
	Weight       Decimal
}
type Decision struct {
	ID      ID
	Reason  string
	Targets []TargetWeight
}
type StrategySpec struct {
	Ref             VersionRef
	ParameterSchema json.RawMessage
	FactorRefs      []VersionRef
	RequiredInputs  []InputRequirement
}

// Each backtest owns its strategy instance; mutable state is never shared.
type Strategy interface {
	Initialize(context.Context, json.RawMessage) error
	OnEvent(context.Context, MarketEvent, StrategyContext) (Decision, error)
	Finish(context.Context) error
}
type StrategyFactory interface {
	Spec() StrategySpec
	New() Strategy
}
type OrderIntent struct {
	ID, InstrumentID, DecisionID ID
	Side, Type, TimeInForce      string
	Quantity                     Decimal
	LimitPrice                   *Decimal
}
type Fill struct {
	ID, OrderID, InstrumentID ID
	At                        time.Time
	Quantity, Price           Decimal
	Side                      string
	Fees                      []Money
}
type MarketRules interface {
	Ref() VersionRef
	Validate(context.Context, OrderIntent, Portfolio, DataView) []Issue
}
type PortfolioConstructor interface {
	Orders(context.Context, Decision, Portfolio, DataView) ([]OrderIntent, error)
}
type CostModel interface {
	Ref() VersionRef
	Fees(context.Context, Fill, DataView) ([]Money, error)
}

// Simulator owns pending order state; fills are handed to the sole ledger writer.
type FillSimulator interface {
	Ref() VersionRef
	Submit(context.Context, []OrderIntent, time.Time) ([]Issue, error)
	Cancel(context.Context, ID, time.Time) error
	Advance(context.Context, MarketEvent, DataView) ([]Fill, error)
}
type Ledger interface {
	ApplyFill(context.Context, Fill) error // Idempotent by fill ID.
	ApplyCorporateAction(context.Context, Observation) error
	Mark(context.Context, DataView) (Portfolio, error)
}
type ModelBinding struct {
	Model  VersionRef
	Params json.RawMessage
}
type BacktestConfig struct {
	SnapshotID                        ID
	Universe, Strategy                VersionRef
	MarketRules, CostModel, FillModel ModelBinding
	MetricPolicy                      ModelBinding
	Range                             Interval
	InitialCash                       Money
	Frequency, DecisionTimezone       string
	Benchmark, PricePolicy            string
	Params                            json.RawMessage
	Seed                              int64
	StrictPIT                         bool
	ValidationSplit                   json.RawMessage
}
type RunResult struct {
	RunID       ID
	Manifest    json.RawMessage
	ArtifactIDs []ID
}
type ProgressSink interface {
	Update(context.Context, string, int64, *int64) error // phase, completed, total
}
type BacktestEngine interface {
	Preflight(context.Context, BacktestConfig) ([]Issue, error)
	Run(context.Context, BacktestConfig, ProgressSink) (RunResult, error)
}
type JobState string

const (
	JobQueued          JobState = "queued"
	JobRunning         JobState = "running"
	JobCancelRequested JobState = "cancel_requested"
	JobSucceeded       JobState = "succeeded"
	JobFailed          JobState = "failed"
	JobCancelled       JobState = "cancelled"
)

type Job struct {
	ID, RunID   ID
	State       JobState
	Kind, Phase string
	Completed   int64
	Total       *int64
	Error       *Issue
}
type JobCommand struct {
	Kind, IdempotencyKey string
	Payload              json.RawMessage // Kind selects an explicit registered schema.
}
type JobEvent struct {
	JobID    ID
	Sequence uint64
	At       time.Time
	Job      Job
}
type JobService interface {
	Submit(context.Context, JobCommand) (Job, error)
	Get(context.Context, ID) (Job, error)
	Cancel(context.Context, ID) (Job, error)
	Retry(context.Context, ID, string) (Job, error)
	Events(context.Context, ID, uint64, Page) (PageResult[JobEvent], error)
}
type Artifact struct {
	ID                  ID
	MediaType, Checksum string
	Size                int64
}
type ExperimentStore interface {
	SaveResult(context.Context, RunResult) error // Publish atomically with artifacts.
	GetResult(context.Context, ID) (RunResult, error)
	List(context.Context, Page) (PageResult[RunResult], error)
}
type ArtifactStore interface {
	Put(context.Context, []byte, string) (Artifact, error)
	Get(context.Context, ID) ([]byte, Artifact, error)
}

// ResearchExporter has no live order permissions or account credentials.
type ResearchExporter interface {
	Export(context.Context, ID) (Artifact, error)
}
type Metric struct {
	Name, Unit, Definition string
	Value                  *float64
	MissingReason          string
}
type EvaluationReport struct {
	RunID        ID
	Metrics      []Metric
	Issues       []Issue
	ArtifactIDs  []ID
	MetricPolicy VersionRef
}
type ResearchAnalyzer interface {
	Evaluate(context.Context, ID, VersionRef) (EvaluationReport, error)
	Compare(context.Context, []ID, []string, string) (Artifact, error)
}
