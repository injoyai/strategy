"""Generate the proposed HTTP contract using Python's standard library only.

This is a documentation utility, not part of the Go backend runtime.
Run: python docs/api/build_openapi.py
"""
import json
from pathlib import Path


def ref(name):
    return {"$ref": f"#/components/schemas/{name}"}


def string(**kwargs):
    return {"type": "string", **kwargs}


def enum(*values):
    return string(enum=list(values))


def array(items, **kwargs):
    return {"type": "array", "items": items, **kwargs}


def obj(properties, required=(), **kwargs):
    return {"type": "object", "properties": properties, "required": list(required),
            "additionalProperties": False, **kwargs}


def mapping(values):
    return {"type": "object", "additionalProperties": values}


def nullable(schema):
    return {"anyOf": [schema, {"type": "null"}]}


ID = string(minLength=1, maxLength=128)
TEXT = string()
TIME = string(format="date-time")
BOOL = {"type": "boolean"}
NUM = {"type": "number"}
INT = {"type": "integer", "minimum": 0}
PARAMS = {"type": "object", "additionalProperties": True,
          "description": "Validated using the referenced registered parameter schema; not arbitrary executable code."}
SCHEMA = {"type": "object", "additionalProperties": True,
          "description": "JSON Schema 2020-12 for a registered configuration."}
S = {}
S["Decimal"] = string(pattern=r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$", description="Finite decimal string. Unit defined by its field.")
S["VersionRef"] = obj({"id": ID, "version": ID}, ["id", "version"],
    description="id identifies an immutable version resource; version must match its version label.")
S["Range"] = obj({"from": TIME, "to": TIME}, ["from", "to"], description="Half-open interval [from,to); from must precede to.")
S["Money"] = obj({"amount": ref("Decimal"), "currency": string(minLength=3, maxLength=12)}, ["amount", "currency"])
S["Issue"] = obj({"code": TEXT, "path": TEXT, "message": TEXT,
                  "severity": enum("error", "warning", "info"), "details": mapping(TEXT)},
                 ["code", "path", "message", "severity"])
S["Error"] = obj({"code": TEXT, "message": TEXT, "request_id": ID, "retryable": BOOL,
                  "issues": array(ref("Issue"))}, ["code", "message", "request_id", "retryable", "issues"])
S["Field"] = obj({"name": TEXT, "type": enum("decimal", "number", "string", "boolean", "timestamp", "unknown"),
                  "unit": TEXT, "nullable": BOOL, "description": TEXT}, ["name", "type", "unit", "nullable"],
                 description="unknown means the field is declared but no value was ever observed, so its type is not known; it is never guessed. An undeclared field observed in data carries an empty unit.")
S["Capability"] = obj({"dataset": TEXT, "fields": array(ref("Field")), "frequencies": array(TEXT),
                       "coverage": nullable(ref("Range")), "pit_level": enum("verified", "date_only", "unverified"),
                       "max_page_size": {"type": "integer", "minimum": 1}},
                      ["dataset", "fields", "frequencies", "coverage", "pit_level", "max_page_size"])
S["Provider"] = obj({"id": ID, "version": ID, "name": TEXT, "config_schema": SCHEMA,
                     "capabilities": array(ref("Capability"))}, ["id", "version", "name", "config_schema", "capabilities"])
S["SecretCreate"] = obj({"name": TEXT, "value": string(writeOnly=True, minLength=1)}, ["name", "value"])
S["SecretRef"] = obj({"secret_ref": ID}, ["secret_ref"])
S["ConnectionCreate"] = obj({"name": TEXT, "provider_ref": ref("VersionRef"), "settings": PARAMS,
                             "secret_ref": ID, "parent_id": ID}, ["name", "provider_ref", "settings"])
S["Connection"] = obj({**S["ConnectionCreate"]["properties"], "id": ID, "version": ID, "created_at": TIME},
                      ["id", "version", "name", "provider_ref", "settings", "created_at"])
S["Mapping"] = obj({"source_field": TEXT, "target_field": TEXT, "source_unit": TEXT,
                    "target_unit": TEXT, "scale": ref("Decimal")},
                   ["source_field", "target_field", "source_unit", "target_unit", "scale"])
S["ImportUpload"] = obj({"file": string(format="binary"), "format": enum("csv", "parquet")}, ["file", "format"])
S["IngestionCreate"] = obj({"connection_ref": ref("VersionRef"), "import_id": ID, "dataset": TEXT,
    "frequency": TEXT, "instrument_ids": array(ID, minItems=1), "range": ref("Range"),
    "mode": enum("incremental", "backfill", "force"), "mapping": array(ref("Mapping")),
    "timezone": TEXT, "availability_policy_ref": ref("VersionRef")},
    ["dataset", "frequency", "instrument_ids", "range", "mode", "mapping", "timezone", "availability_policy_ref"],
    oneOf=[{"required": ["connection_ref"], "not": {"required": ["import_id"]}},
           {"required": ["import_id"], "not": {"required": ["connection_ref"]}}],
    description="Exactly one input source. force always calls the upstream source; imported data is re-normalized into a new batch.")
S["Dataset"] = obj({"id": ID, "name": TEXT, "schema_version": TEXT, "frequencies": array(TEXT),
    "fields": array(ref("Field")),
    "natural_key": array(TEXT), "availability_policy_ref": ref("VersionRef"), "coverage": nullable(ref("Range")),
    "quality_issues": array(ref("Issue"))},
    ["id", "name", "schema_version", "frequencies", "fields", "natural_key", "availability_policy_ref", "coverage", "quality_issues"],
    description="frequency is how often the dataset's rows were ingested; a reader asking for a frequency outside the list finds no rows.")
S["Batch"] = obj({"id": ID, "job_id": ID, "dataset_id": ID, "row_count": INT,
    "checksum": TEXT, "created_at": TIME, "issues": array(ref("Issue")), "ready": BOOL},
    ["id", "job_id", "dataset_id", "row_count", "checksum", "created_at", "issues", "ready"])
S["SnapshotCreate"] = obj({"name": TEXT, "batch_ids": array(ID, minItems=1), "strict_pit": BOOL},
                          ["name", "batch_ids", "strict_pit"])
S["Snapshot"] = obj({**S["SnapshotCreate"]["properties"], "id": ID, "manifest_hash": TEXT,
    "created_at": TIME, "quality_issues": array(ref("Issue"))},
    ["id", "name", "batch_ids", "strict_pit", "manifest_hash", "created_at", "quality_issues"])
S["Provenance"] = obj({"source_id": ID, "source_record_id": TEXT, "revision_id": ID,
    "supersedes_revision_id": nullable(ID), "published_at": nullable(TIME), "available_at": TIME,
    "ingested_at": TIME, "schema_version": TEXT, "policy_version": TEXT, "quality_flags": array(TEXT)},
    ["source_id", "source_record_id", "revision_id", "published_at", "available_at", "ingested_at", "schema_version", "policy_version", "quality_flags"])
S["Value"] = obj({"kind": enum("decimal", "number", "string", "boolean", "timestamp"),
    "value": {"type": ["string", "number", "boolean", "null"]}, "missing_reason": nullable(TEXT)},
    ["kind", "value", "missing_reason"], description="kind determines the scalar type; null requires a missing_reason. Decimal uses string.")
S["Observation"] = obj({"instrument_id": nullable(ID), "entity_id": nullable(ID), "dataset": TEXT,
    "event_time": TIME, "period_end": nullable(string(format="date")), "effective": nullable(ref("Range")),
    "values": mapping(ref("Value")), "provenance": ref("Provenance")},
    ["instrument_id", "entity_id", "dataset", "event_time", "values", "provenance"])
S["DataQuery"] = obj({"snapshot_id": ID, "as_of": TIME, "replay_time": nullable(TIME), "dataset": TEXT, "frequency": TEXT,
    "instrument_ids": array(ID, minItems=1), "fields": array(TEXT, minItems=1), "range": ref("Range"),
    "cursor": TEXT, "limit": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}},
    ["snapshot_id", "as_of", "dataset", "frequency", "instrument_ids", "fields", "range"])
