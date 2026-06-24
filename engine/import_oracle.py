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

from engine import ops
from engine import packs


class OracleError(RuntimeError):
    pass


_MAX_SUBPROCESS_OUTPUT = 1200
_DEFAULT_SUBPROCESS_TIMEOUT_SECONDS = 300
_BACKEND_BLOCK_RE = re.compile(r'\bbackend\s+"[^"]+"\s*\{')
_CLOUD_BLOCK_RE = re.compile(r'\bcloud\s*\{')


def _instance_name(key):
    digest = hashlib.sha1(key.encode("utf-8")).hexdigest()[:16]
    return "iw_%s" % digest


def _address(resource_type, key):
    return "%s.%s" % (resource_type, _instance_name(key))


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
    for key in sorted(keys or []):
        text += '\nresource "%s" "%s" {}\n' % (resource_type, _instance_name(key))
    return text


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


def _sensitive_command_tokens(args):
    if len(args) >= 2 and args[1] == "import" and len(args) >= 3:
        return [str(args[-1])]
    return []


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


def _summarize_text(text, tokens):
    return _summarize_output(text.encode("utf-8", "replace"), tokens)


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


def _run(args, cwd, env, debug_dir=None, debug_name=None):
    try:
        proc = subprocess.run(
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
        tokens = _sensitive_command_tokens(args)
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
    if proc.returncode != 0:
        stdout_path, stderr_path = _write_debug_output(
            debug_dir, debug_name, proc.stdout, proc.stderr)
        debug_hint = ""
        if stdout_path or stderr_path:
            debug_hint = (
                "\nfull stdout: %s\nfull stderr: %s"
                % (stdout_path, stderr_path)
            )
        tokens = _sensitive_command_tokens(args)
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
    return proc.stdout.decode("utf-8", "replace")


def import_state(resource_type, key_to_import_id, keep_workdir=False):
    """Return {key: {"address": ..., "values": ..., "sensitive_values": ...}}."""
    if not key_to_import_id:
        return {}

    seen = {}
    for key, import_id in sorted(key_to_import_id.items()):
        if import_id in seen:
            raise OracleError(
                "%s import_id %r is used by both %r and %r"
                % (resource_type, import_id, seen[import_id], key)
            )
        seen[import_id] = key

    _check_instance_name_collisions(resource_type, key_to_import_id)

    keep = keep_workdir or os.environ.get("INFRAWRIGHT_KEEP_ORACLE")
    temp = tempfile.mkdtemp(prefix="infrawright-oracle-")
    try:
        root = render_root(resource_type, key_to_import_id)
        _assert_local_scratch_root(root)
        with open(os.path.join(temp, "main.tf"), "w", encoding="utf-8") as f:
            f.write(root)
        env = os.environ.copy()
        env["TF_DATA_DIR"] = os.path.join(temp, ".terraform")
        tf = ops.terraform()
        debug_dir = temp if keep else None
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
            _run(
                [
                    tf, "import", "-input=false", "-no-color", "-lock=false",
                    addr, import_id,
                ],
                cwd=temp,
                env=env,
                debug_dir=debug_dir,
                debug_name="import-%s" % _instance_name(key),
            )
        raw = _run(
            [tf, "show", "-json", "terraform.tfstate"],
            cwd=temp,
            env=env,
            debug_dir=debug_dir,
            debug_name="show",
        )
        try:
            state = json.loads(raw)
        except ValueError as exc:
            raise OracleError(
                "%s terraform show -json returned invalid JSON: %s\noutput:\n%s"
                % (resource_type, exc, _summarize_text(raw, []))
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
                "unencrypted provider state, credentials, import IDs, and "
                "provider diagnostics. Remove it when debugging is complete.\n"
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
