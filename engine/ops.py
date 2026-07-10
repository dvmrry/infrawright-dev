"""Operational orchestration helpers for tenant roots.

The artifact layout is flat by Terraform resource type:
  [overlay/]config/<tenant>/<resource_type>.auto.tfvars[.json]
  [overlay/]imports/<tenant>/<resource_type>_imports.tf
  [overlay/]envs/<tenant>/<resource_type>/

Provider packs own behavior and metadata; they do not create path segments.
"""
import json
import os
import hashlib
import re
import shutil
import subprocess
import sys
import tempfile

from engine import deployment
from engine import packs
from engine.artifacts import (
    IMPORTS_SUFFIX,
    MOVES_SUFFIX,
    all_root_labels,
    config_file,
    env_root_for_label,
    expand_resources,
    imports_file,
    moves_file,
    root_label,
    root_members,
    validate_tenant,
)
from engine.filter_imports import filter_imports
from engine.registry import derived_types


def terraform():
    return os.environ.get("TF") or "terraform"


def _env_base_candidates():
    overlay = deployment.overlay()
    if overlay and overlay != ".":
        return [os.path.join(overlay, "envs")]
    return ["envs"]


WHOLE_ROOT_SELECTION_NOTE = (
    "NOTE: selecting %s selects whole root %s; also operating on %s\n"
)
REFERENCE_ORDER_CYCLE_NOTE = (
    "NOTE: reference order cycle detected among %s; breaking alphabetically\n"
)
PLAN_FINGERPRINT = "tfplan.sources"
PLAN_FINGERPRINT_VERSION = 2
ROOT_TOPOLOGY_CONTRACT = "infrawright.root_topology"
ROOT_TOPOLOGY_SCHEMA_VERSION = 1
PLAN_ASSESSMENT_CONTRACT = "infrawright.saved_plan_assessment"
PLAN_ASSESSMENT_SCHEMA_VERSION = 1
MODULE_FINGERPRINT_IGNORED_DIRS = set([
    ".git",
    ".mypy_cache",
    ".pytest_cache",
    ".ruff_cache",
    ".terraform",
    "__pycache__",
])
STALE_PLAN_MESSAGE = (
    "%s: saved plan is stale relative to the current root configuration - "
    "re-run make plan SAVE=1"
)
MISSING_PLAN_FINGERPRINT_DETAIL = (
    "no plan fingerprint found; saved plan predates staleness checking"
)


def _reference_graph(resource_types):
    selected = set(resource_types)
    graph = dict((resource_type, set()) for resource_type in selected)
    indegree = dict((resource_type, 0) for resource_type in selected)
    refs = packs.references()
    for referrer in sorted(selected):
        for _field, spec in sorted((refs.get(referrer) or {}).items()):
            referent = spec.get("referent")
            if referent not in selected:
                continue
            if referrer not in graph[referent]:
                graph[referent].add(referrer)
                indegree[referrer] += 1
    return graph, indegree


def _reference_cycle_members(nodes, graph):
    nodes = set(nodes)
    index = [0]
    indexes = {}
    lowlinks = {}
    stack = []
    on_stack = set()
    cycle_members = set()

    def visit(node):
        indexes[node] = index[0]
        lowlinks[node] = index[0]
        index[0] += 1
        stack.append(node)
        on_stack.add(node)

        for child in sorted(graph.get(node, ())):
            if child not in nodes:
                continue
            if child not in indexes:
                visit(child)
                lowlinks[node] = min(lowlinks[node], lowlinks[child])
            elif child in on_stack:
                lowlinks[node] = min(lowlinks[node], indexes[child])

        if lowlinks[node] != indexes[node]:
            return
        component = []
        while True:
            child = stack.pop()
            on_stack.remove(child)
            component.append(child)
            if child == node:
                break
        if len(component) > 1:
            cycle_members.update(component)
        elif node in graph.get(node, ()):
            cycle_members.add(node)

    for node in sorted(nodes):
        if node not in indexes:
            visit(node)
    return sorted(cycle_members)


def reference_order(resource_types):
    """Return stable referent-before-referrer order for resource types."""
    resource_types = sorted(set(resource_types))
    graph, indegree = _reference_graph(resource_types)
    cycle_members = _reference_cycle_members(resource_types, graph)
    if cycle_members:
        sys.stderr.write(
            REFERENCE_ORDER_CYCLE_NOTE % ", ".join(cycle_members)
        )

    remaining = set(resource_types)
    ready = sorted(rt for rt in resource_types if indegree[rt] == 0)
    out = []
    while remaining:
        if ready:
            resource_type = ready.pop(0)
            if resource_type not in remaining:
                continue
        else:
            cyclic_ready = sorted(rt for rt in cycle_members if rt in remaining)
            resource_type = (
                cyclic_ready[0] if cyclic_ready else sorted(remaining)[0]
            )
        remaining.remove(resource_type)
        out.append(resource_type)
        for child in sorted(graph.get(resource_type, ())):
            indegree[child] -= 1
            if indegree[child] == 0 and child in remaining:
                ready.append(child)
        ready = sorted(ready)
    return out


def discover_env_roots(tenant=None):
    """Return sorted (tenant, label, env_dir, member_types) with generated roots."""
    labels = set(all_root_labels())
    roots = []
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
            for label in sorted(os.listdir(tenant_dir)):
                path = os.path.join(tenant_dir, label)
                if label in labels and os.path.isdir(path):
                    roots.append((
                        tenant_name,
                        label,
                        path,
                        tuple(root_members(label)),
                    ))
    return [
        (tenant_name, label, path, list(member_types))
        for tenant_name, label, path, member_types in sorted(set(roots))
    ]