S["Expression"] = {"oneOf": [
    obj({"kind": enum("field"), "dataset": TEXT, "field": TEXT}, ["kind", "dataset", "field"]),
    obj({"kind": enum("constant"), "value": ref("Decimal")}, ["kind", "value"]),
    obj({"kind": enum("parameter"), "name": TEXT}, ["kind", "name"]),
    obj({"kind": enum("factor"), "factor_ref": ref("VersionRef")}, ["kind", "factor_ref"]),
    obj({"kind": enum("operator"), "operator": enum("add", "subtract", "multiply", "divide", "lag", "mean", "std", "rank", "zscore", "gt", "lt", "and", "or"),
         "args": array(ref("Expression"), minItems=1)}, ["kind", "operator", "args"])
], "description": "Bounded AST; server validates arity, types, units, lookback, depth, and allowed dataset visibility."}
S["UniverseDefinition"] = {"oneOf": [
    obj({"kind": enum("static"), "members": array(ID, minItems=1)}, ["kind", "members"]),
    obj({"kind": enum("historical_rule"), "rule": obj({"dataset": TEXT, "frequency": TEXT}, ["dataset", "frequency"])},
        ["kind", "rule"])
], "description": "static freezes an explicit member list (the research decision, valid exactly as saved); historical_rule resolves membership from a snapshot dataset's effective windows at every decision time, so delisted and later-removed instruments stay inside their historical samples."}
S["UniverseCreate"] = obj({"name": TEXT, "snapshot_id": ID, "definition": ref("UniverseDefinition")},
    ["name", "snapshot_id", "definition"],
    description="Creation is not idempotent: every save mints a new immutable version bound to the named snapshot. Resolution always goes through a view pinned to that snapshot — never current tables.")
S["Universe"] = obj({**S["UniverseCreate"]["properties"], "id": ID, "definition_hash": TEXT, "created_at": TIME},
    ["id", "name", "snapshot_id", "definition", "definition_hash", "created_at"],
    description="definition is the canonical (sorted, deduplicated) form; definition_hash covers the definition alone, excluding name and snapshot binding.")
# Provenance of a universe saved from a screening run: the selection time and the
# quality limits the pool was explored under. Like name and snapshot binding it
# sits outside definition_hash — it does not change which members were selected,
# it records when and under what caveats they were.
S["Universe"]["properties"]["source"] = nullable(ref("ScreenUniverseSource"))
S["Universe"]["description"] += (" source records the screening run a static pool was saved from (null for a manually created universe); "
    "a backtest must refuse a pool whose source as_of is later than its own decision time.")
S["UniverseResolve"] = obj({"as_of": TIME}, ["as_of"],
    description="Read-only preview; the snapshot comes from the universe version's binding, never from the request.")
S["UniverseMembers"] = obj({"universe_id": ID, "as_of": TIME, "instrument_ids": array(ID), "count": INT},
    ["universe_id", "as_of", "instrument_ids", "count"],
    description="Membership resolved through a DataView pinned to the bound snapshot at as_of; an empty list is a valid membership, not an error.")
S["InputRequirement"] = obj({"dataset": TEXT, "fields": array(TEXT, minItems=1), "frequency": TEXT,
    "lookback_periods": INT, "max_staleness_seconds": INT, "requires_strict_pit": BOOL},
    ["dataset", "fields", "frequency", "lookback_periods", "max_staleness_seconds", "requires_strict_pit"])
S["FactorParam"] = obj({"name": TEXT, "type": enum("integer", "number", "string", "boolean"), "required": BOOL,
    "default": {"type": ["integer", "number", "string", "boolean", "null"]}, "min": nullable(NUM),
    "max": nullable(NUM), "enum": array(TEXT)},
    ["name", "type", "required"],
    description="Optional parameters carry a default; canonicalization fills defaults in, so an explicit default and an omitted parameter produce the same run.")
S["FactorInput"] = obj({"name": TEXT, "dataset": TEXT, "field": TEXT, "frequency": TEXT,
    "lookback": {"type": "integer", "minimum": 0}, "unit": TEXT, "pit": BOOL,
    "max_staleness_seconds": nullable({"type": "integer", "minimum": 1})},
    ["name", "dataset", "field", "frequency", "lookback", "unit", "pit"],
    description="lookback 0 means latest-value; parameterized windows resolve per request from the params. unit is mandatory so unit-mismatched arithmetic fails at registration, never mid-run.")
S["Factor"] = obj({"id": ID, "version": ID, "title": TEXT, "kind": enum("builtin", "expression"),
    "params": array(ref("FactorParam")), "inputs": array(ref("FactorInput")),
    "dependencies": array(ref("VersionRef")), "output_unit": TEXT, "asset_classes": array(TEXT, minItems=1)},
    ["id", "version", "title", "kind", "params", "inputs", "dependencies", "output_unit", "asset_classes"],
    description="The factor capability catalog row: everything a form, a preflight or a dependency backlink needs. Factors are registered id+version pairs (M1 registers the builtin family at startup) and immutable once live.")
S["FactorBinding"] = obj({"factor_ref": ref("VersionRef"), "params": PARAMS}, ["factor_ref", "params"])
S["FactorRunRequest"] = obj({"snapshot_id": ID, "universe_id": ID, "factor_ref": ref("VersionRef"), "params": PARAMS,
    "as_of": TIME, "window_from": TIME},
    ["snapshot_id", "universe_id", "factor_ref", "as_of", "window_from"],
    description="Synchronous computation over the pinned view: preflight returns every problem at once, then the engine computes the cross-section and serves the frame inline. [window_from, as_of) is the half-open input window; availability follows the view's available_at <= as_of semantics.")
S["FactorMember"] = obj({"instrument_id": ID, "value": nullable(ref("Decimal")), "missing_reason": nullable(TEXT)},
    ["instrument_id", "value", "missing_reason"],
    description="The frame invariant: exactly one of value / missing_reason is present.")
S["FactorRunResult"] = obj({"factor_ref": ref("VersionRef"), "snapshot_id": ID, "universe_id": ID, "as_of": TIME,
    "covered": INT, "total": INT, "members": array(ref("FactorMember"))},
    ["factor_ref", "snapshot_id", "universe_id", "as_of", "covered", "total", "members"],
    description="Frame at as_of over the universe resolved at that time. covered counts members carrying a value; missing members carry their reason — coverage is reported per reason, never zero-filled.")
S["StrategyTemplate"] = obj({"id": ID, "version": ID, "name": TEXT, "parameter_schema": SCHEMA,
    "inputs": array(ref("InputRequirement")), "description": TEXT}, ["id", "version", "name", "parameter_schema", "inputs", "description"])
S["StrategyCreate"] = obj({"name": TEXT, "hypothesis": TEXT, "failure_criteria": TEXT,
    "template_ref": ref("VersionRef"), "factors": array(ref("FactorBinding")), "params": PARAMS, "parent_id": ID},
    ["name", "hypothesis", "failure_criteria", "template_ref", "factors", "params"])
