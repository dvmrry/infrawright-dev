"""Generate group-local Terraform expression bindings from lookup sidecars."""
import json
import os
import sys

from engine import artifacts
from engine import deployment
from engine import lookup
from engine import packs
from engine import transform
from engine.registry import derived_types
from engine.registry import generated_types


REASON_MISSING_LOOKUP = "missing_lookup"
REASON_ID_ABSENT = "id_absent"
REASON_KEY_MAP_UNAVAILABLE = "key_map_unavailable"
REASON_UNSAFE_KEY = "unsafe_key"
REASON_UNBINDABLE_LIST = "unbindable_list"
REASON_SELF_REFERENCE = "self_reference"
REASON_NESTED_FIELD_UNSUPPORTED = "nested_field_unsupported"


def _empty():
    return {"resources": {}}


def _note(message):
    sys.stderr.write("NOTE bindings: %s\n" % message)


def _roots_binding_enabled(resource_type):
    roots = deployment.roots_config()
    if not roots:
        return False
    provider = packs.provider_of(resource_type)
    return bool((roots.get(provider) or {}).get("bind_references", False))


def bindings_enabled(resource_type):
    return _roots_binding_enabled(resource_type)


def _same_group(resource_type, referent):
    if resource_type == referent:
        return False
    generated = set(generated_types())
    derived = set(derived_types())
    if resource_type in derived or referent in derived:
        return False
    if resource_type not in generated or referent not in generated:
        return False
    return artifacts.root_label(resource_type) == artifacts.root_label(referent)


def _candidate_fields(resource_type):
    fields, _skipped_fields = _candidate_field_decisions(resource_type)
    return fields


def _candidate_field_decisions(resource_type):
    if not _roots_binding_enabled(resource_type):
        return {}, {}
    refs = lookup.reference_manifest().get(resource_type) or {}
    out = {}
    skipped = {}
    for field, spec in sorted(refs.items()):
        if "." in field:
            skipped[field] = (
                REASON_NESTED_FIELD_UNSUPPORTED,
                "nested reference fields are unsupported",
            )
            continue
        referent = spec.get("referent")
        if resource_type == referent:
            skipped[field] = (
                REASON_SELF_REFERENCE,
                "self-referential bindings would create a Terraform cycle",
            )
            continue
        if referent and _same_group(resource_type, referent):
            out[field] = spec
    return out, skipped


def _field_values(items, field):
    values = []
    for key in sorted(items):
        item = items[key]
        if not isinstance(item, dict) or field not in item:
            continue
        value = item.get(field)
        if value is None:
            continue
        if isinstance(value, list):
            for idx, child in enumerate(value):
                if child is not None:
                    values.append((key, "%s[%d]" % (field, idx), child))
        else:
            values.append((key, field, value))
    return values


def _record_skip(resource_type, key, path, value, referent, reason_counts,
                 reason, detail):
    reason_counts[reason] = reason_counts.get(reason, 0) + 1
    _note(
        "%s.%s.%s value %r skipped; %s"
        % (resource_type, key, path, str(value), detail)
    )


def _resolve_expr(resource_type, key, path, value, referent, key_mapping,
                  reason_counts):
    ident = str(value)
    if ident not in key_mapping:
        _record_skip(
            resource_type, key, path, value, referent, reason_counts,
            REASON_ID_ABSENT,
            "id is absent from %s lookup" % referent,
        )
        return None
    referent_key = key_mapping[ident]
    if "${" in referent_key or "%{" in referent_key:
        _record_skip(
            resource_type, key, path, value, referent, reason_counts,
            REASON_UNSAFE_KEY,
            "referent key %r contains a template interpolation" % referent_key,
        )
        return None
    return "module.%s.items[%s].id" % (
        referent,
        transform.hcl_string_literal(referent_key),
    )


def _literal_expr(value):
    return transform.hcl_string_literal(str(value))


def _summary(resource_type, bound, skipped, reason_counts):
    if reason_counts:
        reasons = ", ".join(
            "%s=%d" % (reason, reason_counts[reason])
            for reason in sorted(reason_counts)
        )
        _note("%s: %d bound, %d skipped (%s)"
              % (resource_type, bound, skipped, reasons))
    else:
        _note("%s: %d bound, %d skipped" % (resource_type, bound, skipped))


