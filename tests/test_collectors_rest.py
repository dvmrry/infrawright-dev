import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine.collectors import rest
from packs._shared.zscaler import collector as zscaler


def _write_text(path, content):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)


def _write_external_collector_root(root, marker):
    _write_text(
        os.path.join(root, "zia", "pack.json"),
        json.dumps({
            "provider_prefixes": {"zia_": "zia"},
            "requires_shared": ["zscaler"],
        }),
    )
    _write_text(
        os.path.join(root, "zia", "registry.json"),
        json.dumps({
            "zia_sample": {
                "product": "zia",
                "fetch": {"pagination": "single", "path": "sample"},
            },
        }),
    )
    _write_text(os.path.join(root, "zia", "__init__.py"), "")
    _write_text(
        os.path.join(root, "zia", "collector.py"),
        "def _legacy_zia_base(cloud):\n"
        "    return %r\n"
        "def obfuscate_api_key(api_key, timestamp):\n"
        "    return %r\n"
        % ("provider-" + marker, "obfuscated-" + marker),
    )
    _write_text(
        os.path.join(root, "_shared", "zscaler", "__init__.py"), ""
    )
    _write_text(
        os.path.join(root, "_shared", "zscaler", "collector.py"),
        "MARKER = %r\n" % marker
        + "def host_overrides(env):\n"
        "    return {\"marker\": MARKER, \"zia_legacy_base\": None, "
        "\"zpa_legacy_base\": None}\n"
        "def _oneapi_gateway(cloud):\n"
        "    return \"https://gateway-%s.example\" % MARKER\n"
        "def _gateway_for(ctx):\n"
        "    return _oneapi_gateway(ctx.get(\"cloud\", \"\"))\n"
        "def _zslogin_host(vanity, cloud):\n"
        "    return \"https://login-%s.example\" % MARKER\n",
    )


class ExternalPackRootAuthorityTest(unittest.TestCase):
    def setUp(self):
        self.previous = os.environ.get("INFRAWRIGHT_PACKS")
        self.roots = []

    def tearDown(self):
        if self.previous is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.previous
        packs.reset()
        for root in self.roots:
            shutil.rmtree(root, ignore_errors=True)

    def _root(self, marker):
        root = tempfile.mkdtemp(prefix="external-collector-")
        self.roots.append(root)
        _write_external_collector_root(root, marker)
        return root

    def _assert_active_helpers(self, marker):
        self.assertEqual(
            rest.obfuscate_api_key("key", "timestamp"),
            "obfuscated-" + marker,
        )
        self.assertEqual(
            rest._legacy_zia_base("cloud"), "provider-" + marker
        )
        self.assertEqual(rest.host_overrides({})["marker"], marker)
        lines = rest.debug_config(
            {}, {"cloud": "", "customer_id": ""}, "oneapi", {"zia"}
        )
        self.assertIn(
            "fetch: gateway = https://gateway-%s.example" % marker,
            lines,
        )
        self.assertEqual(
            rest.diag_hosts({}),
            ["gateway-%s.example" % marker, "login-%s.example" % marker],
        )
        self.assertEqual(
            rest.diag_hosts({
                "ZSCALER_USE_LEGACY_CLIENT": "1",
                "ZIA_CLOUD": "external",
            }),
            ["zsapi.external.net"],
        )

    def test_helpers_use_external_root_before_loader_and_after_root_change(self):
        first = self._root("one")
        os.environ["INFRAWRIGHT_PACKS"] = first
        packs.reset()
        self._assert_active_helpers("one")

        second = self._root("two")
        os.environ["INFRAWRIGHT_PACKS"] = second
        self._assert_active_helpers("two")


class _JsonOpener(object):
    def __init__(self, payload):
        self.payload = payload

    def __call__(self, method, url, headers, body):
        return 200, json.dumps(self.payload).encode("utf-8")


class RestCollectorSecurityTest(unittest.TestCase):
    def test_zia_http_header_resources_use_flat_list_endpoints(self):
        payloads = {
            "httpHeaderActionProfile": [{"id": 11, "name": "Action"}],
            "httpHeaderProfile": [{"id": 22, "name": "Match"}],
        }
        calls = []

        def opener(method, url, headers, body):
            calls.append((method, url, headers, body))
            key = url.rsplit("/", 1)[-1]
            return 200, json.dumps(payloads[key]).encode("utf-8")

        expected = {
            "zia_http_header_action_profile": payloads[
                "httpHeaderActionProfile"
            ],
            "zia_http_header_profile": payloads["httpHeaderProfile"],
        }
        for resource_type, payload in sorted(expected.items()):
            with self.subTest(resource_type=resource_type):
                self.assertEqual(
                    rest.fetch_resource(
                        resource_type,
                        "oneapi",
                        {"cloud": "production"},
                        "token",
                        opener,
                    ),
                    payload,
                )

        self.assertEqual(
            [call[1] for call in calls],
            [
                "https://api.zsapi.net/zia/api/v1/httpHeaderActionProfile",
                "https://api.zsapi.net/zia/api/v1/httpHeaderProfile",
            ],
        )
        for method, url, headers, body in calls:
            self.assertEqual(method, "GET")
            self.assertNotIn("?", url)
            self.assertEqual(headers["Authorization"], "Bearer token")
            self.assertIsNone(body)

    def test_sandbox_settings_uses_single_object_zia_endpoint(self):
        calls = []

        def opener(method, url, headers, body):
            calls.append((method, url, headers, body))
            return 200, json.dumps({
                "fileHashesToBeBlocked": [
                    "d41d8cd98f00b204e9800998ecf8427e"
                ]
            }).encode("utf-8")

        self.assertEqual(
            rest.fetch_resource(
                "zia_sandbox_behavioral_analysis",
                "oneapi",
                {"cloud": "production"},
                "token",
                opener,
            ),
            [{
                "fileHashesToBeBlocked": [
                    "d41d8cd98f00b204e9800998ecf8427e"
                ]
            }],
        )
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0][0], "GET")
        self.assertEqual(
            calls[0][1],
            "https://api.zsapi.net/zia/api/v1/"
            "behavioralAnalysisAdvancedSettings",
        )

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