S["Strategy"] = obj({**S["StrategyCreate"]["properties"], "id": ID, "version": ID},
    ["id", "version", *S["StrategyCreate"]["required"]])
S["Model"] = obj({"id": ID, "version": ID, "name": TEXT,
    "kind": enum("market_rules", "cost", "fill", "availability", "metric"), "parameter_schema": SCHEMA,
    "capabilities": array(TEXT)}, ["id", "version", "name", "kind", "parameter_schema", "capabilities"])
S["ModelBinding"] = obj({"model_ref": ref("VersionRef"), "params": PARAMS}, ["model_ref", "params"])
S["ValidationSplit"] = obj({"development": ref("Range"), "validation": ref("Range"), "test": ref("Range")},
    ["development", "validation", "test"], description="Chronologically ordered and non-overlapping; preprocessing uses development only. Server validates label overlap.")
S["BacktestCreate"] = obj({"name": TEXT, "snapshot_id": ID, "universe_ref": ref("VersionRef"),
    "strategy_ref": ref("VersionRef"), "market_rules": ref("ModelBinding"), "cost_model": ref("ModelBinding"),
    "fill_model": ref("ModelBinding"), "metric_policy": ref("ModelBinding"), "range": ref("Range"), "initial_cash": ref("Money"),
    "frequency": TEXT, "decision_timezone": TEXT, "benchmark_id": ID, "price_policy": TEXT,
    "seed": {"type": "integer", "minimum": -9007199254740991, "maximum": 9007199254740991},
    "strict_pit": BOOL, "validation_split": ref("ValidationSplit"), "source_run_id": ID},
    ["name", "snapshot_id", "universe_ref", "strategy_ref", "market_rules", "cost_model", "fill_model", "metric_policy",
     "range", "initial_cash", "frequency", "decision_timezone", "benchmark_id", "price_policy", "seed", "strict_pit", "validation_split"],
    description="Positive initial cash; all models and benchmark must be compatible. No implicit market, currency, zero fee or fill defaults.")
S["Preflight"] = obj({"valid": BOOL, "issues": array(ref("Issue")), "estimated_rows": nullable(INT)},
    ["valid", "issues", "estimated_rows"])
S["Job"] = obj({"id": ID, "run_id": nullable(ID), "parent_job_id": nullable(ID), "kind": TEXT,
    "state": enum("queued", "running", "cancel_requested", "succeeded", "failed", "cancelled"),
    "phase": TEXT, "completed": INT, "total": nullable(INT), "created_at": TIME, "updated_at": TIME,
    "error": nullable(ref("Error")), "result_refs": array(obj({"kind": enum("batch", "snapshot", "import", "factor_run", "backtest", "screen_run", "artifact"), "id": ID}, ["kind", "id"]))},
    ["id", "run_id", "parent_job_id", "kind", "state", "phase", "completed", "total", "created_at", "updated_at", "error", "result_refs"])
S["JobEvent"] = obj({"job_id": ID, "sequence": string(pattern="^[0-9]+$"), "at": TIME, "job": ref("Job")},
    ["job_id", "sequence", "at", "job"], description="Sequence is a string to avoid browser integer precision loss.")
S["Metric"] = obj({"name": TEXT, "value": nullable(NUM), "unit": TEXT, "definition": TEXT,
    "missing_reason": nullable(TEXT)}, ["name", "value", "unit", "definition", "missing_reason"])
S["Artifact"] = obj({"id": ID, "name": TEXT, "media_type": TEXT, "checksum": TEXT,
    "size_bytes": INT, "created_at": TIME}, ["id", "name", "media_type", "checksum", "size_bytes", "created_at"])
S["Report"] = obj({"run_id": ID, "metrics": array(ref("Metric")), "issues": array(ref("Issue")),
    "artifact_ids": array(ID), "metric_policy_ref": ref("VersionRef"), "currency": TEXT,
    "range": ref("Range"), "benchmark_id": ID, "strict_pit": BOOL},
    ["run_id", "metrics", "issues", "artifact_ids", "metric_policy_ref", "currency", "range", "benchmark_id", "strict_pit"])
S["EquityPoint"] = obj({"at": TIME, "equity": ref("Money"), "nav": NUM, "benchmark_nav": nullable(NUM),
    "drawdown": NUM, "segment": enum("development", "validation", "test"), "quality_flags": array(TEXT)},
    ["at", "equity", "nav", "benchmark_nav", "drawdown", "segment", "quality_flags"])
S["AnalysisSegment"] = obj({"kind": enum("train", "validation", "test"), "range": ref("Range")}, ["kind", "range"],
    description="Chronologically ordered, non-overlapping and inside the analysis range; gaps are reported as uncovered dates, never hidden. Any future fitting reads the train segment only.")
S["FactorAnalysisCreate"] = obj({"snapshot_id": ID, "universe_id": ID, "factor_ref": ref("VersionRef"), "params": PARAMS,
    "range": ref("Range"), "as_of": TIME, "horizons": array({"type": "integer", "minimum": 1}, minItems=1),
    "groups": {"type": "integer", "minimum": 2, "maximum": 10}, "min_samples": {"type": "integer", "minimum": 2},
    "method": enum("pearson", "spearman"), "segments": array(ref("AnalysisSegment"))},
    ["snapshot_id", "universe_id", "factor_ref", "range", "as_of", "horizons", "groups", "min_samples", "method"],
    description="Synchronous factor-evidence analysis. as_of is the research present: it pins the label view and must not precede range.to. Horizons are holding periods in trading sessions. Labels are computed server-side from close prices — evaluation only, never factor inputs.")
S["Stat"] = obj({"value": nullable(NUM), "reason": TEXT}, ["value"],
    description="The null+reason contract: zero denominators, insufficient samples and not-applicable cases report null with a reason instead of a zero; a present value is always finite (never NaN or Infinity).")
S["Distribution"] = obj({"count": INT, "mean": ref("Stat"), "std": ref("Stat"), "min": ref("Stat"),
    "q05": ref("Stat"), "q25": ref("Stat"), "q50": ref("Stat"), "q75": ref("Stat"), "q95": ref("Stat"), "max": ref("Stat")},
    ["count", "mean", "std", "min", "q05", "q25", "q50", "q75", "q95", "max"],
    description="Quantiles use linear interpolation over the sorted values.")
S["SeriesSummary"] = obj({"n": INT, "mean": ref("Stat"), "std": ref("Stat"), "ir": ref("Stat"), "positive_rate": ref("Stat")},
    ["n", "mean", "std", "ir", "positive_rate"],
    description="Aggregates the daily values that were computable; n is that date count. ir is mean over sample std — a constant series reports the zero-denominator reason.")
S["BucketSummary"] = obj({"bucket": {"type": "integer", "minimum": 1}, "dates": INT, "mean_return": ref("Stat")},
    ["bucket", "dates", "mean_return"],
    description="bucket 1 holds the lowest factor values; the mean is the equal-weight mean of daily bucket means.")
S["HorizonSummary"] = obj({"horizon": {"type": "integer", "minimum": 1}, "label_dates": INT, "missing_label_dates": INT,
    "pairs": INT, "ic": ref("SeriesSummary"), "rank_ic": ref("SeriesSummary"), "correlation": ref("SeriesSummary"),
    "buckets": array(ref("BucketSummary")), "high_low_spread": ref("SeriesSummary")},
    ["horizon", "label_dates", "missing_label_dates", "pairs", "ic", "rank_ic", "correlation", "high_low_spread"],
    description="The decay view: how IC, Rank IC, quantile bucket means and the high-minus-low spread evolve as the holding period grows. correlation aggregates the series selected by method; ic and rank_ic are always fully reported.")
