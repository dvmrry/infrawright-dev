"""Terraform/OpenTofu import/read oracle.

Imports remote objects into an ephemeral local state and returns the provider
state values Terraform/OpenTofu reports via ``show -json``.
"""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile

from engine import ops
from engine import packs


class OracleError(RuntimeError):
    pass


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


def _run(args, cwd, env):
    proc = subprocess.run(
        args,
        cwd=cwd,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if proc.returncode != 0:
        raise OracleError(
            "%s failed with exit %d\nstdout:\n%s\nstderr:\n%s"
            % (
                " ".join(args),
                proc.returncode,
                proc.stdout.decode("utf-8", "replace"),
                proc.stderr.decode("utf-8", "replace"),
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

    temp = tempfile.mkdtemp(prefix="infrawright-oracle-")
    try:
        with open(os.path.join(temp, "main.tf"), "w", encoding="utf-8") as f:
            f.write(render_root(resource_type, key_to_import_id))
        env = os.environ.copy()
        env["TF_DATA_DIR"] = os.path.join(temp, ".terraform")
        tf = ops.terraform()
        _run([tf, "init", "-input=false", "-no-color"], cwd=temp, env=env)
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
            )
        raw = _run([tf, "show", "-json", "terraform.tfstate"], cwd=temp, env=env)
        state = json.loads(raw)
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
    finally:
        if keep_workdir or os.environ.get("INFRAWRIGHT_KEEP_ORACLE"):
            sys.stderr.write("kept oracle workdir %s\n" % temp)
        else:
            shutil.rmtree(temp)


def _iter_state_resources(state):
    values = state.get("values") or {}
    root = values.get("root_module") or {}
    stack = [root]
    while stack:
        mod = stack.pop()
        for res in mod.get("resources") or []:
            yield res
        stack.extend(mod.get("child_modules") or [])
