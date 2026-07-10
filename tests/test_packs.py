"""Conformance tests for the pack contract (engine/packs.py).

These pin the merge / precedence / resolution rules that the single zscaler
pack exercises only incidentally (every merge is a one-element no-op there).
They are the spec a Go port of the pack layer must satisfy. All fixtures are
synthetic tmp packs swapped in via INFRAWRIGHT_PACKS + packs.reset().
"""
import json
import os
import shutil
import tempfile
import unittest

from engine import packs


def _write_pack(root, name, manifest, with_registry=False):
    d = os.path.join(root, name)
    os.makedirs(d)
    with open(os.path.join(d, "pack.json"), "w", encoding="utf-8") as f:
        json.dump(manifest, f)
    if with_registry:
        with open(os.path.join(d, "registry.json"), "w", encoding="utf-8") as f:
            json.dump({}, f)


def _write_text(path, content):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)


def _write_collector(root, pack_name, pack_marker, shared_marker):
    _write_text(os.path.join(root, pack_name, "__init__.py"), "")
    _write_text(
        os.path.join(root, pack_name, "collector.py"),
        "from packs._shared.common import MARKER as SHARED_MARKER\n"
        "PACK_MARKER = %r\n" % pack_marker,
    )
    _write_text(
        os.path.join(root, "_shared", "common", "__init__.py"),
        "MARKER = %r\n" % shared_marker,
    )


class PackContractTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self._prev = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()
        self.addCleanup(self._restore)

    def _restore(self):
        if self._prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self._prev
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_empty_packs_dir_yields_empty_tables(self):
        self.assertEqual(packs.provider_prefixes(), {})
        self.assertEqual(packs.unescape_products(), ())
        self.assertEqual(packs.product_tokens(), [])

    def test_prefix_and_source_merge_across_packs(self):
        _write_pack(self.tmp, "a", {"provider_prefixes": {"a_": "a"},
                                    "provider_sources": {"a": "ns/a"}})
        _write_pack(self.tmp, "b", {"provider_prefixes": {"b_": "b"},
                                    "provider_sources": {"b": "ns/b"}})
        packs.reset()
        self.assertEqual(packs.provider_prefixes(), {"a_": "a", "b_": "b"})
        self.assertEqual(packs.provider_sources(), {"a": "ns/a", "b": "ns/b"})
        self.assertEqual(packs.product_tokens(), ["a", "b"])

    def test_merge_precedence_is_last_pack_wins(self):
        # discovery is sorted by name; "z" merges after "a", so z wins collisions
        _write_pack(self.tmp, "a", {"provider_sources": {"x": "from-a"}})
        _write_pack(self.tmp, "z", {"provider_sources": {"x": "from-z"}})
        packs.reset()
        self.assertEqual(packs.provider_sources()["x"], "from-z")

    def test_unescape_products_is_ordered_tuple(self):
        _write_pack(self.tmp, "a", {"unescape_products": ["a_"]})
        _write_pack(self.tmp, "b", {"unescape_products": ["b_"]})
        packs.reset()
        up = packs.unescape_products()
        self.assertIsInstance(up, tuple)  # str.startswith requires a tuple
        self.assertEqual(up, ("a_", "b_"))

    def test_provider_of_table_value_is_authoritative(self):
        _write_pack(self.tmp, "a", {"provider_prefixes": {"foo_": "fooprovider"}})
        packs.reset()
        self.assertEqual(packs.provider_of("foo_bar"), "fooprovider")

    def test_provider_of_longest_match_wins(self):
        _write_pack(self.tmp, "a", {"provider_prefixes": {"x_": "short",
                                                          "x_long_": "long"}})
        packs.reset()
        self.assertEqual(packs.provider_of("x_long_thing"), "long")
        self.assertEqual(packs.provider_of("x_other"), "short")

    def test_bare_name_strips_longest_matching_provider_prefix(self):
        _write_pack(self.tmp, "a", {"provider_prefixes": {"x_": "short",
                                                          "x_long_": "long"}})
        packs.reset()
        self.assertEqual(packs.bare_name("x_long_thing"), "thing")
        self.assertEqual(packs.bare_name("x_other"), "other")

    def test_bare_name_lstrips_separator_and_falls_back_to_full_type(self):
        _write_pack(self.tmp, "a", {"provider_prefixes": {"foo": "foo"}})
        packs.reset()
        self.assertEqual(packs.bare_name("foo_bar"), "bar")
        self.assertEqual(packs.bare_name("unknown_thing"), "unknown_thing")
        # a type equal to a bare prefix strips to nothing -> fall back to full
        self.assertEqual(packs.bare_name("foo"), "foo")

    def test_provider_of_falls_back_to_split_when_no_prefix(self):
        packs.reset()  # empty packs dir
        self.assertEqual(packs.provider_of("unknown_thing"), "unknown")

    def test_references_and_lookup_sources_merge(self):
        _write_pack(self.tmp, "a", {
            "references": {"r1": {"f": {"referent": "t1", "name_field": "n"}}},
            "lookup_sources": {"t1": {"name_field": "n"}},
        })
        packs.reset()
        self.assertIn("r1", packs.references())
        self.assertIn("t1", packs.lookup_sources())

    def test_provider_config_requirements_merge_and_infer_single_provider(self):
        _write_pack(self.tmp, "a", {
            "provider_prefixes": {"a_": "a"},
            "provider_config": {
                "requirements": [{
                    "id": "disable_label",
                    "setting": "add_label",
                    "value": False,
                    "plan_paths": ["labels.managed"],
                }]
            },
        })
        _write_pack(self.tmp, "b", {
            "provider_prefixes": {"b_": "b"},
            "provider_config": {
                "requirements": [{
                    "id": "other",
                    "provider": "b",
                    "setting": "other_setting",
                    "value": True,
                    "plan_paths": ["settings.enabled"],
                }]
            },
        })
        packs.reset()

        self.assertEqual(
            [req["id"] for req in packs.provider_config_requirements()],
            ["disable_label", "other"],
        )
        a_req = packs.provider_config_requirements("a")[0]
        self.assertEqual(a_req["provider"], "a")
        self.assertEqual(a_req["setting"], "add_label")
        self.assertEqual(
            [req["id"] for req in packs.provider_config_requirements("b")],
            ["other"],
        )

    def test_reset_rediscovers_after_change(self):
        self.assertEqual(packs.provider_prefixes(), {})
        _write_pack(self.tmp, "a", {"provider_prefixes": {"a_": "a"}})
        self.assertEqual(packs.provider_prefixes(), {})  # stale cache until reset
        packs.reset()
        self.assertEqual(packs.provider_prefixes(), {"a_": "a"})

    def test_recursive_runtime_inputs_require_a_component_owner(self):
        os.makedirs(os.path.join(self.tmp, "_shared"))
        os.makedirs(os.path.join(self.tmp, "owned"))
        with open(
                os.path.join(self.tmp, "_shared", "pack.json"),
                "w", encoding="utf-8") as f:
            json.dump({"provider_sources": {"ghost": "example/ghost"}}, f)
        with open(
                os.path.join(self.tmp, "_shared", "registry.json"),
                "w", encoding="utf-8") as f:
            json.dump({"ghost_resource": {"product": "ghost"}}, f)
        for path in (
                os.path.join(self.tmp, "adoption_status.json"),
                os.path.join(self.tmp, "_shared", "adoption_status.json"),
                os.path.join(self.tmp, "owned", "adoption_status.json")):
            with open(path, "w", encoding="utf-8") as f:
                json.dump({"dispositions": {}}, f)
        packs.reset()

        self.assertEqual(
            packs.adoption_status_paths(),
            [os.path.join(self.tmp, "owned", "adoption_status.json")],
        )
        self.assertEqual(packs.provider_sources(), {})
        self.assertEqual(packs.registry_paths(), [])

    def test_external_root_is_authoritative_for_collector_and_shared_imports(self):
        _write_pack(self.tmp, "distribution", {
            "provider_prefixes": {"sample_": "sample"},
            "requires_shared": ["common"],
        })
        _write_collector(self.tmp, "distribution", "pack-one", "shared-one")
        packs.reset()

        collector = packs.collector_for("sample")

        self.assertEqual(collector.PACK_MARKER, "pack-one")
        self.assertEqual(collector.SHARED_MARKER, "shared-one")
        self.assertTrue(
            os.path.abspath(collector.__file__).startswith(
                os.path.abspath(self.tmp) + os.sep
            )
        )

    def test_changing_external_root_reloads_manifests_and_collector_modules(self):
        _write_pack(self.tmp, "distribution", {
            "provider_prefixes": {"sample_": "sample"},
            "requires_shared": ["common"],
        })
        _write_collector(self.tmp, "distribution", "pack-one", "shared-one")
        packs.reset()
        first = packs.collector_for("sample")

        other = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, other, True)
        _write_pack(other, "distribution", {
            "provider_prefixes": {"sample_": "sample"},
            "requires_shared": ["common"],
        })
        _write_collector(other, "distribution", "pack-two", "shared-two")
        os.environ["INFRAWRIGHT_PACKS"] = other

        second = packs.collector_for("sample")

        self.assertIsNot(first, second)
        self.assertEqual(second.PACK_MARKER, "pack-two")
        self.assertEqual(second.SHARED_MARKER, "shared-two")
        self.assertTrue(
            os.path.abspath(second.__file__).startswith(
                os.path.abspath(other) + os.sep
            )
        )

    def test_missing_declared_shared_component_fails_before_collector_import(self):
        _write_pack(self.tmp, "distribution", {
            "provider_prefixes": {"sample_": "sample"},
            "requires_shared": ["zscaler"],
        })
        packs.reset()

        with self.assertRaisesRegex(
                ValueError,
                "pack distribution requires missing shared component zscaler"):
            packs.collector_for("sample")

    def test_external_root_cannot_fall_back_to_bundled_collector(self):
        _write_pack(self.tmp, "zia", {
            "provider_prefixes": {"zia_": "zia"},
        })
        packs.reset()

        with self.assertRaisesRegex(
                RuntimeError,
                "pack zia declares provider 'zia' but has no collector.py"):
            packs.collector_for("zia")

    def test_requires_shared_rejects_invalid_duplicate_and_unsorted_names(self):
        cases = [
            (["Bad"], "must be a lowercase shared-component name"),
            (["common", "common"], "duplicates 'common'"),
            (["two", "one"], "must be sorted"),
        ]
        for value, message in cases:
            with self.subTest(value=value):
                with self.assertRaisesRegex(ValueError, message):
                    packs.validate_pack_metadata(
                        {"requires_shared": value}, path="sample/pack.json"
                    )

if __name__ == "__main__":
    unittest.main()
