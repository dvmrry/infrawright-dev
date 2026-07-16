#!/usr/bin/env python3
"""Freeze the retained reconcile/OpenAPI Python authorities at bfaf461."""

import contextlib
import hashlib
import importlib.util
import io
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
import unicodedata
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
BASELINE = "bfaf46159f7209fdc58dbc4b85d820442aacaad4"
PYTHON_VERSION = "3.13.13"
UNICODE_VERSION = "15.1.0"
WORK_ROOT = pathlib.Path(".reconcile-openapi-authority")

EXPECTED_SOURCE_SHA256 = {
    "engine/collectors/rest/__init__.py": "610b913dd0ed40bbd27282dc536ba9b9a3e3c2171e5ef3ea26ffed216e0f4841",
    "engine/manifest_checks.py": "4b0402149a4de579165bdd048b1e313f05c7aaf6fd508f7897822f709ee29543",
    "engine/openapi_resource_map.py": "6026a4d25eaa4a2d5d669c32a8d9dbdd7de29f1bf1f8ad9b25c6ed5ded513770",
    "engine/overrides.py": "f7db7b85a64098d17c198a85aebdf643409b28a5b010ce1b5e34b26d9135421c",
    "engine/packs.py": "6c0521a0c4f4374bcf8e1b2e2fe0193197f986d5205b03c9cbd691821dd0ad00",
    "engine/reconcile_schema_api.py": "23deac644d9688df034cbd7f19d8bfcbcea15c3eb7a5109a89debc576037b7ea",
    "engine/registry.py": "b040fe31fbb011a462f821dfb18b58fd9fd13913492d44056509ebbc4f32a201",
    "engine/tfschema.py": "12057bb1ec2922659afeaf1d4220283d66d67309ca047199bd7babeb32d05117",
    "engine/transform.py": "918add8b6792a7a6c7bfff5357ee48ffa845b2ab367cd4cb72d0475130cc7e2f",
    "node-src/authoring/openapi-resource-map.ts": "d3c338ac8efb34a55186681eb65a9adea31a4798cd67b6571feec7ec4d71a3f5",
    "node-src/authoring/openapi.ts": "fc50de84ef7fa7762c3961c3ca81c2ad953cd1558bf8661215ab5e359db237d4",
    "node-src/authoring/reconcile-schema-api.ts": "d0a5f0fbadab3a9d3e40088c7ae9ec6200d927ee415f0d238aaf894dd405977c",
    "node-src/cli/main.ts": "aeb892a7a5392b8a92d06e3a257186d40aab73508bbaa1aa7177b2122e99b4b7",
    "node-src/domain/pull-transform.ts": "cced8e66afcd849d105abe5666fc0414f73979db6cec0019e6d4109c81045dfe",
    "node-src/json/control.ts": "8ebb457938ff6bf474c0188b820aa43f70b454855a48ba4863a1ffffb2826f07",
    "node-src/json/python-compatible.ts": "54505a9d508f103fd40af7897508edf86d0c8bd0028e98d178c1fb9e79749e07",
    "node-src/json/python-equality.ts": "0ad8e6fb9241f5a644ea38435c6a046b5c621f81db04f84581e7af6e0526336a",
    "node-src/metadata/terraform-schema.ts": "bee44a3c9ff079acdb39c3e2c3dc636d86cbfe3b92ff51ecd5a75c62a71a1fec",
    "node-src/metadata/validation.ts": "b8cbc7b930ac4ee8da7dae5a4625a13d1f4902f67c75127e0e222c983c3b5693",
    "node-tests/authoring-cli.test.ts": "f32444cb72372a0fd51ca0f42419bc73a621148f3f43cfe68ddb29a8ca77bfbc",
    "node-tests/authoring-openapi-resource-map.test.ts": "072241a149683728cf67cb3191fe17da60231ccba45c794b398f833ad0b18bbe",
    "node-tests/authoring-reconcile-schema-api.test.ts": "d3a929a3462b288dd2078c7a0c27d8ac6a0282c1023229229ad22cb18785f5a5",
    "node-tests/fixtures/python-source-operation-map-v1.json": "7d80eb5271b82469b0acd5499d88e4a79e22a802379e7f9d1d3c92064a463a10",
    "node-tests/python-oracle.ts": "1a19c591392dc88af45276950293a2d2a09afb023b7a4c8b39dd71c35796effb",
    "package-lock.json": "519d8646702b6a563cb29ab969bb3637e6fe688b4c607341aa476e5dff613521",
    "package.json": "d3763145f127a10aea212f9c06d3e33203c17c799fd0c4470a15aa54e7cb0ae0",
    "packs/aws/pack.json": "735d4d49ecebab11310f07be07a5829bbae487dcbb66cdd77b0bb6701e7a4cc1",
    "packs/cloudflare/pack.json": "139d68c697e43c11b62bda5f78f92d64df8f16a6d20bb3ae82b061be422d48f7",
    "packs/google/pack.json": "5946f75799d85fc8886a56bce5ffa258b7275924b580455b4778423d000af60b",
    "packs/netbox/pack.json": "79840942e07bb7330c58ff181009263265e37a856bbcefaa34db3bd7948da1a9",
    "packs/zcc/pack.json": "3f700f083fd7a10ea7bf7b3b50c034a554668c067541bb8ce0962cfa951253a6",
    "packs/zcc/registry.json": "db76add7a1139092c8018b734acb1f40d95538f6ce67e830989a22a84e360e20",
    "packs/zia/pack.json": "f6279ed5e9b238497e1af1340c37ba2b256429248cdc29ffefcfe1258bd0cdc9",
    "packs/zia/registry.json": "52c212c139e018cbe85fed2179571916598eae32b80b1a8f21fa1a1670e9df41",
    "packs/zpa/pack.json": "bff6cae337a61a68de39ab8267f035f40aa994bbf564df2973033eb8fcce17fb",
    "packs/zpa/registry.json": "8c54fbf9bfd2bf8bebc0244ca5a516cb3d9f4ce08195d619bbc375f5790bdd5a",
    "packs/ztc/pack.json": "e36f3e6494ff661171a7ea6404924e8491c1f1332b83871c2aaa5067cc790f4d",
    "packs/ztc/registry.json": "7f123296cceb1f6d007af3fab4c3bfd043b37b77e6d2612183e62453ddab3b1e",
    "tests/test_openapi_resource_map.py": "565415e1a06195b9dcf9fcf624d30a828a889ed6129edf0a03948769d0d001bf",
    "tests/test_reconcile_schema_api.py": "328f4c5d785f92a2b12e3052a9020063a225cab7cf2faea972e96721969bc893",
    "tsconfig.test.json": "065f1e62534e3f8868391bcb6806c052842d176e0eb0153310c5c8875df2fcc3",
}

