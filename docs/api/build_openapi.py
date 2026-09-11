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
S["Field"] = obj({"name": TEXT, "type": enum("decimal", "number", "string", "boolean", "timestamp"),
                  "unit": TEXT, "nullable": BOOL, "description": TEXT}, ["name", "type", "unit", "nullable"])
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
S["Dataset"] = obj({"id": ID, "name": TEXT, "schema_version": TEXT, "fields": array(ref("Field")),
    "natural_key": array(TEXT), "availability_policy_ref": ref("VersionRef"), "coverage": nullable(ref("Range")),
    "quality_issues": array(ref("Issue"))},
    ["id", "name", "schema_version", "fields", "natural_key", "availability_policy_ref", "coverage", "quality_issues"])
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
S["DataQuery"] = obj({"snapshot_id": ID, "as_of": TIME, "dataset": TEXT, "frequency": TEXT,
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
    obj({"kind": enum("static"), "instrument_ids": array(ID, minItems=1)}, ["kind", "instrument_ids"]),
    obj({"kind": enum("historical_rule"), "membership_dataset": TEXT, "membership_key": TEXT,
         "filter": ref("Expression")}, ["kind", "membership_dataset", "membership_key"])
]}
S["UniverseCreate"] = obj({"name": TEXT, "definition": ref("UniverseDefinition"), "parent_id": ID}, ["name", "definition"])
S["Universe"] = obj({**S["UniverseCreate"]["properties"], "id": ID, "version": ID}, ["id", "version", "name", "definition"])
S["UniverseResolve"] = obj({"snapshot_id": ID, "as_of": TIME, "cursor": TEXT,
    "limit": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}}, ["snapshot_id", "as_of"])
S["UniverseMembers"] = obj({"universe_ref": ref("VersionRef"), "as_of": TIME, "instrument_ids": array(ID),
    "next_cursor": nullable(TEXT), "issues": array(ref("Issue"))}, ["universe_ref", "as_of", "instrument_ids", "next_cursor", "issues"])
S["InputRequirement"] = obj({"dataset": TEXT, "fields": array(TEXT, minItems=1), "frequency": TEXT,
    "lookback_periods": INT, "max_staleness_seconds": INT, "requires_strict_pit": BOOL},
    ["dataset", "fields", "frequency", "lookback_periods", "max_staleness_seconds", "requires_strict_pit"])
factor_props = {"name": TEXT, "description": TEXT, "asset_classes": array(TEXT, minItems=1),
    "parameter_schema": SCHEMA, "inputs": array(ref("InputRequirement")), "dependencies": array(ref("VersionRef")),
    "output_unit": TEXT, "missing_policy": enum("propagate", "exclude", "error"), "expression": ref("Expression"), "parent_id": ID}
S["FactorCreate"] = obj(factor_props, ["name", "description", "asset_classes", "parameter_schema", "inputs", "dependencies", "output_unit", "missing_policy", "expression"])
S["Factor"] = obj({**factor_props, "id": ID, "version": ID, "implementation_ref": ref("VersionRef"),
                   "kind": enum("go", "expression")},
    ["id", "version", "name", "description", "kind", "asset_classes", "parameter_schema", "inputs", "dependencies", "output_unit", "missing_policy"])
S["FactorBinding"] = obj({"factor_ref": ref("VersionRef"), "params": PARAMS}, ["factor_ref", "params"])
S["LabelConfig"] = obj({"horizon_periods": {"type": "integer", "minimum": 1}, "price_policy": TEXT,
    "entry_lag_periods": INT, "groups": {"type": "integer", "minimum": 2}},
    ["horizon_periods", "price_policy", "entry_lag_periods", "groups"], description="Evaluation only; label data is not exposed to strategy views.")
