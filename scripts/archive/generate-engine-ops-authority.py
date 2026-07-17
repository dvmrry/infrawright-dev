#!/usr/bin/env python3
"""Capture the retained Python engine.ops authority through unmodified Node tests."""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import platform
import shutil
import stat
import subprocess
import sys
import tempfile
import unicodedata


ROOT = pathlib.Path(__file__).resolve().parents[2]
BASELINE = "a00510b46b04767d371bf7c05286d13b52784253"
EXPECTED_PYTHON = "3.13.13"
EXPECTED_UCD = "15.1.0"
EXPECTED_NODE_MAJOR = 24
SUITES = {
    "assessment-cli": ("assessment-cli.test.js", 8),
    "differential": ("differential.test.js", 30),
    "plan-cli": ("plan-cli.test.js", 9),
}
FIXTURE_NAMES = {
    name: "python-%s-v1.json" % name for name in SUITES
}
INTERNAL_ENV_PREFIX = "IW_AUTHORITY_"


WRAPPER = r'''#!/usr/bin/env python3
import base64
import hashlib
import json
import os
import pathlib
import re
import stat
import subprocess
import sys

arguments = sys.argv[1:]
stdin_bytes = sys.stdin.buffer.read()

# The resolver's interpreter compatibility probe is not a delegated engine call.
if arguments[:2] == ["-I", "-c"]:
    probe = subprocess.run(
        [os.environ["IW_AUTHORITY_REAL_PYTHON"]] + arguments,
        input=stdin_bytes,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=os.environ,
    )
    sys.stdout.buffer.write(probe.stdout)
    sys.stderr.buffer.write(probe.stderr)
    raise SystemExit(probe.returncode)

repo_input = pathlib.Path(os.environ["IW_AUTHORITY_REPOSITORY"])
run_root_input = pathlib.Path(os.environ["IW_AUTHORITY_RUN_ROOT"])
wrapper_input = pathlib.Path(__file__)
log_input = pathlib.Path(os.environ["IW_AUTHORITY_ORACLE_LOG"])
python_input = pathlib.Path(os.environ["IW_AUTHORITY_REAL_PYTHON"])
repo = repo_input.resolve()
run_root = run_root_input.resolve()
wrapper_path = wrapper_input.resolve()
log_path = log_input.resolve()
real_python = python_input.resolve()

def absolute_strings(value):
    found = []
    if isinstance(value, str):
        # JSON strings, command arguments, and environment values are the only
        # authority inputs. Split conservatively while retaining whole strings.
        found.append(value)
    elif isinstance(value, list):
        for item in value:
            found.extend(absolute_strings(item))
    elif isinstance(value, dict):
        for item in value.values():
            found.extend(absolute_strings(item))
    return found

def top_material_roots(candidate):
    try:
        selected = pathlib.Path(candidate)
    except (TypeError, ValueError):
        return set()
    if not selected.is_absolute():
        return set()
    found = set()

    # Preserve the lexical top-level root so symlink nodes remain evidence.
    try:
        lexical_relative = selected.relative_to(run_root_input)
    except ValueError:
        try:
            lexical_relative = selected.relative_to(repo_input)
        except ValueError:
            lexical_relative = None
        if (lexical_relative is not None and lexical_relative.parts
                and lexical_relative.parts[0].startswith(".node-plan-roots-")):
            found.add(repo_input / lexical_relative.parts[0])
    else:
        if lexical_relative.parts and lexical_relative.parts[0] != "authority-internal":
            found.add(run_root_input / lexical_relative.parts[0])

    # Also preserve the resolved top-level root. A lexical workspace path can
    # traverse a symlink into a separate generated target root, and both trees
    # are material to the Python call.
    resolved = selected.resolve(strict=False)
    try:
        resolved_relative = resolved.relative_to(run_root)
    except ValueError:
        try:
            resolved_relative = resolved.relative_to(repo)
        except ValueError:
            resolved_relative = None
        if (resolved_relative is not None and resolved_relative.parts
                and resolved_relative.parts[0].startswith(".node-plan-roots-")):
            found.add(repo / resolved_relative.parts[0])
    else:
        if resolved_relative.parts and resolved_relative.parts[0] != "authority-internal":
            found.add(run_root / resolved_relative.parts[0])
    return found

environment = {
    key: value for key, value in os.environ.items()
    if not key.startswith("IW_AUTHORITY_")
}
seed_values = list(arguments) + list(environment.values())
try:
    seed_values.extend(absolute_strings(json.loads(stdin_bytes.decode("utf-8"))))
except (UnicodeDecodeError, json.JSONDecodeError):
    pass

roots = set()
for value in seed_values:
    # Direct paths are the normal case. Also find absolute paths embedded in a
    # JSON string without interpreting or rewriting the original bytes.
    possible = [value]
    for token in value.replace('"', " ").replace("'", " ").split():
        possible.append(token.rstrip(",]}"))
    for candidate in possible:
        roots.update(top_material_roots(candidate))

# Deployment documents can name a sibling overlay/target root. Discover only
# generated roots explicitly referenced by material files already selected.
changed = True
while changed:
    changed = False
    for root in list(roots):
        if not root.exists():
            continue
        paths = [root] if root.is_file() else root.rglob("*")
        for path in paths:
            if not path.is_file() or path.stat().st_size > 2_000_000:
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except (UnicodeDecodeError, OSError):
                continue
            for token in text.replace('"', " ").replace("'", " ").split():
                selected = top_material_roots(token.rstrip(",]}"))
                additions = selected - roots
                if additions:
                    roots.update(additions)
                    changed = True

named = {
    "assessment-cli-": "<ASSESSMENT_CLI_ROOT>",
    "assessment-profiles-": "<ASSESSMENT_PROFILES_ROOT>",
    "infrawright-plan-cli-": "<PLAN_CLI_ROOT>",
    "infrawright-node-": "<NODE_WORKSPACE_ROOT>",
    "infrawright-overlay-": "<OVERLAY_ROOT>",
    "infrawright-target-": "<TARGET_ROOT>",
    ".node-plan-roots-": "<PLAN_ROOTS_WORKSPACE>",
}
replacements = {
    str(repo): "<REPOSITORY_ROOT>",
    str(repo_input): "<REPOSITORY_ROOT>",
    str(run_root): "<AUTHORITY_RUN_ROOT>",
    str(run_root_input): "<AUTHORITY_RUN_ROOT>",
    str(wrapper_path): "<AUTHORITY_WRAPPER>",
    str(wrapper_input): "<AUTHORITY_WRAPPER>",
    str(log_path): "<AUTHORITY_LOG>",
    str(log_input): "<AUTHORITY_LOG>",
    str(real_python): "<PYTHON_ORACLE>",
    str(python_input): "<PYTHON_ORACLE>",
}
for root in roots:
    placeholder = None
    for prefix, value in named.items():
        if root.name.startswith(prefix):
            placeholder = value
            break
    if placeholder is None:
        placeholder = "<GENERATED_ROOT_%s>" % hashlib.sha256(root.name.encode()).hexdigest()[:12]
    replacements[str(root)] = placeholder
    if root.is_relative_to(run_root):
        replacements[str(run_root_input / root.relative_to(run_root))] = placeholder
    elif root.is_relative_to(repo):
        replacements[str(repo_input / root.relative_to(repo))] = placeholder

def normalize_text(text):
    for source in sorted(replacements, key=len, reverse=True):
        text = text.replace(source, replacements[source])
    # A selected path may be a dangling symlink, so resolving it can erase the
    # lexical temp-root identity before discovery. Normalize the documented
    # generated basename forms as a final path-prefix-only pass.
    for prefix, placeholder in named.items():
        text = re.sub(
            re.escape("<AUTHORITY_RUN_ROOT>/" + prefix) + r'[^/"\'\s,}\]]+',
            placeholder,
            text,
        )
        if prefix.startswith("."):
            text = re.sub(
                re.escape(prefix) + r'[^/"\'\s,}\]]+',
                placeholder,
                text,
            )
    return text

def encoded_bytes(data):
    try:
        normalized = normalize_text(data.decode("utf-8")).encode("utf-8")
    except UnicodeDecodeError:
        normalized = data
    return {
        "sha256": hashlib.sha256(normalized).hexdigest(),
        "size": len(normalized),
        "base64": base64.b64encode(normalized).decode("ascii"),
    }

report_paths = set()
for index, argument in enumerate(arguments[:-1]):
    if argument == "--report":
        report_paths.add(pathlib.Path(arguments[index + 1]).resolve(strict=False))

def excluded(path):
    resolved = path.resolve(strict=False)
    if resolved in report_paths or resolved in {wrapper_path, log_path}:
        return True
    name = path.name
    return name.endswith(".node.json") or name.endswith(".python.json")

manifest_by_path = {}
blobs = {}
for root in sorted(roots, key=lambda item: str(item)):
    if not root.exists():
        continue
    paths = [root]
    if root.is_dir():
        paths.extend(sorted(root.rglob("*"), key=lambda item: item.as_posix()))
    for path in paths:
        if excluded(path):
            continue
        info = path.lstat()
        item = {
            "path": normalize_text(str(path)),
            "mode": stat.S_IMODE(info.st_mode),
        }
        if stat.S_ISLNK(info.st_mode):
            item["kind"] = "symlink"
            item["target"] = normalize_text(os.readlink(path))
        elif stat.S_ISDIR(info.st_mode):
            item["kind"] = "directory"
        elif stat.S_ISREG(info.st_mode):
            item["kind"] = "file"
            blob = encoded_bytes(path.read_bytes())
            blobs.setdefault(blob["sha256"], blob)
            item["blob"] = blob["sha256"]
        else:
            continue
        key = item["path"]
        previous = manifest_by_path.setdefault(key, item)
        if previous != item:
            raise SystemExit("lexical/resolved manifest disagreement for %s" % key)

manifest = [manifest_by_path[key] for key in sorted(manifest_by_path)]

result = subprocess.run(
    [str(real_python)] + arguments,
    input=stdin_bytes,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    env=os.environ,
)

artifacts = []
for path in sorted(report_paths, key=str):
    if path.is_file():
        blob = encoded_bytes(path.read_bytes())
        blobs.setdefault(blob["sha256"], blob)
        artifacts.append({"path": normalize_text(str(path)), "blob": blob["sha256"]})

record = {
    "arguments": [normalize_text(value) for value in arguments],
    "stdin": encoded_bytes(stdin_bytes),
    "environment": {key: normalize_text(value) for key, value in sorted(environment.items())},
    "input_filesystem": manifest,
    "exit_status": result.returncode,
    "stdout": encoded_bytes(result.stdout),
    "stderr": encoded_bytes(result.stderr),
    "report_artifacts": artifacts,
    "blobs": blobs,
}
with log_path.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps(record, ensure_ascii=True, sort_keys=True, separators=(",", ":")) + "\n")
sys.stdout.buffer.write(result.stdout)
sys.stderr.buffer.write(result.stderr)
raise SystemExit(result.returncode)
'''


