"""HTML escape/unescape round-trip.

The byte-identity demo oracle cannot exercise this path (zero HTML entities in
the demo data), yet it is the most fragile single piece for a Go port. These
tests pin the exact contract: double html.unescape on zpa_/zcc_ name/description,
the no_html_unescape per-resource exception, the zia non-unescape, and the
5-entity Go-SDK re-escape. They are the spec a Go encoder must match bit-for-bit.
"""
import unittest

from engine.transform import (
    _unescape_html_fields,
    _escape_html_fields,
    _go_html_escape,
)


class UnescapeTest(unittest.TestCase):
    def test_double_unescape_on_zpa_name_and_description(self):
        row = {"name": "R&amp;amp;D", "description": "a &amp;gt; b"}
        _unescape_html_fields(row, "zpa_application_segment")
        self.assertEqual(row["name"], "R&D")           # &amp;amp; -> &amp; -> &
        self.assertEqual(row["description"], "a > b")   # &amp;gt; -> &gt; -> >

    def test_double_unescape_on_zcc(self):
        row = {"name": "Q&amp;A"}
        _unescape_html_fields(row, "zcc_forwarding_profile")
        self.assertEqual(row["name"], "Q&A")

    def test_zia_is_not_unescaped(self):
        row = {"name": "R&amp;D"}
        _unescape_html_fields(row, "zia_url_filtering_rules")
        self.assertEqual(row["name"], "R&amp;D")        # zia read has no SDK unescape

    def test_no_html_unescape_override_keeps_escaped(self):
        row = {"description": "----&gt;"}
        _unescape_html_fields(row, "zpa_app_connector_group",
                              {"no_html_unescape": True})
        self.assertEqual(row["description"], "----&gt;")  # list-read resource stays escaped

    def test_only_name_and_description_are_touched(self):
        row = {"name": "A&amp;B", "comment": "C&amp;D"}
        _unescape_html_fields(row, "zpa_application_segment")
        self.assertEqual(row["name"], "A&B")
        self.assertEqual(row["comment"], "C&amp;D")     # other fields untouched

    def test_non_string_values_ignored(self):
        row = {"name": 123, "description": None}
        _unescape_html_fields(row, "zpa_application_segment")
        self.assertEqual(row["name"], 123)
        self.assertIsNone(row["description"])


class EscapeTest(unittest.TestCase):
    def test_go_escape_five_entities_only(self):
        self.assertEqual(
            _go_html_escape("a & b < c > d ' e \" f"),
            "a &amp; b &lt; c &gt; d &#39; e &#34; f",
        )

    def test_go_escape_double_unescapes_before_reencoding(self):
        self.assertEqual(_go_html_escape("R&amp;amp;D"), "R&amp;D")

    def test_escape_html_fields_targets_only_named_fields(self):
        row = {"custom_msg": "R&D > x", "name": "keep & me"}
        _escape_html_fields(row, {"html_escape_fields": ["custom_msg"]})
        self.assertEqual(row["custom_msg"], "R&amp;D &gt; x")
        self.assertEqual(row["name"], "keep & me")      # not declared, untouched


if __name__ == "__main__":
    unittest.main()
