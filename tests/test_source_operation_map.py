import json
import os
import shutil
import tempfile
import unittest

from engine import source_operation_map


class SourceOperationMapTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="source-operation-map-")

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

    def _fixture_json(self, name):
        path = os.path.join(
            os.path.dirname(__file__),
            "fixtures",
            "source_operation_map",
            name,
        )
        with open(path, encoding="utf-8") as f:
            return json.load(f)

    def _source_facts(self, source_root, **overrides):
        facts = {
            "source_root": source_root,
            "files": [],
            "functions": [],
            "resource_registrations": [],
            "resource_references": [],
            "identifier_references": [],
            "read_callbacks": [],
            "selector_calls": [],
            "package_calls": [],
            "raw_rest_calls": [],
        }
        facts.update(overrides)
        return facts

    def test_derives_registry_from_go_operation_ids(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_folder": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/folders": {
                    "get": {
                        "operationId": "RouteGetFolders",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/api/folders/{uid}": {
                    "get": {
                        "operationId": "RouteGetFolder",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/internal/resource_folder.go", """
package internal

func resourceFolder() {
    name := "example_folder"
    _ = name
    client.Provisioning.GetFolders(ctx)
    client.Provisioning.GetFolder("abc")
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
        )

        self.assertEqual(report["summary"]["mapped"], 1)
        entry = report["registry"]["example_folder"]
        self.assertEqual(entry["read"]["path"], "/api/folders/{uid}")
        self.assertEqual(entry["read"]["operation_id"], "RouteGetFolder")
        self.assertEqual(entry["read"]["path_kind"], "detail")
        self.assertEqual(entry["list"]["path"], "/api/folders")
        self.assertEqual(entry["list"]["operation_id"], "RouteGetFolders")
        self.assertEqual(entry, self._fixture_json("example_registry.json")[
            "example_folder"])
        self.assertEqual(
            entry["read"]["hops"],
            [
                {
                    "kind": "provider_call",
                    "client_symbol": "RouteGetFolder",
                    "matched_aliases": ["getfolder"],
                    "source_files": ["internal/resource_folder.go"],
                },
                {
                    "kind": "openapi_operation",
                    "operation_id": "RouteGetFolder",
                    "method": "GET",
                    "path": "/api/folders/{uid}",
                },
            ])

    def test_cli_writes_registry_and_diagnostics(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_project": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/projects/{id}": {
                    "get": {
                        "operationId": "ProjectsRetrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_project.go", """
package provider

var resourceName = "example_project"

func read() {
    client.ProjectsAPI.ProjectsRetrieve(ctx, id)
}
""")
        out_path = os.path.join(self.tmp, "registry.json")
        diagnostics_path = os.path.join(self.tmp, "diagnostics.json")

        rc = source_operation_map.main([
            "--schema", schema_path,
            "--openapi", openapi_path,
            "--source-root", source_root,
            "--provider-source", "registry.terraform.io/example/example",
            "--resource-prefix", "example",
            "--out", out_path,
            "--diagnostics", diagnostics_path,
        ])

        self.assertEqual(rc, 0)
        with open(out_path, encoding="utf-8") as f:
            registry = json.load(f)
        with open(diagnostics_path, encoding="utf-8") as f:
            diagnostics = json.load(f)
        self.assertEqual(registry["example_project"]["read"]["path"], "/projects/{id}")
        self.assertEqual(diagnostics["summary"]["mapped"], 1)

    def test_cli_can_use_ast_facts_and_write_comparison(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_project": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/projects/{id}": {
                    "get": {
                        "operationId": "ProjectsRetrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_project.go", "package provider\n")
        facts_path = self._write_json("facts.json", self._source_facts(
            source_root,
            files=[
                {
                    "path": "resource_project.go",
                    "package": "provider",
                    "imports": [],
                },
            ],
            selector_calls=[
                {
                    "file": "resource_project.go",
                    "function": "read",
                    "symbol": "client.ProjectsAPI.ProjectsRetrieve",
                    "parts": ["client", "ProjectsAPI", "ProjectsRetrieve"],
                },
            ],
        ))
        out_path = os.path.join(self.tmp, "registry.json")
        compare_path = os.path.join(self.tmp, "compare.json")

        rc = source_operation_map.main([
            "--schema", schema_path,
            "--openapi", openapi_path,
            "--source-root", source_root,
            "--provider-source", "registry.terraform.io/example/example",
            "--resource-prefix", "example",
            "--source-facts", facts_path,
            "--source-facts-compare", compare_path,
            "--out", out_path,
        ])

        self.assertEqual(rc, 0)
        with open(out_path, encoding="utf-8") as f:
            registry = json.load(f)
        with open(compare_path, encoding="utf-8") as f:
            comparison = json.load(f)
        self.assertEqual(registry["example_project"]["status"], "mapped")
        self.assertEqual(
            registry["example_project"]["source"]["evidence_backend"],
            "ast_facts")
        self.assertEqual(
            registry["example_project"]["read"]["path"], "/projects/{id}")
        self.assertEqual(comparison["summary"]["status_changes"], 1)

    def test_ast_identifier_tokens_do_not_synthesize_selector_suffixes(self):
        source_facts = self._source_facts(
            self.tmp,
            selector_calls=[
                {
                    "file": "resource.go",
                    "function": "Read",
                    "symbol": "r.client.IAM.UserGroups.Members.List",
                    "parts": [
                        "r",
                        "client",
                        "IAM",
                        "UserGroups",
                        "Members",
                        "List",
                    ],
                },
            ],
        )

        tokens = source_operation_map._identifier_tokens_from_facts(
            ["resource.go"], source_facts)

        self.assertIn("list", tokens)
        self.assertIn("rclientiamusergroupsmemberslist", tokens)
        self.assertNotIn("memberslist", tokens)

    def test_maps_cloudflare_service_dir_sdk_calls_to_openapi_paths(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/cloudflare/cloudflare": {
                    "resource_schemas": {
                        "cloudflare_dns_record": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Cloudflare API"},
            "paths": {
                "/zones/{zone_id}/dns_records": {
                    "get": {
                        "operationId": (
                            "dns-records-for-a-zone-list-dns-records"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/zones/{zone_id}/dns_records/usage": {
                    "get": {
                        "operationId": (
                            "dns-records-for-a-zone-get-usage"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/zones/{zone_id}/dns_records/{dns_record_id}": {
                    "get": {
                        "operationId": (
                            "dns-records-for-a-zone-dns-record-details"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/internal/services/dns_record/resource.go", """
package dns_record

func (r *DNSRecordResource) Metadata() {
    resp.TypeName = req.ProviderTypeName + "_dns_record"
}

func (r *DNSRecordResource) Read() {
    // d.client.DNS.Records.List(ctx, params) should not count from comments.
    _ = "r.client.DNS.Records.Get(ctx, id)"
    _, err := r.client.DNS.Records.Get(ctx, data.ID.ValueString(), params)
    _ = err
}
""")
        self._write("provider/internal/services/dns_record/list_data_source.go", """
package dns_record

func (d *DNSRecordsDataSource) Read() {
    page, err := d.client.DNS.Records.List(ctx, params)
    _ = page
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/cloudflare/cloudflare",
            resource_prefix="cloudflare",
        )

        self.assertEqual(report["summary"]["mapped"], 1)
        self.assertEqual(report["summary"]["resources_with_source_files"], 1)
        entry = report["registry"]["cloudflare_dns_record"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            [
                "internal/services/dns_record/list_data_source.go",
                "internal/services/dns_record/resource.go",
            ])
        self.assertEqual(entry["source"]["client_call_count"], 2)
        self.assertEqual(
            entry["read"]["path"],
            "/zones/{zone_id}/dns_records/{dns_record_id}")
        self.assertEqual(
            entry["read"]["operation_id"],
            "dns-records-for-a-zone-dns-record-details")
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "DNS.Records.Get")
        self.assertEqual(
            entry["list"]["path"],
            "/zones/{zone_id}/dns_records")
        self.assertEqual(
            entry["list"]["hops"][0]["client_symbol"],
            "DNS.Records.List")

    def test_ast_facts_backend_maps_cloudflare_service_calls(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/cloudflare/cloudflare": {
                    "resource_schemas": {
                        "cloudflare_dns_record": {
                            "block": {
                                "attributes": {
                                    "zone_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Cloudflare API"},
            "paths": {
                "/zones/{zone_id}/dns_records": {
                    "get": {
                        "operationId": (
                            "dns-records-for-a-zone-list-dns-records"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/zones/{zone_id}/dns_records/{dns_record_id}": {
                    "get": {
                        "operationId": (
                            "dns-records-for-a-zone-dns-record-details"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/internal/services/dns_record/resource.go",
            "package dns_record\n")
        self._write(
            "provider/internal/services/dns_record/list_data_source.go",
            "package dns_record\n")
        source_facts = self._source_facts(
            source_root,
            files=[
                {
                    "path": "internal/services/dns_record/resource.go",
                    "package": "dns_record",
                    "imports": [],
                },
                {
                    "path": (
                        "internal/services/dns_record/list_data_source.go"),
                    "package": "dns_record",
                    "imports": [],
                },
            ],
            selector_calls=[
                {
                    "file": "internal/services/dns_record/resource.go",
                    "function": "Read",
                    "symbol": "r.client.DNS.Records.Get",
                    "parts": ["r", "client", "DNS", "Records", "Get"],
                },
                {
                    "file": (
                        "internal/services/dns_record/list_data_source.go"),
                    "function": "Read",
                    "symbol": "d.client.DNS.Records.List",
                    "parts": ["d", "client", "DNS", "Records", "List"],
                },
            ],
        )

        control = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/cloudflare/cloudflare",
            resource_prefix="cloudflare",
        )
        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/cloudflare/cloudflare",
            resource_prefix="cloudflare",
            source_facts=source_facts,
        )
        comparison = source_operation_map.compare_registry_reports(
            control, report)

        self.assertEqual(
            control["registry"]["cloudflare_dns_record"]["status"],
            "unmapped")
        entry = report["registry"]["cloudflare_dns_record"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["source"]["evidence_backend"], "ast_facts")
        self.assertEqual(entry["source"]["client_call_count"], 2)
        self.assertEqual(
            entry["read"]["path"],
            "/zones/{zone_id}/dns_records/{dns_record_id}")
        self.assertEqual(entry["list"]["path"], "/zones/{zone_id}/dns_records")
        self.assertEqual(comparison["summary"]["status_changes"], 1)
        self.assertEqual(comparison["summary"]["read_path_changes"], 1)

    def test_ast_facts_backend_uses_resource_reference_files(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_widget": {
                            "block": {
                                "attributes": {
                                    "id": {
                                        "type": "string",
                                        "computed": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/widgets": {
                    "get": {
                        "operationId": "ListWidgets",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/widgets/{id}": {
                    "get": {
                        "operationId": "GetWidget",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        os.makedirs(source_root)
        source_facts = self._source_facts(
            source_root,
            files=[
                {
                    "path": "internal/widgets/framework_resource.go",
                    "package": "widgets",
                    "imports": [],
                },
            ],
            resource_references=[
                {
                    "resource": "example_widget",
                    "file": "internal/widgets/framework_resource.go",
                },
            ],
            identifier_references=[
                {
                    "name": "listWidgets",
                    "file": "internal/widgets/framework_resource.go",
                },
            ],
            selector_calls=[
                {
                    "file": "internal/widgets/framework_resource.go",
                    "function": "Read",
                    "symbol": "client.GetWidget",
                    "parts": ["client", "GetWidget"],
                },
            ],
        )

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
            source_facts=source_facts,
        )

        entry = report["registry"]["example_widget"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            ["internal/widgets/framework_resource.go"])
        self.assertEqual(entry["read"]["path"], "/widgets/{id}")
        self.assertEqual(entry["list"]["path"], "/widgets")

    def test_maps_go_swagger_api_receiver_read_calls_to_retrieve_operations(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/netbox": {
                    "resource_schemas": {
                        "netbox_ip_range": {
                            "block": {
                                "attributes": {
                                    "prefix": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/ipam/ip-ranges/": {
                    "get": {
                        "operationId": "ipam_ip_ranges_list",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/api/ipam/ip-ranges/{id}/": {
                    "get": {
                        "operationId": "ipam_ip_ranges_retrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_netbox_ip_range.go", """
package provider

func resourceIPRange() {
    resourceName := "netbox_ip_range"
    _ = resourceName
    params := ipam.NewIpamIPRangesReadParams()
    item, err := api.Ipam.IpamIPRangesRead(params, nil)
    _ = item
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/netbox",
            resource_prefix="netbox",
        )

        entry = report["registry"]["netbox_ip_range"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"], "/api/ipam/ip-ranges/{id}/")
        self.assertEqual(entry["read"]["operation_id"],
                         "ipam_ip_ranges_retrieve")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "Ipam.IpamIPRangesRead")

    def test_prefers_exact_go_swagger_operation_alias_over_similar_paths(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/netbox": {
                    "resource_schemas": {
                        "netbox_device_console_port": {
                            "block": {
                                "attributes": {
                                    "device_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/dcim/console-port-templates/{id}/": {
                    "get": {
                        "operationId": (
                            "dcim_console_port_templates_retrieve"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/api/dcim/console-ports/{id}/": {
                    "get": {
                        "operationId": "dcim_console_ports_retrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/api/dcim/console-server-ports/{id}/": {
                    "get": {
                        "operationId": "dcim_console_server_ports_retrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_netbox_device_console_port.go", """
package provider

func resourceConsolePort() {
    resourceName := "netbox_device_console_port"
    _ = resourceName
    item, err := api.Dcim.DcimConsolePortsRead(params, nil)
    _ = item
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/netbox",
            resource_prefix="netbox",
        )

        entry = report["registry"]["netbox_device_console_port"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"], "/api/dcim/console-ports/{id}/")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "Dcim.DcimConsolePortsRead")

    def test_follows_provider_registration_to_resource_constructor(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/netbox": {
                    "resource_schemas": {
                        "netbox_power_feed": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/dcim/power-feeds/{id}/": {
                    "get": {
                        "operationId": "dcim_power_feeds_retrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/netbox/provider.go", """
package netbox

func resources() {
    _ = map[string]interface{}{
        "netbox_power_feed": resourceNetboxPowerFeed(),
    }
    _ = map[string]interface{}{
        "netbox_power_feed": dataSourceNetboxPowerFeed(),
    }
}
""")
        self._write("provider/netbox/resource_netbox_device_power_feed.go", """
package netbox

func resourceNetboxPowerFeed() {
    Read: resourceNetboxPowerFeedRead
}

func resourceNetboxPowerFeedRead() {
    item, err := api.Dcim.DcimPowerFeedsRead(params, nil)
    _ = item
    _ = err
}
""")
        self._write("provider/netbox/data_source_netbox_power_feed.go", """
package netbox

func dataSourceNetboxPowerFeed() {
    _, err := api.Dcim.DcimPowerFeedsList(params, nil)
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/netbox",
            resource_prefix="netbox",
        )

        entry = report["registry"]["netbox_power_feed"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            ["netbox/resource_netbox_device_power_feed.go"])
        self.assertEqual(entry["read"]["path"], "/api/dcim/power-feeds/{id}/")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "Dcim.DcimPowerFeedsRead")

    def test_follows_package_qualified_resource_registration(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/linode/linode": {
                    "resource_schemas": {
                        "linode_domain": {
                            "block": {
                                "attributes": {
                                    "domain": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/{apiVersion}/domains/{domainId}": {
                    "get": {
                        "operationId": "get-domain",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/linode/provider.go", """
package linode

import "github.com/example/terraform-provider-linode/linode/domain"

func resources() {
    _ = map[string]interface{}{
        "linode_domain": domain.Resource(),
    }
}
""")
        self._write("provider/linode/domain/resource.go", """
package domain

func Resource() {
    ReadContext: readResource
}

func readResource() {
    domain, err := client.GetDomain(ctx, id)
    _ = domain
    _ = err
}
""")
        self._write("provider/linode/domain/datasource.go", """
package domain

func DataSource() {}

func readDataSource() {
    domains, err := client.ListDomains(ctx, nil)
    _ = domains
    _ = err
}
""")
        self._write("provider/linode/instance/resource.go", """
package instance

func readResource() {
    instance, err := client.GetInstance(ctx, id)
    _ = instance
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/linode/linode",
            resource_prefix="linode",
        )

        entry = report["registry"]["linode_domain"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["source"]["files"], ["linode/domain/resource.go"])
        self.assertEqual(entry["read"]["path"], "/{apiVersion}/domains/{domainId}")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "GetDomain")

    def test_follows_shared_resource_read_callback_file(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/netbox": {
                    "resource_schemas": {
                        "netbox_available_prefix": {
                            "block": {
                                "attributes": {
                                    "parent_prefix_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/ipam/prefixes/{id}/": {
                    "get": {
                        "operationId": "ipam_prefixes_retrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/netbox/resource_netbox_available_prefix.go", """
package netbox

func resourceNetboxAvailablePrefix() {
    resourceName := "netbox_available_prefix"
    _ = resourceName
    Read: resourceNetboxPrefixRead
}
""")
        self._write("provider/netbox/resource_netbox_prefix.go", """
package netbox

func resourceNetboxPrefixRead() {
    item, err := api.Ipam.IpamPrefixesRead(params, nil)
    _ = item
    _ = err
}
""")
        self._write("provider/netbox/data_source_netbox_available_prefix.go", """
package netbox

func dataSourceNetboxAvailablePrefix() {
    resourceName := "netbox_available_prefix"
    _ = resourceName
    _, err := api.Ipam.IpamPrefixesAvailablePrefixesList(params, nil)
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/netbox",
            resource_prefix="netbox",
        )

        entry = report["registry"]["netbox_available_prefix"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            [
                "netbox/resource_netbox_available_prefix.go",
                "netbox/resource_netbox_prefix.go",
            ])
        self.assertEqual(entry["read"]["path"], "/api/ipam/prefixes/{id}/")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "Ipam.IpamPrefixesRead")

    def test_maps_flat_client_methods_to_openapi_operations(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/linode/linode": {
                    "resource_schemas": {
                        "linode_domain": {
                            "block": {
                                "attributes": {
                                    "domain": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/{apiVersion}/domains": {
                    "get": {
                        "operationId": "get-domains",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/{apiVersion}/domains/{domainId}": {
                    "get": {
                        "operationId": "get-domain",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_linode_domain.go", """
package provider

func resourceDomainRead() {
    resourceName := "linode_domain"
    _ = resourceName
    domain, err := client.GetDomain(ctx, id)
    _ = domain
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/linode/linode",
            resource_prefix="linode",
        )

        entry = report["registry"]["linode_domain"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"],
                         "/{apiVersion}/domains/{domainId}")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "GetDomain")

    def test_maps_flat_client_method_with_provider_word_in_operation_id(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/linode/linode": {
                    "resource_schemas": {
                        "linode_instance": {
                            "block": {
                                "attributes": {
                                    "label": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/{apiVersion}/linode/instances/{linodeId}": {
                    "get": {
                        "operationId": "get-linode-instance",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/{apiVersion}/volumes/{volumeId}": {
                    "get": {
                        "operationId": "get-volume",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/linode/instance/resource_linode_instance.go", """
package instance

func readResource() {
    instance, err := client.GetInstance(ctx, id)
    _ = instance
    _ = err
    volume, volumeErr := client.GetVolume(ctx, volumeID)
    _ = volume
    _ = volumeErr
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/linode/linode",
            resource_prefix="linode",
        )

        entry = report["registry"]["linode_instance"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["operation_id"], "get-linode-instance")
        self.assertEqual(entry["read"]["path"],
                         "/{apiVersion}/linode/instances/{linodeId}")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "GetInstance")

    def test_prefers_exact_flat_client_child_resource_method(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/linode/linode": {
                    "resource_schemas": {
                        "linode_nodebalancer_node": {
                            "block": {
                                "attributes": {
                                    "label": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/{apiVersion}/nodebalancers/{nodeBalancerId}": {
                    "get": {
                        "operationId": "get-node-balancer",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/{apiVersion}/nodebalancers/{nodeBalancerId}/configs/{configId}/nodes/{nodeId}": {
                    "get": {
                        "operationId": "get-node-balancer-node",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/linode/nbnode/resource_linode_nodebalancer_node.go", """
package nbnode

func readResource() {
    node, err := client.GetNodeBalancerNode(ctx, nbID, cfgID, id)
    _ = node
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/linode/linode",
            resource_prefix="linode",
        )

        entry = report["registry"]["linode_nodebalancer_node"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["operation_id"],
                         "get-node-balancer-node")
        self.assertEqual(
            entry["read"]["path"],
            "/{apiVersion}/nodebalancers/{nodeBalancerId}/configs/{configId}/nodes/{nodeId}")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "GetNodeBalancerNode")

    def test_prefers_direct_cloudflare_path_sequence(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/cloudflare/cloudflare": {
                    "resource_schemas": {
                        "cloudflare_firewall_rule": {
                            "block": {
                                "attributes": {
                                    "zone_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Cloudflare API"},
            "paths": {
                "/accounts/{account_id}/firewall/access_rules/rules/{rule_id}": {
                    "get": {
                        "operationId": (
                            "ip-access-rules-for-an-account-get-an-ip-access-rule"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/zones/{zone_id}/firewall/rules/{rule_id}": {
                    "get": {
                        "operationId": "firewall-rules-get-a-firewall-rule",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/zones/{zone_id}/firewall/ua_rules/{ua_rule_id}": {
                    "get": {
                        "operationId": (
                            "user-agent-blocking-rules-get-a-user-agent-blocking-rule"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/zones/{zone_id}/firewall/waf/packages/{package_id}/rules/{rule_id}": {
                    "get": {
                        "operationId": "waf-rules-get-a-waf-rule",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/internal/services/firewall_rule/resource.go", """
package firewall_rule

func (r *FirewallRuleResource) Read() {
    _, err := r.client.Firewall.Rules.Get(ctx, data.ID.ValueString(), params)
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/cloudflare/cloudflare",
            resource_prefix="cloudflare",
        )

        self.assertEqual(report["summary"]["mapped"], 1)
        entry = report["registry"]["cloudflare_firewall_rule"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["read"]["path"],
            "/zones/{zone_id}/firewall/rules/{rule_id}")
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "Firewall.Rules.Get")

    def test_maps_zscaler_package_function_calls_to_openapi_paths(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/zia": {
                    "resource_schemas": {
                        "zia_location_management": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Zscaler Internet Access API"},
            "paths": {
                "/locations": {
                    "get": {
                        "operationId": "Get Top Locations",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/locations/{locationId}": {
                    "get": {
                        "operationId": "Get Location",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/locations/{locationId}/sublocations": {
                    "get": {
                        "operationId": "Get Sub Locations",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/zia/resource_zia_location_management.go", """
package zia

import (
    "context"
    "github.com/zscaler/zscaler-sdk-go/v3/zscaler/zia/services/location/locationmanagement"
)

func resourceLocationManagementRead(ctx context.Context) {
    // locationmanagement.GetTopLocations(ctx, service) should not count.
    _ = "locationmanagement.GetLocations(ctx, service) should not count"
    byName, byNameErr := locationmanagement.GetLocationOrSublocationByName(ctx, service, name)
    resp, err := locationmanagement.GetLocationOrSublocationByID(ctx, service, id)
    _ = byName
    _ = byNameErr
    _ = resp
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/zscaler/zia",
            resource_prefix="zia",
        )

        self.assertEqual(report["summary"]["mapped"], 1)
        entry = report["registry"]["zia_location_management"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            ["zia/resource_zia_location_management.go"])
        self.assertEqual(entry["source"]["package_call_count"], 2)
        self.assertEqual(
            entry["read"]["path"],
            "/locations/{locationId}")
        self.assertEqual(
            entry["read"]["operation_id"],
            "Get Location")
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "locationmanagement.GetLocationOrSublocationByID")
        self.assertEqual(
            entry["read"]["hops"][0]["alternate_client_symbols"],
            [
                "locationmanagement.GetLocationOrSublocationByID",
                "locationmanagement.GetLocationOrSublocationByName",
            ])
        self.assertEqual(
            entry["read"]["hops"][0]["sdk_package"],
            "locationmanagement")

    def test_maps_framework_resource_package_calls(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/zcc": {
                    "resource_schemas": {
                        "zcc_forwarding_profile": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/papi/public/v1/webForwardingProfile/listByCompany": {
                    "get": {
                        "operationId": "List Forwarding Profiles By Company",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/internal/framework/resources/forwarding_profile.go", """
package resources

import (
    "context"
    "github.com/zscaler/zscaler-sdk-go/v3/zscaler/zcc/services/forwarding_profile"
)

func (r *ForwardingProfileResource) Read(ctx context.Context) {
    profiles, err := forwarding_profile.GetForwardingProfileByCompanyID(ctx, service, "", nil, nil)
    _ = profiles
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/zscaler/zcc",
            resource_prefix="zcc",
        )

        self.assertEqual(report["summary"]["mapped"], 1)
        entry = report["registry"]["zcc_forwarding_profile"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            ["internal/framework/resources/forwarding_profile.go"])
        self.assertEqual(
            entry["read"]["path"],
            "/papi/public/v1/webForwardingProfile/listByCompany")
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "forwarding_profile.GetForwardingProfileByCompanyID")

    def test_ignores_stdlib_package_get_calls(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_repository": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/repos/{owner}/{repo}": {
                    "get": {
                        "operationId": "repos/get",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/github/resource_github_repository.go", """
package github

import "os"

func readRepository() {
    _ = os.Getenv("GITHUB_OWNER")
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_repository"]
        self.assertEqual(entry["status"], "unmapped")
        self.assertEqual(entry["reason"], "no_source_operation_match")
        self.assertNotIn("package_call_count", entry["source"])

    def test_ignores_local_provider_package_get_calls(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/linode/linode": {
                    "resource_schemas": {
                        "linode_instance": {
                            "block": {
                                "attributes": {
                                    "label": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/{apiVersion}/linode/instances/{linodeId}": {
                    "get": {
                        "operationId": "get-linode-instance",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/linode/instance/resource_linode_instance.go", """
package instance

import "github.com/example/terraform-provider-linode/linode/helper"

func readResource() {
    seconds := helper.GetDeadlineSeconds(ctx)
    _ = seconds
}
""")
        self._write("provider/linode/helper/deadline.go", """
package helper

func GetDeadlineSeconds() {}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/linode/linode",
            resource_prefix="linode",
        )

        entry = report["registry"]["linode_instance"]
        self.assertEqual(entry["status"], "unmapped")
        self.assertEqual(entry["reason"], "no_source_operation_match")
        self.assertNotIn("package_call_count", entry["source"])

    def test_exact_resource_file_excludes_broad_provider_registration(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_issue": {
                            "block": {
                                "attributes": {
                                    "repository": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/repos/{owner}/{repo}/issues/{issue_number}": {
                    "get": {
                        "operationId": "issues/get",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/github/provider.go", """
package github

import "os"

var resourceType = "github_issue"

func configure() {
    _ = os.Getenv("GITHUB_OWNER")
}
""")
        self._write("provider/github/sweep.go", """
package github

var sweepResourceType = "github_issue"

func sweepIssues() {
    _, _, err := client.Repositories.Get(ctx, owner, repo)
    _ = err
}
""")
        self._write("provider/github/resource_github_issue.go", """
package github

func readIssue() {
    _, _, err := client.Issues.Get(ctx, owner, repo, number)
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_issue"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            ["github/resource_github_issue.go"])
        self.assertEqual(entry["read"]["path"],
                         "/repos/{owner}/{repo}/issues/{issue_number}")
        self.assertNotIn("package_call_count", entry["source"])

    def test_maps_prefixed_sdk_client_methods(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_release": {
                            "block": {
                                "attributes": {
                                    "repository": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/orgs/{org}/settings/immutable-releases/repositories": {
                    "get": {
                        "operationId": (
                            "orgs/get-immutable-releases-settings-repositories"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/repos/{owner}/{repo}/releases/{release_id}": {
                    "get": {
                        "operationId": "repos/get-release",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/github/resource_github_release.go", """
package github

func readRelease() {
    release, _, err := client.Repositories.GetRelease(ctx, owner, repo, releaseID)
    _ = release
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_release"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["path"],
                         "/repos/{owner}/{repo}/releases/{release_id}")
        self.assertEqual(entry["read"]["hops"][0]["client_symbol"],
                         "Repositories.GetRelease")

    def test_marks_list_read_winner_with_close_detail_as_ambiguous(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_repository": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/orgs/{org}/actions/cache/usage-by-repository": {
                    "get": {
                        "operationId": (
                            "actions/get-actions-cache-usage-by-repo-for-org"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/repos/{owner}/{repo}": {
                    "get": {
                        "operationId": "repos/get",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/github/resource_github_repository.go", """
package github

func readRepository() {
    repo, _, err := client.Repositories.Get(ctx, owner, name)
    _ = repo
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_repository"]
        self.assertEqual(entry["status"], "ambiguous_source_operation")
        self.assertEqual(entry["reason"], "ambiguous_source_operation")
        self.assertEqual(
            [candidate["path"] for candidate in entry["candidates"]],
            [
                "/orgs/{org}/actions/cache/usage-by-repository",
                "/repos/{owner}/{repo}",
            ])

    def test_maps_openapi_paths_without_operation_ids(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/digitalocean/digitalocean": {
                    "resource_schemas": {
                        "digitalocean_domain": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/v2/domains": {
                    "get": {
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/v2/domains/{domain_name}": {
                    "get": {
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/digitalocean/domain/resource_domain.go", """
package domain

func readDomain() {
    domain, resp, err := client.Domains.Get(ctx, name)
    _ = domain
    _ = resp
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/digitalocean/digitalocean",
            resource_prefix="digitalocean",
        )

        entry = report["registry"]["digitalocean_domain"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["source"]["files"],
            ["digitalocean/domain/resource_domain.go"])
        self.assertEqual(entry["read"]["path"], "/v2/domains/{domain_name}")
        self.assertEqual(entry["read"]["operation_id"],
                         "GET /v2/domains/{domain_name}")
        self.assertEqual(entry["read"]["operation_id_source"],
                         "synthetic_path")
        self.assertEqual(
            entry["read"]["hops"][1]["operation_id_source"],
            "synthetic_path")

    def test_ignores_operation_ids_in_comments_and_strings(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_project": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/projects/{id}": {
                    "get": {
                        "operationId": "ProjectsRetrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_project.go", r'''
package provider

var resourceName = "example_project"

// client.ProjectsAPI.ProjectsRetrieve(ctx, id)
var docs = `ProjectsRetrieve should not count from docs`
''')

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
        )

        self.assertEqual(report["summary"]["mapped"], 0)
        self.assertEqual(report["summary"]["unmapped"], 1)
        self.assertEqual(report["diagnostics"][0]["status"], "unmapped")
        self.assertEqual(
            report["registry"]["example_project"]["status"],
            "unmapped")
        self.assertEqual(
            report["registry"]["example_project"]["reason"],
            "no_source_operation_match")

    def test_reports_when_resource_source_file_is_not_found(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_missing": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/missing/{id}": {
                    "get": {
                        "operationId": "GetMissing",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        os.makedirs(source_root)

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
        )

        self.assertEqual(report["summary"]["resources_with_source_files"], 0)
        self.assertEqual(report["summary"]["resources_without_source_files"], 1)
        self.assertEqual(report["diagnostics"][0]["reason"], "resource_file_not_found")
        self.assertEqual(
            report["registry"]["example_missing"]["reason"],
            "resource_file_not_found")

    def test_marks_close_source_operation_matches_as_ambiguous(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_thing": {
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
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/things/{id}": {
                    "get": {
                        "operationId": "GetThing",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/things/{uid}": {
                    "get": {
                        "operationId": "RetrieveThing",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_thing.go", """
package provider

var resourceName = "example_thing"

func read() {
    client.ThingsAPI.GetThing(ctx, id)
    client.ThingsAPI.RetrieveThing(ctx, uid)
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
        )

        self.assertEqual(report["summary"]["mapped"], 0)
        self.assertEqual(report["summary"]["ambiguous"], 1)
        self.assertIn("example_thing", report["registry"])
        self.assertEqual(
            report["registry"]["example_thing"]["status"],
            "ambiguous_source_operation")
        self.assertEqual(
            len(report["registry"]["example_thing"]["candidates"]), 2)
        self.assertEqual(
            report["diagnostics"][0]["status"],
            "ambiguous_source_operation")
        self.assertEqual(len(report["diagnostics"][0]["ambiguous"]), 2)

    def test_maps_raw_rest_new_request_calls(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_actions_hosted_runner": {
                            "block": {
                                "attributes": {
                                    "runner_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/orgs/{org}/actions/hosted-runners/{hosted_runner_id}": {
                    "get": {
                        "operationId": (
                            "actions/get-hosted-runner-for-org"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/github/resource_github_actions_hosted_runner.go",
            """
package github

func readRunner() {
    req, err := client.NewRequest(
        "GET",
        fmt.Sprintf("orgs/%s/actions/hosted-runners/%s", orgName, runnerID),
        nil,
    )
    err = client.Do(ctx, req, &runner)
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_actions_hosted_runner"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(
            entry["read"]["path"],
            "/orgs/{org}/actions/hosted-runners/{hosted_runner_id}")
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "client.NewRequest GET /orgs/{arg}/actions/hosted-runners/{arg}")
        self.assertEqual(
            entry["read"]["hops"][0]["raw_rest_path"],
            "/orgs/{arg}/actions/hosted-runners/{arg}")

    def test_ast_facts_backend_maps_raw_rest_calls(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_actions_hosted_runner": {
                            "block": {
                                "attributes": {
                                    "runner_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/orgs/{org}/actions/hosted-runners/{hosted_runner_id}": {
                    "get": {
                        "operationId": (
                            "actions/get-hosted-runner-for-org"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/github/resource_github_actions_hosted_runner.go",
            "package github\n")
        source_facts = self._source_facts(
            source_root,
            files=[
                {
                    "path": (
                        "github/resource_github_actions_hosted_runner.go"),
                    "package": "github",
                    "imports": [],
                },
            ],
            raw_rest_calls=[
                {
                    "file": (
                        "github/resource_github_actions_hosted_runner.go"),
                    "function": "resourceGithubActionsHostedRunnerRead",
                    "symbol": "client.NewRequest",
                    "method": "GET",
                    "path": "orgs/%s/actions/hosted-runners/%s",
                },
            ],
        )

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
            source_facts=source_facts,
        )

        entry = report["registry"]["github_actions_hosted_runner"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["source"]["raw_rest_call_count"], 1)
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "client.NewRequest GET /orgs/{arg}/actions/hosted-runners/{arg}")
        self.assertEqual(
            entry["read"]["path"],
            "/orgs/{org}/actions/hosted-runners/{hosted_runner_id}")

    def test_marks_graphql_resource_as_non_rest_source(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_branch_protection": {
                            "block": {
                                "attributes": {
                                    "repository_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/repos/{owner}/{repo}/branches/{branch}/protection": {
                    "get": {
                        "operationId": "repos/get-branch-protection",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/github/resource_github_branch_protection.go", """
package github

import "github.com/shurcooL/githubv4"

type branchProtectionQuery struct {
    Node struct {
        ID githubv4.ID `graphql:"id"`
    } `graphql:"node(id: $id)"`
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_branch_protection"]
        self.assertEqual(entry["status"], "graphql_source")
        self.assertEqual(entry["reason"], "graphql_source")
        self.assertTrue(entry["source"]["graphql"])
        self.assertEqual(report["summary"]["graphql_source"], 1)
        self.assertEqual(report["summary"]["unmapped"], 0)

    def test_uses_relationship_list_operation_as_read_evidence(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_repository_topics": {
                            "block": {
                                "attributes": {
                                    "repository": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/repos/{owner}/{repo}/topics": {
                    "get": {
                        "operationId": "repos/get-all-topics",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/github/resource_github_repository_topics.go", """
package github

func readTopics() {
    topics, _, err := client.Repositories.ListAllTopics(ctx, owner, repo, nil)
    _ = topics
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        entry = report["registry"]["github_repository_topics"]
        self.assertEqual(entry["status"], "mapped")
        self.assertTrue(entry["source"]["relationship_list_read"])
        self.assertEqual(entry["read"]["evidence_kind"],
                         "relationship_list_read")
        self.assertEqual(entry["read"]["path"], "/repos/{owner}/{repo}/topics")

    def test_splits_camel_case_sdk_chain_tokens_for_path_matching(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/pagerduty/pagerduty": {
                    "resource_schemas": {
                        "pagerduty_event_orchestration_global": {
                            "block": {
                                "attributes": {
                                    "event_orchestration": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/event_orchestrations/{id}/global": {
                    "get": {
                        "operationId": "getOrchPathGlobal",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/pagerduty/resource_pagerduty_event_orchestration_global.go",
            """
package pagerduty

func readGlobal() {
    _, _, err := client.EventOrchestrationPaths.GetContext(ctx, id, "global")
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/pagerduty/pagerduty",
            resource_prefix="pagerduty",
        )

        entry = report["registry"]["pagerduty_event_orchestration_global"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["operation_id"], "getOrchPathGlobal")
        self.assertEqual(
            entry["read"]["hops"][0]["client_symbol"],
            "EventOrchestrationPaths.GetContext")

    def test_uses_subscriber_list_operation_as_relationship_read_evidence(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/pagerduty/pagerduty": {
                    "resource_schemas": {
                        "pagerduty_business_service_subscriber": {
                            "block": {
                                "attributes": {
                                    "subscriber_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/business_services/{id}/subscribers": {
                    "get": {
                        "operationId": "getBusinessServiceSubscribers",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/pagerduty/resource_pagerduty_business_service_subscriber.go",
            """
package pagerduty

func readSubscriber() {
    subscribers, _, err := client.BusinessServiceSubscribers.List(ctx, id)
    _ = subscribers
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/pagerduty/pagerduty",
            resource_prefix="pagerduty",
        )

        entry = report["registry"]["pagerduty_business_service_subscriber"]
        self.assertEqual(entry["status"], "mapped")
        self.assertEqual(entry["read"]["evidence_kind"],
                         "relationship_list_read")
        self.assertEqual(entry["read"]["path"],
                         "/business_services/{id}/subscribers")

    def test_relationship_list_read_rejects_broad_collection_path(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/pagerduty/pagerduty": {
                    "resource_schemas": {
                        "pagerduty_service_dependency": {
                            "block": {
                                "attributes": {
                                    "dependent_service": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/business_services": {
                    "get": {
                        "operationId": "listBusinessServices",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/service_dependencies/business_services/{id}": {
                    "get": {
                        "operationId": "getBusinessServiceServiceDependencies",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/service_dependencies/technical_services/{id}": {
                    "get": {
                        "operationId": (
                            "getTechnicalServiceServiceDependencies"),
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/pagerdutyplugin/resource_pagerduty_service_dependency.go",
            """
package pagerdutyplugin

func readDependency() {
    business, _, err := client.ListBusinessServiceDependenciesWithContext(ctx, id)
    technical, _, err := client.ListTechnicalServiceDependenciesWithContext(ctx, id)
    _ = business
    _ = technical
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/pagerduty/pagerduty",
            resource_prefix="pagerduty",
        )

        entry = report["registry"]["pagerduty_service_dependency"]
        self.assertEqual(entry["status"], "ambiguous_source_operation")
        self.assertEqual(
            sorted(candidate["path"] for candidate in entry["candidates"]),
            [
                "/service_dependencies/business_services/{id}",
                "/service_dependencies/technical_services/{id}",
            ])

    def test_sdk_chain_requires_more_than_generic_terminal_match(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/pagerduty/pagerduty": {
                    "resource_schemas": {
                        "pagerduty_slack_connection": {
                            "block": {
                                "attributes": {
                                    "connection_id": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/workflows/integrations/{integration_id}/connections/{id}": {
                    "get": {
                        "operationId": "getWorkflowIntegrationConnection",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/pagerduty/resource_pagerduty_slack_connection.go",
            """
package pagerduty

func readSlackConnection() {
    connection, _, err := client.SlackConnections.Get(ctx, id)
    _ = connection
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/pagerduty/pagerduty",
            resource_prefix="pagerduty",
        )

        entry = report["registry"]["pagerduty_slack_connection"]
        self.assertEqual(entry["status"], "unmapped")
        self.assertEqual(entry["reason"], "no_source_operation_match")

    def test_keeps_selected_client_symbol_when_merging_alternates(self):
        schema_path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/integrations/github": {
                    "resource_schemas": {
                        "github_actions_organization_secret": {
                            "block": {
                                "attributes": {
                                    "secret_name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        openapi_path = self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/orgs/{org}/actions/secrets/public-key": {
                    "get": {
                        "operationId": "actions/get-org-public-key",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/orgs/{org}/actions/secrets/{secret_name}": {
                    "get": {
                        "operationId": "actions/get-org-secret",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        source_root = os.path.join(self.tmp, "provider")
        self._write(
            "provider/github/resource_github_actions_organization_secret.go",
            """
package github

func readSecret() {
    key, _, err := client.Actions.GetOrgPublicKey(ctx, org)
    secret, _, err := client.Actions.GetOrgSecret(ctx, org, name)
    _ = key
    _ = secret
    _ = err
}
""")

        report = source_operation_map.derive_registry(
            schema_path,
            openapi_path,
            source_root,
            provider_source="registry.terraform.io/integrations/github",
            resource_prefix="github",
        )

        provider_call = (
            report["registry"]["github_actions_organization_secret"]
            ["read"]["hops"][0])
        self.assertEqual(
            provider_call["client_symbol"],
            "Actions.GetOrgSecret")
        self.assertIn(
            "Actions.GetOrgPublicKey",
            provider_call["alternate_client_symbols"])

    def test_playlist_name_does_not_make_operation_list_shaped(self):
        operation = {
            "operation_id": "getPlaylist",
            "path": "/playlists/{uid}",
        }

        self.assertEqual(source_operation_map._path_kind(operation), "detail")

    def test_product_search_word_does_not_make_detail_path_list_shaped(self):
        operation = {
            "operation_id": "ai-search-fetch-instance",
            "path": "/accounts/{account_id}/ai-search/instances/{id}",
        }

        self.assertFalse(
            source_operation_map._is_list_operation(
                operation["operation_id"]))
        self.assertEqual(source_operation_map._path_kind(operation), "detail")

    def test_product_list_word_does_not_make_detail_path_list_shaped(self):
        operation = {
            "operation_id": "zero-trust-lists-zero-trust-list-details",
            "path": "/accounts/{account_id}/gateway/lists/{list_id}",
        }

        self.assertFalse(
            source_operation_map._is_list_operation(
                operation["operation_id"]))
        self.assertEqual(source_operation_map._path_kind(operation), "detail")


if __name__ == "__main__":
    unittest.main()
