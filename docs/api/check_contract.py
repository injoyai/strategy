"""Check local contract structure and documentation links without dependencies.

This is a structural check, not a full OpenAPI conformance validator or a test of
an HTTP server. Business and runtime acceptance remain pending implementation.
"""
import importlib.util
import json
import re
import sys
from pathlib import Path
from urllib.parse import unquote

sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parents[2]
SPEC_PATH = Path(__file__).with_name("openapi.json")
spec = json.loads(SPEC_PATH.read_text(encoding="utf-8"))
errors = []
ref_count = 0


def walk(value, location="$", schema_context=False):
    global ref_count
    if isinstance(value, dict):
        if "$ref" in value:
            ref_count += 1
            target = value["$ref"]
            if not target.startswith("#/"):
                errors.append(f"External ref is not supported by this check: {target}")
            else:
                resolved = spec
                try:
                    for part in target[2:].split("/"):
                        resolved = resolved[part.replace("~1", "/").replace("~0", "~")]
                except (KeyError, TypeError):
                    errors.append(f"Unresolved ref at {location}: {target}")
        if "properties" in value:
            for field in value.get("required", []):
                if field not in value["properties"]:
                    errors.append(f"Required property missing at {location}: {field}")
        for key, child in value.items():
            walk(child, f"{location}.{key}")
    elif isinstance(value, list):
        for i, child in enumerate(value):
            walk(child, f"{location}[{i}]")


walk(spec)
operation_ids = set()
for path, methods in spec["paths"].items():
    for method, operation in methods.items():
        op_id = operation.get("operationId")
        if not op_id or op_id in operation_ids:
            errors.append(f"Missing/duplicate operation ID: {method} {path}")
        operation_ids.add(op_id)
        expected_params = set(re.findall(r"\{([^}]+)\}", path))
        actual_params = {p["name"] for p in operation["parameters"] if p["in"] == "path" and p["required"]}
        if expected_params != actual_params:
            errors.append(f"Path parameter mismatch: {path}")
        if not any(code.startswith("2") for code in operation["responses"]):
            errors.append(f"No success response: {method} {path}")
        if "202" in operation["responses"] and "Location" not in operation["responses"]["202"].get("headers", {}):
            errors.append(f"Accepted job missing Location: {path}")

module_spec = importlib.util.spec_from_file_location("contract_builder", SPEC_PATH.with_name("build_openapi.py"))
builder = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(builder)
if builder.spec != spec:
    errors.append("Generated OpenAPI differs from its builder; regenerate it")

link_count = 0
for doc in ROOT.rglob("*.md"):
    if any(part in {".git", "node_modules"} for part in doc.parts):
        continue
    for target in re.findall(r"\]\(([^)]+)\)", doc.read_text(encoding="utf-8")):
        if re.match(r"[a-zA-Z][a-zA-Z0-9+.-]*:", target) or target.startswith("#"):
            continue
        relative = unquote(target.split("#", 1)[0])
        if relative:
            link_count += 1
            if not (doc.parent / relative).is_file():
                errors.append(f"Broken local link in {doc.relative_to(ROOT)}: {target}")

report = {"openapi": spec["openapi"], "paths": len(spec["paths"]),
          "operations": len(operation_ids), "schemas": len(spec["components"]["schemas"]),
          "resolved_refs": ref_count, "local_links_checked": link_count,
          "generated_file_matches": builder.spec == spec, "errors": errors,
          "scope": "Static structure and local links only; not full OpenAPI conformance or runtime behavior."}
print(json.dumps(report, ensure_ascii=False, indent=2))
raise SystemExit(1 if errors else 0)
