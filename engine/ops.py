"""Operational orchestration helpers for tenant roots.

The artifact layout is flat by Terraform resource type:
  [overlay/]config/<tenant>/<resource_type>.auto.tfvars.json
  [overlay/]imports/<tenant>/<resource_type>_imports.tf
  [overlay/]envs/<tenant>/<resource_type>/

Provider packs own behavior and metadata; they do not create path segments.
"""
import json
import os
import shutil
import subprocess
import sys

from engine import deployment
from engine import packs
from engine.artifacts import (
    CONFIG_SUFFIX,
    EXPRESSION_BINDINGS_SUFFIX,
    IMPORTS_SUFFIX,
    MOVES_SUFFIX,
    config_file,
    env_root,
    env_root_under,
    expand_resources,
    expression_bindings_file,
    imports_file,
    moves_file,
    tenant_env_dir,
    validate_resource_type,
    validate_tenant,
)
from engine.filter_imports import filter_imports
from engine.registry import derived_types, generated_types


def terraform():
    return os.environ.get("TF") or "terraform"


def _env_base_candidates():
    overlay = deployment.overlay()
    if overlay and overlay != ".":
        return [os.path.join(overlay, "envs")]
    return ["envs"]


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


def _iter_plan_change_records(plan):
    for resource_change in plan.get("resource_changes") or []:
        yield resource_change
    for resource_drift in plan.get("resource_drift") or []:
        yield resource_drift


def _non_import_change_count(plan):
    from engine.plan_eval import CLEAN, classify_plan

    findings = classify_plan(plan)["findings"]
    return sum(1 for finding in findings if finding["status"] != CLEAN)


def _destroy_count(plan):
    total = 0
    for resource_change in _iter_plan_change_records(plan):
        actions = set((resource_change.get("change") or {}).get("actions") or [])
        if "delete" in actions:
            total += 1
    return total


def _provider_config_guidance(plan, resource_type):
    from engine import adoption_guidance
    from engine import provider_config

    report = provider_config.build_report(resource_type=resource_type, plan=plan)
    annotations = []
    for item in report.get("plan_changes") or []:
        if item.get("status") != "provider_config_requirement":
            continue
        if item.get("mode") not in ("required_external", "renderable_default"):
            continue
        annotations.append(adoption_guidance.provider_config_annotation(
            source=item.get("source"),
            address=item.get("address"),
            matched_plan_path=item.get("path"),
            provider=item.get("provider"),
            resource_type=item.get("resource_type"),
            setting=item.get("setting"),
            expected_value=item.get("value"),
            mode=item.get("mode"),
            reason=item.get("reason"),
            evidence=item.get("evidence"),
        ))
    return annotations


def _absent_default_guidance(plan, resource_type):
    from engine import adoption_guidance
    from engine import guidance_paths

    provider = packs.provider_of(resource_type)
    rules = packs.absent_default_rules(provider)
    by_path = {}
    for rule in rules:
        if rule.get("action") != "manual_review_required":
            continue
        if not _absent_default_rule_matches(rule, provider, resource_type):
            continue
        path = _absent_default_plan_path(rule)
        by_path.setdefault(path, []).append(rule)
    if not by_path:
        return []

    annotations = []
    for candidate in guidance_paths.guidance_candidate_paths(plan, resource_type):
        formatted = candidate["formatted_path"]
        for rule in by_path.get(formatted, []):
            if not _absent_default_observed_value_matches(
                    rule, candidate["before"], candidate["path"]):
                continue
            annotations.append(adoption_guidance.absent_default_annotation(
                source=candidate["source"],
                address=candidate["address"],
                matched_plan_path=formatted,
                provider=rule["provider"],
                resource_type=candidate["resource_type"],
                rule=rule["id"],
                kind=rule["kind"],
                action=rule["action"],
                observed_value=rule.get("observed_value"),
                reason=rule.get("reason"),
                evidence=rule.get("evidence"),
            ))
    return annotations


def _dynamic_schema_guidance(plan, resource_type):
    from engine import adoption_guidance
    from engine import guidance_paths

    provider = packs.provider_of(resource_type)
    rules = packs.dynamic_schema_rules(provider)
    by_path = {}
    for rule in rules:
        if rule.get("action") != "manual_review_required":
            continue
        if not _dynamic_schema_rule_matches(rule, provider, resource_type):
            continue
        path = _dynamic_schema_plan_path(rule)
        by_path.setdefault(path, []).append(rule)
    if not by_path:
        return []

    annotations = []
    for candidate in guidance_paths.guidance_candidate_paths(plan, resource_type):
        formatted = candidate["formatted_path"]
        for rule in by_path.get(formatted, []):
            annotations.append(adoption_guidance.dynamic_schema_annotation(
                source=candidate["source"],
                address=candidate["address"],
                matched_plan_path=formatted,
                provider=rule["provider"],
                resource_type=candidate["resource_type"],
                rule=rule["id"],
                kind=rule["kind"],
                ownership=rule["ownership"],
                action=rule["action"],
                provider_version_constraint=rule.get(
                    "provider_version_constraint"
                ),
                reason=rule.get("reason"),
                evidence=rule.get("evidence"),
            ))
    return annotations


