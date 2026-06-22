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
        self.assertNotIn("example_thing", report["registry"])
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