S["FactorRunCreate"] = obj({"snapshot_id": ID, "universe_ref": ref("VersionRef"),
    "factors": array(ref("FactorBinding"), minItems=1), "range": ref("Range"), "frequency": TEXT,
    "decision_timezone": TEXT, "availability_policy_ref": ref("VersionRef"), "strict_pit": BOOL,
    "analysis": nullable(ref("LabelConfig"))},
    ["snapshot_id", "universe_ref", "factors", "range", "frequency", "decision_timezone", "availability_policy_ref", "strict_pit"])
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
    "error": nullable(ref("Error")), "result_refs": array(obj({"kind": enum("batch", "snapshot", "import", "factor_run", "backtest", "artifact"), "id": ID}, ["kind", "id"]))},
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
S["FactorAnalysis"] = obj({"run_id": ID, "factor_ref": ref("VersionRef"), "metrics": array(ref("Metric")),
    "series_artifact_ids": array(ID), "issues": array(ref("Issue")), "label_config": nullable(ref("LabelConfig"))},
    ["run_id", "factor_ref", "metrics", "series_artifact_ids", "issues", "label_config"])
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
S["FactorRun"] = obj({"id": ID, "job_id": ID, "config": ref("FactorRunCreate"),
    "artifact_ids": array(ID), "issues": array(ref("Issue"))}, ["id", "job_id", "config", "artifact_ids", "issues"])
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
    if method == "post" and path not in ("/data/query", "/backtests/preflight", "/factor-runs/preflight", "/universes/{id}/resolve"):
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
                             ("universes", "UniverseCreate", "Universe"), ("factors", "FactorCreate", "Factor"),
                             ("strategies", "StrategyCreate", "Strategy")]:
    operation(f"/{route}", "post", f"create{result}", f"Create immutable {result}", result, body, "201")

for route, body, name in [("ingestions", "IngestionCreate", "Ingestion"), ("snapshots", "SnapshotCreate", "Snapshot"),
                          ("factor-runs", "FactorRunCreate", "FactorRun"), ("backtests", "BacktestCreate", "Backtest"),
                          ("experiments/compare", "CompareCreate", "Comparison"), ("exports", "ExportCreate", "Export")]:
    operation(f"/{route}", "post", f"start{name}", f"Start {name} job", "Job", body, "202")

operation("/imports", "post", "uploadImport", "Stage an import file and schedule validation", "Job", "ImportUpload", "202", media="multipart/form-data")
operation("/connections/{id}/check", "post", "checkConnection", "Test connection capabilities asynchronously", "Job", "EmptyCommand", "202")
operation("/data/query", "post", "queryData", "Query bounded point-in-time observations", "ObservationPage", "DataQuery")
S["ObservationPage"] = obj({"items": array(ref("Observation")), "next_cursor": nullable(TEXT)}, ["items", "next_cursor"])
operation("/strategy-templates", "get", "listStrategyTemplates", "List registered templates", "StrategyTemplate", paging=True)
operation("/models", "get", "listModels", "List versioned models and schemas", "Model", paging=True,
          extra=[param("kind", "query", S["Model"]["properties"]["kind"])])
operation("/factor-runs/{id}", "get", "getFactorRun", "Read factor computation and artifact references", "FactorRun")
operation("/factor-runs/preflight", "post", "preflightFactorRun", "Check factor dependencies, lookback and time evidence", "Preflight", "FactorRunCreate")
operation("/factor-runs/{id}/analysis", "get", "getFactorAnalysis", "Read one factor's completed analysis", "FactorAnalysis",
          extra=[param("factor_id", "query", ID, True)])
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

spec = {"openapi": "3.1.0", "info": {"title": "Strategy Research API", "version": "0.1.0",
    "description": "Proposed contract, no running server. Go backend; immutable versions, PIT data views and durable jobs. See docs/interfaces.md for semantic invariants. Generated by build_openapi.py."},
    "servers": [{"url": "/api/v1"}], "security": [{"bearerAuth": []}], "paths": paths,
    "components": {"securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer"}}, "schemas": S}}

if __name__ == "__main__":
    target = Path(__file__).with_name("openapi.json")
    target.write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"Wrote {target.name}: {len(paths)} paths, {sum(len(v) for v in paths.values())} operations, {len(S)} schemas")
