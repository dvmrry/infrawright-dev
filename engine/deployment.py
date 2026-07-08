"""Overlay-aware tenant path resolution. Single source of truth for where a
tenant's config/envs/imports and module roots live, driven by deployment.json. Stdlib-only,
Python 3.6-floor. Consumed by both the Makefile (via the CLI verbs) and the
Python tools so path logic lives in exactly one place.

Absent or empty deployment.json => overlay "." (root; the zero-change default).
Present-but-malformed deployment.json => raise / exit non-zero (fail loud).
"""
import json
import os
import re
import sys

from engine import manifest_checks

DEPLOYMENT_JSON = "deployment.json"
ROOTS_PROVIDER_KEYS = set(["strategy", "groups", "bind_references"])
ROOTS_STRATEGIES = set(["explicit", "slug"])
ROOT_LABEL_RE = re.compile(r"^[a-z0-9_]+$")


def _deployment_path():
    """The deployment.json to read: INFRAWRIGHT_DEPLOYMENT if set, else
    deployment.json in the cwd. Mirrors engine.packs' INFRAWRIGHT_PACKS override
    so the test suite (and any alternate deployment) can pin/neutralize the
    overlay rather than depending on whatever deployment.json sits in the cwd —
    a committed adopter deployment.json must not redirect the template's own
    tests. An empty value is treated as unset (falls back to cwd deployment.json);
    a set-but-missing path neutralizes to overlay "." rather than erroring — the
    same absent-file branch os.devnull uses to pin the suite hermetic."""
    return os.environ.get("INFRAWRIGHT_DEPLOYMENT") or DEPLOYMENT_JSON


def _load():
    path = _deployment_path()
    if not os.path.exists(path):
        return {}
    with open(path, encoding="utf-8") as f:
        text = f.read()
    if not text.strip():
        return {}
    return json.loads(text)  # malformed -> ValueError propagates (fail loud)


def overlay():
    return _load().get("overlay", ".") or "."


def tfvars_format():
    """Return the deployment-selected generated tfvars format.

    JSON is the compatibility default. HCL is opt-in via
    deployment.json: {"tfvars_format": "hcl"}.
    """
    value = _load().get("tfvars_format", "json")
    if value in ("json", "hcl"):
        return value
    raise ValueError(
        "%s tfvars_format must be 'json' or 'hcl' (got %r)"
        % (_deployment_path(), value)
    )


def roots_config():
    """Return the validated raw roots section from deployment.json.

    This validates only deployment-local shape and enum constraints. Provider
    and resource-type validation stays in engine.artifacts so deployment.py
    remains independent of packs and registry data.
    """
    data = _load()
    if "roots" not in data:
        return {}
    roots = data.get("roots")
    if not isinstance(roots, dict):
        raise ValueError("%s roots must be an object" % _deployment_path())
    for provider, cfg in roots.items():
        if not isinstance(provider, str) or not provider:
            raise ValueError(
                "%s roots keys must be non-empty strings" % _deployment_path()
            )
        path = "%s roots.%s" % (_deployment_path(), provider)
        if not isinstance(cfg, dict):
            raise ValueError("%s must be an object" % path)
        manifest_checks.reject_unknown_keys(cfg, ROOTS_PROVIDER_KEYS, path)
        if "strategy" in cfg and cfg["strategy"] not in ROOTS_STRATEGIES:
            raise ValueError(
                "%s.strategy must be 'explicit' or 'slug' (got %r)"
                % (path, cfg["strategy"])
            )
        if "bind_references" in cfg and not isinstance(cfg["bind_references"], bool):
            raise ValueError("%s.bind_references must be a bool" % path)
        if "groups" not in cfg:
            continue
        groups = cfg["groups"]
        if not isinstance(groups, dict):
            raise ValueError("%s.groups must be an object" % path)
        for label, members in groups.items():
            group_path = "%s.groups.%s" % (path, label)
            if not isinstance(label, str) or not ROOT_LABEL_RE.match(label):
                raise ValueError(
                    "%s group labels must match [a-z0-9_]+" % path
                )
            if not isinstance(members, list):
                raise ValueError("%s must be a list" % group_path)
            if not members:
                raise ValueError("%s must not be empty" % group_path)
            for idx, member in enumerate(members):
                if not isinstance(member, str) or not member:
                    raise ValueError(
                        "%s[%d] must be a non-empty string" % (group_path, idx)
                    )
    return roots


def module_dir():
    """Directory containing generated modules for this deployment.

    New deployments should set module_dir explicitly so module sets can be
    versioned under the overlay. Missing module_dir keeps the old root modules/
    location as a transitional fallback; an overlay without module_dir uses the
    conventional <overlay>/modules/default module set.
    """
    data = _load()
    explicit = data.get("module_dir")
    if explicit:
        return explicit
    root = data.get("overlay", ".") or "."
    if root == ".":
        return "modules"
    return os.path.join(root, "modules", "default")


def tenant_root(tenant):
    return overlay()


def _path(tenant, kind, provider=None):
    """The one output layout: [<overlay>/]<kind>/<tenant>.

    infrawright owns only this deterministic inner structure; everything above it
    is the adopter's FREE-FORM overlay prefix - a company, a cloud, a repo name,
    or nothing. Provider/pack metadata affects behavior, not artifact paths:
    Terraform resource type names are already globally namespaced. The provider
    argument is accepted for older call sites but intentionally ignored."""
    root = tenant_root(tenant)
    parts = [kind, tenant]
    rel = os.path.join(*parts)
    return rel if root == "." else os.path.join(root, rel)


def config_dir(tenant, provider=None):
    return _path(tenant, "config", provider)


def imports_dir(tenant, provider=None):
    return _path(tenant, "imports", provider)


def envs_dir(tenant, provider=None):
    return _path(tenant, "envs", provider)


def pulls_dir(tenant):
    return os.path.join("pulls", tenant)  # gitignored staging — always root


# Repo-root-relative path strings for consumers that re.escape an anchor against
# `git status --porcelain` output (e.g. pipelines/commitback.sh). Same value as
# the *_dir helpers today, but named so intent is explicit at the call site.
config_prefix = config_dir
imports_prefix = imports_dir

_VERBS = {
    "tenant-root": tenant_root, "config-dir": config_dir,
    "imports-dir": imports_dir, "envs-dir": envs_dir,
    "config-prefix": config_prefix, "imports-prefix": imports_prefix,
}


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    if not argv:
        sys.stderr.write("usage: python -m engine.deployment <verb> [tenant]\n")
        return 2
    verb = argv[0]
    try:
        if verb == "overlay":
            print(overlay())
        elif verb == "module-dir":
            print(module_dir())
        elif verb in _VERBS:
            if len(argv) < 2:
                sys.stderr.write("error: %s requires a tenant\n" % verb)
                return 2
            print(_VERBS[verb](argv[1]))
        else:
            sys.stderr.write("error: unknown verb %r\n" % verb)
            return 2
    except ValueError as exc:
        sys.stderr.write("error: %s is malformed: %s\n" % (_deployment_path(), exc))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
