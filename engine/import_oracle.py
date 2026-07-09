"""Terraform/OpenTofu import/read oracle.

Imports remote objects into an ephemeral local state and returns the provider
state values Terraform/OpenTofu reports via ``show -json``.
"""
import hashlib
import json
import os
import re
import shutil
import shlex
import subprocess
import sys
import tempfile

from engine import packs
from engine import paths
from engine import schema_paths
from engine.drift_policy import parse_path
from engine.ops import _same_json_value
from engine.transform import parse_hcl_string_literal


class OracleError(RuntimeError):
    pass


_MAX_SUBPROCESS_OUTPUT = 1200
_DEFAULT_SUBPROCESS_TIMEOUT_SECONDS = 300
_BACKEND_BLOCK_RE = re.compile(r'\bbackend\s+"[^"]+"\s*\{')
_CLOUD_BLOCK_RE = re.compile(r'\bcloud\s*\{')


def _terraform():
    return os.environ.get("TF") or "terraform"


def _instance_name(key):
    digest = hashlib.sha1(key.encode("utf-8")).hexdigest()[:16]
    return "iw_%s" % digest


def _address(resource_type, key):
    return "%s.%s" % (resource_type, _instance_name(key))


def _hcl_string_literal(value):
    if not isinstance(value, str):
        value = str(value)
    if "\x00" in value:
        raise OracleError("oracle import IDs cannot contain NUL bytes")
    escaped = (
        value.replace("\\", "\\\\")
        .replace('"', '\\"')
        .replace("\n", "\\n")
        .replace("\r", "\\r")
        .replace("\t", "\\t")
        .replace("${", "$${")
        .replace("%{", "%%{")
    )
    return '"%s"' % escaped


def _check_instance_name_collisions(resource_type, keys):
    seen = {}
    for key in sorted(keys):
        name = _instance_name(key)
        if name in seen:
            raise OracleError(
                "%s oracle instance name collision: %r and %r both map to %s"
                % (resource_type, seen[name], key, name)
            )
        seen[name] = key


def _provider_block(provider):
    path = packs.oracle_provider_config_path(provider)
    if path:
        with open(path, encoding="utf-8") as f:
            return f.read()
    return (
        'provider "%s" {\n'
        "  # credentials via provider environment variables\n"
        "}\n" % provider
    )


def render_root(resource_type, keys=None):
    provider = packs.provider_of(resource_type)
    source = packs.provider_sources()[provider]
    pins = packs.provider_pins()
    version_line = ""
    if provider in pins:
        version_line = '      version = "%s"\n' % pins[provider]
    text = (
        "terraform {\n"
        '  required_version = ">= 1.5"\n'
        "  required_providers {\n"
        "    %s = {\n" % provider
        + '      source = "%s"\n' % source
        + version_line
        + "    }\n"
        + "  }\n"
        + "}\n\n"
        + _provider_block(provider)
    )
    return text


def render_import_blocks(resource_type, key_to_import_id):
    blocks = []
    for key in sorted(key_to_import_id):
        blocks.append(
            "import {\n"
            "  to = %s\n"
            "  id = %s\n"
            "}\n" % (
                _address(resource_type, key),
                _hcl_string_literal(key_to_import_id[key]),
            )
        )
    return "\n".join(blocks)


def _assert_local_scratch_root(text):
    if _BACKEND_BLOCK_RE.search(text):
        raise OracleError(
            "oracle scratch root must not declare a Terraform backend; "
            "oracle state is intentionally ephemeral and local"
        )
    if _CLOUD_BLOCK_RE.search(text):
        raise OracleError(
            "oracle scratch root must not declare Terraform cloud; "
            "oracle state is intentionally ephemeral and local"
        )


def _display_args(args):
    shown = list(args)
    if len(shown) >= 2 and shown[1] == "import" and len(shown) >= 3:
        shown[-1] = "<redacted-import-id>"
    return " ".join(shlex.quote(str(arg)) for arg in shown)


def _sensitive_command_tokens(args, extra_tokens=None):
    tokens = []
    if len(args) >= 2 and args[1] == "import" and len(args) >= 3:
        tokens.append(str(args[-1]))
    for token in extra_tokens or []:
        tokens.append(str(token))
    return tokens


