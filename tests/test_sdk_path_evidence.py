import json
import os
import shutil
import tempfile
import unittest

from engine import sdk_path_evidence
from engine import source_operation_map


GODO_DOMAINS = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const domainsBasePath = "v2/domains"

type DomainsServiceOp struct {
    client *Client
}

func (s *DomainsServiceOp) Get(ctx context.Context, domain string) (*Domain, *Response, error) {
    path := fmt.Sprintf("%s/%s", domainsBasePath, domain)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}

func (s *DomainsServiceOp) List(ctx context.Context, opt *ListOptions) ([]Domain, *Response, error) {
    path := domainsBasePath
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""

GODO_DROPLETS = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const dropletsBasePath = "v2/droplets"

type DropletsServiceOp struct {
    client *Client
}

func (s *DropletsServiceOp) Get(ctx context.Context, id int) (*Droplet, *Response, error) {
    path := fmt.Sprintf("%s/%d", dropletsBasePath, id)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""

GODO_VPCS = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const vpcsBasePath = "v2/vpcs"

type VPCsServiceOp struct {
    client *Client
}

func (s *VPCsServiceOp) Get(ctx context.Context, vpcID string) (*VPC, *Response, error) {
    path := fmt.Sprintf("%s/%s", vpcsBasePath, vpcID)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""

GODO_RESERVED_IPS = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const reservedIPsBasePath = "v2/reserved_ips"

type ReservedIPsServiceOp struct {
    client *Client
}

func (s *ReservedIPsServiceOp) Get(ctx context.Context, ip string) (*ReservedIP, *Response, error) {
    path := fmt.Sprintf("%s/%s", reservedIPsBasePath, ip)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}

func (s *ReservedIPsServiceOp) Assign(ctx context.Context, ip string) (*Action, *Response, error) {
    path := fmt.Sprintf("%s/%s/actions", reservedIPsBasePath, ip)
    req, err := s.client.NewRequest(ctx, http.MethodPost, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""

GODO_RESERVED_IPV6S = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const reservedIPv6sBasePath = "v2/reserved_ipv6"

type ReservedIPV6sServiceOp struct {
    client *Client
}

func (s *ReservedIPV6sServiceOp) Get(ctx context.Context, ip string) (*ReservedIPV6, *Response, error) {
    path := fmt.Sprintf("%s/%s", reservedIPv6sBasePath, ip)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""

GODO_ACTIONS = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const actionsBasePath = "v2/actions"

type ActionsServiceOp struct {
    client *Client
}

func (s *ActionsServiceOp) Get(ctx context.Context, id int) (*Action, *Response, error) {
    path := fmt.Sprintf("%s/%d", actionsBasePath, id)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""

GODO_THINGS_NO_REQUEST = """package godo

const thingsBasePath = "v2/things"

type ThingsServiceOp struct {
    client *Client
}

func (s *ThingsServiceOp) Get(ctx context.Context, id int) (*Thing, *Response, error) {
    path := fmt.Sprintf("%s/%d", thingsBasePath, id)
    _ = path
    return nil, nil, nil
}
"""


def _schema(resource):
    return {
        "provider_schemas": {
            "registry.terraform.io/digitalocean/digitalocean": {
                "resource_schemas": {
                    resource: {
                        "block": {
                            "attributes": {
                                "name": {
                                    "type": "string",
                                    "required": True,
                                },
                            },
                        },
                    },
                },
            },
        },
    }


class SdkPathEvidenceTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="sdk-path-evidence-")

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write(self, name, text):
        path = os.path.join(self.tmp, name)
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def _write_json(self, name, data):
        return self._write(name, json.dumps(data))

    def _godo_root(self, *sources):
        root = os.path.join(self.tmp, "godo")
        for filename, text in sources:
            self._write(os.path.join("godo", filename), text)
        return root

    def _provider_root(self, resource, call_body):
        root = os.path.join(self.tmp, "provider")
        bare = resource[len("digitalocean_"):]
        self._write(
            os.path.join("provider", bare, "resource_%s.go" % resource),
            "package %s\n\nfunc read%s() {\n%s\n}\n" % (
                bare, bare.title().replace("_", ""), call_body),
        )
        return root

    # --- extractor unit tests ---

    def test_extractor_recovers_domains_get_and_list(self):
        root = self._godo_root(("domains.go", GODO_DOMAINS))
        evidence, unresolved = sdk_path_evidence.extract_sdk_paths(root)
        self.assertEqual(unresolved, {})
        self.assertEqual(
            evidence["Domains.Get"]["path_template"], "v2/domains/{domain}")
        self.assertEqual(evidence["Domains.Get"]["method"], "GET")
        self.assertEqual(evidence["Domains.Get"]["source_role"], "read")
        self.assertEqual(
            evidence["Domains.List"]["path_template"], "v2/domains")
        self.assertEqual(evidence["Domains.List"]["source_role"], "list")

    def test_extractor_recovers_droplets_get_with_int_verb(self):
        root = self._godo_root(("droplets.go", GODO_DROPLETS))
        evidence, _ = sdk_path_evidence.extract_sdk_paths(root)
        self.assertEqual(
            evidence["Droplets.Get"]["path_template"], "v2/droplets/{id}")

    def test_extractor_reports_method_not_detected(self):
        root = self._godo_root(("things.go", GODO_THINGS_NO_REQUEST))
        _evidence, unresolved = sdk_path_evidence.extract_sdk_paths(root)
        self.assertIn("Things.Get", unresolved)
        self.assertEqual(
            unresolved["Things.Get"]["reason"], "method_not_detected")

    def test_extractor_returns_action_for_post(self):
        root = self._godo_root(("reserved_ips.go", GODO_RESERVED_IPS))
        evidence, _ = sdk_path_evidence.extract_sdk_paths(root)
        assign = evidence["ReservedIPs.Assign"]
        self.assertEqual(assign["method"], "POST")
        self.assertEqual(
            assign["path_template"], "v2/reserved_ips/{ip}/actions")
        self.assertIsNone(assign["source_role"])

    def test_extractor_empty_for_missing_root(self):
        evidence, unresolved = sdk_path_evidence.extract_sdk_paths(None)
        self.assertEqual(evidence, {})
        self.assertEqual(unresolved, {})

    def test_match_openapi_by_path_distinguishes_detail_and_list(self):
        operations = [
            {"method": "GET", "path": "/v2/domains",
             "operation_id": "GET /v2/domains"},
            {"method": "GET", "path": "/v2/domains/{domain_name}",
             "operation_id": "GET /v2/domains/{domain_name}"},
        ]
        detail, detail_ambig = sdk_path_evidence.match_openapi_by_path(
            operations, "v2/domains/{domain}")
        self.assertEqual(detail_ambig, [])
        self.assertEqual(detail["path"], "/v2/domains/{domain_name}")
        listing, list_ambig = sdk_path_evidence.match_openapi_by_path(
            operations, "v2/domains")
        self.assertEqual(list_ambig, [])
        self.assertEqual(listing["path"], "/v2/domains")

    def test_match_openapi_reports_ambiguous_structural_match(self):
        operations = [
            {"method": "GET", "path": "/v2/things/{a}",
             "operation_id": "GET /v2/things/{a}"},
            {"method": "GET", "path": "/v2/things/{b}",
             "operation_id": "GET /v2/things/{b}"},
        ]
        op, ambiguous = sdk_path_evidence.match_openapi_by_path(
            operations, "v2/things/{id}")
        self.assertIsNone(op)
        self.assertEqual(len(ambiguous), 2)

    # --- integration tests through derive_registry ---

    def _derive(self, resource, openapi_paths, provider_call, godo_sources):
        schema_path = self._write_json("schema.json", _schema(resource))
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": openapi_paths,
        })
        provider_root = self._provider_root(resource, provider_call)
        sdk_root = self._godo_root(*godo_sources)
        return source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            provider_root,
            provider_source="registry.terraform.io/digitalocean/digitalocean",
            resource_prefix="digitalocean",
            sdk_root=sdk_root,
        )

    def test_sdk_path_resolves_domains_get(self):
        report = self._derive(
            "digitalocean_domain",
            {
                "/v2/domains": {"get": {"responses": {"200": {"description": "ok"}}}},
                "/v2/domains/{domain_name}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            "    domain, _, err := client.Domains.Get(ctx, name)\n    _ = domain\n    _ = err\n",
            (("domains.go", GODO_DOMAINS),),
        )
        self.assertEqual(report["summary"]["mapped"], 1)
        entry = report["registry"]["digitalocean_domain"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"], "/v2/domains/{domain_name}")
        self.assertEqual(entry["read"]["operation_id"],
                         "GET /v2/domains/{domain_name}")
        hops = entry["read"]["hops"]
        self.assertEqual([hop["kind"] for hop in hops],
                         ["provider_call", "sdk_path", "openapi_operation"])
        self.assertEqual(hops[0]["client_symbol"], "Domains.Get")
        self.assertEqual(hops[1]["path_template"], "v2/domains/{domain}")
        self.assertEqual(hops[1]["method"], "GET")
        self.assertEqual(hops[1]["sdk_file"], "domains.go")
        self.assertEqual(hops[2]["path"], "/v2/domains/{domain_name}")

    def test_sdk_path_resolves_droplets_get(self):
        report = self._derive(
            "digitalocean_droplet",
            {
                "/v2/droplets/{droplet_id}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            "    droplet, _, err := client.Droplets.Get(ctx, id)\n    _ = droplet\n    _ = err\n",
            (("droplets.go", GODO_DROPLETS),),
        )
        entry = report["registry"]["digitalocean_droplet"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"], "/v2/droplets/{droplet_id}")
        self.assertEqual(
            entry["read"]["hops"][1]["path_template"], "v2/droplets/{id}")

    def test_sdk_path_resolves_vpcs_get(self):
        report = self._derive(
            "digitalocean_vpc",
            {
                "/v2/vpcs/{vpc_id}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            "    vpc, _, err := client.VPCs.Get(ctx, id)\n    _ = vpc\n    _ = err\n",
            (("vpcs.go", GODO_VPCS),),
        )
        entry = report["registry"]["digitalocean_vpc"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"], "/v2/vpcs/{vpc_id}")
        self.assertEqual(
            entry["read"]["hops"][1]["path_template"], "v2/vpcs/{vpcID}")

    def test_sdk_path_action_case_reserved_ip_assignment(self):
        report = self._derive(
            "digitalocean_reserved_ip",
            {
                "/v2/reserved_ips/{ip}": {"get": {"responses": {"200": {"description": "ok"}}}},
                "/v2/reserved_ips/{ip}/actions": {"post": {"responses": {"201": {"description": "ok"}}}},
            },
            (
                "    ip, _, err := client.ReservedIPs.Get(ctx, name)\n"
                "    _ = ip\n"
                "    _ = err\n"
                "    _, err = client.ReservedIPs.Assign(ctx, name)\n"
            ),
            (("reserved_ips.go", GODO_RESERVED_IPS),),
        )
        entry = report["registry"]["digitalocean_reserved_ip"]
        self.assertEqual(entry["status"], "mapped")
        # Read path resolved via SDK path evidence (GET).
        self.assertEqual(entry["read"]["path"], "/v2/reserved_ips/{ip}")
        self.assertEqual(
            entry["read"]["hops"][1]["path_template"], "v2/reserved_ips/{ip}")
        # The POST action is surfaced, not silently dropped or treated as read.
        actions = entry["source"]["sdk_action_paths"]
        self.assertEqual(len(actions), 1)
        self.assertEqual(actions[0]["client_symbol"], "ReservedIPs.Assign")
        self.assertEqual(actions[0]["method"], "POST")
        self.assertEqual(
            actions[0]["path_template"], "v2/reserved_ips/{ip}/actions")
        # No unresolved entries: both calls were accounted for.
        self.assertNotIn("sdk_path_unresolved", entry["source"])

    def test_sdk_path_helper_action_get_does_not_ambiguous_resource_read(self):
        report = self._derive(
            "digitalocean_reserved_ipv6",
            {
                "/v2/actions/{action_id}": {"get": {"responses": {"200": {"description": "ok"}}}},
                "/v2/reserved_ipv6/{reserved_ipv6}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            (
                "    action, _, err := client.Actions.Get(ctx, actionID)\n"
                "    _ = action\n"
                "    _ = err\n"
                "    ip, _, err := client.ReservedIPV6s.Get(ctx, name)\n"
                "    _ = ip\n"
                "    _ = err\n"
            ),
            (("reserved_ipv6.go", GODO_RESERVED_IPV6S),
             ("action.go", GODO_ACTIONS)),
        )
        entry = report["registry"]["digitalocean_reserved_ipv6"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"],
                         "/v2/reserved_ipv6/{reserved_ipv6}")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "ReservedIPV6s.Get")

    def test_sdk_path_unresolved_reports_sdk_symbol_not_found(self):
        report = self._derive(
            "digitalocean_domain",
            {
                "/v2/domains/{domain_name}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            "    domain, _, err := client.Domains.Get(ctx, name)\n    _ = domain\n    _ = err\n",
            (("vpcs.go", GODO_VPCS),),  # no domains.go in sdk-root
        )
        entry = report["registry"]["digitalocean_domain"]
        unresolved = entry["source"]["sdk_path_unresolved"]
        self.assertEqual(len(unresolved), 1)
        self.assertEqual(unresolved[0]["client_symbol"], "Domains.Get")
        self.assertEqual(unresolved[0]["reason"], "sdk_symbol_not_found")
        self.assertIsNone(unresolved[0]["sdk_file"])

    def test_sdk_path_unresolved_reports_openapi_path_not_found(self):
        report = self._derive(
            "digitalocean_domain",
            {
                "/v2/something_else": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            "    domain, _, err := client.Domains.Get(ctx, name)\n    _ = domain\n    _ = err\n",
            (("domains.go", GODO_DOMAINS),),
        )
        entry = report["registry"]["digitalocean_domain"]
        unresolved = entry["source"]["sdk_path_unresolved"]
        self.assertEqual(unresolved[0]["client_symbol"], "Domains.Get")
        self.assertEqual(unresolved[0]["reason"], "openapi_path_not_found")
        self.assertEqual(unresolved[0]["sdk_file"], "domains.go")

    def test_sdk_path_ambiguous_structural_match_surfaces_unresolved(self):
        things_src = """package godo

import (
    "context"
    "fmt"
    "net/http"
)

const thingsBasePath = "v2/things"

type ThingsServiceOp struct {
    client *Client
}

func (s *ThingsServiceOp) Get(ctx context.Context, id int) (*Thing, *Response, error) {
    path := fmt.Sprintf("%s/%d", thingsBasePath, id)
    req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
    _ = req
    _ = err
    return nil, nil, nil
}
"""
        report = self._derive(
            "digitalocean_thing",
            {
                "/v2/things/{a}": {"get": {"responses": {"200": {"description": "ok"}}}},
                "/v2/things/{b}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
            "    thing, _, err := client.Things.Get(ctx, id)\n    _ = thing\n    _ = err\n",
            (("things.go", things_src),),
        )
        entry = report["registry"]["digitalocean_thing"]
        unresolved = entry["source"]["sdk_path_unresolved"]
        self.assertEqual(unresolved[0]["client_symbol"], "Things.Get")
        self.assertEqual(unresolved[0]["reason"], "openapi_path_ambiguous")
        self.assertEqual(
            sorted(unresolved[0]["ambiguous_openapi_paths"]),
            ["/v2/things/{a}", "/v2/things/{b}"])

    def test_sdk_root_absent_falls_back_to_fuzzy_scoring(self):
        schema_path = self._write_json("schema.json", _schema("digitalocean_domain"))
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/v2/domains": {"get": {"responses": {"200": {"description": "ok"}}}},
                "/v2/domains/{domain_name}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
        })
        provider_root = self._provider_root(
            "digitalocean_domain",
            "    domain, _, err := client.Domains.Get(ctx, name)\n    _ = domain\n    _ = err\n",
        )
        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            provider_root,
            provider_source="registry.terraform.io/digitalocean/digitalocean",
            resource_prefix="digitalocean",
        )
        entry = report["registry"]["digitalocean_domain"]
        # Without sdk-root the fuzzy path still resolves the read.
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"], "/v2/domains/{domain_name}")
        # No sdk_path hop without sdk-root.
        self.assertEqual([hop["kind"] for hop in entry["read"]["hops"]],
                         ["provider_call", "openapi_operation"])
        self.assertNotIn("sdk_path_unresolved", entry["source"])
        self.assertNotIn("sdk_action_paths", entry["source"])

    def test_cli_accepts_sdk_root_flag(self):
        schema_path = self._write_json("schema.json", _schema("digitalocean_domain"))
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/v2/domains/{domain_name}": {"get": {"responses": {"200": {"description": "ok"}}}},
            },
        })
        provider_root = self._provider_root(
            "digitalocean_domain",
            "    domain, _, err := client.Domains.Get(ctx, name)\n    _ = domain\n    _ = err\n",
        )
        sdk_root = self._godo_root(("domains.go", GODO_DOMAINS))
        out_path = os.path.join(self.tmp, "registry.json")
        rc = source_operation_map.main([
            "--schema", schema_path,
            "--openapi", openapi_path,
            "--source-root", provider_root,
            "--provider-source", "registry.terraform.io/digitalocean/digitalocean",
            "--resource-prefix", "digitalocean",
            "--sdk-root", sdk_root,
            "--out", out_path,
        ])
        self.assertEqual(rc, 0)
        with open(out_path, encoding="utf-8") as f:
            registry = json.load(f)
        entry = registry["digitalocean_domain"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual([hop["kind"] for hop in entry["read"]["hops"]],
                         ["provider_call", "sdk_path", "openapi_operation"])


if __name__ == "__main__":
    unittest.main()
