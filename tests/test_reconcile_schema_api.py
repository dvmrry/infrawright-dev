import json
import os
import shutil
import tempfile
import unittest
from io import StringIO

try:
    from contextlib import redirect_stderr
except ImportError:
    redirect_stderr = None

from engine import reconcile_schema_api as reconcile


def _paths(report, bucket):
    return set(entry["path"] for entry in report.as_dict()["paths"][bucket])


def _entry(report, bucket, path):
    for item in report.as_dict()["paths"][bucket]:
        if item["path"] == path:
            return item
    raise AssertionError("missing %s path %s" % (bucket, path))


class ReconcileSchemaApiTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="reconcile-")

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_json(self, name, data):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return path

    def _schema(self):
        return {
            "block": {
                "attributes": {
                    "id": {"type": "string", "computed": True},
                    "name": {"type": "string", "required": True},
                    "enabled": {"type": "bool", "optional": True},
                    "org_id": {"type": "string", "optional": True},
                    "status": {"type": "string", "computed": True},
                    "metadata": {"type": ["map", "string"], "optional": True},
                    "settings": {
                        "type": ["object", {
                            "mode": "string",
                            "priority": "number",
                        }],
                        "optional": True,
                    },
                    "tags": {"type": ["list", "string"], "optional": True},
                },
                "block_types": {
                    "interfaces": {
                        "nesting_mode": "list",
                        "block": {
                            "attributes": {
                                "address": {
                                    "type": "string",
                                    "required": True,
                                },
                                "port": {"type": "number", "optional": True},
                                "generated": {
                                    "type": "string",
                                    "computed": True,
                                },
                            }
                        },
                    }
                },
            }
        }

    def test_classifies_inputs_computed_fields_and_unknowns(self):
        raw = [{
            "id": "1",
            "name": "core",
            "enabled": True,
            "orgId": 1,
            "status": "active",
            "metadata": {"rack": "r1"},
            "settings": {
                "mode": "managed",
                "priority": 10,
                "apiOnly": "surprise",
            },
            "tags": ["edge"],
            "interfaces": [{
                "address": "192.0.2.10",
                "port": 443,
                "generated": "provider-state",
                "mystery": "new-surface",
            }],
            "extraTop": "new-surface",
        }]
        report = reconcile.reconcile_items(
            "sample_widget", raw, self._schema(), override={})

        self.assertIn("name", _paths(report, "kept"))
        self.assertIn("enabled", _paths(report, "kept"))
        self.assertIn("org_id", _paths(report, "transformed"))
        self.assertIn("metadata", _paths(report, "kept"))
        self.assertIn("settings.mode", _paths(report, "kept"))
        self.assertIn("settings.priority", _paths(report, "kept"))
        self.assertIn("interfaces[].address", _paths(report, "kept"))
        self.assertIn("interfaces[].port", _paths(report, "kept"))

        self.assertIn("id", _paths(report, "dropped_known"))
        self.assertIn("status", _paths(report, "dropped_known"))
        self.assertIn("interfaces[].generated", _paths(report, "dropped_known"))

        self.assertIn("extra_top", _paths(report, "unknown"))
        self.assertIn("settings.api_only", _paths(report, "unknown"))
        self.assertIn("interfaces[].mystery", _paths(report, "unknown"))
        self.assertTrue(report.has_unknowns())

    def test_overrides_explain_renames_drops_defaults_and_acknowledged(self):
        raw = [{
            "oldName": "core",
            "enabled": True,
            "mode": "DEFAULT",
            "ignored": "drop me",
            "apiOnly": "known read-only",
        }]
        schema = {
            "block": {
                "attributes": {
                    "name": {"type": "string", "required": True},
                    "enabled": {"type": "bool", "optional": True},
                    "mode": {"type": "string", "optional": True},
                }
            }
        }
        override = {
            "renames": {"old_name": "name"},
            "drops": ["ignored"],
            "drop_if_default": {"mode": "DEFAULT"},
            "acknowledged_drops": ["api_only"],
        }
        report = reconcile.reconcile_items(
            "sample_widget", raw, schema, override=override)

        self.assertIn("old_name", _paths(report, "renamed"))
        self.assertIn("name", _paths(report, "kept"))
        self.assertIn("ignored", _paths(report, "dropped_override"))
        self.assertIn("mode", _paths(report, "dropped_default"))
        self.assertIn("api_only", _paths(report, "dropped_acknowledged"))
        self.assertFalse(report.has_unknowns())

    def test_netbox_style_relationship_choices_and_tags_are_classified(self):
        raw = [{
            "name": "edge-01",
            "status": {"value": "active", "label": "Active"},
            "site": {"id": 10, "name": "Lab"},
            "tags": [{"name": "IW Test", "slug": "iw-test"}],
            "created": "2026-06-21T00:00:00Z",
            "emptyRelation": None,
        }]
        schema = {
            "block": {
                "attributes": {
                    "name": {"type": "string", "required": True},
                    "status": {"type": "string", "optional": True},
                    "site_id": {"type": "number", "required": True},
                    "tags": {"type": ["set", "string"], "optional": True},
                }
            }
        }
        report = reconcile.reconcile_items(
            "netbox_device", raw, schema, override={})

        self.assertIn("name", _paths(report, "kept"))
        self.assertIn("status", _paths(report, "transformed"))
        self.assertIn("tags", _paths(report, "transformed"))
        self.assertIn("site", _paths(report, "relationship"))
        self.assertIn("created", _paths(report, "dropped_known"))
        self.assertIn("empty_relation", _paths(report, "dropped_known"))
        self.assertFalse(report.has_unknowns())

    def test_api_options_metadata_splits_read_only_and_provider_gaps(self):
        raw = [{
            "name": "edge-01",
            "serverGenerated": "readback-only",
            "comments": "provider cannot set this",
            "emptyNote": "",
        }]
        schema = {
            "block": {
                "attributes": {
                    "name": {"type": "string", "required": True},
                }
            }
        }
        metadata = reconcile.api_metadata_from_options({
            "actions": {
                "POST": {
                    "serverGenerated": {
                        "type": "string",
                        "readOnly": True,
                    },
                    "comments": {
                        "type": "string",
                        "read_only": False,
                        "required": False,
                    },
                    "emptyNote": {
                        "type": "string",
                        "read_only": False,
                        "required": False,
                    },
                }
            }
        })
        report = reconcile.reconcile_items(
            "netbox_device", raw, schema, override={},
            api_metadata=metadata)

        self.assertIn("server_generated", _paths(report, "dropped_known"))
        self.assertIn("empty_note", _paths(report, "dropped_known"))
        self.assertIn("comments", _paths(report, "unknown"))
        self.assertEqual(
            _entry(report, "unknown", "comments")["reasons"],
            {"api_writable_not_in_provider": 1})
        self.assertEqual(
            report.as_dict()["suggestions"]["provider_gaps"],
            ["comments"])

    def test_openapi_metadata_splits_response_only_and_writable_gaps(self):
        spec = {
            "openapi": "3.0.3",
            "paths": {
                "/widgets/{id}": {
                    "get": {
                        "responses": {
                            "200": {
                                "content": {
                                    "application/json": {
                                        "schema": {
                                            "$ref": "#/components/schemas/Widget"
                                        }
                                    }
                                }
                            }
                        }
                    }
                },
                "/widgets": {
                    "post": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/WidgetWrite"
                                    }
                                }
                            }
                        },
                        "responses": {"200": {"description": "ok"}},
                    }
                },
            },
            "components": {
                "schemas": {
                    "Widget": {
                        "type": "object",
                        "properties": {
                            "id": {"type": "string"},
                            "name": {"type": "string"},
                            "display": {
                                "type": "string",
                                "readOnly": True,
                            },
                            "status": {"type": "string"},
                            "settings": {
                                "type": "object",
                                "properties": {
                                    "mode": {"type": "string"},
                                },
                            },
                        },
                    },
                    "WidgetWrite": {
                        "type": "object",
                        "required": ["name"],
                        "properties": {
                            "name": {"type": "string"},
                            "status": {"type": "string"},
                            "settings": {
                                "type": "object",
                                "properties": {
                                    "mode": {"type": "string"},
                                },
                            },
                        },
                    },
                },
            },
        }
        raw = [{
            "id": "w1",
            "name": "core",
            "display": "Core",
            "status": "active",
            "settings": {"mode": "managed"},
        }]
        schema = {
            "block": {
                "attributes": {
                    "name": {"type": "string", "required": True},
                }
            }
        }
        metadata = reconcile.api_metadata_from_openapi(
            spec,
            read_operations=["GET:/widgets/{id}"],
            write_operations=["POST:/widgets"])
        report = reconcile.reconcile_items(
            "sample_widget", raw, schema, override={},
            api_metadata=metadata)

        self.assertIn("id", _paths(report, "dropped_known"))
        self.assertIn("display", _paths(report, "dropped_known"))
        self.assertIn("status", _paths(report, "unknown"))
        self.assertIn("settings.mode", _paths(report, "unknown"))
        self.assertEqual(
            _entry(report, "unknown", "status")["reasons"],
            {"api_writable_not_in_provider": 1})
        self.assertEqual(
            report.as_dict()["suggestions"]["provider_gaps"],
            ["settings.mode", "status"])

    def test_openapi_refs_can_index_allof_array_members(self):
        spec = {
            "openapi": "3.0.3",
            "paths": {
                "/widgets/{id}": {
                    "get": {
                        "responses": {
                            "200": {
                                "content": {
                                    "application/json": {
                                        "schema": {
                                            "$ref": (
                                                "#/components/schemas/"
                                                "WidgetEnvelope/allOf/0")
                                        }
                                    }
                                }
                            }
                        }
                    }
                },
                "/widgets": {
                    "post": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": (
                                            "#/components/schemas/"
                                            "WidgetEnvelope/allOf/1")
                                    }
                                }
                            }
                        },
                        "responses": {"200": {"description": "ok"}},
                    }
                },
            },
            "components": {
                "schemas": {
                    "WidgetEnvelope": {
                        "allOf": [
                            {
                                "type": "object",
                                "properties": {
                                    "id": {"type": "string"},
                                    "name": {"type": "string"},
                                    "display": {
                                        "type": "string",
                                        "readOnly": True,
                                    },
                                },
                            },
                            {
                                "type": "object",
                                "properties": {
                                    "name": {"type": "string"},
                                },
                            },
                        ],
                    },
                },
            },
        }
        metadata = reconcile.api_metadata_from_openapi(
            spec,
            read_operations=["GET:/widgets/{id}"],
            write_operations=["POST:/widgets"])

        self.assertEqual(metadata["name"]["writable"], True)
        self.assertEqual(metadata["id"]["response_only"], True)
        self.assertEqual(metadata["display"]["read_only"], True)

    def test_loads_raw_terraform_provider_schema_shape(self):
        path = self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/netbox": {
                    "resource_schemas": {
                        "netbox_site": self._schema(),
                    }
                }
            }
        })
        schema = reconcile.load_resource_schema(
            "netbox_site", schema_path=path,
            provider_source="registry.terraform.io/example/netbox")
        self.assertEqual(schema["block"]["attributes"]["name"]["required"], True)

    def test_cli_fails_on_unknown_when_requested(self):
        schema_path = self._write_json("schema.json", {
            "resource_schemas": {"sample_widget": self._schema()}
        })
        api_path = self._write_json("api.json", {
            "results": [{
                "name": "core",
                "extraTop": "new-surface",
            }]
        })
        out_path = os.path.join(self.tmp, "report.json")
        argv = [
            "sample_widget",
            "--schema", schema_path,
            "--api", api_path,
            "--out", out_path,
            "--fail-on-unknown",
        ]
        if redirect_stderr is None:
            code = reconcile.main(argv)
        else:
            with redirect_stderr(StringIO()):
                code = reconcile.main(argv)
        self.assertEqual(code, 4)
        with open(out_path, encoding="utf-8") as f:
            data = json.load(f)
        self.assertEqual(data["suggestions"]["review_unknown"], ["extra_top"])


if __name__ == "__main__":
    unittest.main()
