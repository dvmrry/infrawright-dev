#!/usr/bin/env python3
"""Regenerate the frozen source-operation authority from the retained Python suite."""
import hashlib
import importlib.util
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
import tempfile
import unittest
import unicodedata

ROOT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

BASELINE = "7d90752ac4b800c5509b380d02dc828749f891a6"
PLACEHOLDER = "<FIXTURE_ROOT>"
EXPECTED_PYTHON = "3.13.13"
EXPECTED_UCD = "15.1.0"
SOURCE_LOCKS = {
    "engine/openapi_resource_map.py": "6026a4d25eaa4a2d5d669c32a8d9dbdd7de29f1bf1f8ad9b25c6ed5ded513770",
    "engine/reconcile_schema_api.py": "23deac644d9688df034cbd7f19d8bfcbcea15c3eb7a5109a89debc576037b7ea",
    "engine/sdk_path_evidence.py": "b2bd536010df6cfab10bfe1001a0d9990797ea6505387c7a1f02890cb3df0406",
    "engine/source_operation_map.py": "343e756d19c0ed32e51c33cb7885fb103f4bd98f43b54748dc11a8febe4426c4",
    "engine/tfschema.py": "12057bb1ec2922659afeaf1d4220283d66d67309ca047199bd7babeb32d05117",
    "tests/test_sdk_path_evidence.py": "ef6d455b71be3958767df232b5b70004db92e587e663dc63176221a72995e9ad",
    "tests/test_source_operation_map.py": "673a0cb4e0b3eb711449e83c8a7b31a4f6e28174f247b49ad0547aa5e3c7ccc4",
    "node-src/authoring/cli.ts": "d3c5a27296880da3413fbf5d5213defd8de15c0dd40fb45b1c0808b1ff9fccd1",
    "node-src/authoring/openapi-resource-map.ts": "d3c338ac8efb34a55186681eb65a9adea31a4798cd67b6571feec7ec4d71a3f5",
    "node-src/authoring/openapi.ts": "fc50de84ef7fa7762c3961c3ca81c2ad953cd1558bf8661215ab5e359db237d4",
    "node-src/authoring/provider-source-evidence.ts": "eb399182907201dabf016df4a6a030b207519f6b73e1d36535a0c5f176b5bdb0",
    "node-src/authoring/reconcile-schema-api.ts": "d0a5f0fbadab3a9d3e40088c7ae9ec6200d927ee415f0d238aaf894dd405977c",
    "node-src/authoring/sdk-path-evidence.ts": "e90aaaa3547541fe99dfbca6c178be0e78a97423e7be8f45eef9369164ac1306",
    "node-src/authoring/source-operation-map.ts": "571c3d3cf2413c185be2ac46eca05fe9f33b528aa439182ad972165303e0f6a9",
    "node-src/json/python-compatible.ts": "54505a9d508f103fd40af7897508edf86d0c8bd0028e98d178c1fb9e79749e07",
    "node-src/metadata/terraform-schema.ts": "bee44a3c9ff079acdb39c3e2c3dc636d86cbfe3b92ff51ecd5a75c62a71a1fec",
    "node-src/metadata/validation.ts": "b8cbc7b930ac4ee8da7dae5a4625a13d1f4902f67c75127e0e222c983c3b5693",
    "node-tests/authoring-cli.test.ts": "0247a7f3710b1f94a57c60f97f9ea3ed929c4a30be379e364d7d62576ba980f3",
    "node-tests/authoring-sdk-path-evidence.test.ts": "2ac9c2512daa9a5d5d300028e2c3f7ac2c1da45858ae8efc67e2265e084aa0a6",
    "node-tests/authoring-source-operation-map.test.ts": "80461adad1b994fdba1f4f5907dd85d473bcbe23b18431aa11a0b3923ac389fd",
}
source_operation_map = None
current_test = None
derive_ordinals = {}
derive_cases = []
cli_cases = []
node_differential_cases = []


