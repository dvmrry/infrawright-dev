"""Operational orchestration helpers for tenant roots.

The artifact layout is flat by Terraform resource type:
  [overlay/]config/<tenant>/<resource_type>.auto.tfvars.json
  [overlay/]imports/<tenant>/<resource_type>_imports.tf
  [overlay/]envs/<tenant>/<resource_type>/

Provider packs own behavior and metadata; they do not create path segments.
"""
import json
import os
import re
import shutil
import subprocess
import sys

from engine import deployment
from engine import packs
from engine.filter_imports import filter_imports
from engine.registry import derived_types, generated_types, load_registry

CONFIG_SUFFIX = ".auto.tfvars.json"
IMPORTS_SUFFIX = "_imports.tf"
MOVES_SUFFIX = "_moves.tf"
VALID_TENANT = re.compile(r"^[A-Za-z0-9_.-]+$")


def terraform():
    return os.environ.get("TF") or "terraform"


def validate_tenant(tenant):
    if not VALID_TENANT.match(tenant or "") or tenant in (".", ".."):
        raise ValueError(
            "TENANT must match [A-Za-z0-9_.-]+ and not be . or .. (got %r)"
            % tenant
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


def config_file(tenant, resource_type):
    return os.path.join(
        deployment.config_dir(tenant), resource_type + CONFIG_SUFFIX
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


def _env_base_candidates():
    bases = {"envs"}
    try:
        overlay = deployment.overlay()
    except ValueError:
        overlay = "."
    if overlay and overlay != ".":
        bases.add(os.path.join(overlay, "envs"))
    return sorted(bases)


def discover_env_pairs(tenant=None):
    """Return sorted (tenant, resource_type, env_dir) with generated roots."""
    generated = set(generated_types())
    pairs = []
    bases = [deployment.envs_dir(tenant)] if tenant else _env_base_candidates()
    for base in bases:
        if not os.path.isdir(base):
            continue
        if tenant:
            tenant_names = [tenant]
            tenant_dirs = {tenant: base}
        else:
            tenant_names = sorted(os.listdir(base))
            tenant_dirs = dict(
                (name, os.path.join(base, name)) for name in tenant_names
            )
        for tenant_name in tenant_names:
            tenant_dir = tenant_dirs[tenant_name]
            if not os.path.isdir(tenant_dir):
                continue
            for resource_type in sorted(os.listdir(tenant_dir)):
                path = os.path.join(tenant_dir, resource_type)
                if resource_type in generated and os.path.isdir(path):
                    pairs.append((tenant_name, resource_type, path))
    return sorted(set(pairs))


def selected_env_pairs(tenant=None, selectors=None, require_plan=False):
    selected = set(expand_resources(selectors or [])) if selectors else None
    out = []
    for tenant_name, resource_type, path in discover_env_pairs(tenant):
        if selected is not None and resource_type not in selected:
            continue
        if require_plan and not os.path.exists(os.path.join(path, "tfplan")):
            continue
        out.append((tenant_name, resource_type, path))
    return out


def _init_args(env_dir, tenant, resource_type, backend_config=None):
    args = [terraform(), "-chdir=" + env_dir, "init", "-input=false"]
    if backend_config:
        args.extend([
            "-reconfigure",
            "-backend-config=" + os.path.abspath(backend_config),
            "-backend-config=key=%s/%s.tfstate" % (tenant, resource_type),
        ])
    return args


def _requires_backend_config(env_dir, resource_type, backend_config):
    main_tf = os.path.join(env_dir, "main.tf")
    if backend_config or not os.path.exists(main_tf):
        return False
    with open(main_tf, encoding="utf-8") as f:
        return any(line.startswith('  backend "') for line in f)


def _check_backend(env_dir, resource_type, backend_config):
    if _requires_backend_config(env_dir, resource_type, backend_config):
        raise RuntimeError(
            "%s declares a remote backend; run with BACKEND_CONFIG=<file>"
            % resource_type
        )


def _check_call(args, stdout=None):
    return subprocess.check_call(args, stdout=stdout)


def _show_plan_json(env_dir):
    raw = subprocess.check_output([
        terraform(), "-chdir=" + env_dir, "show", "-json", "tfplan"
    ])
    plan = json.loads(raw.decode("utf-8"))
    if not isinstance(plan, dict) or "format_version" not in plan:
        raise RuntimeError(
            "%s: terraform show output is not plan JSON; re-run the plan stage"
            % env_dir
        )
    return plan


def _plan_change_count(plan):
    total = 0
    for resource_change in plan.get("resource_changes") or []:
        actions = set((resource_change.get("change") or {}).get("actions") or [])
        if actions - {"no-op"}:
            total += 1
    return total


def _destroy_count(plan):
    total = 0
    for resource_change in plan.get("resource_changes") or []:
        actions = set((resource_change.get("change") or {}).get("actions") or [])
        if "delete" in actions:
            total += 1
    return total


def cmd_stage_imports(opts):
    tenant = opts["tenant"]
    validate_tenant(tenant)
    selected = expand_resources(opts["selectors"])
    staged = 0
    sources = 0
    for resource_type in selected:
        env_dir = env_root(tenant, resource_type)
        for source in (imports_file(tenant, resource_type), moves_file(tenant, resource_type)):
            if not os.path.exists(source):
                continue
            sources += 1
            base = os.path.basename(source)
            if not os.path.isdir(env_dir):
                sys.stderr.write(
                    "skip %s (no env root %s - run make gen-env)\n"
                    % (base, env_dir)
                )
                continue
            dest = os.path.join(env_dir, base)
            if source.endswith(IMPORTS_SUFFIX) and opts["state_aware"]:
                _check_backend(env_dir, resource_type, opts["backend_config"])
                _check_call(
                    _init_args(
                        env_dir, tenant, resource_type,
                        backend_config=opts["backend_config"],
                    ),
                    stdout=subprocess.DEVNULL,
                )
                state = subprocess.run(
                    [terraform(), "-chdir=" + env_dir, "state", "list"],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL,
                    check=False,
                )
                addresses = (
                    state.stdout.decode("utf-8").splitlines()
                    if state.returncode == 0 else []
                )
                with open(source, encoding="utf-8") as f:
                    text, kept, skipped = filter_imports(f.read(), addresses)
                if text:
                    with open(dest, "w", encoding="utf-8") as f:
                        f.write(text)
                    sys.stderr.write(
                        "%d import(s) kept, %d already managed (skipped)\n"
                        % (kept, skipped)
                    )
                else:
                    if os.path.exists(dest):
                        os.remove(dest)
                    sys.stderr.write(
                        "skip %s (every import already managed - delta is empty)\n"
                        % base
                    )
                    continue
            else:
                shutil.copyfile(source, dest)
            sys.stderr.write("staged %s\n" % dest)
            staged += 1
    if sources == 0:
        raise RuntimeError(
            "nothing to stage for TENANT=%s (run make transform first)" % tenant
        )
    if staged == 0:
        sys.stderr.write(
            "NOTE: 0 staged - every import is already managed or no roots matched\n"
        )
    return 0


def cmd_unstage_imports(opts):
    tenant = opts["tenant"]
    validate_tenant(tenant)
    removed = 0
    for _tenant, resource_type, path in selected_env_pairs(tenant, opts["selectors"]):
        for suffix in (IMPORTS_SUFFIX, MOVES_SUFFIX):
            target = os.path.join(path, resource_type + suffix)
            if os.path.exists(target):
                os.remove(target)
                sys.stderr.write("removed %s\n" % target)
                removed += 1
    sys.stderr.write("%d file(s) removed\n" % removed)
    return 0


def cmd_plan(opts):
    tenant = opts["tenant"]
    validate_tenant(tenant)
    skipped_derived = set(derived_types()) if opts["imports_only"] else set()
    planned = 0
    for _tenant, resource_type, path in selected_env_pairs(tenant, opts["selectors"]):
        if resource_type in skipped_derived:
            sys.stderr.write(
                "skip %s (IMPORTS_ONLY: derived/non-importable)\n" % resource_type
            )
            continue
        var_file = config_file(tenant, resource_type)
        if not os.path.exists(var_file):
            sys.stderr.write("skip %s (no %s)\n" % (resource_type, var_file))
            continue
        _check_backend(path, resource_type, opts["backend_config"])
        sys.stderr.write("== plan %s\n" % resource_type)
        _check_call(
            _init_args(
                path, tenant, resource_type, backend_config=opts["backend_config"]
            ),
            stdout=subprocess.DEVNULL,
        )
        args = [
            terraform(), "-chdir=" + path, "plan", "-input=false",
            "-var-file=" + os.path.abspath(var_file),
        ]
        if opts["save"]:
            args.append("-out=tfplan")
        _check_call(args)
        planned += 1
    if planned == 0:
        raise RuntimeError(
            "no roots planned for TENANT=%s (missing env roots or config?)" % tenant
        )
    return 0


def cmd_assert_clean(opts):
    checked = 0
    dirty = 0
    for tenant, resource_type, path in selected_env_pairs(
            opts.get("tenant"), opts["selectors"], require_plan=True):
        plan = _show_plan_json(path)
        changes = _plan_change_count(plan)
        checked += 1
        if changes:
            sys.stderr.write(
                "NOT CLEAN: %s/%s plan contains %d change(s) beyond imports\n"
                % (tenant, resource_type, changes)
            )
            dirty += 1
    if checked == 0:
        raise RuntimeError("no saved plans to check - run make plan SAVE=1 first")
    if dirty:
        raise RuntimeError(
            "tenant moved since fetch (or transform disagrees) - do not auto-merge"
        )
    sys.stderr.write("all %d saved plan(s) clean (no-op/imports only)\n" % checked)
    return 0


def cmd_clean_plans(opts):
    removed = 0
    for _tenant, _resource_type, path in selected_env_pairs(opts.get("tenant"), opts["selectors"]):
        plan = os.path.join(path, "tfplan")
        if os.path.exists(plan):
            os.remove(plan)
            sys.stderr.write("removed %s\n" % plan)
            removed += 1
    sys.stderr.write("%d stale plan(s) removed\n" % removed)
    return 0


def _current_branch():
    ref = (
        os.environ.get("BUILD_SOURCEBRANCH")
        or os.environ.get("GITHUB_REF")
        or os.environ.get("BITBUCKET_BRANCH")
        or ""
    )
    if ref:
        return ref.split("refs/heads/")[-1]
    try:
        out = subprocess.check_output(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            stderr=subprocess.DEVNULL,
        )
        return out.decode("utf-8").strip()
    except subprocess.CalledProcessError:
        return "unknown"


def cmd_apply(opts):
    main_branch = opts["main_branch"] or "main"
    branch = _current_branch()
    if branch != main_branch and not opts["allow_non_main"]:
        raise RuntimeError(
            "apply refused from %r - only merged %s config gets applied "
            "(use ALLOW_NON_MAIN=1 for an intentional exception)"
            % (branch, main_branch)
        )
    applied = 0
    for tenant, resource_type, path in selected_env_pairs(
            opts.get("tenant"), opts["selectors"], require_plan=True):
        sys.stderr.write("== apply %s/%s\n" % (tenant, resource_type))
        _check_backend(path, resource_type, opts["backend_config"])
        _check_call(
            _init_args(
                path, tenant, resource_type, backend_config=opts["backend_config"]
            ),
            stdout=subprocess.DEVNULL,
        )
        plan = _show_plan_json(path)
        destroys = _destroy_count(plan)
        if destroys and not opts["allow_destroy"]:
            raise RuntimeError(
                "%s/%s saved plan destroys (or replaces) %d resource(s) - refused"
                % (tenant, resource_type, destroys)
            )
        _check_call([terraform(), "-chdir=" + path, "apply", "-input=false", "tfplan"])
        os.remove(os.path.join(path, "tfplan"))
        applied += 1
    if applied == 0:
        raise RuntimeError("no saved plans found - run make plan SAVE=1 first")
    return 0


def _parse(argv, allow_optional_tenant=False):
    opts = {
        "tenant": None,
        "selectors": [],
        "backend_config": None,
        "state_aware": False,
        "save": False,
        "imports_only": False,
        "allow_destroy": False,
        "allow_non_main": False,
        "main_branch": None,
    }
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--tenant":
            i += 1
            if i >= len(argv):
                raise ValueError("--tenant requires a value")
            opts["tenant"] = argv[i]
        elif arg == "--backend-config":
            i += 1
            if i >= len(argv):
                raise ValueError("--backend-config requires a value")
            opts["backend_config"] = argv[i]
        elif arg == "--state-aware":
            opts["state_aware"] = True
        elif arg == "--save":
            opts["save"] = True
        elif arg == "--imports-only":
            opts["imports_only"] = True
        elif arg == "--allow-destroy":
            opts["allow_destroy"] = True
        elif arg == "--allow-non-main":
            opts["allow_non_main"] = True
        elif arg == "--main-branch":
            i += 1
            if i >= len(argv):
                raise ValueError("--main-branch requires a value")
            opts["main_branch"] = argv[i]
        elif arg.startswith("-"):
            raise ValueError("unknown option %s" % arg)
        else:
            opts["selectors"].append(arg)
        i += 1
    if not allow_optional_tenant and not opts["tenant"]:
        raise ValueError("--tenant is required")
    if opts["tenant"]:
        validate_tenant(opts["tenant"])
    return opts


def _usage():
    return (
        "usage: python -m engine.ops <resources|stage-imports|unstage-imports|plan|"
        "assert-clean|clean-plans|apply> [options] [resource|provider ...]\n"
    )


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    if not argv:
        sys.stderr.write(_usage())
        return 2
    command = argv[0]
    rest = argv[1:]
    try:
        if command == "resources":
            for resource_type in expand_resources(rest):
                sys.stdout.write(resource_type + "\n")
            return 0
        if command == "stage-imports":
            return cmd_stage_imports(_parse(rest))
        if command == "unstage-imports":
            return cmd_unstage_imports(_parse(rest))
        if command == "plan":
            return cmd_plan(_parse(rest))
        if command == "assert-clean":
            return cmd_assert_clean(_parse(rest, allow_optional_tenant=True))
        if command == "clean-plans":
            return cmd_clean_plans(_parse(rest, allow_optional_tenant=True))
        if command == "apply":
            return cmd_apply(_parse(rest, allow_optional_tenant=True))
        sys.stderr.write("error: unknown command %r\n" % command)
        sys.stderr.write(_usage())
        return 2
    except ValueError as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    except (OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1


if __name__ == "__main__":
    sys.exit(main())
