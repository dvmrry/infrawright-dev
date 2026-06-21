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

    def test_pack_root_raises_with_no_registry_pack(self):
        _write_pack(self.tmp, "a", {"provider_prefixes": {"a_": "a"}})
        packs.reset()
        with self.assertRaises(RuntimeError):
            packs.pack_root()

    def test_pack_root_returns_sole_registry_pack(self):
        _write_pack(self.tmp, "only", {}, with_registry=True)
        packs.reset()
        self.assertEqual(os.path.basename(packs.pack_root()), "only")

    def test_pack_root_raises_on_multiple_registry_packs(self):
        _write_pack(self.tmp, "one", {}, with_registry=True)
        _write_pack(self.tmp, "two", {}, with_registry=True)
        packs.reset()
        with self.assertRaises(RuntimeError):
            packs.pack_root()

    def test_references_and_lookup_sources_merge(self):
        _write_pack(self.tmp, "a", {
            "references": {"r1": {"f": {"referent": "t1", "name_field": "n"}}},
            "lookup_sources": {"t1": {"name_field": "n"}},
        })
        packs.reset()
        self.assertIn("r1", packs.references())
        self.assertIn("t1", packs.lookup_sources())

    def test_reset_rediscovers_after_change(self):
        self.assertEqual(packs.provider_prefixes(), {})
        _write_pack(self.tmp, "a", {"provider_prefixes": {"a_": "a"}})
        self.assertEqual(packs.provider_prefixes(), {})  # stale cache until reset
        packs.reset()
        self.assertEqual(packs.provider_prefixes(), {"a_": "a"})


if __name__ == "__main__":
    unittest.main()