def _redact(text, tokens):
    out = text
    for token in tokens:
        if token:
            out = out.replace(token, "<redacted-import-id>")
    return out


def _summarize_output(raw, tokens):
    text = raw.decode("utf-8", "replace")
    text = _redact(text, tokens)
    if len(text) <= _MAX_SUBPROCESS_OUTPUT:
        return text
    return (
        text[:_MAX_SUBPROCESS_OUTPUT]
        + "\n[truncated %d chars]"
        % (len(text) - _MAX_SUBPROCESS_OUTPUT)
    )


def _output_bytes(value):
    if value is None:
        return b""
    if isinstance(value, bytes):
        return value
    return str(value).encode("utf-8", "replace")


def _env_truthy(name):
    value = os.environ.get(name)
    if value is None:
        return False
    return value.strip().lower() in ("1", "true", "yes", "on")


def _subprocess_timeout():
    raw = os.environ.get("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS")
    if not raw:
        return _DEFAULT_SUBPROCESS_TIMEOUT_SECONDS
    try:
        timeout = float(raw)
    except ValueError:
        raise OracleError(
            "INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS must be a positive number"
        )
    if timeout <= 0:
        raise OracleError(
            "INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS must be a positive number"
        )
    return timeout


def _write_debug_output(debug_dir, debug_name, stdout, stderr):
    if not debug_dir:
        return None, None
    out_dir = os.path.join(debug_dir, "oracle-subprocess")
    os.makedirs(out_dir, exist_ok=True)
    safe_name = re.sub(r"[^A-Za-z0-9_.-]+", "_", debug_name or "command")
    stdout_path = os.path.join(out_dir, safe_name + ".stdout")
    stderr_path = os.path.join(out_dir, safe_name + ".stderr")
    with open(stdout_path, "wb") as f:
        f.write(stdout)
    with open(stderr_path, "wb") as f:
        f.write(stderr)
    return stdout_path, stderr_path


def _raise_run_error(args, proc, debug_dir=None, debug_name=None,
                     sensitive_tokens=None):
    stdout_path, stderr_path = _write_debug_output(
        debug_dir, debug_name, proc.stdout, proc.stderr)
    debug_hint = ""
    if stdout_path or stderr_path:
        debug_hint = (
            "\nfull stdout: %s\nfull stderr: %s"
            % (stdout_path, stderr_path)
        )
    tokens = _sensitive_command_tokens(args, sensitive_tokens)
    raise OracleError(
        "%s failed with exit %d\nstdout:\n%s\nstderr:\n%s%s"
        % (
            _display_args(args),
            proc.returncode,
            _summarize_output(proc.stdout, tokens),
            _summarize_output(proc.stderr, tokens),
            debug_hint,
        )
    )


def _assert_import_plan_only(resource_type, plan, expected_addresses):
    drift = plan.get("resource_drift") or []
    if drift:
        raise OracleError(
            "%s oracle import plan reported resource drift; refusing to apply "
            "the scratch plan" % resource_type
        )
    changes = plan.get("resource_changes") or []
    addresses = set()
    expected = set(expected_addresses)
    if len(changes) != len(expected):
        raise OracleError(
            "%s oracle import plan reported %d resource change(s), expected "
            "%d import(s); refusing to apply the scratch plan"
            % (resource_type, len(changes), len(expected))
        )
    for change in changes:
        address = change.get("address")
        addresses.add(address)
        details = change.get("change") or {}
        actions = details.get("actions") or []
        importing = details.get("importing")
        if actions != ["no-op"] or not importing:
            raise OracleError(
                "%s oracle import plan was not import-only for %s "
                "(actions=%r importing=%s); refusing to apply the scratch plan"
                % (resource_type, address, actions, bool(importing))
            )
    if addresses != expected:
        missing = sorted(expected - addresses)
        unexpected = sorted(addresses - expected)
        raise OracleError(
            "%s oracle import plan addresses did not match expected scratch "
            "addresses (missing=%s unexpected=%s); refusing to apply the "
            "scratch plan"
            % (
                resource_type,
                ", ".join(missing) or "<none>",
                ", ".join(unexpected) or "<none>",
            )
        )