S["SegmentHorizon"] = obj({"horizon": {"type": "integer", "minimum": 1}, "labels": INT}, ["horizon", "labels"])
S["SegmentSummary"] = obj({"kind": TEXT, "from": TIME, "to": TIME, "dates": INT, "samples": INT,
    "horizons": array(ref("SegmentHorizon"))}, ["kind", "from", "to", "dates", "samples", "horizons"])
S["CoverageSummary"] = obj({"covered": INT, "total": INT, "rate": ref("Stat")}, ["covered", "total", "rate"])
S["AnalysisSummary"] = obj({"dates": INT, "coverage": ref("CoverageSummary"), "missing_reasons": mapping(INT),
    "pooled_distribution": ref("Distribution"), "horizons": array(ref("HorizonSummary")),
    "segments": array(ref("SegmentSummary")), "uncovered_dates": array(TIME)},
    ["dates", "coverage", "pooled_distribution", "horizons"],
    description="Page-level rollup of the full series; chart downsampling happens at render time and never mutates the stored artifact.")
S["AnalysisConfig"] = obj({"ref": ref("VersionRef"), "definition_hash": TEXT, "params": PARAMS,
    "label_price": TEXT, "label_entry": TEXT, "label_cost": TEXT,
    "horizons": array({"type": "integer", "minimum": 1}), "method": TEXT,
    "groups": {"type": "integer", "minimum": 2}, "min_samples": {"type": "integer", "minimum": 2},
    "range": ref("Range"), "segments": array(ref("AnalysisSegment"))},
    ["ref", "definition_hash", "params", "label_price", "label_entry", "label_cost", "horizons", "method", "groups", "min_samples", "range"],
    description="The fixed analysis configuration the artifact was computed under; label values themselves never appear — labels are an input, not a stored dataset.")
S["FactorAnalysisResult"] = obj({"artifact": ref("Artifact"), "config": ref("AnalysisConfig"),
    "summary": ref("AnalysisSummary"), "evidence_note": TEXT},
    ["artifact", "config", "summary", "evidence_note"],
    description="The full per-date series lives in the canonical artifact (schema analysis-artifact/1) behind artifact.id; the response embeds only the page summary.")
S["ResearchSeries"] = obj({"schema_version": ID, "series_id": ID, "name": TEXT, "unit": TEXT,
    "points": array(obj({"at": TIME, "value": nullable(NUM), "group": nullable(TEXT), "missing_reason": nullable(TEXT)},
        ["at", "value", "group", "missing_reason"]))}, ["schema_version", "series_id", "name", "unit", "points"],
    description="JSON artifact format for factor IC, coverage and grouped-return series; large outputs are split into ordered artifacts.")
S["ComparisonResult"] = obj({"schema_version": ID, "experiment_ids": array(ID),
    "rows": array(obj({"experiment_id": ID, "metrics": array(ref("Metric"))}, ["experiment_id", "metrics"])),
    "basis_differences": array(ref("Issue"))}, ["schema_version", "experiment_ids", "rows", "basis_differences"],
    description="JSON artifact returned by a completed comparison job. Basis mismatches must remain visible.")
S["Record"] = obj({"id": ID, "kind": enum("decision", "order", "fill", "position", "cash"),
    "at": TIME, "instrument_id": nullable(ID), "parent_id": nullable(ID), "fields": mapping(ref("Value"))},
    ["id", "kind", "at", "instrument_id", "parent_id", "fields"],
    description="Field schemas and units are returned in record_schema; parent_id traces the causal chain.")
S["Manifest"] = obj({"schema_version": ID, "build_hash": TEXT, "environment_hash": TEXT,
    "snapshot_hash": TEXT, "config_hash": TEXT, "factor_refs": array(ref("VersionRef")),
    "artifact_ids": array(ID), "availability_policy_ref": ref("VersionRef"), "seed": TEXT},
    ["schema_version", "build_hash", "environment_hash", "snapshot_hash", "config_hash", "factor_refs", "artifact_ids", "availability_policy_ref", "seed"])
S["Backtest"] = obj({"id": ID, "job_id": ID, "config": ref("BacktestCreate"),
    "manifest": nullable(ref("Manifest"))}, ["id", "job_id", "config", "manifest"])
S["Experiment"] = obj({"id": ID, "name": TEXT, "kind": enum("factor", "backtest"),
    "run_id": ID, "job_id": ID, "created_at": TIME, "manifest": nullable(ref("Manifest")),
    "metrics": array(ref("Metric"))}, ["id", "name", "kind", "run_id", "job_id", "created_at", "manifest", "metrics"])
S["CompareCreate"] = obj({"experiment_ids": array(ID, minItems=2, maxItems=20, uniqueItems=True),
    "metric_names": array(TEXT, minItems=1), "alignment": enum("require_same_basis", "show_differences")},
    ["experiment_ids", "metric_names", "alignment"])
S["ExportCreate"] = obj({"run_id": ID, "kind": enum("report", "records", "factor_matrix", "research_bundle"),
    "format": enum("json", "csv", "parquet", "html", "zip")}, ["run_id", "kind", "format"],
    description="Server rejects unsupported kind/format combinations and unauthorized raw-data exports.")
S["EmptyCommand"] = obj({})

S["ScreenInputBinding"] = {"oneOf": [
    obj({"binding_id": ID, "kind": enum("field"), "dataset": TEXT, "field": TEXT},
        ["binding_id", "kind", "dataset", "field"]),
    obj({"binding_id": ID, "kind": enum("factor"), "factor_ref": ref("VersionRef"), "params": PARAMS},
        ["binding_id", "kind", "factor_ref", "params"]),
], "description": "Distinct binding_id values are required for parameterized factors; the same factor with different params is a different binding."}
S["ScreenInput"] = obj({"binding_id": ID}, ["binding_id"])
S["ScreenCondition"] = {"oneOf": [
    obj({"node_id": ID, "kind": enum("all"), "children": array(ref("ScreenCondition"), minItems=1)},
        ["node_id", "kind", "children"]),
    obj({"node_id": ID, "kind": enum("any"), "children": array(ref("ScreenCondition"), minItems=1)},
        ["node_id", "kind", "children"]),
    obj({"node_id": ID, "kind": enum("not"), "child": ref("ScreenCondition")}, ["node_id", "kind", "child"]),
    obj({"node_id": ID, "kind": enum("compare"), "input": ref("ScreenInput"),
         "operator": enum("eq", "ne", "gt", "gte", "lt", "lte"), "value": ref("Value")},
        ["node_id", "kind", "input", "operator", "value"]),
    obj({"node_id": ID, "kind": enum("range"), "input": ref("ScreenInput"), "lower": nullable(ref("Value")),
         "upper": nullable(ref("Value")), "lower_inclusive": BOOL, "upper_inclusive": BOOL},
        ["node_id", "kind", "input", "lower", "upper", "lower_inclusive", "upper_inclusive"],
        description="At least one bound must be present; lower must not exceed upper."),
    obj({"node_id": ID, "kind": enum("set"), "input": ref("ScreenInput"), "in": array(ref("Value"), minItems=1)},
        ["node_id", "kind", "input", "in"]),
    obj({"node_id": ID, "kind": enum("set"), "input": ref("ScreenInput"), "not_in": array(ref("Value"), minItems=1)},
        ["node_id", "kind", "input", "not_in"]),
    obj({"node_id": ID, "kind": enum("missing"), "input": ref("ScreenInput"), "is_missing": BOOL},
        ["node_id", "kind", "input", "is_missing"]),
    obj({"node_id": ID, "kind": enum("missing"), "input": ref("ScreenInput"), "is_present": BOOL},
        ["node_id", "kind", "input", "is_present"]),
], "description": "Typed condition tree; node_id is unique within a screener version and locates per-condition explanations and issues. Three-valued: NOT unknown = unknown, AND is false-dominant, OR is true-dominant; only a true root enters ranking. See docs/stock-screening-design.md."}
S["ScreenRankComponent"] = obj({"input": ref("ScreenInput"), "direction": enum("asc", "desc")}, ["input", "direction"])
S["ScreenScoreComponent"] = obj({"input": ref("ScreenInput"), "weight": ref("Decimal"),
    "direction": enum("larger_is_better", "smaller_is_better")},
    ["input", "weight", "direction"],
    description="Weights are non-negative, submitted normalized, sum to 1 and are never re-distributed when a component is missing.")