REPRODUCTION_COMMAND = """set -eu
repo="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
root="$tmp/baseline"
cleanup() {
  git -C "$repo" worktree remove --force "$root" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM
git -C "$repo" worktree add --detach "$root" bfaf46159f7209fdc58dbc4b85d820442aacaad4
cp "$repo/scripts/archive/generate-reconcile-openapi-authority.py" "$root/scripts/archive/"
(
  cd "$root"
  npm ci --ignore-scripts
  python3.13 scripts/archive/generate-reconcile-openapi-authority.py
)
cp "$root/node-tests/fixtures/python-reconcile-schema-api-v1.json" "$repo/node-tests/fixtures/"
cp "$root/node-tests/fixtures/python-openapi-resource-map-v1.json" "$repo/node-tests/fixtures/"
"""

RECONCILE_NODE_CASE_NAMES = [
    "comprehensive_reconciliation",
    "codepoint_path_ordering",
]
OPENAPI_NODE_CASE_NAMES = [
    "generic_mapping",
    "ztc_aliases_and_action_resource",
    "parent_scoped_allocations",
    "computed_relationship_alias",
    "half_even_ratio_32",
    "half_even_ratio_160",
]


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def run(arguments, **kwargs):
    return subprocess.run(arguments, cwd=ROOT, check=True, **kwargs)


