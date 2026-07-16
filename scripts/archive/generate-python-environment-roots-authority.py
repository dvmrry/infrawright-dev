"""Reproduce the frozen environment-root authority from its pinned baseline."""

import hashlib
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
import tempfile
import unicodedata


BASELINE = "55f6189efe888564b515a6c2f5a505348f921f6e"
if len(sys.argv) != 3:
    raise SystemExit("usage: generate-python-environment-roots-authority.py BASELINE_WORKTREE OUTPUT")
BASE = pathlib.Path(sys.argv[1]).resolve()
OUTPUT = pathlib.Path(sys.argv[2]).resolve()
head = subprocess.run(
    ["git", "-C", str(BASE), "rev-parse", "HEAD"],
    check=True,
    capture_output=True,
    text=True,
).stdout.strip()
if head != BASELINE:
    raise SystemExit("baseline worktree must be at %s (got %s)" % (BASELINE, head))
if platform.python_version() != "3.13.13" or unicodedata.unidata_version != "15.1.0":
    raise SystemExit(
        "authority requires CPython 3.13.13 / UCD 15.1.0 (got %s / %s)"
        % (platform.python_version(), unicodedata.unidata_version)
    )

os.chdir(BASE)
sys.path.insert(0, str(BASE))
from engine.gen_env import generate_env  # noqa: E402


def write_json(path, value):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def snapshot_tree(root):
    root = pathlib.Path(root)
    return {
        str(path.relative_to(root)): path.read_text(encoding="utf-8")
        for path in sorted(root.rglob("*"))
        if path.is_file() and not path.is_symlink()
    }


def run_case(name, deployment, selectors, prepare=None, backend=None, tenant="tenant"):
    workspace = pathlib.Path(tempfile.mkdtemp(prefix="iw-env-authority-"))
    try:
        deployment_path = workspace / "deployment.json"
        output = workspace / "generated"
        write_json(deployment_path, {
            "overlay": str(workspace),
            "module_dir": str(workspace / "modules"),
            **deployment,
        })
        if prepare:
            prepare(workspace)
        old = dict(os.environ)
        try:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = str(deployment_path)
            os.environ["INFRAWRIGHT_PACK_PROFILE"] = str(BASE / "packsets/full.json")
            generate_env(
                tenant,
                out_root=str(output),
                fmt=True,
                backend=backend,
                selectors=selectors,
            )
        finally:
            os.environ.clear()
            os.environ.update(old)
        return {"name": name, "tree": snapshot_tree(output)}
    finally:
        shutil.rmtree(workspace)


def prepare_ungrouped(workspace):
    write_json(workspace / "config/tenant/zia_url_categories.auto.tfvars.json", {
        "items": {"example": {
            "configured_name": "Example",
            "custom_category": True,
            "urls": [],
        }},
    })


def prepare_grouped(workspace):
    config = workspace / "config/tenant"
    write_json(config / "zpa_application_segment.auto.tfvars.json", {
        "zpa_application_segment_items": {"app": {"segment_group_id": "sg-1"}},
    })
    write_json(config / "zpa_segment_group.auto.tfvars.json", {
        "zpa_segment_group_items": {"group": {
            "description": "Group", "enabled": True, "name": "Group",
        }},
    })
    write_json(config / "zpa_application_segment.generated.expressions.json", {
        "resources": {"zpa_application_segment.app": {
            "segment_group_id": {
                "expression": 'module.zpa_segment_group.items["generated"].id',
            },
        }},
    })
    write_json(config / "zpa_application_segment.expressions.json", {
        "resources": {"zpa_application_segment.app": {
            "segment_group_id": {
                "expression": 'module.zpa_segment_group.items["operator"].id',
            },
        }},
    })


def prepare_hcl(workspace):
    config = workspace / "config/tenant"
    config.mkdir(parents=True, exist_ok=True)
    (config / "zpa_segment_group.auto.tfvars").write_text(
        "zpa_segment_group_items = {}\n", encoding="utf-8"
    )
    write_json(config / "zpa_segment_group.expressions.json", {
        "resources": {"zpa_segment_group.group": {
            "description": {"expression": "var.description"},
        }},
    })


representative = [
    run_case("ungrouped-json", {}, ["zia_url_categories"], prepare_ungrouped),
    run_case(
        "grouped-bound-azurerm",
        {"roots": {"zpa": {"bind_references": True, "groups": {
            "zpa_custom": ["zpa_application_segment", "zpa_segment_group"],
        }}}},
        ["zpa_application_segment"],
        prepare_grouped,
        backend="azurerm",
    ),
    run_case(
        "singleton-hcl",
        {
            "tfvars_format": "hcl",
            "roots": {"zpa": {"groups": {"zpa_solo": ["zpa_segment_group"]}}},
        },
        ["zpa_segment_group"],
        prepare_hcl,
    ),
    run_case(
        "slug-root",
        {"roots": {"zia": {"strategy": "slug"}}},
        ["zia_url_categories"],
    ),
]