def run(arguments, **kwargs):
    return subprocess.run(arguments, cwd=ROOT, check=True, **kwargs)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def canonical_json(value):
    return (json.dumps(
        value, ensure_ascii=True, sort_keys=True, separators=(",", ":"),
    ) + "\n").encode("utf-8")


def validate_environment():
    head = run(["git", "rev-parse", "HEAD"], text=True, stdout=subprocess.PIPE).stdout.strip()
    if head != BASELINE:
        raise SystemExit("generator requires exact HEAD %s (found %s)" % (BASELINE, head))
    if platform.python_implementation() != "CPython" or platform.python_version() != EXPECTED_PYTHON:
        raise SystemExit("generator requires CPython %s" % EXPECTED_PYTHON)
    if unicodedata.unidata_version != EXPECTED_UCD:
        raise SystemExit("generator requires UCD %s" % EXPECTED_UCD)
    node = run(["node", "--version"], text=True, stdout=subprocess.PIPE).stdout.strip()
    if not node.startswith("v%d." % EXPECTED_NODE_MAJOR):
        raise SystemExit("generator requires Node %d (found %s)" % (EXPECTED_NODE_MAJOR, node))
    return node


def source_closure():
    listing = run(
        ["git", "ls-tree", "-r", "--full-tree", BASELINE],
        text=True, stdout=subprocess.PIPE,
    ).stdout.splitlines()
    closure = {}
    for line in listing:
        metadata, path = line.split("\t", 1)
        mode, kind, object_id = metadata.split()
        if kind != "blob":
            continue
        data = run(["git", "show", "%s:%s" % (BASELINE, path)], stdout=subprocess.PIPE).stdout
        actual_path = ROOT / path
        if not actual_path.is_file() or actual_path.read_bytes() != data:
            raise SystemExit("material source differs from baseline: %s" % path)
        closure[path] = {"git_mode": mode, "git_object": object_id, "sha256": sha256(data)}
    return closure


