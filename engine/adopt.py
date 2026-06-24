"""Adopt raw API pulls using Terraform/OpenTofu import state as field oracle."""
import json
import os
import sys

from engine import deployment
from engine import lookup
from engine import ops
from engine import transform
from engine.adoption_meta import (
    adoption_entry,
    derive_import_id_from_identity,
    derive_key_from_identity,
    identity_item,
    skip_identity_item,
)
from engine.drift_policy import DriftPolicy
from engine.import_oracle import import_state
from engine.registry import derive_entry
from engine.state_project import project_item


def adopt_items(raw_items, resource_type, policy=None):
    meta = adoption_entry(resource_type)
    key_to_identity = {}
    key_to_import_id = {}
    import_id_to_key = {}
    for raw in raw_items:
        ident = identity_item(raw, resource_type)
        if skip_identity_item(ident, meta):
            sys.stderr.write(
                "skipped %s item %r (identity skip_if matched)\n"
                % (resource_type, ident.get("name") or ident.get("id"))
            )
            continue
        key = derive_key_from_identity(ident, meta)
        if key in key_to_identity:
            raise ValueError("duplicate derived key %r for %s" % (key, resource_type))
        import_id = derive_import_id_from_identity(ident, meta, resource_type, key)
        if import_id in import_id_to_key:
            raise ValueError(
                "%s duplicate import_id for keys %r and %r"
                % (resource_type, import_id_to_key[import_id], key)
            )
        import_id_to_key[import_id] = key
        key_to_identity[key] = ident
        key_to_import_id[key] = import_id

    if not key_to_import_id:
        return {}, key_to_identity

    oracle = import_state(resource_type, key_to_import_id)
    items = {}
    for key in sorted(oracle):
        state_obj = oracle[key]
        items[key] = project_item(
            resource_type,
            state_obj["values"],
            sensitive_values=state_obj.get("sensitive_values"),
            policy=policy,
        )
    return items, key_to_identity


def write_outputs(resource_type, raw_items, tenant, policy):
    config_dir = deployment.config_dir(tenant)
    imports_dir = deployment.imports_dir(tenant)
    os.makedirs(config_dir, exist_ok=True)
    os.makedirs(imports_dir, exist_ok=True)

    items, originals = adopt_items(raw_items, resource_type, policy=policy)
    if resource_type in lookup.lookup_sources():
        lookup_path = lookup.write_lookup(
            tenant, resource_type, [transform.snake_keys(raw) for raw in raw_items]
        )
        sys.stderr.write("wrote %s\n" % lookup_path)

    tfvars_path = ops.config_file(tenant, resource_type)
    imports_path = ops.imports_file(tenant, resource_type)
    moves_path = ops.moves_file(tenant, resource_type)
    override = {"import_id": adoption_entry(resource_type)["import_id"]}
    new_imports = transform.render_imports(resource_type, originals, override)
    moves = []
    if os.path.exists(imports_path):
        with open(imports_path, encoding="utf-8") as f:
            moves = transform.derive_moves(f.read(), new_imports)
    if moves:
        with open(moves_path, "w", encoding="utf-8") as f:
            f.write(transform.render_moves(resource_type, moves))
        sys.stderr.write("RENAME(S) DETECTED: wrote %s\n" % moves_path)
    elif os.path.exists(moves_path):
        os.remove(moves_path)
        sys.stderr.write("removed stale %s\n" % moves_path)

    with open(tfvars_path, "w", encoding="utf-8") as f:
        f.write(transform.render_tfvars(items))
    with open(imports_path, "w", encoding="utf-8") as f:
        f.write(new_imports)
    sys.stderr.write("wrote %s\nwrote %s\n" % (tfvars_path, imports_path))


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    policy_path = None
    if "--policy" in argv:
        idx = argv.index("--policy")
        try:
            policy_path = argv[idx + 1]
        except IndexError:
            sys.stderr.write("error: --policy requires a value\n")
            return 2
        del argv[idx:idx + 2]
    if len(argv) != 3:
        sys.stderr.write(
            "usage: python -m engine.adopt <resource_type> <input.json> <tenant> "
            "[--policy <file>]\n"
        )
        return 2
    resource_type, input_path, tenant = argv
    policy = DriftPolicy.load(policy_path)
    try:
        with open(input_path, encoding="utf-8") as f:
            raw_items = json.load(f)
    except ValueError as exc:
        sys.stderr.write("error: failed to parse %s: %s\n" % (input_path, exc))
        return 1
    if not isinstance(raw_items, list):
        sys.stderr.write("error: %s must be a JSON LIST of items\n" % input_path)
        return 2

    derive = derive_entry(resource_type)
    if derive is not None:
        sys.stderr.write(
            "NOTE: %s is derived from %s; using legacy derived transform path\n"
            % (resource_type, derive["from"])
        )
        return transform.main([resource_type, input_path, tenant])
    try:
        write_outputs(resource_type, raw_items, tenant, policy)
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
