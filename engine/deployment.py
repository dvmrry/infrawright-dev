"""Overlay-aware tenant path resolution. Single source of truth for where a
tenant's config/envs/imports live, driven by deployment.json. Stdlib-only,
Python 3.6-floor. Consumed by both the Makefile (via the CLI verbs) and the
Python tools so path logic lives in exactly one place.

Absent or empty deployment.json => overlay "." (root; the zero-change default).
Present-but-malformed deployment.json => raise / exit non-zero (fail loud).
"""
import json
import os
import sys

DEPLOYMENT_JSON = "deployment.json"
TEMPLATE_TENANTS = frozenset({"demo"})


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


def tenant_root(tenant):
    return "." if tenant in TEMPLATE_TENANTS else overlay()


def layout():
    """Output-layout strategy from deployment.json. 'flat' (default) is the
    historical kind/tenant tree, byte-identical to the original output;
    'vendor-provider' groups config+imports under $COMPANY/<vendor>/<provider>/
    with envs at the vendor level. Layout is an adopter choice, not an engine
    mandate — the engine emits per-(tenant, provider, rt) artifacts and the
    strategy maps them to paths."""
    return _load().get("layout", "flat") or "flat"


def _flat(tenant, kind):
    root = tenant_root(tenant)
    return os.path.join(kind, tenant) if root == "." else os.path.join(root, kind, tenant)


def _vendor_provider(tenant, provider, leaf):
    """$COMPANY/<vendor>/<leaf> under the tenant root (vendor level omitted for a
    standalone provider). config+imports share the <provider> leaf (co-located);
    envs uses the 'envs' leaf, shared across the vendor's providers."""
    from engine import packs
    vendor = packs.vendor_of(provider)
    rel = os.path.join(tenant, *([vendor, leaf] if vendor else [leaf]))
    root = tenant_root(tenant)
    return rel if root == "." else os.path.join(root, rel)


def config_dir(tenant, provider=None):
    if layout() == "vendor-provider" and provider is not None:
        return _vendor_provider(tenant, provider, provider)
    return _flat(tenant, "config")


def imports_dir(tenant, provider=None):
    if layout() == "vendor-provider" and provider is not None:
        return _vendor_provider(tenant, provider, provider)
    return _flat(tenant, "imports")


def envs_dir(tenant, provider=None):
    if layout() == "vendor-provider" and provider is not None:
        return _vendor_provider(tenant, provider, "envs")
    return _flat(tenant, "envs")


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
