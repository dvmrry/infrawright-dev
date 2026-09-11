#!/usr/bin/env python3
"""Capture managed-resource create plans using Terraform's built-in provider."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    fixture_dir = Path(__file__).resolve().parent
    terraform = shutil.which("terraform")
    if terraform is None:
        raise SystemExit("Terraform 1.15.4 must be on PATH")
    with tempfile.TemporaryDirectory(prefix="iw-managed-create-") as directory:
        workspace = Path(directory)
        shutil.copyfile(fixture_dir / "main.tf", workspace / "main.tf")
        shutil.copytree(fixture_dir / "managed", workspace / "managed")
        cli_config = workspace / "terraform.tfrc"
        cli_config.write_text("disable_checkpoint = true\n", encoding="utf-8")
        environment = {
            key: value for key, value in os.environ.items()
            if not key.startswith("TF_")
        }
        environment.update({
            "TF_CLI_CONFIG_FILE": str(cli_config),
            "TF_IN_AUTOMATION": "1",
            "TF_INPUT": "0",
            "CHECKPOINT_DISABLE": "1",
            "TZ": "UTC",
            "LANG": "C",
        })

        def run(*arguments):
            result = subprocess.run(
                [terraform, *arguments], cwd=workspace, env=environment,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
            )
            return result.stdout

        version = json.loads(run("version", "-json"))["terraform_version"]
        if version != "1.15.4":
            raise SystemExit(f"Expected Terraform 1.15.4, got {version}")
        run("init", "-input=false", "-no-color")
        run("plan", "-input=false", "-no-color", "-out=initial.tfplan")
        initial = run("show", "-json", "initial.tfplan")
        # Only terraform_data is instantiated: this apply writes temporary local
        # state and creates no remote infrastructure or provider connections.
        run("apply", "-input=false", "-no-color", "initial.tfplan")
        run(
            "plan", "-input=false", "-no-color", "-out=mixed.tfplan",
            '-var=items={existing="Existing item",new="New item"}',
        )
        mixed = run("show", "-json", "mixed.tfplan")
        for scenario, data in (("initial_create", initial), ("mixed_create", mixed)):
            target = fixture_dir / scenario
            target.mkdir(exist_ok=True)
            (target / "show.json").write_bytes(data)
            print(f"Captured {scenario} with Terraform {version}")


if __name__ == "__main__":
    main()