S["ScreenRanking"] = {"oneOf": [
    obj({"mode": enum("sort"), "fields": array(ref("ScreenRankComponent"), minItems=1)}, ["mode", "fields"]),
    obj({"mode": enum("score"), "components": array(ref("ScreenScoreComponent"), minItems=1)}, ["mode", "components"]),
], "description": "Mutually exclusive ranking modes. instrument_id ascending is always appended as the stable tie-breaker; instruments missing ranking values never rank, without zero-filling."}
S["ScreenSelection"] = {"oneOf": [
    obj({"mode": enum("all")}, ["mode"]),
    obj({"mode": enum("top_n"), "n": {"type": "integer", "minimum": 1}}, ["mode", "n"]),
], "description": "Fewer than N qualified instruments returns all of them; unqualified instruments are never padded in."}
S["ScreenerCreate"] = obj({"name": TEXT, "description": TEXT,
    "input_bindings": array(ref("ScreenInputBinding"), minItems=1), "condition_tree": ref("ScreenCondition"),
    "ranking": ref("ScreenRanking"), "selection": ref("ScreenSelection"),
    "display_columns": array(ID), "parent_id": ID},
    ["name", "input_bindings", "condition_tree", "ranking", "selection"])
S["Screener"] = obj({**S["ScreenerCreate"]["properties"], "id": ID, "version": ID,
                     "rule_schema_version": TEXT, "created_at": TIME},
                    ["id", "version", *S["ScreenerCreate"]["required"], "rule_schema_version", "created_at"])
S["ScreenRunCreate"] = obj({"screener_ref": ref("VersionRef"), "snapshot_id": ID, "universe_ref": ref("VersionRef"),
    "as_of": TIME, "decision_timezone": TEXT, "strict_pit": BOOL,
    "required_value_policy": enum("exclude_instrument", "fail_run"), "source_run_id": ID},
    ["screener_ref", "snapshot_id", "universe_ref", "as_of", "decision_timezone", "strict_pit", "required_value_policy"],
    description="All inputs are frozen at submission; no implicit snapshot, universe, time or policy defaults. source_run_id links a re-run to its origin.")
S["ScreenInputCoverage"] = obj({"binding_id": ID, "available": BOOL, "reason": nullable(TEXT)},
    ["binding_id", "available", "reason"])
S["ScreenPreflight"] = obj({"valid": BOOL, "issues": array(ref("Issue")), "coverage": array(ref("ScreenInputCoverage")),
    "estimated_scan_rows": nullable(INT), "estimated_rows": nullable(INT)},
    ["valid", "issues", "coverage", "estimated_scan_rows", "estimated_rows"],
    description="Read-only check; submitting a run re-validates and may still fail.")
S["ScreenSummary"] = obj({"population": INT, "condition_false": INT, "condition_unknown": INT, "condition_true": INT,
    "rank_insufficient": INT, "rankable": INT, "selected": INT, "not_selected": INT, "empty_reason": nullable(TEXT)},
    ["population", "condition_false", "condition_unknown", "condition_true", "rank_insufficient", "rankable", "selected", "not_selected", "empty_reason"],
    description="Stage counts are mutually exclusive and conserve: population = condition_false + condition_unknown + condition_true; condition_true = rank_insufficient + rankable; rankable = selected + not_selected.")
S["ScreenRun"] = obj({"id": ID, "job_id": ID, "config": ref("ScreenRunCreate"), "engine_version": TEXT,
    "scoring_policy_version": TEXT, "config_hash": TEXT, "snapshot_hash": TEXT, "created_at": TIME,
    "summary": nullable(ref("ScreenSummary")), "artifact_ids": array(ID)},
    ["id", "job_id", "config", "engine_version", "scoring_policy_version", "config_hash", "snapshot_hash", "created_at", "summary", "artifact_ids"],
    description="summary and rows stay unset until the run publishes; earlier access returns 409 result_not_ready.")
S["ScreenRow"] = obj({"instrument_id": ID, "symbol": nullable(TEXT), "name": nullable(TEXT), "selected": BOOL,
    "rank": nullable(INT), "score": nullable(ref("Decimal")), "values": mapping(ref("Value")), "reason": TEXT,
    "quality_flags": array(TEXT)},
    ["instrument_id", "selected", "rank", "score", "values", "reason", "quality_flags"],
    description="Excluded rows have a null rank and sort after selected and rankable rows; values carry display columns with missing reasons.")
S["ScreenNodeEvaluation"] = obj({"node_id": ID, "truth": enum("true", "false", "unknown"),
    "input": nullable(ref("ScreenInput")), "threshold": nullable(ref("Value")), "missing_reason": nullable(TEXT),
    "data_time": nullable(TIME), "revision_id": nullable(ID), "children": array(ref("ScreenNodeEvaluation"))},
    ["node_id", "truth", "children"],
    description="Per-node evidence mirroring the condition tree; unknown is a distinct truth value, never coerced. Explanations reference frozen data, not current values.")
S["ScreenScoreEvidence"] = obj({"binding_id": ID, "raw_value": nullable(ref("Value")), "percentile": nullable(NUM),
    "weight": ref("Decimal"), "contribution": NUM}, ["binding_id", "weight", "contribution"],
    description="Missing components keep their declared weight with no redistribution; raw_value and percentile are then null with the missing reason carried by the Value.")
S["ScreenExplanation"] = obj({"run_id": ID, "instrument_id": ID,
    "stage": enum("selected", "condition_false", "condition_unknown", "rank_insufficient", "not_selected"),
    "nodes": ref("ScreenNodeEvaluation"), "score": array(ref("ScreenScoreEvidence"))},
    ["run_id", "instrument_id", "stage", "nodes", "score"],
    description="score is empty in sort mode. stage is the single mutually exclusive classification used by ScreenSummary.")
S["ScreenUniverseSource"] = obj({"screen_run_id": ID, "as_of": TIME, "snapshot_hash": TEXT, "quality_limits": array(TEXT)},
    ["screen_run_id", "as_of", "snapshot_hash", "quality_limits"])
S["ScreenUniverseCreate"] = obj({"name": TEXT}, ["name"],
    description="Saves all selected instruments of a successful run as a new static universe; an empty selection returns 422 empty_selection.")
S["ScreenExportCreate"] = obj({"scope": enum("selected", "all_candidates"), "format": enum("csv", "json")},
    ["scope", "format"])