def validate_source_hashes(root=ROOT, expected_source_sha256=None, check_git=True):
    if expected_source_sha256 is None:
        expected_source_sha256 = EXPECTED_SOURCE_SHA256
    for relative, expected in sorted(expected_source_sha256.items()):
        current = sha256((root / relative).read_bytes())
        if current != expected:
            raise SystemExit(
                "source hash mismatch for %s: expected %s, got %s"
                % (relative, expected, current)
            )
        if check_git:
            baseline = subprocess.run(
                ["git", "show", "%s:%s" % (BASELINE, relative)],
                cwd=root,
                check=True,
                stdout=subprocess.PIPE,
            ).stdout
            if sha256(baseline) != expected:
                raise SystemExit("pinned Git source hash mismatch for %s" % relative)


def validate_baseline():
    head = run(
        ["git", "rev-parse", "HEAD"],
        stdout=subprocess.PIPE,
        text=True,
    ).stdout.strip()
    if head != BASELINE:
        raise SystemExit("authority requires HEAD %s (got %s)" % (BASELINE, head))
    if platform.python_implementation().lower() != "cpython":
        raise SystemExit("authority requires CPython")
    if platform.python_version() != PYTHON_VERSION or unicodedata.unidata_version != UNICODE_VERSION:
        raise SystemExit(
            "authority requires CPython %s / UCD %s (got %s / %s)"
            % (
                PYTHON_VERSION,
                UNICODE_VERSION,
                platform.python_version(),
                unicodedata.unidata_version,
            )
        )
    validate_source_hashes()


def json_copy(value):
    return json.loads(json.dumps(value, ensure_ascii=True, sort_keys=True))


def file_input(path):
    if path is None:
        return None
    selected = pathlib.Path(path)
    raw = selected.read_text(encoding="utf-8")
    return {
        "path": selected.as_posix(),
        "bytes": raw,
        "json": json.loads(raw),
    }


def load_test_module(name, relative):
    spec = importlib.util.spec_from_file_location(name, ROOT / relative)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def stable_test_directories(test_class, suite_name):
    def set_up(test):
        path = WORK_ROOT / suite_name / test._testMethodName
        shutil.rmtree(path, ignore_errors=True)
        path.mkdir(parents=True)
        test.tmp = path.as_posix()

    def tear_down(test):
        shutil.rmtree(test.tmp, ignore_errors=True)

    test_class.setUp = set_up
    test_class.tearDown = tear_down


class CapturingResult(unittest.TextTestResult):
    current = None

    def startTest(self, test):
        CapturingResult.current = test
        super().startTest(test)


def case_name(ordinals, prefix):
    test = CapturingResult.current
    if test is None:
        raise RuntimeError("authority call occurred outside a retained unittest")
    method = test._testMethodName
    ordinal = ordinals.get(method, 0) + 1
    ordinals[method] = ordinal
    return "%s:%s#%d" % (prefix, method, ordinal)


def run_test_class(test_class):
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(test_class)
    result = unittest.TextTestRunner(
        stream=sys.stderr,
        verbosity=1,
        resultclass=CapturingResult,
    ).run(suite)
    if not result.wasSuccessful():
        raise SystemExit(1)
    return result.testsRun


def cli_input(arguments, file_options):
    files = []
    index = 0
    while index < len(arguments):
        argument = arguments[index]
        if argument in file_options:
            value = arguments[index + 1]
            files.append({"option": argument, **file_input(value)})
            index += 2
        else:
            index += 1
    return {"argv": list(arguments), "files": files}