def _absent_default_rule_matches(rule, provider, resource_type):
    if rule.get("provider") != provider:
        return False
    if "resource_type" in rule:
        return rule["resource_type"] == resource_type
    prefix = rule.get("resource_prefix")
    return bool(prefix and resource_type.startswith(prefix))


def _absent_default_plan_path(rule):
    from engine import schema_paths

    path = rule.get("plan_path") or rule.get("path")
    try:
        return schema_paths.format_path(schema_paths.parse_report_path(path))
    except Exception:
        return path


def _dynamic_schema_rule_matches(rule, provider, resource_type):
    if rule.get("provider") != provider:
        return False
    if "resource_type" in rule:
        return rule["resource_type"] == resource_type
    prefix = rule.get("resource_prefix")
    return bool(prefix and resource_type.startswith(prefix))


def _dynamic_schema_plan_path(rule):
    from engine import schema_paths

    path = rule.get("plan_path") or rule.get("path")
    try:
        return schema_paths.format_path(schema_paths.parse_report_path(path))
    except Exception:
        return path


_MISSING_ABSENT_DEFAULT_VALUE = object()


def _absent_default_observed_value_matches(rule, before, path):
    if "observed_value" not in rule:
        return True
    actual = _absent_default_path_value(before, path)
    if actual is _MISSING_ABSENT_DEFAULT_VALUE:
        return False
    return _same_json_value(actual, rule.get("observed_value"))


def _absent_default_path_value(value, path):
    cur = value
    for segment in path:
        if isinstance(segment, int):
            if not isinstance(cur, list):
                return _MISSING_ABSENT_DEFAULT_VALUE
            if segment < 0 or segment >= len(cur):
                return _MISSING_ABSENT_DEFAULT_VALUE
            cur = cur[segment]
        elif isinstance(cur, dict) and segment in cur:
            cur = cur[segment]
        else:
            return _MISSING_ABSENT_DEFAULT_VALUE
    return cur


def _same_json_value(actual, expected):
    if isinstance(actual, bool) or isinstance(expected, bool):
        return actual is expected
    if isinstance(actual, (int, float)) and isinstance(expected, (int, float)):
        return actual == expected
    return type(actual) is type(expected) and actual == expected


def _guidance_annotations(plan, resource_type):
    from engine import adoption_guidance

    annotations = []
    annotations.extend(adoption_guidance.safe_collect_guidance(
        _provider_config_guidance, plan, resource_type
    ))
    annotations.extend(adoption_guidance.safe_collect_guidance(
        _absent_default_guidance, plan, resource_type
    ))
    annotations.extend(adoption_guidance.safe_collect_guidance(
        _dynamic_schema_guidance, plan, resource_type
    ))
    return adoption_guidance.sort_annotations(annotations)


def _print_findings(findings, guidance_annotations=None):
    from engine import adoption_guidance
    from engine.paths import format_path
    from engine.plan_eval import BLOCKED, TOLERATED

    guidance_annotations = guidance_annotations or []
    all_annotations = []
    for finding in findings:
        if finding.get("status") not in (BLOCKED, TOLERATED):
            continue
        sys.stderr.write(
            "  %s %s %s\n"
            % (
                finding.get("address"),
                ",".join(finding.get("actions") or []),
                finding.get("status"),
            )
        )
        for path in finding.get("paths") or []:
            rendered = format_path(path)
            sys.stderr.write("    - %s\n" % rendered)
            if finding.get("status") != BLOCKED:
                continue
            all_annotations.extend(
                adoption_guidance.annotations_for_finding_path(
                    guidance_annotations, finding, path
                )
            )
    adoption_guidance.print_guidance_sections(all_annotations, sys.stderr.write)


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
            "nothing to stage for TENANT=%s "
            "(run make transform or make adopt first)" % tenant
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
        changes = _non_import_change_count(plan)
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


