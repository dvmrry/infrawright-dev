import importlib.util
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
GENERATOR_PATH = ROOT / "scripts/archive/generate-reconcile-openapi-authority.py"


def load_generator():
    spec = importlib.util.spec_from_file_location(
        "generate_reconcile_openapi_authority",
        GENERATOR_PATH,
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SourceHashValidationTests(unittest.TestCase):
    def test_modified_registry_fails_validation(self):
        generator = load_generator()
        relative = "packs/zia/registry.json"
        expected = generator.EXPECTED_SOURCE_SHA256[relative]

        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            target = root / relative
            target.parent.mkdir(parents=True)
            target.write_bytes((ROOT / relative).read_bytes() + b"\n")

            with self.assertRaisesRegex(
                SystemExit,
                r"source hash mismatch for packs/zia/registry\.json",
            ):
                generator.validate_source_hashes(
                    root=root,
                    expected_source_sha256={relative: expected},
                    check_git=False,
                )


if __name__ == "__main__":
    unittest.main()