# ---- Replay (historical manual trading) ----
# Models referenced by ref are fail-closed: no implicit market, fill, cost or valuation defaults.
# Unknown or unsupported policy/model values are rejected at preflight, never silently defaulted.
S["ReplayConfig"] = obj({"name": TEXT, "snapshot_id": ID, "universe_ref": ref("VersionRef"),
    "range": ref("Range"), "decision_timezone": TEXT, "warmup_days": INT,
    "day_end_policy": TEXT, "strict_pit": BOOL, "initial_cash": ref("Money"),
    "market_rules": ref("ModelBinding"), "fill_model": ref("ModelBinding"), "cost_model": ref("ModelBinding"),
    "valuation_policy": ref("ModelBinding"), "metrics_policy": ref("ModelBinding"),
    "cash_policy": TEXT, "end_policy": TEXT, "benchmark_ref": nullable(ref("VersionRef")),
    "order_types": array(TEXT), "parent_id": ID},
    ["name", "snapshot_id", "universe_ref", "range", "decision_timezone", "warmup_days", "day_end_policy",
     "strict_pit", "initial_cash", "market_rules", "fill_model", "cost_model", "valuation_policy",
     "metrics_policy", "cash_policy", "end_policy", "order_types"],
    description="Frozen inputs for a historical replay session. day_end_policy, cash_policy, end_policy and order_types are explicit validated strings; supported model/parameter values are declared by the referenced versioned Model and rejected otherwise (no implicit defaults).")
S["ReplayPreflight"] = obj({"valid": BOOL, "issues": array(ref("Issue")),
    "execution_dates": nullable(INT), "model_availability": array(ref("ScreenInputCoverage"))},
    ["valid", "issues", "execution_dates", "model_availability"],
    description="Read-only check of config, data coverage and referenced model availability; creating the session re-validates.")
S["ReplaySessionState"] = enum("initializing", "awaiting_action", "advancing", "closing", "completed", "failed")
S["ReplaySession"] = obj({"id": ID, "config_hash": TEXT, "config": ref("ReplayConfig"),
    "current_as_of": TIME, "revision": INT, "state": ref("ReplaySessionState"),
    "account_ref": nullable(ID), "checkpoint_ref": nullable(ID), "end_reason": nullable(TEXT),
    "created_at": TIME},
    ["id", "config_hash", "config", "current_as_of", "revision", "state", "created_at"],
    description="ReplaySession can span multiple Jobs and real days; revision is the optimistic-concurrency token every mutating command must match.")
S["SessionCommand"] = obj({"session_id": ID, "command_id": ID, "expected_revision": INT},
    ["session_id", "command_id", "expected_revision"],
    description="Base for every mutating replay command; command_id keys idempotency, expected_revision guards optimistic concurrency. Browsers never supply a historical decision time.")
S["ManualOrderCreate"] = obj({**S["SessionCommand"]["properties"], "instrument_id": ID,
    "direction": enum("buy", "sell"), "quantity": ref("Decimal"), "order_type": TEXT,
    "limit_price": nullable(ref("Money")), "time_in_force": enum("day")},
    ["session_id", "command_id", "expected_revision", "instrument_id", "direction", "quantity", "order_type", "time_in_force"],
    description="User provides trade intent only; fills are determined by the model, never by the client.")
S["ManualOrder"] = obj({"id": ID, "session_id": ID, "command_id": ID, "instrument_id": ID,
    "direction": enum("buy", "sell"), "quantity": ref("Decimal"), "order_type": TEXT,
    "limit_price": nullable(ref("Money")), "time_in_force": enum("day"),
    "submitted_at": TIME, "decision_at": TIME,
    "state": enum("accepted", "partially_filled", "filled", "cancelled", "expired", "rejected"),
    "filled_quantity": ref("Decimal"), "remaining_quantity": ref("Decimal"),
    "reserved_cash": nullable(ref("Money")), "reject_reason": nullable(TEXT)},
    ["id", "session_id", "command_id", "instrument_id", "direction", "quantity", "order_type", "time_in_force",
     "submitted_at", "decision_at", "state", "filled_quantity", "remaining_quantity"],
    description="Rejected orders keep a persisted rejected state with a reason; cancellation only affects unfilled remainder, never historical fills.")
S["ManualOrderCancel"] = obj({**S["SessionCommand"]["properties"], "order_id": ID},
    ["session_id", "command_id", "expected_revision", "order_id"])
S["ReplayStep"] = obj({"id": ID, "session_id": ID, "from": TIME, "to": TIME,
    "start_revision": INT, "job_id": ID, "checkpoint_hash": TEXT, "summary": PARAMS},
    ["id", "session_id", "from", "to", "start_revision", "job_id", "checkpoint_hash"],
    description="A committed daily simulation checkpoint; recovery atomically republishes from the last committed checkpoint.")
S["ReplayDataQuery"] = obj({"session_id": ID, "range": nullable(ref("Range")), "dataset": TEXT,
    "frequency": TEXT, "instrument_ids": array(ID, minItems=1), "fields": array(TEXT, minItems=1),
    "cursor": TEXT, "limit": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}},
    ["session_id", "dataset", "frequency", "instrument_ids", "fields"],
    description="Data is truncated to the session current_as_of by the server; future reads are rejected, never preloaded.")
S["ReplayScreenRunCreate"] = obj({**S["SessionCommand"]["properties"], "screener_ref": ref("VersionRef")},
    ["session_id", "command_id", "expected_revision", "screener_ref"],
    description="snapshot, universe and as_of come from the session; the browser cannot shift to a future point.")
S["PositionRow"] = obj({"instrument_id": ID, "quantity": ref("Decimal"), "available_quantity": ref("Decimal"),
    "average_cost": ref("Money"), "market_value": ref("Money"), "unrealized_pnl": ref("Money"),
    "valuation_timestamp": nullable(TIME)},
    ["instrument_id", "quantity", "available_quantity", "average_cost", "market_value", "unrealized_pnl"])
S["ReplayAccountSnapshot"] = obj({"session_id": ID, "as_of": TIME, "cash": ref("Money"),
    "frozen_cash": ref("Money"), "available_cash": ref("Money"), "equity": ref("Money"),
    "positions": array(ref("PositionRow")), "valuation_complete": BOOL, "valuation_reason": nullable(TEXT)},
    ["session_id", "as_of", "cash", "frozen_cash", "available_cash", "equity", "positions", "valuation_complete"])
S["ReplayNoteCreate"] = obj({**S["SessionCommand"]["properties"], "text": TEXT},
    ["session_id", "command_id", "expected_revision", "text"])
S["ReplayNote"] = obj({"id": ID, "session_id": ID, "command_id": ID, "decision_at": TIME,
    "submitted_at": TIME, "text": TEXT},
    ["id", "session_id", "command_id", "decision_at", "submitted_at", "text"])
S["ReplayEvent"] = obj({"session_id": ID, "seq": INT, "decision_at": TIME,
    "kind": enum("order", "fill", "cancel", "advance", "screen", "note", "ledger", "close"),
    "command_id": nullable(ID), "order_id": nullable(ID), "detail": PARAMS},
    ["session_id", "seq", "decision_at", "kind"],
    description="Persistent business log; Job SSE with a window cap is not a substitute for full trading history.")
S["ReplayReport"] = obj({"session_id": ID, "is_final": BOOL, "advanced_to": TIME,
    "fills": INT, "trades": INT, "metrics": array(ref("Metric")), "artifact_ids": array(ID)},
    ["session_id", "is_final", "advanced_to", "fills", "trades", "metrics", "artifact_ids"])
