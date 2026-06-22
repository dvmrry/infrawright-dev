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

    def test_playlist_name_does_not_make_operation_list_shaped(self):
        operation = {
            "operation_id": "getPlaylist",
            "path": "/playlists/{uid}",
        }

        self.assertEqual(source_operation_map._path_kind(operation), "detail")


if __name__ == "__main__":
    unittest.main()