def run_reconcile_unittests(reconcile):
    module = load_test_module(
        "retained_test_reconcile_schema_api",
        "tests/test_reconcile_schema_api.py",
    )
    stable_test_directories(module.ReconcileSchemaApiTest, "reconcile")
    report_cases = []
    helper_cases = []
    cli_cases = []
    ordinals = {}

    original_reconcile_items = reconcile.reconcile_items
    original_options = reconcile.api_metadata_from_options
    original_openapi = reconcile.api_metadata_from_openapi
    original_load_schema = reconcile.load_resource_schema
    original_main = reconcile.main

    def capture_reconcile_items(resource_type, items, resource_schema, override=None, api_metadata=None):
        result = original_reconcile_items(
            resource_type,
            items,
            resource_schema,
            override=override,
            api_metadata=api_metadata,
        )
        report_cases.append({
            "name": case_name(ordinals, "report"),
            "input": json_copy({
                "resource_type": resource_type,
                "items": items,
                "schema": resource_schema,
                "override": override,
                "api_metadata": api_metadata,
            }),
            "report": json_copy(result.as_dict()),
        })
        return result

    def capture_options(value, source="<options>"):
        result = original_options(value, source=source)
        helper_cases.append({
            "name": case_name(ordinals, "api_metadata_from_options"),
            "input": json_copy({"value": value, "source": source}),
            "output": json_copy(result),
        })
        return result

    def capture_openapi(spec, read_operations=None, write_operations=None):
        result = original_openapi(
            spec,
            read_operations=read_operations,
            write_operations=write_operations,
        )
        helper_cases.append({
            "name": case_name(ordinals, "api_metadata_from_openapi"),
            "input": json_copy({
                "spec": spec,
                "read_operations": read_operations,
                "write_operations": write_operations,
            }),
            "output": json_copy(result),
        })
        return result

    def capture_load_schema(resource_type, schema_path=None, provider_source=None):
        result = original_load_schema(
            resource_type,
            schema_path=schema_path,
            provider_source=provider_source,
        )
        helper_cases.append({
            "name": case_name(ordinals, "load_resource_schema"),
            "input": {
                "resource_type": resource_type,
                "schema": file_input(schema_path),
                "provider_source": provider_source,
            },
            "output": json_copy(result),
        })
        return result

    reconcile_file_options = {
        "--api",
        "--api-options",
        "--openapi",
        "--override",
        "--schema",
    }

    def capture_main(arguments=None):
        selected = list(arguments or [])
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            exit_code = original_main(selected)
        stdout_value = stdout.getvalue()
        stderr_value = stderr.getvalue()
        sys.stdout.write(stdout_value)
        sys.stderr.write(stderr_value)
        artifacts = {}
        if "--out" in selected:
            output = pathlib.Path(selected[selected.index("--out") + 1])
            if output.exists():
                artifacts["report"] = output.read_text(encoding="utf-8")
        cli_cases.append({
            "name": case_name(ordinals, "cli"),
            "input": cli_input(selected, reconcile_file_options),
            "exit": exit_code,
            "stdout": stdout_value,
            "stderr": stderr_value,
            "artifacts": artifacts,
        })
        return exit_code

    reconcile.reconcile_items = capture_reconcile_items
    reconcile.api_metadata_from_options = capture_options
    reconcile.api_metadata_from_openapi = capture_openapi
    reconcile.load_resource_schema = capture_load_schema
    reconcile.main = capture_main
    try:
        tests_run = run_test_class(module.ReconcileSchemaApiTest)
    finally:
        reconcile.reconcile_items = original_reconcile_items
        reconcile.api_metadata_from_options = original_options
        reconcile.api_metadata_from_openapi = original_openapi
        reconcile.load_resource_schema = original_load_schema
        reconcile.main = original_main
    return {
        "tests_run": tests_run,
        "report_cases": report_cases,
        "helper_cases": helper_cases,
        "cli_cases": cli_cases,
    }