S["ReplayExportCreate"] = obj({**S["SessionCommand"]["properties"], "scope": enum("orders", "fills", "ledger", "report"),
    "format": enum("csv", "json")}, ["session_id", "command_id", "expected_revision", "scope", "format"])

paths = {}


def param(name, location, schema, required=False):
    return {"name": name, "in": location, "required": required, "schema": schema}


def operation(path, method, name, summary, result, body=None, status="200", paging=False, extra=(), media="application/json"):
    params = list(extra)
    if "{id}" in path:
        params.append(param("id", "path", ID, True))
    if paging:
        params += [param("limit", "query", {"type": "integer", "minimum": 1, "maximum": 200, "default": 50}),
                   param("cursor", "query", TEXT), param("q", "query", TEXT),
                   param("sort", "query", enum("id", "-id"))]
        page_name = result + "Page"
        S.setdefault(page_name, obj({"items": array(ref(result)), "next_cursor": nullable(TEXT)}, ["items", "next_cursor"]))
        result = page_name
    if method == "post" and path not in ("/data/query", "/backtests/preflight", "/factor-runs/preflight", "/factor-runs",
                                         "/universes/{id}/resolve", "/screen-runs/preflight",
                                         "/replay-sessions/preflight", "/replay-sessions/{id}/data/query"):
        params.append(param("Idempotency-Key", "header", string(minLength=8, maxLength=128), True))
    response = {"description": "Accepted; follow Location and wait for a terminal job state." if status == "202" else "Success",
                "content": {"application/json": {"schema": ref(result)}}}
    if status == "202":
        response["headers"] = {"Location": {"description": "Job resource URL", "schema": TEXT}}
    op = {"operationId": name, "summary": summary, "tags": [path.split('/')[1]],
          "parameters": params, "responses": {status: response}}
    for code, description in [("400", "Invalid request"), ("401", "Unauthenticated"), ("403", "Forbidden"),
                              ("404", "Not found"), ("409", "Conflict or result not ready"),
                              ("422", "Semantic validation failed"), ("429", "Rate limited"),
                              ("500", "Internal error"), ("503", "Unavailable")]:
        op["responses"][code] = {"description": description, "content": {"application/json": {"schema": ref("Error")}}}
    if body:
        op["requestBody"] = {"required": True, "content": {media: {"schema": ref(body)}}}
    paths.setdefault(path, {})[method] = op


for route, resource in [("providers", "Provider"), ("connections", "Connection"), ("datasets", "Dataset"),
                        ("batches", "Batch"), ("snapshots", "Snapshot"), ("universes", "Universe"),
                        ("factors", "Factor"), ("strategies", "Strategy"), ("experiments", "Experiment"), ("jobs", "Job")]:
    filters = []
    if route == "jobs":
        filters = [param("state", "query", S["Job"]["properties"]["state"]), param("kind", "query", TEXT)]
    elif route == "batches":
        filters = [param("job_id", "query", ID), param("dataset_id", "query", ID)]
    operation(f"/{route}", "get", f"list{resource}s", f"List {route}", resource, paging=True, extra=filters)
    operation(f"/{route}/{{id}}", "get", f"get{resource}", f"Read {resource}", resource)

for route, body, result in [("secrets", "SecretCreate", "SecretRef"), ("connections", "ConnectionCreate", "Connection"),
                             ("universes", "UniverseCreate", "Universe"),
                             ("strategies", "StrategyCreate", "Strategy")]:
    operation(f"/{route}", "post", f"create{result}", f"Create immutable {result}", result, body, "201")

for route, body, name in [("ingestions", "IngestionCreate", "Ingestion"), ("snapshots", "SnapshotCreate", "Snapshot"),
                          ("backtests", "BacktestCreate", "Backtest"),
                          ("experiments/compare", "CompareCreate", "Comparison"), ("exports", "ExportCreate", "Export")]:
    operation(f"/{route}", "post", f"start{name}", f"Start {name} job", "Job", body, "202")

operation("/imports", "post", "uploadImport", "Stage an import file and schedule validation", "Job", "ImportUpload", "202", media="multipart/form-data")
operation("/connections/{id}/check", "post", "checkConnection", "Test connection capabilities asynchronously", "Job", "EmptyCommand", "202")
operation("/data/query", "post", "queryData", "Query bounded point-in-time observations", "ObservationPage", "DataQuery")
S["ObservationPage"] = obj({"items": array(ref("Observation")), "next_cursor": nullable(TEXT)}, ["items", "next_cursor"])
operation("/strategy-templates", "get", "listStrategyTemplates", "List registered templates", "StrategyTemplate", paging=True)
operation("/models", "get", "listModels", "List versioned models and schemas", "Model", paging=True,
          extra=[param("kind", "query", S["Model"]["properties"]["kind"])])
operation("/factors/{id}", "get", "getFactor", "Read one registered factor version", "Factor",
          extra=[param("version", "query", ID, True)])
operation("/factor-runs/preflight", "post", "preflightFactorRun", "Check graph, params, universe and data availability without computing", "Preflight", "FactorRunRequest")
operation("/factor-runs", "post", "runFactor", "Compute one factor cross-section synchronously", "FactorRunResult", "FactorRunRequest")
operation("/factor-analyses", "post", "runFactorAnalysis", "Compute the factor evidence analysis and store its canonical artifact", "FactorAnalysisResult", "FactorAnalysisCreate")
operation("/universes/{id}/resolve", "post", "resolveUniverse", "Preview historical members at an explicit decision time", "UniverseMembers", "UniverseResolve")
operation("/backtests/{id}", "get", "getBacktest", "Read immutable backtest input and manifest", "Backtest")
operation("/backtests/preflight", "post", "preflightBacktest", "Validate all dependencies without starting computation", "Preflight", "BacktestCreate")
operation("/backtests/{id}/report", "get", "getBacktestReport", "Read completed report; unfinished returns 409", "Report")
operation("/backtests/{id}/series", "get", "listEquitySeries", "Read full-resolution paginated equity and drawdown", "EquityPoint", paging=True)
paths["/backtests/{id}/series"]["get"]["parameters"] = [
    p for p in paths["/backtests/{id}/series"]["get"]["parameters"] if p["name"] not in ("sort", "q")]
paths["/backtests/{id}/series"]["get"]["description"] = "Ascending timestamp order with a stable sequence tie-breaker; cursor is bound to run ID. No search or user-selected sort."
operation("/backtests/{id}/records", "get", "listBacktestRecords", "Read a bounded page of traceable records", "Record", paging=True,
          extra=[param("kind", "query", S["Record"]["properties"]["kind"], True)])
S["RecordPage"]["properties"]["record_schema"] = array(ref("Field"))
S["RecordPage"]["required"].append("record_schema")
operation("/jobs/{id}/cancel", "post", "cancelJob", "Request cancellation; already cancelled returns its state", "Job", "EmptyCommand")
operation("/jobs/{id}/retry", "post", "retryJob", "Retry failed or cancelled job with frozen inputs", "Job", "EmptyCommand", "202")
operation("/jobs/{id}/events", "get", "streamJobEvents", "SSE job.updated events with resumable per-job sequence", "JobEvent",
          extra=[param("Last-Event-ID", "header", string(pattern="^[0-9]+$"))])
event_op = paths["/jobs/{id}/events"]["get"]
event_op["responses"]["200"]["content"] = {"text/event-stream": {"schema": TEXT,
    "example": 'id: 1\nevent: job.updated\ndata: {"job_id":"job_example","sequence":"1","at":"2026-09-12T00:00:00Z","job":{...}}\n\n'}}