full = run_case("full-profile", {}, [], tenant="full-profile-parity")
manifest = [
    {
        "path": path,
        "length": len(text.encode("utf-8")),
        "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
    }
    for path, text in sorted(full["tree"].items())
]


def dangling_case():
    workspace = pathlib.Path(tempfile.mkdtemp(prefix="iw-env-dangling-authority-"))
    try:
        deployment_path = workspace / "deployment.json"
        output = workspace / "generated"
        write_json(deployment_path, {
            "module_dir": str(workspace / "modules"),
            "overlay": str(workspace),
            "roots": {"zia": {"bind_references": True}},
        })
        config = workspace / "config/tenant"
        config.mkdir(parents=True)
        config_links = {}
        for name in (
            "zia_url_categories.auto.tfvars.json",
            "zia_url_categories.expressions.json",
            "zia_url_categories.generated.expressions.json",
        ):
            target = "missing-" + name
            (config / name).symlink_to(target)
            config_links[name] = target
        expression = output / "tenant/zia_url_categories/expression_bindings.tf"
        backend = output / "tenant/.backend"
        expression.parent.mkdir(parents=True)
        expression.symlink_to("missing-expression-bindings.tf")
        backend.symlink_to("missing-backend")
        old = dict(os.environ)
        try:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = str(deployment_path)
            os.environ["INFRAWRIGHT_PACK_PROFILE"] = str(BASE / "packsets/full.json")
            generate_env(
                "tenant",
                out_root=str(output),
                fmt=True,
                selectors=["zia_url_categories"],
            )
        finally:
            os.environ.clear()
            os.environ.update(old)
        return {
            "tree": snapshot_tree(output),
            "config_symlinks": config_links,
            "output_symlinks": {
                "tenant/.backend": os.readlink(backend),
                "tenant/zia_url_categories/expression_bindings.tf": os.readlink(expression),
            },
        }
    finally:
        shutil.rmtree(workspace)


fixture = {
    "kind": "infrawright.python-environment-roots-authority",
    "version": 1,
    "baseline": BASELINE,
    "authority": {"implementation": "cpython", "python": "3.13.13", "unicode": "15.1.0"},
    "source_blobs": {
        "python_gen_env": "dea7692fb6fb8c634e59635083bd416fb65f1650",
        "python_expression_bindings": "d80478081d66e29641254dd105e5b8f2602bb41f",
        "python_artifacts": "ad53594534e65f8bcd018757f409982039a43983",
        "python_deployment": "9da5237c915102eef937341d399ba97a323db9ca",
        "python_packs": "e128e51381aac05ba141c63f31c6f9831d84661d",
        "python_lookup": "596c6c010827aab2a8b39a3a9d5cfb2f8061883f",
        "python_root_catalog": "0cf89ab18487450f1b9d59d4efc22c3340c8bd7e",
        "python_test_gen_env": "ae3042cee968171b3213417dee7e7266123875de",
        "python_test_group_bindings": "778006a669222e48bd56855ed2e579bb65f8e6e7",
        "node_environment_generator": "ff20e50dd48a4956452e3bffc8b1c4e222ba5fcc",
        "packset_full": "ddea3183e5e9eaadc102a14544da27cbf59d09ea",
        "registry_zia": "0aa87786b50e36a03c09304aa9ce63a0c4641e7e",
        "registry_zpa": "17be526eefecdeeed682ed6f90d35a26b66d185b",
        "registry_zcc": "3e23f6a3471c83860988a7353b4f4d59feafd52c",
        "registry_ztc": "76dbb464a9ae10e3bed96f504019fb25731edaf5",
    },
    "normalization": "none",
    "producing_command": (
        "PYTHON=python3.13 python3.13 "
        "scripts/archive/generate-python-environment-roots-authority.py "
        "<baseline-worktree> <output>"
    ),
    "representative_cases": representative,
    "full_profile": {"file_count": len(manifest), "manifest": manifest},
    "dangling_symlinks": dangling_case(),
}

OUTPUT.parent.mkdir(parents=True, exist_ok=True)
OUTPUT.write_text(
    json.dumps(fixture, ensure_ascii=True, indent=2, sort_keys=True) + "\n",
    encoding="utf-8",
)
print(OUTPUT)
print(hashlib.sha256(OUTPUT.read_bytes()).hexdigest())
print(len(OUTPUT.read_bytes()), len(manifest))