def run_openapi_unittests(openapi_resource_map):
    module = load_test_module(
        "retained_test_openapi_resource_map",
        "tests/test_openapi_resource_map.py",
    )
    stable_test_directories(module.OpenApiResourceMapTest, "openapi")
    report_cases = []
    cli_cases = []
    ordinals = {}
    original_build_report = openapi_resource_map.build_report
    original_main = openapi_resource_map.main

    def capture_build_report(
            schema_path,
            openapi_path,
            provider_source=None,
            resource_prefix="",
            api_prefix="/api/",
            registry_data=None):
        result = original_build_report(
            schema_path,
            openapi_path,
            provider_source=provider_source,
            resource_prefix=resource_prefix,
            api_prefix=api_prefix,
            registry_data=registry_data,
        )
        report_cases.append({
            "name": case_name(ordinals, "report"),
            "input": {
                "schema": file_input(schema_path),
                "openapi": file_input(openapi_path),
                "provider_source": provider_source,
                "resource_prefix": resource_prefix,
                "api_prefix": api_prefix,
                "registry_data": json_copy(registry_data),
            },
            "report": json_copy(result),
        })
        return result

    openapi_file_options = {"--openapi", "--registry", "--schema"}

    def capture_main(arguments=None):
        selected = list(arguments or [])
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            exit_code = original_main(selected)
        stdout_value = stdout.getvalue()
        stderr_value = stderr.getvalue()
        sys.stdout.write(stdout_value)
        sys.stderr.write(stderr_value)
        artifacts = {}
        if "--out" in selected:
            output = pathlib.Path(selected[selected.index("--out") + 1])
            if output.exists():
                artifacts["report"] = output.read_text(encoding="utf-8")
        cli_cases.append({
            "name": case_name(ordinals, "cli"),
            "input": cli_input(selected, openapi_file_options),
            "exit": exit_code,
            "stdout": stdout_value,
            "stderr": stderr_value,
            "artifacts": artifacts,
        })
        return exit_code

    openapi_resource_map.build_report = capture_build_report
    openapi_resource_map.main = capture_main
    try:
        tests_run = run_test_class(module.OpenApiResourceMapTest)
    finally:
        openapi_resource_map.build_report = original_build_report
        openapi_resource_map.main = original_main
    return {
        "tests_run": tests_run,
        "report_cases": report_cases,
        "cli_cases": cli_cases,
    }


ORACLE_WRAPPER = r'''#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import sys

arguments = sys.argv[1:]
stdin = sys.stdin.buffer.read()
real = os.environ["IW_AUTHORITY_REAL_PYTHON"]
result = subprocess.run(
    [real] + arguments,
    input=stdin,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
file_options = {"--api", "--api-options", "--openapi", "--override", "--registry", "--schema"}
files = []
index = 0
while index < len(arguments):
    argument = arguments[index]
    if argument in file_options and index + 1 < len(arguments):
        selected = pathlib.Path(arguments[index + 1])
        if selected.is_file():
            files.append({
                "option": argument,
                "bytes": selected.read_text(encoding="utf-8"),
            })
        index += 2
    else:
        index += 1
record = {
    "arguments": arguments,
    "stdin": stdin.decode("utf-8"),
    "files": files,
    "exit": result.returncode,
    "stdout": result.stdout.decode("utf-8"),
    "stderr": result.stderr.decode("utf-8"),
}
with open(os.environ["IW_AUTHORITY_ORACLE_LOG"], "a", encoding="utf-8") as handle:
    handle.write(json.dumps(record, ensure_ascii=True, sort_keys=True, separators=(",", ":")) + "\n")
sys.stdout.buffer.write(result.stdout)
sys.stderr.buffer.write(result.stderr)
raise SystemExit(result.returncode)
'''


