"""Evaluate text-scanned source evidence against AST-backed source facts.

This is an evaluation harness for provider-readiness work. It runs the current
source mapper twice: once with the legacy text scanner and once with
`source-evidence-ast` facts. The output classifies deltas so parser changes can
be reviewed as regressions, improvements, or harmless diagnostic cleanup.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import subprocess
import sys

from engine import source_operation_map


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _write_json(data, path=None):
    text = json.dumps(data, indent=2, sort_keys=True) + "\n"
    if path:
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
    else:
        sys.stdout.write(text)


def _write_text(text, path):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def _repo_root():
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _default_ast_tool_dir():
    return os.path.join(_repo_root(), "tools", "source-evidence-ast")


def _artifact_paths(out_dir):
    return {
        "facts": os.path.join(out_dir, "source-facts.json"),
        "control_report": os.path.join(out_dir, "control-report.json"),
        "ast_report": os.path.join(out_dir, "ast-report.json"),
        "compare": os.path.join(out_dir, "source-facts-compare.json"),
        "evaluation": os.path.join(out_dir, "source-evidence-eval.json"),
        "markdown": os.path.join(out_dir, "source-evidence-eval.md"),
    }


def generate_source_facts(source_root, out_path, ast_tool_dir=None):
    ast_tool_dir = ast_tool_dir or _default_ast_tool_dir()
    cmd = [
        "go",
        "run",
        ".",
        "--source-root",
        source_root,
        "--out",
        out_path,
    ]
    try:
        subprocess.check_call(cmd, cwd=ast_tool_dir)
    except OSError as exc:
        raise RuntimeError("failed to run source-evidence-ast: %s" % exc)
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(
            "source-evidence-ast failed with exit code %s" % exc.returncode)
    return out_path


def _changed_files_only(before, after):
    before_copy = dict(before)
    after_copy = dict(after)
    before_files = before_copy.pop("files", [])
    after_files = after_copy.pop("files", [])
    return before_copy == after_copy and before_files != after_files


def classify_change(change):
    before = change.get("before") or {}
    after = change.get("after") or {}
    before_status = before.get("status")
    after_status = after.get("status")
    before_read = before.get("read_path")
    after_read = after.get("read_path")
    before_files = before.get("files") or []
    after_files = after.get("files") or []

    if before_status == "mapped" and after_status == "unmapped":
        return {
            "classification": "regression",
            "reason": "mapped_to_unmapped",
        }
    if before_status == "mapped" and after_status == "mapped":
        if before_read and after_read and before_read != after_read:
            return {
                "classification": "regression",
                "reason": "mapped_read_path_changed",
            }
    if before_files and not after_files:
        return {
            "classification": "regression",
            "reason": "source_files_dropped_to_zero",
        }

    if _changed_files_only(before, after):
        if len(after_files) < len(before_files):
            reason = "source_files_narrowed"
        else:
            reason = "source_files_changed"
        return {
            "classification": "acceptable",
            "reason": reason,
        }

    if before_status != "mapped" and after_status == "mapped":
        return {
            "classification": "review",
            "reason": "new_mapping",
        }
    if before_status == "mapped" and after_status == "ambiguous_source_operation":
        return {
            "classification": "review",
            "reason": "mapped_to_ambiguous",
        }
    if before.get("list_path") != after.get("list_path"):
        return {
            "classification": "review",
            "reason": "list_path_changed",
        }
    if before_read != after_read:
        return {
            "classification": "review",
            "reason": "read_path_changed",
        }
    if before_status != after_status:
        return {
            "classification": "review",
            "reason": "status_changed",
        }

    return {
        "classification": "review",
        "reason": "diagnostic_changed",
    }


def classify_comparison(compare_report):
    changes = []
    counts = {
        "regression": 0,
        "review": 0,
        "acceptable": 0,
    }
    reasons = {}
    for change in compare_report.get("changes") or []:
        verdict = classify_change(change)
        classification = verdict["classification"]
        reason = verdict["reason"]
        counts[classification] += 1
        reasons[reason] = reasons.get(reason, 0) + 1
        classified = dict(change)
        classified["classification"] = classification
        classified["classification_reason"] = reason
        changes.append(classified)
    return {
        "summary": {
            "resources": compare_report.get("summary", {}).get("resources", 0),
            "unchanged": compare_report.get("summary", {}).get("unchanged", 0),
            "changed": len(changes),
            "regressions": counts["regression"],
            "review_required": counts["review"],
            "acceptable": counts["acceptable"],
            "reasons": dict(sorted(reasons.items())),
            "control": compare_report.get("summary", {}).get("control") or {},
            "candidate": compare_report.get("summary", {}).get("candidate") or {},
        },
        "changes": changes,
    }


_SHORTCOMING_SEVERITY = {
    "regression": "gap",
    "resource_file_not_found": "gap",
    "source_files_without_operation_calls": "gap",
    "calls_without_openapi_match": "gap",
    "unmapped_without_reason": "gap",
    "ambiguous_source_operation": "review",
    "graphql_source": "notice",
    "mapped_read_without_list": "notice",
}

_SEVERITY_ORDER = {
    "gap": 0,
    "review": 1,
    "notice": 2,
}

_CHANGE_CLASS_ORDER = {
    "regression": 0,
    "review": 1,
    "acceptable": 2,
}

MAX_MARKDOWN_CHANGE_ROWS = 100


def _operation_call_count(detail):
    return sum(
        detail.get(key, 0) or 0
        for key in [
            "client_call_count",
            "package_call_count",
            "raw_rest_call_count",
        ])


def _unmapped_bucket(reason, detail):
    if reason == "no_source_operation_match":
        if _operation_call_count(detail) or detail.get("candidate_count", 0):
            return "calls_without_openapi_match"
        return "source_files_without_operation_calls"
    return reason or "unmapped_without_reason"


def _candidate_samples(candidates, limit=5):
    samples = []
    for candidate in (candidates or [])[:limit]:
        sample = {}
        for key in [
                "client_symbol",
                "operation_id",
                "method",
                "path",
                "path_kind",
                "source_role",
                "read_score",
                "list_score"]:
            if key in candidate:
                sample[key] = candidate[key]
        samples.append(sample)
    return samples


def _shortcoming_bucket(shortcomings, bucket, resource, detail):
    severity = _SHORTCOMING_SEVERITY.get(bucket, "review")
    entry = shortcomings.setdefault(bucket, {
        "severity": severity,
        "count": 0,
        "resources": [],
    })
    entry["count"] += 1
    item = {"resource": resource}
    item.update(detail)
    entry["resources"].append(item)


def summarize_shortcomings(ast_report, evaluation):
    """Summarize candidate-side gaps independent of text-vs-AST deltas."""
    shortcomings = {}
    registry = ast_report.get("registry") or {}
    diagnostics_by_resource = dict(
        (item.get("resource"), item)
        for item in ast_report.get("diagnostics") or [])

    for change in evaluation.get("changes") or []:
        if change.get("classification") != "regression":
            continue
        _shortcoming_bucket(shortcomings, "regression", change.get("resource"), {
            "reason": change.get("classification_reason"),
            "before": change.get("before") or {},
            "after": change.get("after") or {},
        })

    for resource, entry in sorted(registry.items()):
        status = entry.get("status")
        reason = entry.get("reason")
        diagnostic = diagnostics_by_resource.get(resource) or {}
        source = entry.get("source") or {}
        detail = {
            "status": status,
            "reason": reason,
            "files": source.get("files") or diagnostic.get("files") or [],
            "candidate_count": source.get("candidate_count", 0),
            "client_call_count": source.get("client_call_count", 0),
            "package_call_count": source.get("package_call_count", 0),
            "raw_rest_call_count": source.get("raw_rest_call_count", 0),
        }
        if source.get("client_calls"):
            detail["client_calls"] = source.get("client_calls")[:10]
        if source.get("package_calls"):
            detail["package_calls"] = source.get("package_calls")[:10]
        if source.get("raw_rest_calls"):
            detail["raw_rest_calls"] = source.get("raw_rest_calls")[:10]
        candidates = (
            entry.get("candidates") or
            diagnostic.get("ambiguous") or
            diagnostic.get("hits") or [])
        if candidates:
            detail["candidate_samples"] = _candidate_samples(candidates)
        if status == "ambiguous_source_operation":
            _shortcoming_bucket(
                shortcomings, "ambiguous_source_operation", resource, detail)
            continue
        if status == "graphql_source":
            _shortcoming_bucket(shortcomings, "graphql_source", resource, detail)
            continue
        if status != "mapped":
            bucket = _unmapped_bucket(reason, detail)
            _shortcoming_bucket(shortcomings, bucket, resource, detail)
            continue

        read_entry = entry.get("read") or {}
        list_entry = entry.get("list") or {}
        if read_entry and not list_entry:
            detail = dict(detail)
            detail["read_path"] = read_entry.get("path")
            detail["read_operation_id"] = read_entry.get("operation_id")
            _shortcoming_bucket(shortcomings, "mapped_read_without_list",
                                resource, detail)

    buckets = {}
    for bucket, detail in sorted(shortcomings.items()):
        resources = sorted(
            detail["resources"],
            key=lambda item: item.get("resource") or "")
        buckets[bucket] = {
            "severity": detail["severity"],
            "count": detail["count"],
            "resources": resources,
        }
    severity_summary = {}
    for detail in buckets.values():
        severity = detail.get("severity", "review")
        severity_summary[severity] = (
            severity_summary.get(severity, 0) + detail.get("count", 0))
    return {
        "summary": dict(
            (bucket, detail["count"])
            for bucket, detail in buckets.items()),
        "severity_summary": dict(sorted(severity_summary.items())),
        "buckets": buckets,
    }


def _status_table(summary):
    control = summary.get("control") or {}
    candidate = summary.get("candidate") or {}
    rows = [
        ("resources", control.get("resources", 0), candidate.get("resources", 0)),
        ("mapped", control.get("mapped", 0), candidate.get("mapped", 0)),
        ("ambiguous", control.get("ambiguous", 0), candidate.get("ambiguous", 0)),
        ("graphql_source", control.get("graphql_source", 0), candidate.get("graphql_source", 0)),
        ("unmapped", control.get("unmapped", 0), candidate.get("unmapped", 0)),
        ("resources_with_source_files",
         control.get("resources_with_source_files", 0),
         candidate.get("resources_with_source_files", 0)),
    ]
    out = [
        "| Metric | Text Scanner | AST Facts |",
        "|---|---:|---:|",
    ]
    for name, control_value, candidate_value in rows:
        out.append("| `%s` | `%s` | `%s` |" % (
            name, control_value, candidate_value))
    return "\n".join(out)


def render_markdown(evaluation, title="Source Evidence A/B Evaluation"):
    summary = evaluation.get("summary") or {}
    lines = [
        "# %s" % title,
        "",
        _status_table(summary),
        "",
        "## Delta Summary",
        "",
        "| Classification | Count |",
        "|---|---:|",
        "| `regression` | `%s` |" % summary.get("regressions", 0),
        "| `review` | `%s` |" % summary.get("review_required", 0),
        "| `acceptable` | `%s` |" % summary.get("acceptable", 0),
        "| `unchanged` | `%s` |" % summary.get("unchanged", 0),
        "",
    ]
    reasons = summary.get("reasons") or {}
    if reasons:
        lines.extend([
            "## Reasons",
            "",
            "| Reason | Count |",
            "|---|---:|",
        ])
        for reason, count in sorted(reasons.items()):
            lines.append("| `%s` | `%s` |" % (reason, count))
        lines.append("")

    changes = evaluation.get("changes") or []
    if changes:
        changes = sorted(
            changes,
            key=lambda change: (
                _CHANGE_CLASS_ORDER.get(change.get("classification"), 99),
                change.get("resource") or "",
            ))
        shown_changes = changes[:MAX_MARKDOWN_CHANGE_ROWS]
        lines.extend([
            "## Changes",
            "",
            "| Resource | Class | Reason | Before | After |",
            "|---|---|---|---|---|",
        ])
        for change in shown_changes:
            before = change.get("before") or {}
            after = change.get("after") or {}
            before_value = "%s `%s`" % (
                before.get("status"), before.get("read_path"))
            after_value = "%s `%s`" % (
                after.get("status"), after.get("read_path"))
            lines.append("| `%s` | `%s` | `%s` | %s | %s |" % (
                change.get("resource"),
                change.get("classification"),
                change.get("classification_reason"),
                before_value,
                after_value,
            ))
        if len(changes) > len(shown_changes):
            lines.extend([
                "",
                "Showing `%s` of `%s` changes; full detail is in JSON." % (
                    len(shown_changes), len(changes)),
            ])
        lines.append("")

    shortcomings = evaluation.get("shortcomings") or {}
    shortcoming_buckets = shortcomings.get("buckets") or {}
    if shortcoming_buckets:
        lines.extend([
            "## Shortcomings",
            "",
            "| Bucket | Severity | Count | Sample Resources |",
            "|---|---|---:|---|",
        ])
        def sort_key(item):
            bucket, detail = item
            return (
                _SEVERITY_ORDER.get(detail.get("severity"), 99),
                bucket,
            )
        for bucket, detail in sorted(shortcoming_buckets.items(), key=sort_key):
            resources = [
                "`%s`" % item.get("resource")
                for item in (detail.get("resources") or [])[:8]
            ]
            if detail.get("count", 0) > len(resources):
                resources.append("...")
            lines.append("| `%s` | `%s` | `%s` | %s |" % (
                bucket,
                detail.get("severity", "review"),
                detail.get("count", 0),
                ", ".join(resources),
            ))
        lines.append("")

    return "\n".join(lines)


def run_eval(schema_path, openapi_path, source_root, out_dir,
             provider_source=None, resource_prefix="", source_facts_path=None,
             ast_tool_dir=None, resource_filter=None):
    paths = _artifact_paths(out_dir)
    os.makedirs(out_dir, exist_ok=True)
    if source_facts_path:
        facts_path = source_facts_path
    else:
        facts_path = generate_source_facts(
            source_root, paths["facts"], ast_tool_dir=ast_tool_dir)
    source_facts = _read_json(facts_path)

    control_report = source_operation_map.derive_registry(
        schema_path,
        openapi_path,
        source_root,
        provider_source=provider_source,
        resource_prefix=resource_prefix,
        resource_filter=resource_filter,
    )
    ast_report = source_operation_map.derive_registry(
        schema_path,
        openapi_path,
        source_root,
        provider_source=provider_source,
        resource_prefix=resource_prefix,
        source_facts=source_facts,
        resource_filter=resource_filter,
    )
    compare_report = source_operation_map.compare_registry_reports(
        control_report, ast_report)
    evaluation = classify_comparison(compare_report)
    evaluation["shortcomings"] = summarize_shortcomings(ast_report, evaluation)
    evaluation["artifacts"] = {
        "source_facts": facts_path,
        "control_report": paths["control_report"],
        "ast_report": paths["ast_report"],
        "compare": paths["compare"],
        "evaluation": paths["evaluation"],
        "markdown": paths["markdown"],
    }

    _write_json(control_report, paths["control_report"])
    _write_json(ast_report, paths["ast_report"])
    _write_json(compare_report, paths["compare"])
    _write_json(evaluation, paths["evaluation"])
    _write_text(render_markdown(evaluation), paths["markdown"])
    return evaluation


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Evaluate legacy source scanning against AST source facts")
    parser.add_argument("--schema", required=True, help="Terraform provider schema JSON")
    parser.add_argument("--openapi", required=True, help="OpenAPI/Swagger JSON")
    parser.add_argument("--source-root", required=True, help="Provider source root")
    parser.add_argument("--provider-source", help="Provider source address")
    parser.add_argument("--resource-prefix", default="", help="Resource name prefix/product")
    parser.add_argument(
        "--resources",
        help="Comma-separated Terraform resource names to evaluate")
    parser.add_argument("--source-facts", help="Existing source-evidence-ast facts JSON")
    parser.add_argument(
        "--ast-tool-dir",
        default=_default_ast_tool_dir(),
        help="source-evidence-ast tool directory used when --source-facts is omitted")
    parser.add_argument("--out-dir", required=True, help="Directory for eval artifacts")
    parser.add_argument(
        "--fail-on-regression",
        action="store_true",
        help="Exit non-zero when classified regressions are present")
    args = parser.parse_args(argv)
    try:
        evaluation = run_eval(
            args.schema,
            args.openapi,
            args.source_root,
            args.out_dir,
            provider_source=args.provider_source,
            resource_prefix=args.resource_prefix,
            source_facts_path=args.source_facts,
            ast_tool_dir=args.ast_tool_dir,
            resource_filter=source_operation_map._parse_resource_filter(
                args.resources),
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    _write_json(evaluation)
    if args.fail_on_regression and evaluation["summary"]["regressions"]:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