def _run(args, cwd, env, debug_dir=None, debug_name=None,
         sensitive_tokens=None):
    proc = _run_process(
        args,
        cwd,
        env,
        debug_dir=debug_dir,
        debug_name=debug_name,
        sensitive_tokens=sensitive_tokens,
    )
    if proc.returncode != 0:
        _raise_run_error(
            args,
            proc,
            debug_dir=debug_dir,
            debug_name=debug_name,
            sensitive_tokens=sensitive_tokens,
        )
    return proc.stdout.decode("utf-8", "replace")


def _run_process(args, cwd, env, debug_dir=None, debug_name=None,
                 sensitive_tokens=None):
    try:
        return subprocess.run(
            args,
            cwd=cwd,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
            timeout=_subprocess_timeout(),
        )
    except subprocess.TimeoutExpired as exc:
        stdout = _output_bytes(exc.stdout)
        stderr = _output_bytes(exc.stderr)
        stdout_path, stderr_path = _write_debug_output(
            debug_dir, debug_name, stdout, stderr)
        debug_hint = ""
        if stdout_path or stderr_path:
            debug_hint = (
                "\nfull stdout: %s\nfull stderr: %s"
                % (stdout_path, stderr_path)
            )
        tokens = _sensitive_command_tokens(args, sensitive_tokens)
        raise OracleError(
            "%s timed out after %s seconds\nstdout:\n%s\nstderr:\n%s%s"
            % (
                _display_args(args),
                exc.timeout,
                _summarize_output(stdout, tokens),
                _summarize_output(stderr, tokens),
                debug_hint,
            )
        )


def _generated_config_policy_entries(resource_type, policy):
    if not policy:
        return []
    entries = []
    for mode in ("projection_omit", "projection_omit_if"):
        for entry in policy.entries(resource_type, mode):
            selector = parse_path(entry["path"])
            if schema_paths.schema_status(resource_type, selector) == "required":
                raise OracleError(
                    "%s generated import config policy cannot %s required "
                    "path %s" % (resource_type, mode, entry["path"])
                )
            if _has_exact_index(selector):
                continue
            entries.append((mode, entry, selector))
    return entries


def _has_exact_index(selector):
    return any(isinstance(segment, int) for segment in selector)


def _apply_generated_config_policy(
        resource_type, expected_addresses, generated_config_path, policy,
        entries=None):
    if entries is None:
        entries = _generated_config_policy_entries(resource_type, policy)
    if not entries or not os.path.exists(generated_config_path):
        return 0
    with open(generated_config_path, encoding="utf-8") as f:
        original = f.readlines()
    filtered, removed = _filter_generated_config_lines(
        resource_type, set(expected_addresses), original, entries, policy)
    if removed:
        with open(generated_config_path, "w", encoding="utf-8") as f:
            f.writelines(filtered)
    return removed