event_op["description"] = "Each data JSON value conforms to JobEvent. The abbreviated framing example is illustrative only. Heartbeats are SSE comments. On 410, GET the current job and subscribe again. Auth uses headers, never URL tokens."
event_op["responses"]["410"] = {"description": "Replay window expired", "content": {"application/json": {"schema": ref("Error")}}}
operation("/artifacts/{id}", "get", "getArtifact", "Read authorized artifact metadata", "Artifact")
operation("/artifacts/{id}/content", "get", "downloadArtifact", "Download authorized artifact bytes", "Artifact")
paths["/artifacts/{id}/content"]["get"]["responses"]["200"] = {
    "description": "Artifact bytes; Content-Type matches artifact.media_type and Content-Disposition is attachment.",
    "headers": {"Content-Disposition": {"schema": TEXT}, "ETag": {"schema": TEXT}},
    "content": {"application/octet-stream": {"schema": string(format="binary")}}}

operation("/screeners", "get", "listScreeners", "List screener versions", "Screener", paging=True)
operation("/screeners", "post", "createScreener", "Create an immutable screener version", "Screener", "ScreenerCreate", "201")
operation("/screeners/{id}", "get", "getScreener", "Read a specific immutable screener version", "Screener",
          extra=[param("version", "query", ID, True)])
operation("/screen-runs", "post", "startScreenRun", "Start a screening run job with frozen inputs", "Job", "ScreenRunCreate", "202")
operation("/screen-runs", "get", "listScreenRuns", "List screening runs", "ScreenRun", paging=True,
          extra=[param("screener_id", "query", ID), param("state", "query", S["Job"]["properties"]["state"])])
operation("/screen-runs/preflight", "post", "preflightScreenRun", "Check inputs, conditions and scale without starting a run", "ScreenPreflight", "ScreenRunCreate")
operation("/screen-runs/{id}", "get", "getScreenRun", "Read frozen configuration, job link and published summary", "ScreenRun")
operation("/screen-runs/{id}/rows", "get", "listScreenRows", "Read a bounded page of frozen screening results", "ScreenRow", paging=True,
          extra=[param("state", "query", enum("selected", "excluded"))])
paths["/screen-runs/{id}/rows"]["get"]["parameters"] = [
    p for p in paths["/screen-runs/{id}/rows"]["get"]["parameters"] if p["name"] not in ("sort", "q")]
paths["/screen-runs/{id}/rows"]["get"]["description"] = ("Official rank ascending with instrument_id as the stable tie-breaker; "
    "excluded rows have a null rank and sort last. The cursor is bound to the run, its frozen result hash and the state filter.")
S["ScreenRowPage"]["properties"]["columns"] = array(ref("Field"))
S["ScreenRowPage"]["required"].append("columns")
operation("/screen-runs/{id}/explanations/{instrument_id}", "get", "getScreenExplanation",
          "Read per-condition and scoring evidence for one instrument", "ScreenExplanation",
          extra=[param("instrument_id", "path", ID, True)])
operation("/screen-runs/{id}/universe", "post", "saveScreenUniverse", "Save all selected instruments as a new static universe", "Universe", "ScreenUniverseCreate", "201")
operation("/screen-runs/{id}/exports", "post", "startScreenExport", "Start an export job for the frozen selection", "Job", "ScreenExportCreate", "202")

# Replay (historical manual trading) operations
operation("/replay-sessions/preflight", "post", "preflightReplayConfig", "Validate replay config and model/data coverage without creating a session", "ReplayPreflight", "ReplayConfig")
operation("/replay-sessions", "post", "startReplaySession", "Start a historical replay session job with frozen config", "Job", "ReplayConfig", "202")
operation("/replay-sessions", "get", "listReplaySessions", "List replay sessions in the current workspace", "ReplaySession", paging=True,
          extra=[param("state", "query", S["ReplaySessionState"])])
operation("/replay-sessions/{id}", "get", "getReplaySession", "Read frozen config, current_as_of, revision and state", "ReplaySession")
operation("/replay-sessions/{id}/data/query", "post", "queryReplayData", "Query data truncated to the session decision time", "ObservationPage", "ReplayDataQuery")
operation("/replay-sessions/{id}/screens", "post", "startReplayScreen", "Run a saved screener against the frozen session inputs", "Job", "ReplayScreenRunCreate", "202")
operation("/replay-sessions/{id}/screens/{screen_run_id}", "get", "getReplayScreenRun", "Read a replay-context screener run and its explanation link", "ScreenRun",
          extra=[param("screen_run_id", "path", ID, True)])
operation("/replay-sessions/{id}/orders", "post", "submitManualOrder", "Submit a manual order intent for the current session", "ManualOrder", "ManualOrderCreate", "201")
operation("/replay-sessions/{id}/orders", "get", "listManualOrders", "List orders for the session by state", "ManualOrder", paging=True,
          extra=[param("state", "query", S["ManualOrder"]["properties"]["state"])])
operation("/replay-sessions/{id}/orders/{order_id}/cancel", "post", "cancelManualOrder", "Cancel the unfilled remainder of an order", "ManualOrder", "ManualOrderCancel",
          extra=[param("order_id", "path", ID, True)])
operation("/replay-sessions/{id}/advance", "post", "advanceReplayDay", "Advance the session by exactly one trading day", "Job", "SessionCommand", "202")
operation("/replay-sessions/{id}/account", "get", "getReplayAccount", "Read the committed account snapshot", "ReplayAccountSnapshot")
operation("/replay-sessions/{id}/events", "get", "listReplayEvents", "Page the persistent session event log by monotonic sequence", "ReplayEvent", paging=True)
operation("/replay-sessions/{id}/notes", "post", "createReplayNote", "Append a textual note at the current decision time", "ReplayNote", "ReplayNoteCreate")
operation("/replay-sessions/{id}/close", "post", "closeReplaySession", "Close the session, cancel remainders, value and publish a report", "Job", "SessionCommand", "202")
operation("/replay-sessions/{id}/report", "get", "getReplayReport", "Read the preview or final replay report", "ReplayReport")
operation("/replay-sessions/{id}/exports", "post", "startReplayExport", "Start an export job for orders/fills/ledger/report", "Job", "ReplayExportCreate", "202")
# The orders listing is scope-free: nested orders use the {id}-parameterized pagination and a stable sort below.
paths["/replay-sessions/{id}/orders"]["get"]["parameters"] = [
    p for p in paths["/replay-sessions/{id}/orders"]["get"]["parameters"] if p["name"] not in ("q", "sort")]
paths["/replay-sessions/{id}/orders"]["get"]["description"] = ("Order results are bound to the session, its committed revision, the ordering filter "
    "and a stable sort; state changes that invalidate the cursor require a fresh query.")

spec = {"openapi": "3.1.0", "info": {"title": "Strategy Research API", "version": "0.1.0",
    "description": "Proposed contract, no running server. Go backend; immutable versions, PIT data views and durable jobs. See docs/interfaces.md for semantic invariants. Generated by build_openapi.py."},
    "servers": [{"url": "/api/v1"}], "security": [{"bearerAuth": []}], "paths": paths,
    "components": {"securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer"}}, "schemas": S}}

if __name__ == "__main__":
    target = Path(__file__).with_name("openapi.json")
    target.write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"Wrote {target.name}: {len(paths)} paths, {sum(len(v) for v in paths.values())} operations, {len(S)} schemas")