def compile_node_tests():
    node = run(["node", "--version"], stdout=subprocess.PIPE, text=True).stdout.strip()
    if not node.startswith("v24."):
        raise SystemExit("Node input enumeration requires Node 24 (got %s)" % node)
    run(["npm", "run", "build:test"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    return "24"


def oracle_records(test_name, wrapper, log):
    log.unlink(missing_ok=True)
    environment = dict(os.environ)
    environment.update({
        "IW_AUTHORITY_ORACLE_LOG": str(log.resolve()),
        "IW_AUTHORITY_REAL_PYTHON": sys.executable,
        "PYTHON": str(wrapper.resolve()),
    })
    result = subprocess.run(
        [
            "node",
            "--test",
            "--test-concurrency=1",
            str(ROOT / ".node-test/node-tests" / test_name),
        ],
        cwd=ROOT,
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if result.returncode != 0:
        sys.stderr.write(result.stdout)
        sys.stderr.write(result.stderr)
        raise SystemExit("Node input enumeration failed for %s" % test_name)
    return [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()]


def live_report_cases(records, names):
    selected = [
        record for record in records
        if record["arguments"][:1] == ["-c"] and record["stdin"]
    ]
    if len(selected) != len(names):
        raise SystemExit(
            "expected %d live report inputs, captured %d"
            % (len(names), len(selected))
        )
    cases = []
    for name, record in zip(names, selected):
        if record["exit"] != 0 or record["stderr"]:
            raise SystemExit("live Python report case %s did not succeed cleanly" % name)
        cases.append({
            "name": name,
            "input_bytes": record["stdin"],
            "input": json.loads(record["stdin"]),
            "python_exit": record["exit"],
            "python_stdout": record["stdout"],
            "python_stderr": record["stderr"],
            "python_report": json.loads(record["stdout"]),
        })
    return cases


def stable_node_cli_case(record, name, module):
    arguments = record["arguments"]
    if arguments[:2] != ["-m", module]:
        raise SystemExit("unexpected CLI authority invocation: %r" % arguments)
    if record["exit"] != 0 or record["stderr"]:
        raise SystemExit("live Python CLI case %s did not succeed cleanly" % name)
    file_values = {}
    for item in record["files"]:
        file_values.setdefault(item["option"], []).append(item["bytes"])
    positional = []
    options = {}
    index = 2
    while index < len(arguments):
        argument = arguments[index]
        if argument.startswith("--"):
            if argument in file_values:
                index += 2
                continue
            value = arguments[index + 1]
            options.setdefault(argument, []).append(value)
            index += 2
        else:
            positional.append(argument)
            index += 1
    return {
        "name": name,
        "module": module,
        "input": {
            "positional": positional,
            "options": options,
            "files": file_values,
        },
        "python_exit": record["exit"],
        "python_stdout": record["stdout"],
        "python_stderr": record["stderr"],
    }


def capture_node_live_inputs():
    node_version = compile_node_tests()
    node_root = WORK_ROOT / "node-inputs"
    node_root.mkdir(parents=True, exist_ok=True)
    wrapper = node_root / "python-oracle-wrapper.py"
    wrapper.write_text(ORACLE_WRAPPER, encoding="utf-8")
    wrapper.chmod(0o755)
    log = node_root / "oracle.jsonl"

    reconcile_records = oracle_records(
        "authoring-reconcile-schema-api.test.js", wrapper, log)
    openapi_records = oracle_records(
        "authoring-openapi-resource-map.test.js", wrapper, log)
    cli_records = oracle_records("authoring-cli.test.js", wrapper, log)
    cli_selected = [
        record for record in cli_records
        if record["arguments"][:2] in (
            ["-m", "engine.reconcile_schema_api"],
            ["-m", "engine.openapi_resource_map"],
        )
    ]
    if len(cli_selected) != 2:
        raise SystemExit("expected two authoring CLI Python invocations")
    return {
        "node_version": node_version,
        "reconcile_reports": live_report_cases(
            reconcile_records,
            RECONCILE_NODE_CASE_NAMES,
        ),
        "openapi_reports": live_report_cases(
            openapi_records,
            OPENAPI_NODE_CASE_NAMES,
        ),
        "reconcile_cli": stable_node_cli_case(
            cli_selected[0],
            "authoring_cli_reconcile",
            "engine.reconcile_schema_api",
        ),
        "openapi_cli": stable_node_cli_case(
            cli_selected[1],
            "authoring_cli_openapi_map",
            "engine.openapi_resource_map",
        ),
    }


def provenance(kind, node_version):
    return {
        "kind": kind,
        "version": 1,
        "baseline_commit": BASELINE,
        "authority": {
            "implementation": "cpython",
            "python": PYTHON_VERSION,
            "unicode_database": UNICODE_VERSION,
        },
        "node_input_enumerator": {
            "node_major": node_version,
            "rule": (
                "execute the baseline Node live-differential tests through a "
                "teeing Python wrapper; retain only their raw Python inputs and "
                "the delegated Python exit/stdout/stderr, never Node outputs"
            ),
        },
        "normalization": "none",
        "source_blobs_sha256": dict(sorted(EXPECTED_SOURCE_SHA256.items())),
        "generator_sha256": sha256(pathlib.Path(__file__).read_bytes()),
        "producing_command": REPRODUCTION_COMMAND,
    }


def write_fixture(target, value):
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )
    return {
        "path": target.relative_to(ROOT).as_posix(),
        "bytes": target.stat().st_size,
        "sha256": sha256(target.read_bytes()),
    }


def validate_capture_counts(reconcile_tests, openapi_tests, node_inputs):
    expected = {
        "reconcile retained tests": (reconcile_tests["tests_run"], 9),
        "reconcile retained reports": (len(reconcile_tests["report_cases"]), 7),
        "reconcile retained helpers": (len(reconcile_tests["helper_cases"]), 5),
        "reconcile retained CLI cases": (len(reconcile_tests["cli_cases"]), 1),
        "reconcile Node report inputs": (len(node_inputs["reconcile_reports"]), 2),
        "openapi retained tests": (openapi_tests["tests_run"], 13),
        "openapi retained reports": (len(openapi_tests["report_cases"]), 13),
        "openapi retained CLI cases": (len(openapi_tests["cli_cases"]), 1),
        "openapi Node report inputs": (len(node_inputs["openapi_reports"]), 6),
    }
    failures = [
        "%s expected %d, got %d" % (label, wanted, actual)
        for label, (actual, wanted) in expected.items()
        if actual != wanted
    ]
    if failures:
        raise SystemExit("incomplete authority capture: " + "; ".join(failures))


def main():
    validate_baseline()
    os.chdir(ROOT)
    sys.path.insert(0, str(ROOT))
    from engine import openapi_resource_map  # noqa: E402
    from engine import reconcile_schema_api  # noqa: E402

    shutil.rmtree(WORK_ROOT, ignore_errors=True)
    try:
        reconcile_tests = run_reconcile_unittests(reconcile_schema_api)
        openapi_tests = run_openapi_unittests(openapi_resource_map)
        node_inputs = capture_node_live_inputs()
        validate_capture_counts(reconcile_tests, openapi_tests, node_inputs)

        reconcile_fixture = {
            **provenance(
                "infrawright.python-reconcile-schema-api-authority",
                node_inputs["node_version"],
            ),
            "retained_unittest": reconcile_tests,
            "node_live_differential": {
                "report_cases": node_inputs["reconcile_reports"],
                "cli_cases": [node_inputs["reconcile_cli"]],
            },
        }
        openapi_fixture = {
            **provenance(
                "infrawright.python-openapi-resource-map-authority",
                node_inputs["node_version"],
            ),
            "retained_unittest": openapi_tests,
            "node_live_differential": {
                "report_cases": node_inputs["openapi_reports"],
                "cli_cases": [node_inputs["openapi_cli"]],
            },
        }
        summaries = [
            write_fixture(
                ROOT / "node-tests/fixtures/python-reconcile-schema-api-v1.json",
                reconcile_fixture,
            ),
            write_fixture(
                ROOT / "node-tests/fixtures/python-openapi-resource-map-v1.json",
                openapi_fixture,
            ),
        ]
    finally:
        shutil.rmtree(WORK_ROOT, ignore_errors=True)

    print(
        "reconcile tests=%d reports=%d helpers=%d cli=%d node_reports=%d node_cli=1"
        % (
            reconcile_tests["tests_run"],
            len(reconcile_tests["report_cases"]),
            len(reconcile_tests["helper_cases"]),
            len(reconcile_tests["cli_cases"]),
            len(node_inputs["reconcile_reports"]),
        )
    )
    print(
        "openapi tests=%d reports=%d cli=%d node_reports=%d node_cli=1"
        % (
            openapi_tests["tests_run"],
            len(openapi_tests["report_cases"]),
            len(openapi_tests["cli_cases"]),
            len(node_inputs["openapi_reports"]),
        )
    )
    for summary in summaries:
        print(
            "%s bytes=%d sha256=%s"
            % (summary["path"], summary["bytes"], summary["sha256"])
        )


if __name__ == "__main__":
    main()
