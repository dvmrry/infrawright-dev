"""Tests for the shared path grammar/matcher/formatter module."""
import unittest

from engine import paths


class ParsePathTest(unittest.TestCase):
    def test_plain_dotted(self):
        self.assertEqual(paths.parse_path("a.b.c"), ("a", "b", "c"))

    def test_selectors(self):
        self.assertEqual(
            paths.parse_path("a[].b[*].c[3]"),
            ("a", paths.WILDCARD, "b", paths.WILDCARD, "c", 3),
        )

    def test_quoted_map_key(self):
        self.assertEqual(
            paths.parse_path('a["k.1"].b'), ("a", "k.1", "b")
        )

    def test_quoted_key_with_escapes(self):
        self.assertEqual(
            paths.parse_path(r'a["q\"uo\\te"]'), ("a", 'q"uo\\te')
        )

    def test_non_strict_accepts_arbitrary_plain_segment(self):
        self.assertEqual(paths.parse_path("weird-name"), ("weird-name",))

    def test_strict_rejects_arbitrary_plain_segment(self):
        with self.assertRaises(ValueError):
            paths.parse_path("weird-name", strict_names=True)

    def test_strict_accepts_identifier_and_selectors(self):
        self.assertEqual(
            paths.parse_path("a_1[].b[2]", strict_names=True),
            ("a_1", paths.WILDCARD, "b", 2),
        )

    def test_empty_segment_non_strict_message(self):
        with self.assertRaises(ValueError) as ctx:
            paths.parse_path("a..b")
        self.assertIn("empty path segment", str(ctx.exception))

    def test_empty_segment_strict_message(self):
        with self.assertRaises(ValueError) as ctx:
            paths.parse_path("a..b", strict_names=True, what="policy path")
        self.assertIn("invalid policy path segment", str(ctx.exception))

    def test_unterminated_quote(self):
        with self.assertRaises(ValueError):
            paths.parse_path('a["open')

    def test_unterminated_selector(self):
        with self.assertRaises(ValueError):
            paths.parse_path("a[0")

    def test_invalid_selector(self):
        with self.assertRaises(ValueError):
            paths.parse_path("a[x]")

    def test_what_label_appears_in_messages(self):
        with self.assertRaises(ValueError) as ctx:
            paths.parse_path("a[x]", what="policy path")
        self.assertIn("policy path", str(ctx.exception))


class SelectorMatchesTest(unittest.TestCase):
    def test_exact(self):
        self.assertTrue(paths.selector_matches(("a", 0), ("a", 0)))

    def test_wildcard_matches_int_only(self):
        self.assertTrue(paths.selector_matches(("a", paths.WILDCARD), ("a", 3)))
        self.assertFalse(
            paths.selector_matches(("a", paths.WILDCARD), ("a", "key"))
        )

    def test_length_mismatch(self):
        self.assertFalse(paths.selector_matches(("a",), ("a", 0)))

    def test_int_does_not_match_string(self):
        self.assertFalse(paths.selector_matches(("a", 0), ("a", "0")))


class NormalizeTest(unittest.TestCase):
    def test_int_and_wildcard_become_list_marker(self):
        self.assertEqual(
            paths.normalize(("a", 0, "b", paths.WILDCARD)),
            ("a", paths.LIST_MARKER, "b", paths.LIST_MARKER),
        )

    def test_string_input_is_parsed(self):
        self.assertEqual(
            paths.normalize("a[3].b"), ("a", paths.LIST_MARKER, "b")
        )

    def test_bare_star_segment_is_not_a_collection_selector(self):
        self.assertEqual(paths.normalize("a.*.b"), ("a", "*", "b"))
        self.assertEqual(paths.format_report_path(paths.parse_path("a.*.b")), "a.*.b")


class FormatPathTest(unittest.TestCase):
    def test_str_passthrough(self):
        self.assertEqual(paths.format_path("already.formatted"), "already.formatted")

    def test_empty_is_root(self):
        self.assertEqual(paths.format_path(()), "<root>")

    def test_int_faithful(self):
        self.assertEqual(paths.format_path(("a", 0, "b")), "a[0].b")

    def test_list_marker(self):
        self.assertEqual(
            paths.format_path(("a", paths.LIST_MARKER, "b")), "a[].b"
        )

    def test_wildcard_formats_as_list_marker(self):
        self.assertEqual(paths.format_path(("a", paths.WILDCARD)), "a[]")

    def test_leading_index_does_not_crash(self):
        self.assertEqual(paths.format_path((0, "x")), "[0].x")

    def test_leading_list_marker(self):
        self.assertEqual(paths.format_path((paths.LIST_MARKER, "x")), "[].x")

    def test_format_report_path_collapses_indexes(self):
        self.assertEqual(paths.format_report_path(("a", 0, "b")), "a[].b")

    def test_round_trip_report_style(self):
        for text in ("a[].b", "a.b.c", 'a["k"].b[]'):
            parsed = paths.normalize(paths.parse_path(text))
            self.assertEqual(paths.normalize(paths.parse_path(
                paths.format_path(parsed))), parsed)


if __name__ == "__main__":
    unittest.main()