def discover_env_pairs(tenant=None):
    """Return sorted (tenant, resource_type, env_dir) with generated roots."""
    pairs = []
    for tenant_name, _label, path, member_types in discover_env_roots(tenant):
        for resource_type in member_types:
            pairs.append((tenant_name, resource_type, path))
    return sorted(set(pairs))


def _note_whole_root_selection(selected_members, label, members):
    selected_members = sorted(selected_members)
    other_members = sorted(set(members) - set(selected_members))
    if not selected_members or not other_members:
        return
    sys.stderr.write(
        WHOLE_ROOT_SELECTION_NOTE
        % (", ".join(selected_members), label, ", ".join(other_members))
    )


def _selected_root_specs(selectors=None):
    if selectors:
        selected = set(expand_resources(selectors or []))
        labels = sorted(set(root_label(resource_type)
                            for resource_type in selected))
    else:
        selected = None
        labels = all_root_labels()
    out = []
    for label in labels:
        members = root_members(label)
        if selected is not None:
            _note_whole_root_selection(set(members) & selected, label, members)
        out.append((label, members))
    return out


def root_topology(tenant=None, selectors=None):
    """Return the stable logical root topology contract."""
    if tenant is not None:
        validate_tenant(tenant)
    roots = []
    resource_roots = {}
    for label, members in _selected_root_specs(selectors):
        members = sorted(members)
        provider = packs.provider_of(members[0]) if members else None
        for resource_type in members:
            resource_roots[resource_type] = label
        roots.append({
            "label": label,
            "provider": provider,
            "members": members,
            "env_dir": (
                env_root_for_label(tenant, label)
                if tenant is not None else None
            ),
        })
    directories = None
    if tenant is not None:
        directories = {
            "config": deployment.config_dir(tenant),
            "imports": deployment.imports_dir(tenant),
            "envs": deployment.envs_dir(tenant),
        }
    return {
        "kind": ROOT_TOPOLOGY_CONTRACT,
        "schema_version": ROOT_TOPOLOGY_SCHEMA_VERSION,
        "tenant": tenant,
        "selectors": list(selectors or []),
        "directories": directories,
        "roots": roots,
        "resource_roots": resource_roots,
    }


def cmd_roots(opts):
    if not opts.get("json"):
        raise ValueError("roots requires --json")
    sys.stdout.write(json.dumps(
        root_topology(opts.get("tenant"), opts.get("selectors")),
        indent=2,
        sort_keys=True,
    ) + "\n")
    return 0


def selected_env_pairs(tenant=None, selectors=None, require_plan=False):
    out = []
    for tenant_name, _label, path, member_types in selected_env_roots(
            tenant, selectors, require_plan=require_plan):
        for resource_type in member_types:
            out.append((tenant_name, resource_type, path))
    return out


def selected_env_roots(tenant=None, selectors=None, require_plan=False):
    selected = set(expand_resources(selectors or [])) if selectors else None
    out = []
    for tenant_name, label, path, member_types in discover_env_roots(tenant):
        if selected is not None:
            selected_members = set(member_types) & selected
            if not selected_members:
                continue
            _note_whole_root_selection(selected_members, label, member_types)
        if require_plan and not os.path.exists(os.path.join(path, "tfplan")):
            continue
        out.append((tenant_name, label, path, member_types))
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


def _plan_fingerprint_path(env_dir):
    return os.path.join(env_dir, PLAN_FINGERPRINT)


def _file_sha256(path):
    hasher = hashlib.sha256()
    with open(path, "rb") as f:
        while True:
            chunk = f.read(1024 * 1024)
            if not chunk:
                break
            hasher.update(chunk)
    return hasher.hexdigest()


def _root_tf_fingerprints(env_dir):
    out = []
    if not os.path.isdir(env_dir):
        return out
    for name in sorted(os.listdir(env_dir)):
        is_plan_input = (
            name.endswith(".tf")
            or name.endswith(".tf.json")
            or name == ".terraform.lock.hcl"
            or name == "terraform.tfvars"
            or name == "terraform.tfvars.json"
            or name.endswith(".auto.tfvars")
            or name.endswith(".auto.tfvars.json")
        )
        if not is_plan_input:
            continue
        path = os.path.join(env_dir, name)
        if os.path.isfile(path):
            out.append((name, _file_sha256(path)))
    return out


def _root_config_fingerprints(env_dir):
    return [
        entry for entry in _root_tf_fingerprints(env_dir)
        if entry[0].endswith(".tf") or entry[0].endswith(".tf.json")
    ]


def _tree_fingerprints(root):
    out = []
    if not os.path.isdir(root):
        return out
    for current, dirs, files in os.walk(root):
        dirs[:] = sorted(
            name for name in dirs
            if name not in MODULE_FINGERPRINT_IGNORED_DIRS
        )
        for name in sorted(files):
            path = os.path.join(current, name)
            if not os.path.isfile(path):
                continue
            relative = os.path.relpath(path, root)
            if os.sep != "/":
                relative = relative.replace(os.sep, "/")
            out.append((relative, _file_sha256(path)))
    return out


_HCL_HEREDOC_START = re.compile(
    r"<<(-?)([A-Za-z_][A-Za-z0-9_-]*)"
)


