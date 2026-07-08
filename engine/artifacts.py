"""Artifact layout contract for tenant roots.

The artifact layout is flat by Terraform resource type:
  [overlay/]config/<tenant>/<resource_type>.auto.tfvars[.json]
  [overlay/]imports/<tenant>/<resource_type>_imports.tf
  [overlay/]envs/<tenant>/<resource_type>/

Provider packs own behavior and metadata; they do not create path segments.
Single home for tenant/resource label validation and artifact path helpers;
ops (CLI), transform, adopt, gen_env, and lookup all consume this module.
"""
import os
import re

from engine import deployment
from engine import packs
from engine.registry import generated_types, load_registry

CONFIG_SUFFIX = ".auto.tfvars.json"
EXPRESSION_BINDINGS_SUFFIX = ".expressions.json"
IMPORTS_SUFFIX = "_imports.tf"
MOVES_SUFFIX = "_moves.tf"
VALID_TENANT = re.compile(r"^[A-Za-z0-9_.-]+$")


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


def config_suffix():
    if deployment.tfvars_format() == "hcl":
        return ".auto.tfvars"
    return CONFIG_SUFFIX


def config_file(tenant, resource_type):
    return os.path.join(
        deployment.config_dir(tenant), resource_type + config_suffix()
    )


def expression_bindings_file(tenant, resource_type):
    return os.path.join(
        deployment.config_dir(tenant), resource_type + EXPRESSION_BINDINGS_SUFFIX
    )


def imports_file(tenant, resource_type):
    return os.path.join(
        deployment.imports_dir(tenant), resource_type + IMPORTS_SUFFIX
    )


def moves_file(tenant, resource_type):
    return os.path.join(
        deployment.imports_dir(tenant), resource_type + MOVES_SUFFIX
    )


def env_root(tenant, resource_type):
    return os.path.join(deployment.envs_dir(tenant), resource_type)


def tenant_env_dir(tenant, out_root=None):
    if out_root is None:
        return deployment.envs_dir(tenant)
    return os.path.join(out_root, tenant)


def env_root_under(tenant, resource_type, out_root=None):
    if out_root is None:
        return env_root(tenant, resource_type)
    return os.path.join(out_root, tenant, resource_type)