def cmd_assert_adoptable(opts):
    from engine.drift_policy import DriftPolicy
    from engine.plan_eval import BLOCKED, TOLERATED, classify_plan

    policy = DriftPolicy.load(opts.get("policy"))
    checked = 0
    blocked = 0
    tolerated = 0
    checked_types = set()
    for tenant, resource_type, path in selected_env_pairs(
            opts.get("tenant"), opts["selectors"], require_plan=True):
        plan = _show_plan_json(path)
        result = classify_plan(plan, policy=policy)
        checked += 1
        checked_types.add(resource_type)
        if result["status"] == BLOCKED:
            blocked += 1
            sys.stderr.write("BLOCKED: %s/%s\n" % (tenant, resource_type))
            _print_findings(
                result["findings"],
                guidance_annotations=_guidance_annotations(plan, resource_type),
            )
        elif result["status"] == TOLERATED:
            tolerated += 1
            sys.stderr.write("TOLERATED: %s/%s\n" % (tenant, resource_type))
            _print_findings(result["findings"])
    if checked == 0:
        raise RuntimeError("no saved plans to check - run make plan SAVE=1 first")
    for rt, mode, path in policy.stale_entries(
            resource_types=checked_types, modes=("plan_tolerate",)):
        sys.stderr.write(
            "STALE DRIFT POLICY: %s %s %s matched no path\n"
            % (rt, mode, path)
        )
    if blocked:
        raise RuntimeError("%d saved plan(s) blocked by untolerated changes" % blocked)
    if tolerated:
        sys.stderr.write(
            "%d saved plan(s) adoptable with consumer-tolerated drift\n" % tolerated
        )
    else:
        sys.stderr.write("all %d saved plan(s) clean\n" % checked)
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
    from engine.drift_policy import DriftPolicy
    from engine.plan_eval import BLOCKED, TOLERATED

    main_branch = opts["main_branch"] or "main"
    branch = _current_branch()
    if branch != main_branch and not opts["allow_non_main"]:
        raise RuntimeError(
            "apply refused from %r - only merged %s config gets applied "
            "(use ALLOW_NON_MAIN=1 for an intentional exception)"
            % (branch, main_branch)
        )
    policy = DriftPolicy.load(opts.get("policy"))
    if opts["allow_plan_changes"]:
        sys.stderr.write(
            "WARNING: --allow-plan-changes is a broad legacy override for "
            "BLOCKED saved plans; prefer POLICY=<file> for explicit tolerated "
            "drift.\n"
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
        result = _classify_apply_plan(plan, policy)
        destroys = _destroy_count(plan)
        if result["status"] == BLOCKED and destroys and not opts["allow_destroy"]:
            raise RuntimeError(
                "%s/%s saved plan destroys (or replaces) %d resource(s) - refused"
                % (tenant, resource_type, destroys)
            )
        if result["status"] == BLOCKED and not opts["allow_plan_changes"]:
            raise RuntimeError(
                "%s/%s saved plan is blocked by untolerated changes; refused. "
                "Run assert-adoptable for review, pass POLICY=<file> for "
                "explicit tolerated drift, or use --allow-plan-changes only as "
                "a broad unsafe override."
                % (tenant, resource_type)
            )
        if result["status"] == TOLERATED:
            sys.stderr.write(
                "TOLERATED: %s/%s saved plan has consumer-tolerated drift\n"
                % (tenant, resource_type)
            )
        elif result["status"] == BLOCKED:
            sys.stderr.write(
                "WARNING: applying BLOCKED %s/%s saved plan because "
                "--allow-plan-changes was set\n" % (tenant, resource_type)
            )
        _check_call([terraform(), "-chdir=" + path, "apply", "-input=false", "tfplan"])
        os.remove(os.path.join(path, "tfplan"))
        applied += 1
    if applied == 0:
        raise RuntimeError("no saved plans found - run make plan SAVE=1 first")
    return 0


def _classify_apply_plan(plan, policy):
    from engine.plan_eval import classify_plan

    return classify_plan(plan, policy=policy)


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
        "allow_plan_changes": False,
        "main_branch": None,
        "policy": None,
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
        elif arg == "--allow-plan-changes":
            opts["allow_plan_changes"] = True
        elif arg == "--main-branch":
            i += 1
            if i >= len(argv):
                raise ValueError("--main-branch requires a value")
            opts["main_branch"] = argv[i]
        elif arg == "--policy":
            i += 1
            if i >= len(argv):
                raise ValueError("--policy requires a value")
            opts["policy"] = argv[i]
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
        "assert-clean|assert-adoptable|clean-plans|apply> [options] "
        "[resource|provider ...]\n"
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
        if command == "assert-adoptable":
            return cmd_assert_adoptable(_parse(rest, allow_optional_tenant=True))
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
