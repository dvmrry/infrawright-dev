"""Artifact layout contract for tenant roots.

The artifact layout is flat by Terraform resource type:
  [overlay/]config/<tenant>/<resource_type>.auto.tfvars[.json]
  [overlay/]imports/<tenant>/<resource_type>_imports.tf
  [overlay/]envs/<tenant>/<root_label>/

Provider packs own behavior and metadata; they do not create path segments.
Single home for tenant/resource label validation and artifact path helpers;
ops (CLI), transform, adopt, gen_env, and lookup all consume this module.
"""
import os
import re

from engine import deployment
from engine import packs
from engine.registry import derived_types, generated_types, load_registry

CONFIG_SUFFIX = ".auto.tfvars.json"
HCL_CONFIG_SUFFIX = ".auto.tfvars"
EXPRESSION_BINDINGS_SUFFIX = ".expressions.json"
GENERATED_EXPRESSION_BINDINGS_SUFFIX = ".generated.expressions.json"
IMPORTS_SUFFIX = "_imports.tf"
MOVES_SUFFIX = "_moves.tf"
PENDING_MOVES_SUFFIX = "_moves.pending.json"
VALID_TENANT = re.compile(r"^[A-Za-z0-9_.-]+$")
VALID_ROOT_LABEL = re.compile(r"^[a-z0-9_]+$")


def validate_tenant(tenant):
    if not VALID_TENANT.match(tenant or "") or tenant in (".", ".."):
        raise ValueError(
            "TENANT must match [A-Za-z0-9_.-]+ and not be . or .. (got %r)"
            % tenant
        )


def validate_resource_type(resource_type):
    if resource_type not in set(generated_types()):
        raise ValueError(
            "RESOURCE must be an exact generated resource type (got %r)"
            % resource_type
        )


def expand_resources(selectors=None):
    """Expand exact resource, provider/product, or provider/bare selectors."""
    selectors = selectors or []
    generated = set(generated_types())
    registry = load_registry()
    if not selectors:
        return sorted(generated)

    selected = set()
    unknown = []
    for selector in selectors:
        if selector in generated:
            selected.add(selector)
            continue
        if selector in registry:
            unknown.append(selector)
            continue

        product_matches = sorted(
            rt for rt in generated if registry.get(rt, {}).get("product") == selector
        )
        if product_matches:
            selected.update(product_matches)
            continue

        if "/" in selector:
            provider, bare = selector.split("/", 1)
            path_matches = sorted(
                rt for rt in generated
                if packs.provider_of(rt) == provider and packs.bare_name(rt) == bare
            )
            if path_matches:
                selected.update(path_matches)
                continue

        unknown.append(selector)

    if unknown:
        raise ValueError(
            "unknown or non-generated resource selector(s): %s"
            % ", ".join(sorted(unknown))
        )
    return sorted(selected)


def _provider_prefix(resource_type, provider):
    prefixes = packs.provider_prefixes()
    for prefix in sorted(prefixes, key=len, reverse=True):
        if resource_type.startswith(prefix) and prefixes[prefix] == provider:
            return prefix
    raise ValueError(
        "resource type %s has no declared prefix for provider %s"
        % (resource_type, provider)
    )


def _slug_label(resource_type, provider):
    prefix = _provider_prefix(resource_type, provider)
    rest = resource_type[len(prefix):]
    return prefix + rest.split("_")[0]


def _validate_group_label(label, generated, used_labels, provider):
    if not VALID_ROOT_LABEL.match(label or ""):
        raise ValueError("roots.%s group label %r must match [a-z0-9_]+"
                         % (provider, label))
    if label in generated:
        raise ValueError(
            "roots.%s group label %r collides with a generated resource type"
            % (provider, label)
        )
    if label in used_labels:
        raise ValueError(
            "roots.%s group label %r collides with another provider group"
            % (provider, label)
        )
    used_labels.add(label)


def _validate_member(provider, resource_type, generated):
    if resource_type in set(derived_types()):
        raise ValueError(
            "roots.%s member %s is a derived type; derived types keep "
            "per-resource roots so IMPORTS_ONLY sequencing works"
            % (provider, resource_type)
        )
    if resource_type not in generated:
        raise ValueError(
            "roots.%s references unknown generated resource type %r"
            % (provider, resource_type)
        )
    actual = packs.provider_of(resource_type)
    if actual != provider:
        raise ValueError(
            "roots.%s member %r belongs to provider %s"
            % (provider, resource_type, actual)
        )


