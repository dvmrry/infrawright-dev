import os
import subprocess
import tempfile
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _run_make(args, **kwargs):
    cmd = ["make", "-C", ROOT] + list(args)
    proc = subprocess.run(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        universal_newlines=True,
        **kwargs
    )
    if proc.returncode != 0:
        raise AssertionError(
            "make failed (%s)\nstdout:\n%s\nstderr:\n%s"
            % (" ".join(cmd), proc.stdout, proc.stderr)
        )
    return proc


class MakefileOverlayTest(unittest.TestCase):
    def test_overlay_makefile_target_can_be_called(self):
        with tempfile.TemporaryDirectory() as td:
            sentinel = os.path.join(td, "sentinel.txt")
            with open(os.path.join(td, "Makefile"), "w", encoding="utf-8") as f:
                f.write(
                    ".PHONY: overlay-sentinel\n"
                    "overlay-sentinel:\n"
                    "\t@printf 'overlay-ok\\n' > \"$(SENTINEL)\"\n"
                )

            _run_make(["OVERLAY=%s" % td, "SENTINEL=%s" % sentinel, "overlay-sentinel"])

            with open(sentinel, encoding="utf-8") as f:
                self.assertEqual(f.read(), "overlay-ok\n")

    def test_missing_overlay_makefile_does_not_break_core_targets(self):
        with tempfile.TemporaryDirectory() as td:
            missing_overlay = os.path.join(td, "missing")
            proc = _run_make(["OVERLAY=%s" % missing_overlay, "-n", "test"])
            self.assertIn("python3 -m unittest discover", proc.stdout)

    def test_check_demo_reenters_demo_overlay(self):
        with tempfile.TemporaryDirectory() as td:
            missing_overlay = os.path.join(td, "missing")
            proc = _run_make(["OVERLAY=%s" % missing_overlay, "-n", "check-demo"])
            self.assertIn("OVERLAY=demo", proc.stdout)
            self.assertIn(" demo > /dev/null", proc.stdout)
            self.assertIn("INFRAWRIGHT_DEPLOYMENT=\"demo/deployment.json\"", proc.stdout)

    def test_demo_contract_reenters_demo_overlay(self):
        with tempfile.TemporaryDirectory() as td:
            missing_overlay = os.path.join(td, "missing")
            proc = _run_make(["OVERLAY=%s" % missing_overlay, "-n", "demo-contract"])
            self.assertIn("OVERLAY=demo", proc.stdout)
            self.assertIn(" demo > /dev/null", proc.stdout)
            self.assertIn("INFRAWRIGHT_DEPLOYMENT=\"demo/deployment.json\"", proc.stdout)
            self.assertIn("engine.gen_module --check-output", proc.stdout)

    def test_check_tfvars_fmt_skips_json_deployment(self):
        with tempfile.TemporaryDirectory() as td:
            dep = os.path.join(td, "deployment.json")
            with open(dep, "w", encoding="utf-8") as f:
                f.write('{"tfvars_format": "json"}\n')
            proc = _run_make([
                "OVERLAY=%s" % os.path.join(td, "missing"),
                "DEPLOYMENT=%s" % dep,
                "check-tfvars-fmt",
            ])
            self.assertIn("skip (json tfvars)", proc.stdout)

    def test_check_runs_tfvars_fmt_after_modules(self):
        with tempfile.TemporaryDirectory() as td:
            missing_overlay = os.path.join(td, "missing")
            proc = _run_make(["OVERLAY=%s" % missing_overlay, "-n", "check"])
            modules = proc.stdout.index("engine.gen_module --check-output")
            tfvars = proc.stdout.index("check-tfvars-fmt")
            self.assertLess(modules, tfvars)


if __name__ == "__main__":
    unittest.main()