def _hcl_structure_lines(text, path):
    """Return HCL lines with comments blanked for structural parsing.

    This is deliberately a small structural scanner, not an HCL evaluator. It
    preserves quoted strings and braces needed by the generated-root parser,
    while ensuring comment text cannot masquerade as configuration. Generated
    roots do not contain heredocs, so reject them instead of approximating
    their delimiter semantics.
    """
    out = []
    block_comment = False
    for line_number, line in enumerate(text.splitlines(True), 1):
        code = []
        in_string = False
        escaped = False
        i = 0
        while i < len(line):
            if block_comment:
                end = line.find("*/", i)
                if end < 0:
                    if line.endswith(("\r", "\n")):
                        code.append("\n")
                    i = len(line)
                    continue
                code.append(" " * (end + 2 - i))
                block_comment = False
                i = end + 2
                continue

            ch = line[i]
            if in_string:
                code.append(ch)
                if escaped:
                    escaped = False
                elif ch == "\\":
                    escaped = True
                elif ch == '"':
                    in_string = False
                i += 1
                continue
            if ch == '"':
                code.append(ch)
                in_string = True
                i += 1
                continue
            if ch == "#" or line.startswith("//", i):
                if line.endswith(("\r", "\n")):
                    code.append("\n")
                break
            if line.startswith("/*", i):
                code.append("  ")
                block_comment = True
                i += 2
                continue
            if line.startswith("<<", i):
                match = _HCL_HEREDOC_START.match(line, i)
                if match:
                    raise RuntimeError(
                        "%s:%d contains a heredoc outside the generated-root "
                        "contract; run make gen-env to regenerate the root"
                        % (path, line_number)
                    )
            code.append(ch)
            i += 1

        if in_string:
            raise RuntimeError(
                "%s:%d contains an unterminated quoted string"
                % (path, line_number)
            )
        out.append("".join(code))

    if block_comment:
        raise RuntimeError("%s contains an unterminated block comment" % path)
    return out


def _hcl_brace_delta(line):
    delta = 0
    in_string = False
    escaped = False
    for ch in line:
        if in_string:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
            continue
        if ch == '"':
            in_string = True
        elif ch == "{":
            delta += 1
        elif ch == "}":
            delta -= 1
    return delta


def _root_module_sources(env_dir):
    sources = {}
    if not os.path.isdir(env_dir):
        return sources
    module_start = re.compile(r'^\s*module\s+"([^"]+)"\s*\{\s*$')
    source_line = re.compile(r'^\s*source\s*=\s*"([^"\\]+)"\s*$')
    items_line = re.compile(
        r'^\s*items\s*=\s*(?:var|local)\.[A-Za-z_][A-Za-z0-9_]*\s*$'
    )
    for name in sorted(os.listdir(env_dir)):
        if not name.endswith(".tf"):
            continue
        path = os.path.join(env_dir, name)
        if not os.path.isfile(path):
            continue
        with open(path, encoding="utf-8") as f:
            lines = _hcl_structure_lines(f.read(), path)
        current = None
        source = None
        items_seen = False
        module_depth = None
        depth = 0
        for line_number, line in enumerate(lines, 1):
            if current is None and depth == 0:
                match = module_start.match(line)
                if match:
                    current = match.group(1)
                    source = None
                    items_seen = False
                    module_depth = 1
            elif current is not None and depth == module_depth:
                stripped = line.strip()
                source_match = source_line.match(line)
                if source_match:
                    if source is not None:
                        raise RuntimeError(
                            "%s:%d module %s has multiple source values"
                            % (path, line_number, current)
                        )
                    candidate = source_match.group(1)
                    if "${" in candidate or "%{" in candidate:
                        raise RuntimeError(
                            "%s:%d module %s source uses HCL template syntax "
                            "outside the generated-root contract; run make "
                            "gen-env to regenerate the root"
                            % (path, line_number, current)
                        )
                    source = candidate
                elif items_line.match(line):
                    if items_seen:
                        raise RuntimeError(
                            "%s:%d module %s has multiple items values"
                            % (path, line_number, current)
                        )
                    items_seen = True
                elif stripped and stripped != "}":
                    raise RuntimeError(
                        "%s:%d module %s is outside the generated-root "
                        "contract; run make gen-env to regenerate the root"
                        % (path, line_number, current)
                    )
            depth += _hcl_brace_delta(line)
            if depth < 0:
                raise RuntimeError(
                    "%s:%d has an unexpected closing brace"
                    % (path, line_number)
                )
            if current is not None and depth < module_depth:
                if source is None or not items_seen:
                    raise RuntimeError(
                        "%s module %s is outside the generated-root contract; "
                        "run make gen-env to regenerate the root"
                        % (path, current)
                    )
                if current in sources:
                    raise RuntimeError(
                        "%s contains duplicate module %s"
                        % (env_dir, current)
                    )
                sources[current] = source
                current = None
                source = None
                items_seen = False
                module_depth = None
        if depth != 0:
            raise RuntimeError("%s has unbalanced braces" % path)
    return sources


def _local_module_path(env_dir, source):
    if not source:
        return None
    if os.path.isabs(source):
        return os.path.normpath(source)
    if source.startswith(("./", "../")):
        return os.path.normpath(os.path.join(env_dir, source))
    return None


