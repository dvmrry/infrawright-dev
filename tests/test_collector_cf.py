"""Cloudflare collector tests.

Fake openers only: no live network or credentials.
"""
import json
import unittest
from urllib.parse import parse_qs, urlparse

from collectors import rest
from packs.cloudflare import collector


def _body(payload):
    return json.dumps(payload).encode()


class CloudflareCollectorTest(unittest.TestCase):
    def test_acquire_returns_token(self):
        token = collector.acquire(
            "token", {"CLOUDFLARE_API_TOKEN": "cf-token"}, {}, None)
        self.assertEqual(token, "cf-token")

    def test_acquire_raises_without_token(self):
        with self.assertRaises(SystemExit) as cm:
            collector.acquire("token", {}, {}, None)
        self.assertIn("CLOUDFLARE_API_TOKEN", str(cm.exception))

    def test_compose_url_resolves_account_path(self):
        url = collector.compose_url(
            "token",
            "accounts/{account_id}/rules/lists",
            {"account_id": "acct-1"},
        )
        self.assertEqual(
            url,
            "https://api.cloudflare.com/client/v4/accounts/acct-1/rules/lists",
        )

    def test_compose_url_resolves_zone_path(self):
        url = collector.compose_url(
            "token",
            "zones/{zone_id}/dns_records",
            {"account_id": "acct-1", "_current_zone_id": "zone-1"},
        )
        self.assertEqual(
            url,
            "https://api.cloudflare.com/client/v4/zones/zone-1/dns_records",
        )

    def test_cf_page_stops_at_total_pages(self):
        calls = []

        def opener(method, url, headers, body):
            calls.append(url)
            query = parse_qs(urlparse(url).query)
            page = int(query["page"][0])
            self.assertEqual(query["per_page"], ["100"])
            payload = {
                "success": True,
                "result": [{"id": "page-%d" % page}],
                "result_info": {"page": page, "total_pages": 2},
            }
            return 200, _body(payload)

        items = rest.paginate_cf_page(opener, "https://example.test/items", {}, {})

        self.assertEqual(items, [{"id": "page-1"}, {"id": "page-2"}])
        self.assertEqual(len(calls), 2)

    def test_cf_cursor_stops_when_after_absent(self):
        calls = []
        payloads = [
            {
                "success": True,
                "result": [{"id": "first"}],
                "result_info": {"cursors": {"after": "next-token"}},
            },
            {
                "success": True,
                "result": [{"id": "second"}],
                "result_info": {},
            },
        ]

        def opener(method, url, headers, body):
            calls.append(url)
            return 200, _body(payloads[len(calls) - 1])

        items = rest.paginate_cf_cursor(
            opener, "https://example.test/items", {}, {})

        self.assertEqual(items, [{"id": "first"}, {"id": "second"}])
        self.assertEqual(len(calls), 2)
        self.assertEqual(parse_qs(urlparse(calls[0]).query), {})
        self.assertEqual(
            parse_qs(urlparse(calls[1]).query),
            {"cursor": ["next-token"]},
        )

    def test_zone_bootstrap_injects_zone_id(self):
        ctx = {"account_id": "acct-1"}
        calls = []

        def cf_page(result):
            return {
                "success": True,
                "result": result,
                "result_info": {"page": 1, "total_pages": 1},
            }

        def opener(method, url, headers, body):
            calls.append(url)
            parsed = urlparse(url)
            query = parse_qs(parsed.query)
            self.assertEqual(query.get("page"), ["1"])
            self.assertEqual(query.get("per_page"), ["100"])
            if parsed.path.endswith("/zones"):
                self.assertEqual(query.get("account.id"), ["acct-1"])
                return 200, _body(cf_page([
                    {"id": "zone-a", "name": "a.example"},
                    {"id": "zone-b", "name": "b.example"},
                ]))
            if parsed.path.endswith("/zones/zone-a/dns_records"):
                return 200, _body(cf_page([{"id": "record-a"}]))
            if parsed.path.endswith("/zones/zone-b/dns_records"):
                return 200, _body(cf_page([{"id": "record-b"}]))
            raise AssertionError("unexpected URL %s" % url)

        items = rest.fetch_resource(
            "cloudflare_dns_record", "token", ctx, "cf-token", opener)

        self.assertEqual(
            items,
            [
                {"id": "record-a", "zone_id": "zone-a"},
                {"id": "record-b", "zone_id": "zone-b"},
            ],
        )
        self.assertNotIn("_current_zone_id", ctx)
        self.assertEqual(len(calls), 3)


if __name__ == "__main__":
    unittest.main()
