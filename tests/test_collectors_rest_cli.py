import os
import subprocess
import sys
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


class RestCollectorCliTest(unittest.TestCase):
    def test_no_args_reports_engine_module_usage(self):
        proc = subprocess.run(
            [sys.executable, "-m", "engine.collectors.rest"],
            cwd=ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )

        output = proc.stdout + proc.stderr
        self.assertEqual(2, proc.returncode)
        self.assertIn("usage:", output)
        self.assertIn("python -m engine.collectors.rest", output)
        self.assertNotIn("python -m collectors.rest", output)


if __name__ == "__main__":
    unittest.main()