def _module_fingerprints(env_dir, member_types):
    sources = _root_module_sources(env_dir)
    out = []
    for resource_type in sorted(member_types):
        source = sources.get(resource_type)
        if source is None:
            raise RuntimeError(
                "%s member %s has no module source; run make gen-env to "
                "regenerate the root" % (env_dir, resource_type)
            )
        path = _local_module_path(env_dir, source)
        if path is None:
            raise RuntimeError(
                "%s member %s module source %r is not local; generated roots "
                "must use local module sources"
                % (env_dir, resource_type, source)
            )
        out.append({
            "files": _tree_fingerprints(path),
            "local": True,
            "present": os.path.isdir(path),
            "resource_type": resource_type,
            "source": source,
        })
    return out


def _backend_fingerprint(backend_config, backend_key):
    if not backend_config:
        return None
    path = os.path.abspath(backend_config)
    present = os.path.isfile(path)
    out = {
        "key": backend_key,
        "present": present,
    }
    if present:
        out["sha256"] = _file_sha256(path)
    return out


def _backend_state_key(tenant, root_label, backend_config):
    if not backend_config:
        return None
    return "%s/%s.tfstate" % (tenant, root_label)


def _var_file_fingerprints(var_files):
    # Key by basename, not absolute path: fingerprints must not vary by
    # checkout location, and member config basenames are unique per root.
    out = []
    for path in sorted(var_files, key=os.path.basename):
        if os.path.isfile(path):
            out.append((os.path.basename(path), _file_sha256(path)))
    return out


def _current_var_files(tenant, member_types):
    out = []
    for resource_type in sorted(member_types):
        var_file = config_file(tenant, resource_type)
        if os.path.exists(var_file):
            out.append(var_file)
    return out


