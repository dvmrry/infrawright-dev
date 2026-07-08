import json
import os
import unittest

from engine.collectors import rest
from packs._shared.zscaler import collector as zscaler


class _JsonOpener(object):
    def __init__(self, payload):
        self.payload = payload

    def __call__(self, method, url, headers, body):
        return 200, json.dumps(self.payload).encode("utf-8")


class RestCollectorSecurityTest(unittest.TestCase):
    def test_paginate_zia_requires_configured_envelope_key(self):
        opener = _JsonOpener({
            "totalCount": 1,
            "trustedNetworks": [{"id": 1}],
        })

        with self.assertRaisesRegex(RuntimeError, "missing envelope"):
            rest.paginate_zia(
                opener,
                "https://api.zsapi.net/zcc/papi/public/v1/test",
                {},
                {},
                envelope="trustedNetworkContracts",
            )

    def test_paginate_zia_requires_configured_envelope_list(self):
        opener = _JsonOpener({"trustedNetworkContracts": {"id": 1}})

        with self.assertRaisesRegex(RuntimeError, "did not contain a list"):
            rest.paginate_zia(
                opener,
                "https://api.zsapi.net/zcc/papi/public/v1/test",
                {},
                {},
                envelope="trustedNetworkContracts",
            )

    def test_paginate_zia_accepts_configured_envelope_list(self):
        opener = _JsonOpener({"trustedNetworkContracts": [{"id": 1}]})

        self.assertEqual(
            rest.paginate_zia(
                opener,
                "https://api.zsapi.net/zcc/papi/public/v1/test",
                {},
                {},
                envelope="trustedNetworkContracts",
            ),
            [{"id": 1}],
        )

    def test_paginate_zia_unconfigured_envelope_accepts_raw_list(self):
        opener = _JsonOpener([{"id": 1}])

        self.assertEqual(
            rest.paginate_zia(
                opener,
                "https://api.zsapi.net/zia/api/v1/test",
                {},
                {},
            ),
            [{"id": 1}],
        )

    def test_oneapi_main_ignores_stale_zia_cloud_for_data_context(self):
        captured = {}
        old_fetch_all = rest.fetch_all
        old_real_opener = rest.real_opener
        old_environ = os.environ.copy()

        def fake_fetch_all(auth_mode, env, ctx, opener, out_dir, only=None):
            captured["auth_mode"] = auth_mode
            captured["ctx"] = dict(ctx)
            captured["only"] = set(only or [])
            return 0

        try:
            rest.fetch_all = fake_fetch_all
            rest.real_opener = lambda: None
            os.environ.clear()
            os.environ.update({
                "ZSCALER_CLOUD": "production",
                "ZIA_CLOUD": "zscalertwo",
                "ZSCALER_VANITY_DOMAIN": "tenant",
            })

            self.assertEqual(rest.main(["tenant", "zia_url_categories"]), 0)
        finally:
            rest.fetch_all = old_fetch_all
            rest.real_opener = old_real_opener
            os.environ.clear()
            os.environ.update(old_environ)

        self.assertEqual(captured["auth_mode"], "oneapi")
        self.assertEqual(captured["ctx"]["cloud"], "production")
        self.assertIn("zia_url_categories", captured["only"])

    def test_zscaler_cloud_rejects_host_smuggling(self):
        with self.assertRaisesRegex(SystemExit, "ZSCALER_CLOUD"):
            zscaler._zslogin_host("tenant", ".attacker.test/x")

    def test_zscaler_vanity_rejects_dotted_host_smuggling(self):
        with self.assertRaisesRegex(SystemExit, "ZSCALER_VANITY_DOMAIN"):
            zscaler._zslogin_host("tenant.attacker", "")

    def test_legacy_base_override_requires_https_host_only(self):
        bad_values = [
            "http://attacker.test",
            "https://user:pass@example.test",
            "https://example.test/path",
            "https://example.test?x=1",
            "https://example.test#frag",
        ]

        for value in bad_values:
            with self.subTest(value=value):
                with self.assertRaises(SystemExit):
                    zscaler.host_overrides({"ZIA_LEGACY_BASE_URL": value})

    def test_legacy_base_override_normalizes_https_host(self):
        self.assertEqual(
            zscaler.host_overrides({
                "ZIA_LEGACY_BASE_URL": "https://EXAMPLE.test/",
            })["zia_legacy_base"],
            "https://example.test",
        )


if __name__ == "__main__":
    unittest.main()