def normalize(value, fixture_root):
    if isinstance(value, str):
        return value.replace(str(fixture_root), PLACEHOLDER)
    if isinstance(value, list):
        return [normalize(item, fixture_root) for item in value]
    if isinstance(value, tuple):
        return [normalize(item, fixture_root) for item in value]
    if isinstance(value, set):
        return sorted(normalize(item, fixture_root) for item in value)
    if isinstance(value, dict):
        return {key: normalize(item, fixture_root) for key, item in value.items()}
    return value


def files_under(root):
    root = pathlib.Path(root)
    if not root.is_dir():
        return None
    result = {}
    for path in sorted(root.rglob("*")):
        if path.is_file():
            result[path.relative_to(root).as_posix()] = path.read_text(encoding="utf-8")
    return result


def json_file(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def write_files(root, files):
    for relative, contents in files.items():
        target = pathlib.Path(root) / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(contents, encoding="utf-8")


def node_differential_report(base, name, files, schema, openapi, *, facts=None, sdk_files=None):
    root = pathlib.Path(base) / name
    root.mkdir(parents=True)
    write_files(root, files)
    schema_path = pathlib.Path(base) / f"{name}-schema.json"
    openapi_path = pathlib.Path(base) / f"{name}-openapi.json"
    schema_path.write_text(json.dumps(schema), encoding="utf-8")
    openapi_path.write_text(json.dumps(openapi), encoding="utf-8")
    sdk_root = None
    if sdk_files is not None:
        sdk_root = pathlib.Path(base) / f"{name}-sdk"
        sdk_root.mkdir()
        write_files(sdk_root, sdk_files)
    options = {
        "provider_source": "registry.terraform.io/example/example",
        "resource_prefix": "example",
        "source_facts": facts,
        "sdk_root": None if sdk_root is None else str(sdk_root),
    }
    report = original_derive(str(schema_path), str(openapi_path), str(root), **options)
    node_differential_cases.append({
        "name": name,
        "report": normalize(report, base),
    })
    return report


original_derive = None
original_main = None


def capture_derive(schema_path, openapi_path, source_root, **kwargs):
    result = original_derive(schema_path, openapi_path, source_root, **kwargs)
    fixture_root = pathlib.Path(current_test.tmp)
    name = current_test._testMethodName
    ordinal = derive_ordinals.get(name, 0) + 1
    derive_ordinals[name] = ordinal
    derive_cases.append({
        "name": f"{name}#{ordinal}",
        "input": {
            "schema": json_file(schema_path),
            "openapi": json_file(openapi_path),
            "source_files": files_under(source_root),
            "source_root": normalize(str(source_root), fixture_root),
            "source_root_exists": pathlib.Path(source_root).is_dir(),
            "provider_source": kwargs.get("provider_source"),
            "resource_prefix": kwargs.get("resource_prefix", ""),
            "source_facts": normalize(kwargs.get("source_facts"), fixture_root),
            "resource_filter": kwargs.get("resource_filter"),
            "sdk_files": files_under(kwargs["sdk_root"]) if kwargs.get("sdk_root") else None,
            "sdk_root": normalize(kwargs.get("sdk_root"), fixture_root),
        },
        "report": normalize(result, fixture_root),
    })
    return result


def capture_main(arguments):
    result = original_main(arguments)
    fixture_root = pathlib.Path(current_test.tmp)
    artifacts = {}
    for option in ("--out", "--diagnostics", "--source-facts-compare"):
        if option in arguments:
            path = pathlib.Path(arguments[arguments.index(option) + 1])
            if path.exists():
                artifacts[option] = normalize(path.read_text(encoding="utf-8"), fixture_root)
    process = subprocess.run(
        [sys.executable, "-m", "engine.source_operation_map", *arguments],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    cli_cases.append({
        "name": current_test._testMethodName,
        "artifacts": artifacts,
        "exit_status": process.returncode,
        "stdout": normalize(process.stdout, fixture_root),
        "stderr": normalize(process.stderr, fixture_root),
    })
    return result


class CapturingResult(unittest.TextTestResult):
    def startTest(self, test):
        global current_test
        current_test = test
        super().startTest(test)


def sha_at_baseline(relative):
    content = subprocess.run(
        ["git", "show", f"{BASELINE}:{relative}"], cwd=ROOT,
        check=True, stdout=subprocess.PIPE,
    ).stdout
    return hashlib.sha256(content).hexdigest()


def validate_authority_environment():
    head = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=ROOT, check=True,
        text=True, stdout=subprocess.PIPE,
    ).stdout.strip()
    if head != BASELINE:
        raise SystemExit(
            "authority generator must run at baseline %s (found %s)" %
            (BASELINE, head))
    if platform.python_implementation().lower() != "cpython":
        raise SystemExit("authority generator requires CPython")
    if platform.python_version() != EXPECTED_PYTHON:
        raise SystemExit(
            "authority generator requires CPython %s (found %s)" %
            (EXPECTED_PYTHON, platform.python_version()))
    if unicodedata.unidata_version != EXPECTED_UCD:
        raise SystemExit(
            "authority generator requires UCD %s (found %s)" %
            (EXPECTED_UCD, unicodedata.unidata_version))
    for relative, expected in sorted(SOURCE_LOCKS.items()):
        baseline = sha_at_baseline(relative)
        if baseline != expected:
            raise SystemExit(
                "baseline source lock mismatch for %s: expected %s, found %s" %
                (relative, expected, baseline))
        actual = hashlib.sha256((ROOT / relative).read_bytes()).hexdigest()
        if actual != expected:
            raise SystemExit(
                "working source lock mismatch for %s: expected %s, found %s" %
                (relative, expected, actual))


SDK_SCANNER_SOURCE = r'''package sdk
const widgetsBasePath = "v2/widgets"
const ( groupedBasePath = "v2/grouped" )
type WidgetsServiceOp struct { client *Client }
type GroupedClient struct { client *Client }
type RawAPI struct { client *Client }
func (s *WidgetsServiceOp) Get(ctx context.Context, widgetID int) error {
  path := fmt.Sprintf("%s/%d", widgetsBasePath, widgetID)
  path = fmt.Sprintf("%s/%s/%v", path, "literal", complexID())
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err
}
func (s WidgetsServiceOp) List(ctx context.Context) error {
  path := widgetsBasePath
  _, err := s.client.NewRequest(ctx, "GET", path, nil); return err
}
func (s *GroupedClient) Read(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", groupedBasePath, id)
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err
}
func (s *RawAPI) Create(ctx context.Context) error {
  path := widgetsBasePath
  _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err
}
func (s *WidgetsService) MissingMethod(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", widgetsBasePath, id); return nil
}
func (s *WidgetsService) MissingPath(ctx context.Context) error {
  _, err := s.client.NewRequest(ctx, http.MethodGet, unknown, nil); return err
}
'''
SDK_TESTS_ONLY = r'''package sdk
const testsBasePath = "v2/tests"
type TestsServiceOp struct { client *Client }
func (s *TestsServiceOp) Get(ctx context.Context) error {
  path := testsBasePath
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err
}
'''


def sdk_scanner_authorities(sdk_path_evidence):
    root = pathlib.Path(tempfile.mkdtemp(prefix="sdk-path-authority-"))
    try:
        for relative in (
                ".git/ignored.go", "a/widgets.go", "a/widgets_test.go",
                "test/ignored.go", "testdata/ignored.go"):
            target = root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(SDK_SCANNER_SOURCE, encoding="utf-8")
        target = root / "tests/only.go"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(SDK_TESTS_ONLY, encoding="utf-8")
        invalid = root / "z/invalid.go"
        invalid.parent.mkdir(parents=True, exist_ok=True)
        invalid.write_bytes(b"\xff\xfe\xfd")
        evidence, unresolved = sdk_path_evidence.extract_sdk_paths(str(root))
        supported = {"evidence": evidence, "unresolved": unresolved}
    finally:
        shutil.rmtree(root)
    evidence, unresolved = sdk_path_evidence.extract_sdk_paths(None)
    return {
        "missing_root": {"evidence": evidence, "unresolved": unresolved},
        "supported_path_shapes": supported,
    }


def sdk_source_operation_authorities(test_module):
    mapping = (
        ("domain", "test_sdk_path_resolves_domains_get"),
        ("droplet", "test_sdk_path_resolves_droplets_get"),
        ("vpc", "test_sdk_path_resolves_vpcs_get"),
        ("reserved_ip_action", "test_sdk_path_action_case_reserved_ip_assignment"),
        ("ast_sdk_action", "test_sdk_path_uses_ast_facts_for_read_and_action_calls"),
        ("helper_action_disambiguation", "test_sdk_path_helper_action_get_does_not_ambiguous_resource_read"),
        ("unresolved_symbol", "test_sdk_path_unresolved_reports_sdk_symbol_not_found"),
        ("unresolved_openapi_path", "test_sdk_path_unresolved_reports_openapi_path_not_found"),
        ("unresolved_ambiguous_path", "test_sdk_path_ambiguous_structural_match_surfaces_unresolved"),
        ("fuzzy_fallback", "test_sdk_root_absent_falls_back_to_fuzzy_scoring"),
        ("cli_sdk_root", "test_cli_accepts_sdk_root_flag"),
    )
    reports = {}
    for name, method in mapping:
        captured = []

        def capture(*args, **kwargs):
            report = original_derive(*args, **kwargs)
            captured.append(report)
            return report

        source_operation_map.derive_registry = capture
        case = test_module.SdkPathEvidenceTest(method)
        case.setUp()
        try:
            getattr(case, method)()
            if not captured:
                raise SystemExit("SDK authority case did not derive a report: %s" % name)
            reports[name] = captured[-1]
        finally:
            case.tearDown()
    source_operation_map.derive_registry = original_derive
    return reports


def write_sdk_authority(generator_sha, resurrection):
    from engine import sdk_path_evidence

    spec = importlib.util.spec_from_file_location(
        "retained_test_sdk_path_evidence",
        ROOT / "tests/test_sdk_path_evidence.py",
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    source_operation_map.main = original_main
    reports = sdk_source_operation_authorities(module)
    cli_report = reports.pop("cli_sdk_root")
    cases = sdk_scanner_authorities(sdk_path_evidence)
    cases["source_operation_reports"] = reports
    cases["cli_sdk_root"] = {
        "diagnostics_bytes": json.dumps({
            "diagnostics": cli_report["diagnostics"],
            "summary": cli_report["summary"],
        }, indent=2, sort_keys=True) + "\n",
        "exit_code": 0,
        "registry_bytes": json.dumps(
            cli_report["registry"], indent=2, sort_keys=True) + "\n",
        "report": cli_report,
        "stderr": "",
        "stdout": "",
    }
    fixture = {
        "cases": cases,
        "provenance": {
            "baseline_commit": BASELINE,
            "generator_sha256": generator_sha,
            "normalization": "none; scanner and source-operation reports contain only SDK-root-relative paths",
            "python": EXPECTED_PYTHON,
            "python_implementation": "cpython",
            "resurrection": resurrection,
            "source_blobs_sha256": SOURCE_LOCKS,
            "unicode_database": EXPECTED_UCD,
        },
        "schema_version": 1,
    }
    target = ROOT / "node-tests/fixtures/python-sdk-path-evidence-v1.json"
    target.write_text(
        json.dumps(fixture, ensure_ascii=True, sort_keys=True, indent=2) + "\n",
        encoding="utf-8",
    )
    return target


def main():
    global current_test, source_operation_map, original_derive, original_main
    validate_authority_environment()
    from engine import source_operation_map as loaded_source_operation_map
    source_operation_map = loaded_source_operation_map
    original_derive = source_operation_map.derive_registry
    original_main = source_operation_map.main
    spec = importlib.util.spec_from_file_location(
        "retained_test_source_operation_map",
        ROOT / "tests/test_source_operation_map.py",
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    source_operation_map.derive_registry = capture_derive
    source_operation_map.main = capture_main
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(module.SourceOperationMapTest)
    runner = unittest.TextTestRunner(
        stream=sys.stderr,
        verbosity=1,
        resultclass=CapturingResult,
    )
    result = runner.run(suite)
    if not result.wasSuccessful():
        raise SystemExit(1)

    differential_root = pathlib.Path(tempfile.mkdtemp(prefix="source-operation-node-differentials-"))
    try:
        provider = "registry.terraform.io/example/example"
        folder_schema = {"provider_schemas": {provider: {"resource_schemas": {
            "example_folder": {"block": {"attributes": {"name": {"required": True, "type": "string"}}}},
        }}}}
        folder_openapi = {"openapi": "3.0.3", "paths": {
            "/api/folders": {"get": {"operationId": "RouteGetFolders"}},
            "/api/folders/{uid}": {"get": {"operationId": "RouteGetFolder"}},
        }}
        node_differential_report(differential_root, "text_scanner", {"internal/resource_folder.go": '''package internal
func resourceFolder() {
  name := "example_folder"
  _ = name
  client.Provisioning.GetFolders(ctx)
  client.Provisioning.GetFolder("abc")
}
'''}, folder_schema, folder_openapi)

        ast_root = differential_root / "ast_facts"
        ast_root.mkdir(); write_files(ast_root, {"resource_folder.go": "package provider\n"})
        ast_facts = {
            "source_root": str(ast_root), "files": [{"path": "resource_folder.go", "package": "provider", "imports": []}],
            "functions": [], "resource_registrations": [], "resource_references": [], "identifier_references": [],
            "read_callbacks": [], "package_calls": [], "raw_rest_calls": [], "selector_calls": [
                {"file": "resource_folder.go", "function": "read", "parts": ["client", "Provisioning", "GetFolders"], "symbol": "client.Provisioning.GetFolders"},
                {"file": "resource_folder.go", "function": "read", "parts": ["client", "Provisioning", "GetFolder"], "symbol": "client.Provisioning.GetFolder"},
            ],
        }
        ast_schema_path = differential_root / "ast-schema.json"; ast_schema_path.write_text(json.dumps(folder_schema))
        ast_openapi_path = differential_root / "ast-openapi.json"; ast_openapi_path.write_text(json.dumps(folder_openapi))
        ast_candidate = original_derive(str(ast_schema_path), str(ast_openapi_path), str(ast_root), provider_source=provider, resource_prefix="example", source_facts=ast_facts)
        ast_control = original_derive(str(ast_schema_path), str(ast_openapi_path), str(ast_root), provider_source=provider, resource_prefix="example")
        node_differential_cases.append({"name": "ast_facts", "report": normalize(ast_candidate, differential_root)})
        node_differential_cases.append({"name": "ast_facts_comparison", "report": normalize(source_operation_map.compare_registry_reports(ast_control, ast_candidate), differential_root)})

        layout_files = {
            "provider.go": 'package provider\nimport project "example.com/provider/internal/project"\nvar resources = map[string]func(){"example_registered": resourceRegistered, "example_packaged": project.NewResource}\nfunc resourceRegistered() { _ = &Resource{Read: readRegistered} }\n',
            "registered/read.go": "package registered\nfunc readRegistered() { client.Registered.GetRegistered(ctx) }\n",
            "internal/project/resource.go": "package project\nfunc NewResource() { client.Packaged.GetPackaged(ctx) }\n",
            "internal/project/data_source_skip.go": "package project\nfunc ignored() { client.Wrong.GetWrong(ctx) }\n",
            "internal/services/service/resource.go": "package service\nfunc read() { client.Service.GetService(ctx) }\n",
            "internal/framework/resources/framework.go": 'package resources\nimport external "example.net/sdk"\nfunc read() { external.GetFramework(ctx) }\n',
            "resource_raw.go": 'package provider\nimport ("fmt"; "net/http")\nfunc readRaw() { _, _ = client.NewRequest(http.MethodGet, fmt.Sprintf("/raw/%s", id), nil) }\n',
            "resource_graphql.go": 'package provider\nimport "github.com/shurcooL/githubv4"\nfunc readGraphql() { githubv4.NewRequest() }\n',
        }
        names = ["registered", "packaged", "service", "framework", "raw", "graphql"]
        layout_schema = {"provider_schemas": {provider: {"resource_schemas": {f"example_{name}": {"block": {"attributes": {}}} for name in names}}}}
        layout_openapi = {"paths": {f"/{name}/{{id}}": {"get": {"operationId": f"Get{name[0].upper()}{name[1:]}"}} for name in names if name != "graphql"}}
        node_differential_report(differential_root, "source_layout", layout_files, layout_schema, layout_openapi)

        widget_schema = {"provider_schemas": {provider: {"resource_schemas": {"example_widget": {"block": {"attributes": {}}}}}}}
        widget_openapi = {"paths": {"/v2/widgets/{widget_id}": {"get": {"operationId": "GetWidget"}}, "/v2/widgets": {"post": {"operationId": "CreateWidget"}}}}
        widget_files = {"resource_widget.go": "package provider\nfunc read() { client.Widgets.Get(ctx, id); client.Widgets.Create(ctx) }\n"}
        sdk_files = {"widgets.go": 'package sdk\nconst widgetsBasePath = "v2/widgets"\ntype WidgetsServiceOp struct { client *Client }\nfunc (s *WidgetsServiceOp) Get(ctx context.Context, id string) error { path := fmt.Sprintf("%s/%s", widgetsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }\nfunc (s *WidgetsServiceOp) Create(ctx context.Context) error { path := widgetsBasePath; _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err }\n'}
        node_differential_report(differential_root, "sdk_text", widget_files, widget_schema, widget_openapi, sdk_files=sdk_files)
        sdk_facts_root = differential_root / "sdk_facts"
        sdk_facts = {"source_root": str(sdk_facts_root), "files": [{"path": "resource_widget.go", "imports": [], "package": "provider"}], "functions": [], "resource_registrations": [], "resource_references": [], "identifier_references": [], "read_callbacks": [], "package_calls": [], "raw_rest_calls": [], "selector_calls": [
            {"file": "resource_widget.go", "parts": ["client", "Widgets", "Get"], "symbol": "client.Widgets.Get"},
            {"file": "resource_widget.go", "parts": ["client", "Widgets", "Create"], "symbol": "client.Widgets.Create"},
        ]}
        node_differential_report(differential_root, "sdk_facts", widget_files, widget_schema, widget_openapi, facts=sdk_facts, sdk_files=sdk_files)

        ambiguity_files = {
            "resource_thing.go": 'package provider\nvar name = "example_thing"\nfunc read() { client.ThingsAPI.GetThing(ctx, id); client.ThingsAPI.RetrieveThing(ctx, uid) }\n',
            "resource_repository_topics.go": 'package provider\nvar name = "example_repository_topics"\nfunc read() { client.Repositories.ListAllTopics(ctx, owner, repo, nil) }\n',
        }
        ambiguity_schema = {"provider_schemas": {provider: {"resource_schemas": {"example_thing": {"block": {"attributes": {}}}, "example_repository_topics": {"block": {"attributes": {}}}}}}}
        ambiguity_openapi = {"paths": {"/things/{id}": {"get": {"operationId": "GetThing"}}, "/things/{uid}": {"get": {"operationId": "RetrieveThing"}}, "/repos/{owner}/{repo}/topics": {"get": {"operationId": "repos/get-all-topics"}}}}
        node_differential_report(differential_root, "ambiguity_relationship", ambiguity_files, ambiguity_schema, ambiguity_openapi)

        escaped_openapi = {"paths": {"/widgets/{id}": {"get": {"operationId": "GetWidget"}}}}
        escaped_files = {"resource_widget.go": r'''package provider
func read() { client.NewRequest("GET", "\x2fwidgets\u002f%s", nil) }
'''}
        node_differential_report(differential_root, "escaped_rest_text", escaped_files, widget_schema, escaped_openapi)
        escaped_facts_root = differential_root / "escaped_rest_facts"
        escaped_facts = {"source_root": str(escaped_facts_root), "files": [{"path": "resource_widget.go", "imports": [], "package": "provider"}], "functions": [], "resource_registrations": [], "resource_references": [], "identifier_references": [], "read_callbacks": [], "package_calls": [], "raw_rest_calls": [], "selector_calls": [{"file": "resource_widget.go", "parts": [], "symbol": "client.Widgets.Get"}]}
        node_differential_report(differential_root, "escaped_rest_facts", escaped_files, widget_schema, escaped_openapi, facts=escaped_facts)
        unresolved_root = differential_root / "unresolved_rest_facts"
        unresolved_facts = {"source_root": str(unresolved_root), "files": [{"path": "resource_widget.go", "imports": [], "package": "provider"}], "functions": [], "resource_registrations": [], "resource_references": [], "identifier_references": [], "read_callbacks": [], "package_calls": [], "selector_calls": [], "raw_rest_calls": [{"file": "resource_widget.go", "function": "read", "method": "GET", "symbol": "client.NewRequest"}]}
        node_differential_report(differential_root, "unresolved_rest_facts", {"resource_widget.go": 'package provider\nfunc read() { client.NewRequest("GET", dynamicPath, nil) }\n'}, widget_schema, escaped_openapi, facts=unresolved_facts)
    finally:
        shutil.rmtree(differential_root)

    cli_root = pathlib.Path(tempfile.mkdtemp(prefix="source-operation-cli-authority-"))
    try:
        source_root = cli_root / "source"
        source_root.mkdir()
        (source_root / "resource_widget.go").write_text(
            "package provider\nfunc resourceWidgetRead() { client.Widgets.Get(ctx, id) }\n",
            encoding="utf-8",
        )
        schema = {"resource_schemas": {"example_widget": {"block": {
            "attributes": {"name": {"required": True, "type": "string"}},
            "block_types": {"settings": {"block": {"attributes": {"mode": {"optional": True, "type": "string"}}}, "max_items": 1, "nesting_mode": "list"}},
        }}}}
        openapi = {"info": {"title": "authoring CLI fixture", "version": "1"}, "openapi": "3.0.3", "paths": {
            "/widgets": {"get": {"operationId": "ListWidgets", "responses": {"200": {"description": "ok"}}}, "post": {"operationId": "CreateWidget", "responses": {"200": {"description": "ok"}}}},
            "/widgets/{id}": {"get": {"operationId": "GetWidget", "responses": {"200": {"description": "ok"}}}},
        }}
        facts = {
            "files": [{"imports": [], "package": "provider", "path": "resource_widget.go"}], "functions": [],
            "identifier_references": [], "package_calls": [], "raw_rest_calls": [], "read_callbacks": [],
            "resource_references": [], "resource_registrations": [],
            "selector_calls": [{"file": "resource_widget.go", "parts": ["client", "Widgets", "Get"], "symbol": "client.Widgets.Get"}],
            "source_root": str(source_root),
        }
        schema_path = cli_root / "schema.json"; schema_path.write_text(json.dumps(schema), encoding="utf-8")
        openapi_path = cli_root / "openapi.json"; openapi_path.write_text(json.dumps(openapi), encoding="utf-8")
        facts_path = cli_root / "facts.json"; facts_path.write_text(json.dumps(facts), encoding="utf-8")
        process = subprocess.run([
            sys.executable, "-m", "engine.source_operation_map",
            "--schema", str(schema_path), "--openapi", str(openapi_path),
            "--source-root", str(source_root), "--resource-prefix", "example",
            "--source-facts", str(facts_path),
        ], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
        cli_cases.append({
            "name": "authoring_cli_stdout",
            "exit_status": process.returncode,
            "artifacts": {
                "stderr": normalize(process.stderr, cli_root),
                "stdout": normalize(process.stdout, cli_root),
            },
            "stdout": normalize(process.stdout, cli_root),
            "stderr": normalize(process.stderr, cli_root),
        })
    finally:
        shutil.rmtree(cli_root)

    helper_cases = {
        "ast_identifier_tokens": source_operation_map._identifier_tokens_from_facts(
            ["resource.go"],
            {
                "source_root": PLACEHOLDER,
                "files": [],
                "functions": [],
                "resource_registrations": [],
                "resource_references": [],
                "identifier_references": [],
                "read_callbacks": [],
                "package_calls": [],
                "raw_rest_calls": [],
                "selector_calls": [{
                    "file": "resource.go",
                    "function": "Read",
                    "parts": ["r", "client", "IAM", "UserGroups", "Members", "List"],
                    "symbol": "r.client.IAM.UserGroups.Members.List",
                }],
            },
        ),
        "sdk_ip_get_member_role": source_operation_map._sdk_method_role("ImageShareGroupGetMember"),
        "sdk_ip_list_members_role": source_operation_map._sdk_method_role("ImageShareGroupListMembers"),
        "sdk_ip_aliases": source_operation_map._sdk_method_tokens({"method": "ListIPAddresses"}),
        "path_kind_playlist": source_operation_map._path_kind({"path": "/playlists/{uid}", "operation_id": "getPlaylist"}),
        "path_kind_product_search": source_operation_map._path_kind({"path": "/accounts/{account_id}/ai-search/instances/{id}", "operation_id": "ai-search-fetch-instance"}),
        "path_kind_product_list": source_operation_map._path_kind({"path": "/accounts/{account_id}/gateway/lists/{list_id}", "operation_id": "zero-trust-lists-zero-trust-list-details"}),
    }
    generator_sha = hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
    resurrection = "git worktree add <path> %s && cp scripts/archive/generate-source-operation-authority.py <path>/scripts/archive/ && (cd <path> && python3 scripts/archive/generate-source-operation-authority.py)" % BASELINE
    fixture = {
        "schema_version": 2,
        "provenance": {
            "baseline_commit": BASELINE,
            "python_implementation": "cpython",
            "python": "3.13.13",
            "unicode_database": "15.1.0",
            "normalization": {
                "placeholder": PLACEHOLDER,
                "rule": "replace each retained unittest temporary root in complete report values and CLI artifact bytes; no other normalization",
            },
            "source_blobs_sha256": SOURCE_LOCKS,
            "generator_sha256": generator_sha,
            "resurrection": resurrection,
        },
        "derive_cases": derive_cases,
        "node_differential_cases": node_differential_cases,
        "cli_cases": cli_cases,
        "helper_cases": normalize(helper_cases, pathlib.Path("/__no_fixture_root__")),
    }
    target = ROOT / "node-tests/fixtures/python-source-operation-map-v1.json"
    target.write_text(
        json.dumps(fixture, ensure_ascii=True, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )
    sdk_target = write_sdk_authority(generator_sha, resurrection)
    print(f"cases derive={len(derive_cases)} node_differential={len(node_differential_cases)} cli={len(cli_cases)} helper={len(helper_cases)}")
    print(f"bytes={target.stat().st_size}")
    print(f"sha256={hashlib.sha256(target.read_bytes()).hexdigest()}")
    print(f"sdk_bytes={sdk_target.stat().st_size}")
    print(f"sdk_sha256={hashlib.sha256(sdk_target.read_bytes()).hexdigest()}")


if __name__ == "__main__":
    main()