def _plan_sources_sha256(env_dir, var_files, member_types,
                         backend_config=None, backend_key=None):
    payload = {
        "backend": _backend_fingerprint(backend_config, backend_key),
        "member_types": sorted(member_types),
        "modules": _module_fingerprints(env_dir, member_types),
        "root_tf": _root_tf_fingerprints(env_dir),
        "var_files": _var_file_fingerprints(var_files),
    }
    text = json.dumps(payload, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def _init_sources_sha256(env_dir, member_types, backend_config=None,
                         backend_key=None):
    payload = {
        "backend": _backend_fingerprint(backend_config, backend_key),
        "modules": _module_fingerprints(env_dir, member_types),
        "root_config": _root_config_fingerprints(env_dir),
    }
    text = json.dumps(payload, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def _plan_fingerprint(env_dir, var_files, member_types,
                      backend_config=None, backend_key=None):
    return {
        "version": PLAN_FINGERPRINT_VERSION,
        "sha256": _plan_sources_sha256(
            env_dir,
            var_files,
            member_types,
            backend_config=backend_config,
            backend_key=backend_key,
        ),
    }


def _write_plan_fingerprint(env_dir, var_files, member_types,
                            backend_config=None, backend_key=None):
    data = _plan_fingerprint(
        env_dir,
        var_files,
        member_types,
        backend_config=backend_config,
        backend_key=backend_key,
    )
    return _write_plan_fingerprint_data(env_dir, data)


def _write_plan_fingerprint_data(env_dir, data):
    path = _plan_fingerprint_path(env_dir)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, sort_keys=True)
        f.write("\n")
    return path


def _load_plan_fingerprint(env_dir):
    path = _plan_fingerprint_path(env_dir)
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise ValueError("%s must contain a JSON object" % path)
    return data


def _stale_plan_error(env_dir, detail=None):
    message = STALE_PLAN_MESSAGE % env_dir
    if detail:
        message += " (%s)" % detail
    return message


def _assert_saved_plan_fresh(env_dir, tenant, member_types, root_label=None,
                             backend_config=None):
    path = _plan_fingerprint_path(env_dir)
    if not os.path.exists(path):
        raise RuntimeError(
            _stale_plan_error(env_dir, MISSING_PLAN_FINGERPRINT_DETAIL)
        )
    try:
        saved = _load_plan_fingerprint(env_dir)
    except (IOError, OSError, ValueError) as exc:
        raise RuntimeError(_stale_plan_error(env_dir, str(exc)))
    current = _plan_fingerprint(
        env_dir,
        _current_var_files(tenant, member_types),
        member_types,
        backend_config=backend_config,
        backend_key=_backend_state_key(
            tenant, root_label or os.path.basename(env_dir), backend_config),
    )
    if saved != current:
        raise RuntimeError(_stale_plan_error(env_dir))
    return saved


def _remove_saved_plan_artifacts(env_dir):
    for name in ("tfplan", PLAN_FINGERPRINT):
        path = os.path.join(env_dir, name)
        if os.path.exists(path):
            os.remove(path)


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


def _rule_lane_guidance(plan, resource_type, rules, candidate_ok, annotate):
    from engine import guidance_paths
    from engine import lanes

    provider = packs.provider_of(resource_type)
    by_path = {}
    for rule in rules:
        if rule.get("action") != "manual_review_required":
            continue
        if not lanes.rule_matches(rule, provider, resource_type):
            continue
        by_path.setdefault(lanes.rule_plan_path(rule), []).append(rule)
    if not by_path:
        return []

    annotations = []
    for candidate in guidance_paths.guidance_candidate_paths(plan, resource_type):
        formatted = candidate["formatted_path"]
        for rule in by_path.get(formatted, []):
            if not candidate_ok(rule, candidate):
                continue
            annotations.append(annotate(rule, candidate, formatted))
    return annotations


def _absent_default_guidance(plan, resource_type):
    from engine import adoption_guidance

    def candidate_ok(rule, candidate):
        return _absent_default_observed_value_matches(
            rule, candidate["before"], candidate["path"])

    def annotate(rule, candidate, formatted):
        return adoption_guidance.absent_default_annotation(
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
        )

    return _rule_lane_guidance(
        plan, resource_type,
        packs.absent_default_rules(packs.provider_of(resource_type)),
        candidate_ok, annotate)


def _dynamic_schema_guidance(plan, resource_type):
    from engine import adoption_guidance

    def annotate(rule, candidate, formatted):
        return adoption_guidance.dynamic_schema_annotation(
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
        )

    return _rule_lane_guidance(
        plan, resource_type,
        packs.dynamic_schema_rules(packs.provider_of(resource_type)),
        lambda rule, candidate: True, annotate)


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


def _write_json_contract(path, data):
    if not path:
        return
    text = json.dumps(data, indent=2, sort_keys=True) + "\n"
    if path == "-":
        sys.stdout.write(text)
        return
    target = os.path.abspath(path)
    directory = os.path.dirname(target)
    os.makedirs(directory, exist_ok=True)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(
                mode="w", encoding="utf-8", dir=directory,
                prefix=".infrawright-report-", delete=False) as f:
            temporary = f.name
            f.write(text)
        os.replace(temporary, target)
    finally:
        if temporary and os.path.exists(temporary):
            os.remove(temporary)


def _new_assessment_report(mode, opts):
    policy = opts.get("policy") if mode == "assert-adoptable" else None
    return {
        "kind": PLAN_ASSESSMENT_CONTRACT,
        "schema_version": PLAN_ASSESSMENT_SCHEMA_VERSION,
        "mode": mode,
        "request": {
            "tenant": opts.get("tenant"),
            "selectors": list(opts.get("selectors") or []),
            "policy": policy,
            "policy_sha256": None,
        },
        "summary": {
            "status": "error",
            "checked": 0,
            "clean": 0,
            "tolerated": 0,
            "blocked": 0,
        },
        "roots": [],
        "stale_policy": [],
    }


def _finding_resource_types(plan):
    out = {}
    for source, key in (
            ("resource_changes", "resource_changes"),
            ("resource_drift", "resource_drift")):
        for change in plan.get(key) or []:
            out[(source, change.get("address"))] = change.get("type")
    return out


def _normalize_findings(plan, findings):
    from engine.paths import format_path

    resource_types = _finding_resource_types(plan)
    out = []
    for finding in findings:
        source = finding.get("source")
        address = finding.get("address")
        out.append({
            "status": finding.get("status"),
            "source": source,
            "address": address,
            "resource_type": resource_types.get((source, address)),
            "actions": list(finding.get("actions") or []),
            "paths": [
                format_path(path) for path in (finding.get("paths") or [])
            ],
        })
    return out


def _matching_guidance(findings, annotations):
    from engine import adoption_guidance
    from engine.plan_eval import BLOCKED

    matched = []
    for finding in findings:
        if finding.get("status") != BLOCKED:
            continue
        for path in finding.get("paths") or []:
            matched.extend(adoption_guidance.annotations_for_finding_path(
                annotations, finding, path
            ))
    out = []
    seen = set()
    for annotation in adoption_guidance.sort_annotations(matched):
        normalized = dict(
            (key, value) for key, value in annotation.items()
            if key != "sort_key"
        )
        marker = json.dumps(normalized, sort_keys=True, separators=(",", ":"))
        if marker in seen:
            continue
        seen.add(marker)
        out.append(normalized)
    return out


def _append_root_assessment(report, tenant, label, member_types, plan, result,
                            plan_sha256, plan_fingerprint, guidance=None):
    report["roots"].append({
        "tenant": tenant,
        "label": label,
        "members": sorted(member_types),
        "status": result["status"],
        "plan": {
            "sha256": plan_sha256,
            "format_version": plan.get("format_version"),
            "terraform_version": plan.get("terraform_version"),
        },
        "plan_fingerprint": plan_fingerprint,
        "findings": _normalize_findings(plan, result["findings"]),
        "guidance": list(guidance or []),
    })


def _capture_assessment_plan(path, tenant, label, member_types,
                             backend_config=None):
    """Read plan JSON while binding it to stable plan/source evidence."""
    saved_fingerprint = _assert_saved_plan_fresh(
        path,
        tenant,
        member_types,
        root_label=label,
        backend_config=backend_config,
    )
    plan_path = os.path.join(path, "tfplan")
    plan_sha256 = _file_sha256(plan_path)
    plan = _show_plan_json(path)
    if _file_sha256(plan_path) != plan_sha256:
        raise RuntimeError(_stale_plan_error(
            path, "saved plan changed while terraform show was reading it"
        ))
    evidence = {
        "path": path,
        "tenant": tenant,
        "label": label,
        "member_types": list(member_types),
        "backend_config": backend_config,
        "plan_sha256": plan_sha256,
        "plan_fingerprint": saved_fingerprint,
    }
    _recheck_assessment_plan(evidence)
    return plan, evidence


def _recheck_assessment_plan(evidence):
    """Fail if any plan evidence changed after it was classified."""
    path = evidence["path"]
    plan_path = os.path.join(path, "tfplan")
    if _file_sha256(plan_path) != evidence["plan_sha256"]:
        raise RuntimeError(_stale_plan_error(
            path, "saved plan changed during assessment"
        ))
    saved_fingerprint = _assert_saved_plan_fresh(
        path,
        evidence["tenant"],
        evidence["member_types"],
        root_label=evidence["label"],
        backend_config=evidence["backend_config"],
    )
    if saved_fingerprint != evidence["plan_fingerprint"]:
        raise RuntimeError(_stale_plan_error(
            path, "plan fingerprint changed during assessment"
        ))
    if _file_sha256(plan_path) != evidence["plan_sha256"]:
        raise RuntimeError(_stale_plan_error(
            path, "saved plan changed during assessment"
        ))


def _recheck_assessment_inputs(evidence, policy_path=None,
                               policy_sha256=None):
    for root_evidence in evidence:
        _recheck_assessment_plan(root_evidence)
    if policy_path and _file_sha256(policy_path) != policy_sha256:
        raise RuntimeError(
            "%s changed during saved-plan assessment" % policy_path
        )


def _finish_assessment_report(report, clean, tolerated, blocked):
    from engine.plan_eval import BLOCKED, CLEAN, TOLERATED

    status = CLEAN
    if blocked:
        status = BLOCKED
    elif tolerated:
        status = TOLERATED
    report["summary"] = {
        "status": status,
        "checked": clean + tolerated + blocked,
        "clean": clean,
        "tolerated": tolerated,
        "blocked": blocked,
    }


def _write_assessment_error(report, path, kind, message):
    roots = report.get("roots") or []
    report["summary"] = {
        "status": "error",
        "checked": len(roots),
        "clean": sum(1 for root in roots if root.get("status") == "clean"),
        "tolerated": sum(
            1 for root in roots
            if root.get("status") == "clean_with_tolerated_drift"
        ),
        "blocked": sum(
            1 for root in roots if root.get("status") == "blocked"
        ),
    }
    report["error"] = {"kind": kind, "message": message}
    _write_json_contract(path, report)


def cmd_stage_imports(opts):
    tenant = opts["tenant"]
    validate_tenant(tenant)
    staged = 0
    sources = 0
    for label, member_types in _selected_root_specs(opts["selectors"]):
        env_dir = env_root_for_label(tenant, label)
        for resource_type in member_types:
            for source in (
                    imports_file(tenant, resource_type),
                    moves_file(tenant, resource_type),
            ):
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
                    _check_backend(env_dir, label, opts["backend_config"])
                    _check_call(
                        _init_args(
                            env_dir, tenant, label,
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
    for _tenant, _label, path, member_types in selected_env_roots(
            tenant, opts["selectors"]):
        for resource_type in member_types:
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
    for _tenant, label, path, member_types in selected_env_roots(
            tenant, opts["selectors"]):
        derived_members = sorted(set(member_types) & skipped_derived)
        if derived_members:
            sys.stderr.write(
                "skip %s (IMPORTS_ONLY: derived/non-importable member %s)\n"
                % (label, ", ".join(derived_members))
            )
            continue
        if opts["save"]:
            _remove_saved_plan_artifacts(path)
        var_files = []
        missing = []
        for resource_type in member_types:
            var_file = config_file(tenant, resource_type)
            if os.path.exists(var_file):
                var_files.append(var_file)
            else:
                missing.append(var_file)
        if not var_files:
            for var_file in missing:
                sys.stderr.write("skip %s (no %s)\n" % (label, var_file))
            continue
        if missing:
            raise RuntimeError(
                "root %s is missing member config(s): %s - run "
                "make transform or make adopt for every group member first"
                % (label, ", ".join(missing))
            )
        _check_backend(path, label, opts["backend_config"])
        sys.stderr.write("== plan %s\n" % label)
        backend_key = _backend_state_key(
            tenant, label, opts["backend_config"])
        init_sources_before = None
        if opts["save"]:
            init_sources_before = _init_sources_sha256(
                path,
                member_types,
                backend_config=opts["backend_config"],
                backend_key=backend_key,
            )
        _check_call(
            _init_args(
                path, tenant, label, backend_config=opts["backend_config"]
            ),
            stdout=subprocess.DEVNULL,
        )
        if opts["save"] and _init_sources_sha256(
                path,
                member_types,
                backend_config=opts["backend_config"],
                backend_key=backend_key) != init_sources_before:
            _remove_saved_plan_artifacts(path)
            raise RuntimeError(
                "%s: init inputs changed while init was running - "
                "re-run make plan SAVE=1" % path
            )
        args = [terraform(), "-chdir=" + path, "plan", "-input=false"]
        for var_file in var_files:
            args.append("-var-file=" + os.path.abspath(var_file))
        planned_fingerprint = None
        if opts["save"]:
            planned_fingerprint = _plan_fingerprint(
                path,
                var_files,
                member_types,
                backend_config=opts["backend_config"],
                backend_key=backend_key,
            )
            args.append("-out=tfplan")
        _check_call(args)
        if opts["save"]:
            current_fingerprint = _plan_fingerprint(
                path,
                var_files,
                member_types,
                backend_config=opts["backend_config"],
                backend_key=backend_key,
            )
            if current_fingerprint != planned_fingerprint:
                _remove_saved_plan_artifacts(path)
                raise RuntimeError(_stale_plan_error(
                    path, "plan inputs changed while the plan was running"))
            _write_plan_fingerprint_data(path, planned_fingerprint)
        planned += 1
    if planned == 0:
        raise RuntimeError(
            "no roots planned for TENANT=%s (missing env roots or config?)" % tenant
        )
    return 0


def cmd_assert_clean(opts):
    from engine.plan_eval import CLEAN, classify_plan

    report = _new_assessment_report("assert-clean", opts)
    evidence = []
    checked = 0
    dirty = 0
    clean = 0
    try:
        for tenant, label, path, member_types in selected_env_roots(
                opts.get("tenant"), opts["selectors"], require_plan=True):
            plan, root_evidence = _capture_assessment_plan(
                path, tenant, label, member_types,
                backend_config=opts.get("backend_config"),
            )
            result = classify_plan(plan)
            changes = sum(
                1 for finding in result["findings"]
                if finding["status"] != CLEAN
            )
            checked += 1
            if changes:
                sys.stderr.write(
                    "NOT CLEAN: %s/%s plan contains %d change(s) beyond imports\n"
                    % (tenant, label, changes)
                )
                dirty += 1
            else:
                clean += 1
            _append_root_assessment(
                report, tenant, label, member_types, plan, result,
                root_evidence["plan_sha256"],
                root_evidence["plan_fingerprint"],
            )
            evidence.append(root_evidence)
    except (OSError, RuntimeError, ValueError,
            subprocess.CalledProcessError) as exc:
        _write_assessment_error(
            report, opts.get("report"), "assessment_error", str(exc)
        )
        raise
    if checked == 0:
        message = "no saved plans to check - run make plan SAVE=1 first"
        _write_assessment_error(
            report, opts.get("report"), "no_saved_plans", message
        )
        raise RuntimeError(message)
    try:
        _recheck_assessment_inputs(evidence)
    except (OSError, RuntimeError, ValueError) as exc:
        _write_assessment_error(
            report, opts.get("report"), "assessment_error", str(exc)
        )
        raise
    _finish_assessment_report(report, clean, 0, dirty)
    _write_json_contract(opts.get("report"), report)
    if dirty:
        raise RuntimeError(
            "tenant moved since fetch (or transform disagrees) - do not auto-merge"
        )
    if report["summary"]["status"] != CLEAN:
        raise RuntimeError("assert-clean produced an unexpected classification")
    sys.stderr.write("all %d saved plan(s) clean (no-op/imports only)\n" % checked)
    return 0


def cmd_assert_adoptable(opts):
    from engine.drift_policy import DriftPolicy
    from engine.plan_eval import BLOCKED, CLEAN, TOLERATED, classify_plan

    report = _new_assessment_report("assert-adoptable", opts)
    policy_path = opts.get("policy")
    policy_sha256 = None
    try:
        if policy_path:
            with open(policy_path, "rb") as f:
                policy_bytes = f.read()
            policy_sha256 = hashlib.sha256(policy_bytes).hexdigest()
            report["request"]["policy_sha256"] = policy_sha256
            policy = DriftPolicy(
                json.loads(policy_bytes.decode("utf-8")), source=policy_path
            )
        else:
            policy = DriftPolicy(None)
    except (OSError, ValueError) as exc:
        _write_assessment_error(
            report, opts.get("report"), "policy_error", str(exc)
        )
        raise
    evidence = []
    checked = 0
    clean = 0
    blocked = 0
    tolerated = 0
    checked_types = set()
    try:
        for tenant, label, path, member_types in selected_env_roots(
                opts.get("tenant"), opts["selectors"], require_plan=True):
            plan, root_evidence = _capture_assessment_plan(
                path, tenant, label, member_types,
                backend_config=opts.get("backend_config"),
            )
            result = classify_plan(plan, policy=policy)
            checked += 1
            checked_types.update(member_types)
            guidance_annotations = []
            matched_guidance = []
            if result["status"] == BLOCKED:
                blocked += 1
                sys.stderr.write("BLOCKED: %s/%s\n" % (tenant, label))
                for resource_type in member_types:
                    guidance_annotations.extend(
                        _guidance_annotations(plan, resource_type))
                matched_guidance = _matching_guidance(
                    result["findings"], guidance_annotations
                )
                _print_findings(
                    result["findings"],
                    guidance_annotations=guidance_annotations,
                )
            elif result["status"] == TOLERATED:
                tolerated += 1
                sys.stderr.write("TOLERATED: %s/%s\n" % (tenant, label))
                _print_findings(result["findings"])
            elif result["status"] == CLEAN:
                clean += 1
            _append_root_assessment(
                report, tenant, label, member_types, plan, result,
                root_evidence["plan_sha256"],
                root_evidence["plan_fingerprint"],
                guidance=matched_guidance,
            )
            evidence.append(root_evidence)
    except (OSError, RuntimeError, ValueError,
            subprocess.CalledProcessError) as exc:
        _write_assessment_error(
            report, opts.get("report"), "assessment_error", str(exc)
        )
        raise
    if checked == 0:
        message = "no saved plans to check - run make plan SAVE=1 first"
        _write_assessment_error(
            report, opts.get("report"), "no_saved_plans", message
        )
        raise RuntimeError(message)
    for rt, mode, path in policy.stale_entries(
            resource_types=checked_types, modes=("plan_tolerate",)):
        report["stale_policy"].append({
            "resource_type": rt,
            "mode": mode,
            "path": path,
        })
        sys.stderr.write(
            "STALE DRIFT POLICY: %s %s %s matched no path\n"
            % (rt, mode, path)
        )
    try:
        _recheck_assessment_inputs(
            evidence,
            policy_path=policy_path,
            policy_sha256=policy_sha256,
        )
    except (OSError, RuntimeError, ValueError) as exc:
        _write_assessment_error(
            report, opts.get("report"), "assessment_error", str(exc)
        )
        raise
    _finish_assessment_report(report, clean, tolerated, blocked)
    _write_json_contract(opts.get("report"), report)
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
    for _tenant, _label, path, _member_types in selected_env_roots(
            opts.get("tenant"), opts["selectors"]):
        removed_any = False
        for name in ("tfplan", PLAN_FINGERPRINT):
            plan = os.path.join(path, name)
            if os.path.exists(plan):
                os.remove(plan)
                sys.stderr.write("removed %s\n" % plan)
                removed_any = True
        if removed_any:
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
    for tenant, label, path, member_types in selected_env_roots(
            opts.get("tenant"), opts["selectors"], require_plan=True):
        _assert_saved_plan_fresh(
            path,
            tenant,
            member_types,
            root_label=label,
            backend_config=opts["backend_config"],
        )
        sys.stderr.write("== apply %s/%s\n" % (tenant, label))
        _check_backend(path, label, opts["backend_config"])
        _check_call(
            _init_args(
                path, tenant, label, backend_config=opts["backend_config"]
            ),
            stdout=subprocess.DEVNULL,
        )
        _assert_saved_plan_fresh(
            path,
            tenant,
            member_types,
            root_label=label,
            backend_config=opts["backend_config"],
        )
        plan = _show_plan_json(path)
        result = _classify_apply_plan(plan, policy)
        destroys = _destroy_count(plan)
        if result["status"] == BLOCKED and destroys and not opts["allow_destroy"]:
            raise RuntimeError(
                "%s/%s saved plan destroys (or replaces) %d resource(s) - refused"
                % (tenant, label, destroys)
            )
        if result["status"] == BLOCKED and not opts["allow_plan_changes"]:
            raise RuntimeError(
                "%s/%s saved plan is blocked by untolerated changes; refused. "
                "Run assert-adoptable for review, pass POLICY=<file> for "
                "explicit tolerated drift, or use --allow-plan-changes only as "
                "a broad unsafe override."
                % (tenant, label)
            )
        if result["status"] == TOLERATED:
            sys.stderr.write(
                "TOLERATED: %s/%s saved plan has consumer-tolerated drift\n"
                % (tenant, label)
            )
        elif result["status"] == BLOCKED:
            sys.stderr.write(
                "WARNING: applying BLOCKED %s/%s saved plan because "
                "--allow-plan-changes was set\n" % (tenant, label)
            )
        _check_call([terraform(), "-chdir=" + path, "apply", "-input=false", "tfplan"])
        _remove_saved_plan_artifacts(path)
        applied += 1
    if applied == 0:
        raise RuntimeError("no saved plans found - run make plan SAVE=1 first")
    return 0


def _classify_apply_plan(plan, policy):
    from engine.plan_eval import classify_plan

    return classify_plan(plan, policy=policy)


def _parse(argv, allow_optional_tenant=False, allow_report=False):
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
        "report": None,
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
        elif arg == "--report":
            if not allow_report:
                raise ValueError("unknown option --report")
            i += 1
            if i >= len(argv):
                raise ValueError("--report requires a value")
            if opts["report"] is not None:
                raise ValueError("--report may be specified only once")
            opts["report"] = argv[i]
        elif arg.startswith("-"):
            raise ValueError("unknown option %s" % arg)
        else:
            opts["selectors"].append(arg)
        i += 1
    if not allow_optional_tenant and opts["tenant"] is None:
        raise ValueError("--tenant is required")
    if opts["tenant"] is not None:
        validate_tenant(opts["tenant"])
    return opts


def _parse_roots(argv):
    opts = {"tenant": None, "selectors": [], "json": False}
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--tenant":
            i += 1
            if i >= len(argv):
                raise ValueError("--tenant requires a value")
            if opts["tenant"] is not None:
                raise ValueError("--tenant may be specified only once")
            opts["tenant"] = argv[i]
        elif arg == "--json":
            if opts["json"]:
                raise ValueError("--json may be specified only once")
            opts["json"] = True
        elif arg.startswith("-"):
            raise ValueError("unknown option %s" % arg)
        else:
            opts["selectors"].append(arg)
        i += 1
    if opts["tenant"] is not None:
        validate_tenant(opts["tenant"])
    return opts


def _parse_resources(argv):
    order = "sorted"
    selectors = []
    for arg in argv:
        if arg.startswith("--order="):
            order = arg.split("=", 1)[1]
            if order != "references":
                raise ValueError(
                    "resources --order must be references (got %r)" % order
                )
        elif arg.startswith("-"):
            raise ValueError("unknown option %s" % arg)
        else:
            selectors.append(arg)
    return order, selectors


def _usage():
    return (
        "usage: python -m engine.ops <resources|roots|stage-imports|unstage-imports|plan|"
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
            order, selectors = _parse_resources(rest)
            resource_types = expand_resources(selectors)
            if order == "references":
                resource_types = reference_order(resource_types)
            for resource_type in resource_types:
                sys.stdout.write(resource_type + "\n")
            return 0
        if command == "roots":
            return cmd_roots(_parse_roots(rest))
        if command == "stage-imports":
            return cmd_stage_imports(_parse(rest))
        if command == "unstage-imports":
            return cmd_unstage_imports(_parse(rest))
        if command == "plan":
            return cmd_plan(_parse(rest))
        if command == "assert-clean":
            return cmd_assert_clean(_parse(
                rest, allow_optional_tenant=True, allow_report=True
            ))
        if command == "assert-adoptable":
            return cmd_assert_adoptable(_parse(
                rest, allow_optional_tenant=True, allow_report=True
            ))
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
