"""Run repeatable provider-readiness probes from a recipe.

The probe harness intentionally orchestrates existing tools instead of adding a
new mapper. Recipes pin where provider source, Terraform schema, and OpenAPI
contracts come from; the harness materializes those inputs, runs source
operation mapping, then feeds the source-derived registry into the OpenAPI
coverage report.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile

try:
    from urllib.request import urlretrieve
except ImportError:  # pragma: no cover - Python 2 guard for clarity only.
    from urllib import urlretrieve

from engine import openapi_resource_map
from engine import source_operation_map


DEFAULT_WORK_ROOT = os.path.join(
    tempfile.gettempdir(), "infrawright-provider-probes")
HTTP_METHODS = set(("get", "post", "put", "patch", "delete"))


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _write_json(data, path):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, sort_keys=True)
        f.write("\n")


def _write_text(text, path):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def _recipe_dir(recipe_path):
    return os.path.abspath(os.path.dirname(recipe_path))


def _recipe_path(recipe_dir, path):
    if os.path.isabs(path):
        return path
    return os.path.abspath(os.path.join(recipe_dir, path))


def _run(args, cwd=None, stdout_path=None):
    stdout_file = None
    try:
        if stdout_path:
            parent = os.path.dirname(stdout_path)
            if parent:
                os.makedirs(parent, exist_ok=True)
            stdout_file = open(stdout_path, "w", encoding="utf-8")
            stdout = stdout_file
        else:
            stdout = subprocess.PIPE
        proc = subprocess.run(
            args,
            cwd=cwd,
            stdout=stdout,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )
    finally:
        if stdout_file:
            stdout_file.close()
    if proc.returncode != 0:
        raise RuntimeError(
            "command failed (%s): %s\n%s"
            % (proc.returncode, " ".join(args), proc.stderr.strip()))
    return proc.stdout or ""


def _is_yaml_path(path, explicit_format=None):
    if explicit_format:
        return explicit_format.lower() in ("yaml", "yml")
    lowered = path.lower()
    return lowered.endswith(".yaml") or lowered.endswith(".yml")


def _copy_json_input(source_path, dest_path):
    data = _read_json(source_path)
    _write_json(data, dest_path)
    return dest_path


def _convert_yaml_to_json(yaml_path, dest_path):
    ruby = shutil.which("ruby")
    if not ruby:
        raise RuntimeError(
            "OpenAPI input %s is YAML, but ruby is not available for "
            "YAML->JSON conversion" % yaml_path)
    script = (
        "require 'yaml'; require 'json'; "
        "STDOUT.write(JSON.pretty_generate(YAML.load_file(ARGV[0])))"
    )
    _run([ruby, "-e", script, yaml_path], stdout_path=dest_path)
    # Validate the converted output before using it downstream.
    _read_json(dest_path)
    return dest_path


def _prepare_openapi(recipe, recipe_path, work_dir):
    spec = recipe.get("openapi") or {}
    inputs_dir = os.path.join(work_dir, "inputs")
    os.makedirs(inputs_dir, exist_ok=True)
    raw_path = os.path.join(inputs_dir, "openapi.raw")
    json_path = os.path.join(inputs_dir, "openapi.json")
    fmt = spec.get("format")
    if spec.get("path"):
        source = _recipe_path(_recipe_dir(recipe_path), spec["path"])
        if _is_yaml_path(source, fmt):
            return _convert_yaml_to_json(source, json_path)
        return _copy_json_input(source, json_path)
    if not spec.get("url"):
        raise ValueError("recipe openapi must include path or url")
    urlretrieve(spec["url"], raw_path)
    if _is_yaml_path(spec.get("url", ""), fmt):
        return _convert_yaml_to_json(raw_path, json_path)
    return _copy_json_input(raw_path, json_path)


def _prepare_source(recipe, recipe_path, work_dir):
    source = recipe.get("source") or {}
    if source.get("path"):
        root = _recipe_path(_recipe_dir(recipe_path), source["path"])
    elif source.get("git"):
        root = os.path.join(work_dir, "source")
        if os.path.exists(root):
            shutil.rmtree(root)
        clone = ["git", "clone", "--depth", "1"]
        if source.get("ref"):
            clone.extend(["--branch", source["ref"]])
        clone.extend([source["git"], root])
        _run(clone)
    else:
        raise ValueError("recipe source must include path or git")
    if source.get("subdir"):
        root = os.path.join(root, source["subdir"])
    if not os.path.isdir(root):
        raise ValueError("provider source root does not exist: %s" % root)
    return root


def _provider_local_name(provider_source):
    return provider_source.rstrip("/").split("/")[-1].replace("-", "_")


def _terraform_source(provider_source):
    if provider_source.startswith("registry.terraform.io/"):
        return provider_source[len("registry.terraform.io/"):]
    return provider_source


def _terraform_schema_hcl(terraform_provider, provider_source, provider_version):
    source = terraform_provider.get("source") or _terraform_source(provider_source)
    version = terraform_provider.get("version") or provider_version
    local_name = terraform_provider.get("local_name") or _provider_local_name(source)
    lines = [
        "terraform {",
        "  required_providers {",
        "    %s = {" % local_name,
        "      source = %s" % json.dumps(source),
    ]
    if version:
        lines.append("      version = %s" % json.dumps(version))
    lines.extend([
        "    }",
        "  }",
        "}",
        "",
    ])
    return "\n".join(lines)


def _prepare_schema(recipe, recipe_path, work_dir):
    schema = recipe.get("terraform_schema") or {}
    inputs_dir = os.path.join(work_dir, "inputs")
    os.makedirs(inputs_dir, exist_ok=True)
    schema_path = os.path.join(inputs_dir, "provider-schema.json")
    if schema.get("path"):
        source = _recipe_path(_recipe_dir(recipe_path), schema["path"])
        return _copy_json_input(source, schema_path)

    terraform_provider = recipe.get("terraform_provider") or {}
    provider_source = recipe.get("provider_source")
    if not provider_source:
        raise ValueError("recipe must include provider_source")
    terraform_dir = os.path.join(work_dir, "terraform-schema")
    os.makedirs(terraform_dir, exist_ok=True)
    _write_text(
        _terraform_schema_hcl(
            terraform_provider,
            provider_source,
            recipe.get("provider_version")),
        os.path.join(terraform_dir, "main.tf"),
    )
    terraform_bin = (recipe.get("tools") or {}).get("terraform", "terraform")
    _run([terraform_bin, "init", "-backend=false"], cwd=terraform_dir)
    _run(
        [terraform_bin, "providers", "schema", "-json"],
        cwd=terraform_dir,
        stdout_path=schema_path,
    )
    _read_json(schema_path)
    return schema_path


def _openapi_operation_profile(openapi_path):
    spec = _read_json(openapi_path)
    operations = 0
    get_operations = 0
    missing_operation_ids = 0
    for path_obj in (spec.get("paths") or {}).values():
        for method, operation in (path_obj or {}).items():
            if method.lower() not in HTTP_METHODS:
                continue
            if not isinstance(operation, dict):
                continue
            operations += 1
            if method.lower() == "get":
                get_operations += 1
            if not operation.get("operationId"):
                missing_operation_ids += 1
    return {
        "operations": operations,
        "get_operations": get_operations,
        "missing_operation_ids": missing_operation_ids,
        "operation_id_coverage_ratio": (
            round(
                float(operations - missing_operation_ids) / operations,
                4)
            if operations else None
        ),
    }


def _warning_codes(report):
    codes = []
    for warning in report.get("coverage", {}).get("warnings", []):
        codes.append(warning.get("code"))
    for section in ("registry_read_coverage", "registry_fetch_coverage"):
        for warning in report.get(section, {}).get("warnings", []):
            codes.append(warning.get("code"))
    return sorted(code for code in codes if code)


def _provider_metadata(recipe):
    return {
        "name": recipe.get("name"),
        "provider_source": recipe.get("provider_source"),
        "provider_version": recipe.get("provider_version"),
        "resource_prefix": recipe.get("resource_prefix", ""),
        "api_prefix": recipe.get("api_prefix", "/api/"),
    }


def _build_summary(recipe, source_report, openapi_report, openapi_profile):
    return {
        "provider": _provider_metadata(recipe),
        "openapi_operation_profile": openapi_profile,
        "source_evidence": source_report["summary"],
        "generic_openapi_map": openapi_report["summary"],
        "registry_read_coverage": (
            openapi_report["registry_read_coverage"]["summary"]),
        "registry_fetch_coverage": (
            openapi_report["registry_fetch_coverage"]["summary"]),
        "warning_codes": _warning_codes(openapi_report),
    }


def _artifact_paths(work_dir):
    artifacts = os.path.join(work_dir, "artifacts")
    os.makedirs(artifacts, exist_ok=True)
    return {
        "source_registry": os.path.join(artifacts, "source-registry.json"),
        "source_diagnostics": os.path.join(
            artifacts, "source-diagnostics.json"),
        "openapi_map": os.path.join(artifacts, "openapi-map.json"),
        "summary": os.path.join(artifacts, "summary.json"),
        "markdown": os.path.join(artifacts, "summary.md"),
    }


def run_probe(recipe_path, work_dir=None):
    recipe = _read_json(recipe_path)
    name = recipe.get("name") or os.path.splitext(
        os.path.basename(recipe_path))[0]
    work_dir = work_dir or os.path.join(DEFAULT_WORK_ROOT, name)
    work_dir = os.path.abspath(work_dir)
    os.makedirs(work_dir, exist_ok=True)

    schema_path = _prepare_schema(recipe, recipe_path, work_dir)
    openapi_path = _prepare_openapi(recipe, recipe_path, work_dir)
    source_root = _prepare_source(recipe, recipe_path, work_dir)

    provider_source = recipe.get("provider_source")
    resource_prefix = recipe.get("resource_prefix", "")
    api_prefix = recipe.get("api_prefix", "/api/")

    source_report = source_operation_map.derive_registry(
        schema_path,
        openapi_path,
        source_root,
        provider_source=provider_source,
        resource_prefix=resource_prefix,
    )
    openapi_report = openapi_resource_map.build_report(
        schema_path,
        openapi_path,
        provider_source=provider_source,
        resource_prefix=resource_prefix,
        api_prefix=api_prefix,
        registry_data=source_report["registry"],
    )
    openapi_profile = _openapi_operation_profile(openapi_path)
    summary = _build_summary(
        recipe, source_report, openapi_report, openapi_profile)

    artifacts = _artifact_paths(work_dir)
    _write_json(source_report["registry"], artifacts["source_registry"])
    _write_json({
        "summary": source_report["summary"],
        "diagnostics": source_report["diagnostics"],
    }, artifacts["source_diagnostics"])
    _write_json(openapi_report, artifacts["openapi_map"])
    _write_json(summary, artifacts["summary"])
    _write_text(render_markdown(summary, artifacts), artifacts["markdown"])

    return {
        "work_dir": work_dir,
        "inputs": {
            "schema": schema_path,
            "openapi": openapi_path,
            "source_root": source_root,
        },
        "artifacts": artifacts,
        "summary": summary,
    }


def _summary_row(label, data, keys):
    values = [str(data.get(key, "")) for key in keys]
    return "| %s | %s |" % (label, " | ".join(values))


def render_markdown(summary, artifacts=None):
    provider = summary["provider"]
    lines = [
        "# Provider Probe: %s" % (provider.get("name") or provider.get(
            "resource_prefix") or "unknown"),
        "",
        "- Provider source: `%s`" % (provider.get("provider_source") or ""),
        "- Provider version: `%s`" % (provider.get("provider_version") or ""),
        "- Resource prefix: `%s`" % (provider.get("resource_prefix") or ""),
        "- API prefix: `%s`" % (provider.get("api_prefix") or ""),
        "",
        "## Coverage",
        "",
        "| Section | Resources | Mapped | Ambiguous | Unmapped | Matched | Coverage |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]
    source = summary["source_evidence"]
    lines.append(_summary_row("source evidence", {
        "resources": source.get("resources"),
        "mapped": source.get("mapped"),
        "ambiguous": source.get("ambiguous"),
        "unmapped": source.get("unmapped"),
        "matched": "",
        "coverage": "",
    }, ("resources", "mapped", "ambiguous", "unmapped", "matched", "coverage")))
    generic = summary["generic_openapi_map"]
    lines.append(_summary_row("generic OpenAPI map", {
        "resources": generic.get("resources"),
        "mapped": "",
        "ambiguous": generic.get("ambiguous"),
        "unmapped": generic.get("unmatched"),
        "matched": generic.get("matched"),
        "coverage": "",
    }, ("resources", "mapped", "ambiguous", "unmapped", "matched", "coverage")))
    read = summary["registry_read_coverage"]
    lines.append(_summary_row("registry read coverage", {
        "resources": read.get("read_resources"),
        "mapped": "",
        "ambiguous": read.get("ambiguous"),
        "unmapped": read.get("unmatched"),
        "matched": read.get("matched"),
        "coverage": read.get("coverage_ratio"),
    }, ("resources", "mapped", "ambiguous", "unmapped", "matched", "coverage")))
    profile = summary["openapi_operation_profile"]
    lines.extend([
        "",
        "## OpenAPI",
        "",
        "- Operations: `%s`" % profile.get("operations"),
        "- GET operations: `%s`" % profile.get("get_operations"),
        "- Missing operationIds: `%s`" % profile.get("missing_operation_ids"),
        "- operationId coverage: `%s`" % (
            profile.get("operation_id_coverage_ratio")),
    ])
    warnings = summary.get("warning_codes") or []
    lines.extend(["", "## Warnings", ""])
    if warnings:
        for code in warnings:
            lines.append("- `%s`" % code)
    else:
        lines.append("- none")
    if artifacts:
        lines.extend(["", "## Artifacts", ""])
        for name, path in sorted(artifacts.items()):
            lines.append("- `%s`: `%s`" % (name, path))
    lines.append("")
    return "\n".join(lines)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Run a repeatable provider-readiness probe")
    parser.add_argument("recipe", help="Provider probe recipe JSON")
    parser.add_argument(
        "--work-dir",
        help=(
            "Probe workspace. Defaults to %s/<recipe-name>"
            % DEFAULT_WORK_ROOT))
    parser.add_argument("--out", help="Copy summary JSON to this path")
    parser.add_argument("--markdown", help="Copy summary Markdown to this path")
    args = parser.parse_args(argv)
    try:
        result = run_probe(args.recipe, work_dir=args.work_dir)
        if args.out:
            _write_json(result["summary"], args.out)
        if args.markdown:
            _write_text(render_markdown(result["summary"]), args.markdown)
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    sys.stdout.write("wrote %s\n" % result["artifacts"]["summary"])
    sys.stdout.write("wrote %s\n" % result["artifacts"]["markdown"])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