def derive(resource_type, items, tenant, config_root=None):
    """Return expression-bindings JSON for same-root reference fields."""
    fields, skipped_fields = _candidate_field_decisions(resource_type)
    resources = {}
    bound = 0
    skipped = 0
    reason_counts = {}
    for field, (reason, detail) in sorted(skipped_fields.items()):
        candidates = _field_values(items, field)
        if not candidates:
            continue
        reason_counts[reason] = reason_counts.get(reason, 0) + len(candidates)
        skipped += len(candidates)
        _note("%s.%s skipped; %s" % (resource_type, field, detail))
    if not fields:
        if skipped:
            _summary(resource_type, bound, skipped, reason_counts)
        return _empty()
    for field, spec in sorted(fields.items()):
        referent = spec["referent"]
        candidates = _field_values(items, field)
        if not candidates:
            continue
        lookup_path = lookup.lookup_path(tenant, referent, config_root=config_root)
        if not os.path.exists(lookup_path):
            reason_counts[REASON_MISSING_LOOKUP] = (
                reason_counts.get(REASON_MISSING_LOOKUP, 0) + len(candidates)
            )
            skipped += len(candidates)
            _note(
                "%s.%s skipped; lookup for %s is missing at %s"
                % (resource_type, field, referent, lookup_path)
            )
            continue
        key_mapping = lookup.load_lookup_keys(
            tenant, referent, config_root=config_root)
        if not key_mapping:
            reason_counts[REASON_KEY_MAP_UNAVAILABLE] = (
                reason_counts.get(REASON_KEY_MAP_UNAVAILABLE, 0)
                + len(candidates)
            )
            skipped += len(candidates)
            _note(
                "%s.%s skipped; lookup for %s has no key_by_id map"
                % (resource_type, field, referent)
            )
            continue
        by_item = {}
        for key, path, value in candidates:
            by_item.setdefault(key, []).append((path, value))
        for key in sorted(by_item):
            item = items.get(key)
            if not isinstance(item, dict):
                continue
            value = item.get(field)
            if isinstance(value, list):
                if not all(isinstance(child, str) and child for child in value):
                    # A list mixing ids with null/non-string cannot be re-emitted
                    # as a faithful HCL list (unbound siblings would be type-
                    # coerced), so leave the raw tfvars value untouched.
                    reason_counts[REASON_UNBINDABLE_LIST] = (
                        reason_counts.get(REASON_UNBINDABLE_LIST, 0) + 1)
                    skipped += 1
                    _note(
                        "%s.%s.%s skipped; list has null or non-string elements"
                        % (resource_type, key, field))
                    continue
                fragments = []
                bound_any = False
                for idx, child in enumerate(value):
                    child_path = "%s[%d]" % (field, idx)
                    expr = _resolve_expr(
                        resource_type, key, child_path, child, referent,
                        key_mapping, reason_counts)
                    if expr is None:
                        skipped += 1
                        fragments.append(_literal_expr(child))
                    else:
                        bound += 1
                        bound_any = True
                        fragments.append(expr)
                if not bound_any:
                    continue
                binding_expr = "[%s]" % ", ".join(fragments)
            else:
                expr = _resolve_expr(
                    resource_type, key, field, value, referent,
                    key_mapping, reason_counts)
                if expr is None:
                    skipped += 1
                    continue
                bound += 1
                binding_expr = expr
            address = "%s.%s" % (resource_type, key)
            resources.setdefault(address, {})[field] = {
                "expression": binding_expr,
                "reason": "group-local reference binding via %s.items"
                % referent,
            }
    _summary(resource_type, bound, skipped, reason_counts)
    return {"resources": resources}


def _generated_path(tenant, resource_type, config_root=None):
    if config_root is None:
        return artifacts.generated_expression_bindings_file(tenant, resource_type)
    return os.path.join(
        config_root,
        tenant,
        resource_type + artifacts.GENERATED_EXPRESSION_BINDINGS_SUFFIX,
    )


def render(data):
    return json.dumps(data, indent=2, sort_keys=True) + "\n"


def write_generated(resource_type, items, tenant, config_root=None):
    path = _generated_path(tenant, resource_type, config_root=config_root)
    data = derive(resource_type, items, tenant, config_root=config_root)
    if data.get("resources"):
        directory = os.path.dirname(path)
        if directory:
            os.makedirs(directory, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(render(data))
        sys.stderr.write("wrote %s\n" % path)
        return path
    if os.path.exists(path):
        os.remove(path)
        sys.stderr.write("removed stale %s\n" % path)
    return None