def _filter_generated_config_lines(
        resource_type, expected_addresses, lines, entries, policy):
    out = []
    stack = []
    heredoc_end = None
    value_depth = 0
    removed = 0
    for line in lines:
        stripped = line.strip()
        if heredoc_end is not None:
            out.append(line)
            if stripped == heredoc_end:
                heredoc_end = None
            continue
        if value_depth:
            out.append(line)
            value_depth += _hcl_value_depth_delta(stripped)
            if value_depth <= 0:
                value_depth = 0
            continue

        resource_match = re.match(
            r'^resource\s+"([^"]+)"\s+"([^"]+)"\s*\{\s*$', stripped)
        if resource_match:
            rtype, name = resource_match.groups()
            address = "%s.%s" % (rtype, name)
            if rtype != resource_type or address not in expected_addresses:
                raise OracleError(
                    "%s generated import config contained unexpected "
                    "resource block %s" % (resource_type, address)
                )
            stack.append({"kind": "resource", "path": (), "counts": {}})
            out.append(line)
            continue

        if stripped == "}":
            if stack:
                stack.pop()
            out.append(line)
            continue

        block_match = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\{\s*$', stripped)
        if block_match and stack:
            name = block_match.group(1)
            parent = stack[-1]
            index = parent["counts"].get(name, 0)
            parent["counts"][name] = index + 1
            stack.append({
                "kind": "block",
                "path": parent["path"] + (name, index),
                "counts": {},
            })
            out.append(line)
            continue

        if stack and stack[0]["kind"] == "resource":
            attr_match = re.match(
                r'^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$', stripped)
            if attr_match:
                attr, raw_value = attr_match.groups()
                actual_path = stack[-1]["path"] + (attr,)
                value_known, value = _parse_generated_hcl_scalar(raw_value)
                matched_entry = _generated_config_match(
                    resource_type, actual_path, value_known, value, entries)
                if matched_entry is not None:
                    policy.mark_matched(matched_entry)
                    removed += 1
                    continue
                heredoc_end = _hcl_heredoc_end(raw_value)
                if heredoc_end is None:
                    value_depth = max(0, _hcl_value_depth_delta(raw_value))

        out.append(line)
    return out, removed


def _parse_generated_hcl_scalar(raw):
    text = raw.strip()
    if text.startswith('"'):
        try:
            value, end = parse_hcl_string_literal(text, 0)
        except ValueError:
            return False, None
        if text[end:].strip():
            return False, None
        return True, value
    if text == "true":
        return True, True
    if text == "false":
        return True, False
    if text == "null":
        return True, None
    if re.match(r'^-?(0|[1-9][0-9]*)(\.[0-9]+)?$', text):
        return True, float(text) if "." in text else int(text)
    return False, None


def _hcl_heredoc_end(raw):
    match = re.match(r'^<<-?\s*([A-Za-z_][A-Za-z0-9_]*)$', raw.strip())
    return match.group(1) if match else None


def _hcl_value_depth_delta(text):
    depth = 0
    in_string = False
    escaped = False
    index = 0
    while index < len(text):
        char = text[index]
        if escaped:
            escaped = False
            index += 1
            continue
        if in_string:
            if char == "\\":
                escaped = True
            elif char == '"':
                in_string = False
            index += 1
            continue
        if char == '"':
            in_string = True
        elif char == "#":
            break
        elif char == "/" and index + 1 < len(text) and text[index + 1] == "/":
            break
        elif char in "{[(":
            depth += 1
        elif char in "}])":
            depth -= 1
        index += 1
    return depth


def _generated_config_match(resource_type, actual_path, value_known, value,
                            entries):
    if not value_known:
        return None
    for mode, entry, selector in entries:
        if not paths.selector_matches(selector, actual_path):
            continue
        status = schema_paths.schema_status(resource_type, selector)
        if status != "optional":
            raise OracleError(
                "%s generated import config policy matched non-optional "
                "path %s (schema status %s)"
                % (resource_type, entry["path"], status)
            )
        if mode == "projection_omit":
            return entry
        if value_known and any(
                _same_json_value(value, candidate)
                for candidate in entry["values"]):
            return entry
    return None


def _plan_imports_with_generated_config(
        tf, temp, env, resource_type, plan_path, generated_config_path,
        expected_addresses, policy, debug_dir, import_ids):
    entries = _generated_config_policy_entries(resource_type, policy)
    generate_args = [
        tf, "plan", "-input=false", "-no-color", "-lock=false",
        "-generate-config-out=%s" % generated_config_path,
        "-out=%s" % plan_path,
    ]
    proc = _run_process(
        generate_args,
        cwd=temp,
        env=env,
        debug_dir=debug_dir,
        debug_name="plan-generate-config",
        sensitive_tokens=import_ids,
    )
    removed = _apply_generated_config_policy(
        resource_type, expected_addresses, generated_config_path, policy,
        entries=entries)
    if proc.returncode != 0 and not removed:
        _raise_run_error(
            generate_args,
            proc,
            debug_dir=debug_dir,
            debug_name="plan-generate-config",
            sensitive_tokens=import_ids,
        )
    if proc.returncode == 0 and not removed:
        return
    _run(
        [
            tf, "plan", "-input=false", "-no-color", "-lock=false",
            "-out=%s" % plan_path,
        ],
        cwd=temp,
        env=env,
        debug_dir=debug_dir,
        debug_name="plan-imports",
        sensitive_tokens=import_ids,
    )


def import_state(resource_type, key_to_import_id, keep_workdir=False,
                 policy=None):
    """Return {key: {"address": ..., "values": ..., "sensitive_values": ...}}."""
    if not key_to_import_id:
        return {}

    seen = {}
    for key, import_id in sorted(key_to_import_id.items()):
        if import_id in seen:
            raise OracleError(
                "%s duplicate import_id for keys %r and %r"
                % (resource_type, seen[import_id], key)
            )
        seen[import_id] = key

    _check_instance_name_collisions(resource_type, key_to_import_id)

    keep = bool(keep_workdir) or _env_truthy("INFRAWRIGHT_KEEP_ORACLE")
    temp = tempfile.mkdtemp(prefix="infrawright-oracle-")
    try:
        root = render_root(resource_type, key_to_import_id)
        imports = render_import_blocks(resource_type, key_to_import_id)
        _assert_local_scratch_root(root)
        with open(os.path.join(temp, "main.tf"), "w", encoding="utf-8") as f:
            f.write(root)
        with open(os.path.join(temp, "imports.tf"), "w", encoding="utf-8") as f:
            f.write(imports)
        env = os.environ.copy()
        env["TF_DATA_DIR"] = os.path.join(temp, ".terraform")
        tf = _terraform()
        debug_dir = temp if keep else None
        import_ids = sorted(str(value) for value in key_to_import_id.values())
        _run(
            [tf, "init", "-input=false", "-no-color"],
            cwd=temp,
            env=env,
            debug_dir=debug_dir,
            debug_name="init",
        )
        address_to_key = {}
        for key, import_id in sorted(key_to_import_id.items()):
            addr = _address(resource_type, key)
            address_to_key[addr] = key
        plan_path = os.path.join(temp, "oracle.tfplan")
        generated_config_path = os.path.join(temp, "generated.tf")
        _plan_imports_with_generated_config(
            tf,
            temp,
            env,
            resource_type,
            plan_path,
            generated_config_path,
            set(address_to_key),
            policy,
            debug_dir,
            import_ids,
        )
        raw_plan = _run(
            [tf, "show", "-json", plan_path],
            cwd=temp,
            env=env,
            debug_dir=debug_dir,
            debug_name="show-plan",
            sensitive_tokens=import_ids,
        )
        try:
            plan = json.loads(raw_plan)
        except ValueError as exc:
            raise OracleError(
                "%s terraform show -json plan returned invalid JSON: %s"
                % (resource_type, exc)
            )
        _assert_import_plan_only(resource_type, plan, set(address_to_key))
        _run(
            [
                tf, "apply", "-input=false", "-no-color", "-lock=false",
                plan_path,
            ],
            cwd=temp,
            env=env,
            debug_dir=debug_dir,
            debug_name="apply-imports",
            sensitive_tokens=import_ids,
        )
        raw = _run(
            [tf, "show", "-json", "terraform.tfstate"],
            cwd=temp,
            env=env,
            debug_dir=debug_dir,
            debug_name="show",
            sensitive_tokens=import_ids,
        )
        try:
            state = json.loads(raw)
        except ValueError as exc:
            raise OracleError(
                "%s terraform show -json returned invalid JSON: %s"
                % (resource_type, exc)
            )
        out = {}
        for res in _iter_state_resources(state):
            if res.get("type") != resource_type:
                continue
            key = address_to_key.get(res.get("address"))
            if key is None:
                continue
            out[key] = {
                "address": res.get("address"),
                "values": res.get("values") or {},
                "sensitive_values": res.get("sensitive_values") or {},
            }
        missing = sorted(set(key_to_import_id) - set(out))
        if missing:
            raise OracleError(
                "%s import oracle did not return state for key(s): %s"
                % (resource_type, ", ".join(missing))
            )
        return out
    except BaseException:
        primary_error = sys.exc_info()[1]
        raise
    finally:
        if keep:
            sys.stderr.write(
                "WARNING: kept oracle workdir %s; it may contain "
                "unencrypted provider state, generated configuration, "
                "credentials, import IDs, and provider diagnostics. Remove it "
                "when debugging is complete.\n"
                % temp
            )
        else:
            try:
                shutil.rmtree(temp)
            except OSError as exc:
                if "primary_error" in locals():
                    sys.stderr.write(
                        "WARNING: failed to remove oracle workdir %s "
                        "after error %s: %s\n"
                        % (temp, primary_error, exc)
                    )
                else:
                    raise OracleError(
                        "failed to remove oracle workdir %s: %s" % (temp, exc)
                    )


def _iter_state_resources(state):
    values = state.get("values") or {}
    root = values.get("root_module") or {}
    stack = [root]
    while stack:
        mod = stack.pop()
        for res in mod.get("resources") or []:
            yield res
        stack.extend(mod.get("child_modules") or [])