def controlled_environment(run_root, internal, wrapper, log):
    # Deliberately exclude the caller's ambient environment, including secrets.
    return {
        "HOME": str(ROOT),
        "LANG": "C.UTF-8",
        "LC_ALL": "C.UTF-8",
        "PATH": "/run/current-system/sw/bin:/usr/bin:/bin",
        "TMPDIR": str(run_root),
        "__CF_USER_TEXT_ENCODING": "0x0:0x0:0x0",
        "PYTHON": str(wrapper),
        "IW_AUTHORITY_ORACLE_LOG": str(log),
        "IW_AUTHORITY_REAL_PYTHON": sys.executable,
        "IW_AUTHORITY_REPOSITORY": str(ROOT),
        "IW_AUTHORITY_RUN_ROOT": str(run_root),
    }


def capture_suite(suite, test_file, expected, run_root):
    internal = run_root / "authority-internal" / suite
    internal.mkdir(parents=True, exist_ok=True)
    wrapper = internal / "python-wrapper.py"
    wrapper.write_text(WRAPPER, encoding="utf-8")
    wrapper.chmod(0o755)
    log = internal / "oracle.jsonl"
    result = subprocess.run(
        ["node", "--test", "--test-concurrency=1", str(ROOT / ".node-test/node-tests" / test_file)],
        cwd=ROOT,
        env=controlled_environment(run_root, internal, wrapper, log),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if result.returncode != 0:
        sys.stderr.buffer.write(result.stdout)
        sys.stderr.buffer.write(result.stderr)
        raise SystemExit("unmodified Node suite failed: %s" % suite)
    records = [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()]
    if len(records) != expected:
        raise SystemExit("%s: expected %d records, captured %d" % (suite, expected, len(records)))
    blobs = {}
    for record in records:
        for digest, blob in record.pop("blobs").items():
            previous = blobs.setdefault(digest, blob)
            if previous != blob:
                raise SystemExit("content-address collision")
    if suite == "differential":
        validate_differential_symlink_boundary(records)
    return records, blobs


def validate_symlink_boundary_record(record):
    expected_arguments = ["-m", "engine.ops", "scope-paths", "--json", "--paths-json", "-"]
    expected_stdin = (
        '["<NODE_WORKSPACE_ROOT>/deployment-alias.json",'
        '"<TARGET_ROOT>/deleted-deployment.json"]'
    ).encode("utf-8")
    actual_stdin = base64.b64decode(record["stdin"]["base64"])
    if (record["arguments"] != expected_arguments
            or record["environment"].get("INFRAWRIGHT_DEPLOYMENT")
            != "<NODE_WORKSPACE_ROOT>/deployment-alias.json"
            or actual_stdin != expected_stdin):
        raise SystemExit(
            "human differential record 24 is no longer the deleted-deployment symlink authority")
    entries = {item["path"]: item for item in record["input_filesystem"]}
    required = {
        "<NODE_WORKSPACE_ROOT>": ("directory", None),
        "<NODE_WORKSPACE_ROOT>/jump": ("symlink", "<TARGET_ROOT>/nested"),
        "<NODE_WORKSPACE_ROOT>/deployment-alias.json": (
            "symlink", "jump/../deleted-deployment.json"),
        "<TARGET_ROOT>": ("directory", None),
        "<TARGET_ROOT>/nested": ("directory", None),
    }
    missing = []
    for path, (kind, target) in required.items():
        item = entries.get(path, {})
        if item.get("kind") != kind or (
                target is not None and item.get("target") != target):
            missing.append("%s (%s%s)" % (
                path, kind, " -> %s" % target if target is not None else ""))
    if missing:
        raise SystemExit(
            "deleted-deployment authority lost lexical/resolved material roots: %s"
            % ", ".join(missing))


def validate_differential_symlink_boundary(records):
    # Human authority record 24 is zero-based records[23]. Record 25 exercises
    # the related deleted-overlay path and is intentionally not this guard.
    if len(records) <= 23:
        raise SystemExit("differential authority is missing human record 24")
    validate_symlink_boundary_record(records[23])

    # Deliberate negative self-test: model a capture implementation that keeps
    # only resolved target evidence and loses the lexical symlink nodes.
    negative = dict(records[23])
    negative["input_filesystem"] = [
        item for item in records[23]["input_filesystem"]
        if item["path"] not in {
            "<NODE_WORKSPACE_ROOT>/jump",
            "<NODE_WORKSPACE_ROOT>/deployment-alias.json",
        }
    ]
    try:
        validate_symlink_boundary_record(negative)
    except SystemExit:
        pass
    else:
        raise SystemExit("lexical-root negative self-test did not fail")


def resurrection_command(fixture_name):
    return "\n".join([
        'authority_checkout="${IW_AUTHORITY_CHECKOUT:?set to the checkout containing this fixture}"',
        'resurrection_checkout="${IW_RESURRECTION_CHECKOUT:?set to an empty path}"',
        'git -C "$authority_checkout" worktree add --detach "$resurrection_checkout" %s' % BASELINE,
        'mkdir -p "$resurrection_checkout/scripts/archive"',
        'cp "$authority_checkout/scripts/archive/generate-engine-ops-authority.py" "$resurrection_checkout/scripts/archive/"',
        'cd "$resurrection_checkout"',
        'npm ci --ignore-scripts',
        '%s scripts/archive/generate-engine-ops-authority.py --output-dir "$resurrection_checkout/node-tests/fixtures"' % sys.executable,
        'for fixture in python-assessment-cli-v1.json python-differential-v1.json python-plan-cli-v1.json; do',
        '  cmp "$authority_checkout/node-tests/fixtures/$fixture" "$resurrection_checkout/node-tests/fixtures/$fixture"',
        'done',
    ])


def generate(output_dir):
    node_version = validate_environment()
    generator_bytes = pathlib.Path(__file__).read_bytes()
    closure = source_closure()
    run(["npm", "run", "build:test", "--silent"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    output_dir.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="engine-ops-authority-") as temporary:
        run_root = pathlib.Path(temporary)
        for suite, (test_file, expected) in SUITES.items():
            records, blobs = capture_suite(suite, test_file, expected, run_root)
            name = FIXTURE_NAMES[suite]
            fixture = {
                "schema_version": 1,
                "kind": "python-engine-ops-delegation-authority",
                "suite": suite,
                "record_count": len(records),
                "authority": {
                    "git_head": BASELINE,
                    "python": {"implementation": "CPython", "version": EXPECTED_PYTHON, "ucd": EXPECTED_UCD},
                    "node": node_version,
                    "generator_sha256": sha256(generator_bytes),
                    "generator_source_base64": base64.b64encode(generator_bytes).decode("ascii"),
                    "source_closure": closure,
                    "normalization": {
                        "only": "generated repository/workspace/temp prefixes are replaced by named placeholders",
                        "node_output_recorded": False,
                        "ambient_environment": "excluded; Node test process receives the fixed environment recorded by each delegation",
                    },
                    "clean_checkout_resurrection": resurrection_command(name),
                },
                "records": records,
                "content_blobs": blobs,
            }
            (output_dir / name).write_bytes(canonical_json(fixture))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-dir", type=pathlib.Path, default=ROOT / "node-tests/fixtures")
    options = parser.parse_args()
    generate(options.output_dir.resolve())


if __name__ == "__main__":
    main()