def _root_resolution():
    roots = deployment.roots_config()
    registry = load_registry()
    generated = set(generated_types())
    slug_grouped = set(
        resource_type for resource_type in generated
        if registry.get(resource_type, {}).get("slug_group", True)
    )
    labels_to_members = dict((rt, [rt]) for rt in sorted(generated))
    type_to_label = dict((rt, rt) for rt in sorted(generated))
    if not roots:
        return labels_to_members, type_to_label

    known_providers = set(packs.provider_prefixes().values())
    used_group_labels = set()
    explicit_members = {}
    for provider in sorted(roots):
        if provider not in known_providers:
            raise ValueError(
                "roots.%s is not a declared provider prefix value" % provider
            )
        cfg = roots[provider]
        groups = cfg.get("groups") or {}
        for label in sorted(groups):
            _validate_group_label(label, generated, used_group_labels, provider)
            members = sorted(groups[label])
            for member in members:
                _validate_member(provider, member, generated)
                if member in explicit_members:
                    raise ValueError(
                        "%s appears in more than one roots group (%s and %s)"
                        % (member, explicit_members[member], label)
                    )
                explicit_members[member] = label
            for member in members:
                labels_to_members.pop(member, None)
                type_to_label[member] = label
            labels_to_members[label] = members

    for provider in sorted(roots):
        cfg = roots[provider]
        if cfg.get("strategy", "explicit") != "slug":
            continue
        slug_groups = {}
        derived = set(derived_types())
        for resource_type in sorted(generated):
            if resource_type in derived:
                continue
            if resource_type not in slug_grouped:
                continue
            if type_to_label[resource_type] != resource_type:
                continue
            if packs.provider_of(resource_type) != provider:
                continue
            label = _slug_label(resource_type, provider)
            slug_groups.setdefault(label, []).append(resource_type)
        for label in sorted(slug_groups):
            members = sorted(slug_groups[label])
            if len(members) < 2:
                continue
            _validate_group_label(label, generated, used_group_labels, provider)
            for member in members:
                labels_to_members.pop(member, None)
                type_to_label[member] = label
            labels_to_members[label] = members

    return labels_to_members, type_to_label


def root_label(resource_type):
    if not deployment.roots_config():
        return resource_type
    labels_to_members, type_to_label = _root_resolution()
    if resource_type not in type_to_label:
        validate_resource_type(resource_type)
    return type_to_label[resource_type]


def root_members(label):
    if not deployment.roots_config():
        return [label]
    labels_to_members, type_to_label = _root_resolution()
    if label in labels_to_members:
        return list(labels_to_members[label])
    if label in type_to_label:
        return [label]
    raise ValueError("unknown env root label %r" % label)


def all_root_labels():
    labels_to_members, _type_to_label = _root_resolution()
    return sorted(labels_to_members)


def tfvars_var_name(resource_type):
    if root_label(resource_type) == resource_type:
        return "items"
    return "%s_items" % resource_type


def config_suffix():
    if deployment.tfvars_format() == "hcl":
        return HCL_CONFIG_SUFFIX
    return CONFIG_SUFFIX


def config_file(tenant, resource_type):
    return os.path.join(
        deployment.config_dir(tenant), resource_type + config_suffix()
    )


def expression_bindings_file(tenant, resource_type):
    return os.path.join(
        deployment.config_dir(tenant), resource_type + EXPRESSION_BINDINGS_SUFFIX
    )


def generated_expression_bindings_file(tenant, resource_type):
    return os.path.join(
        deployment.config_dir(tenant),
        resource_type + GENERATED_EXPRESSION_BINDINGS_SUFFIX,
    )


def imports_file(tenant, resource_type):
    return os.path.join(
        deployment.imports_dir(tenant), resource_type + IMPORTS_SUFFIX
    )


def moves_file(tenant, resource_type):
    return os.path.join(
        deployment.imports_dir(tenant), resource_type + MOVES_SUFFIX
    )


def pending_moves_file(tenant, resource_type):
    return os.path.join(
        deployment.imports_dir(tenant), resource_type + PENDING_MOVES_SUFFIX
    )


def assert_no_pending_moves(tenant, resource_type):
    """Refuse artifact writers while a Node move transition is in flight."""
    if os.path.lexists(pending_moves_file(tenant, resource_type)):
        raise RuntimeError(
            "pending move transition for %s must be applied and acknowledged "
            "before transform or adopt can run" % resource_type
        )


def env_root(tenant, resource_type):
    return env_root_for_label(tenant, root_label(resource_type))


def env_root_for_label(tenant, label):
    return os.path.join(deployment.envs_dir(tenant), label)


def tenant_env_dir(tenant, out_root=None):
    if out_root is None:
        return deployment.envs_dir(tenant)
    return os.path.join(out_root, tenant)


def env_root_under(tenant, resource_type, out_root=None):
    if out_root is None:
        return env_root(tenant, resource_type)
    return os.path.join(out_root, tenant, root_label(resource_type))
