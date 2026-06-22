import json
import os
import shutil
import tempfile
import unittest

from engine import openapi_resource_map


class OpenApiResourceMapTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="openapi-map-")

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_json(self, name, data):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return path

    def _schema_path(self):
        return self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/netbox": {
                    "resource_schemas": {
                        "netbox_device_interface": {
                            "block": {
                                "attributes": {
                                    "device_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                }
                            }
                        },
                        "netbox_interface": {
                            "block": {
                                "attributes": {
                                    "virtual_machine_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                }
                            }
                        },
                        "netbox_available_ip_address": {
                            "block": {
                                "attributes": {
                                    "prefix_id": {
                                        "type": "number",
                                        "optional": True,
                                    },
                                    "ip_range_id": {
                                        "type": "number",
                                        "optional": True,
                                    },
                                    "ip_address": {
                                        "type": "string",
                                        "computed": True,
                                    },
                                    "description": {
                                        "type": "string",
                                        "optional": True,
                                    },
                                }
                            }
                        },
                        "netbox_available_prefix": {
                            "block": {
                                "attributes": {
                                    "parent_prefix_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "prefix_length": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "prefix": {
                                        "type": "string",
                                        "computed": True,
                                    },
                                }
                            }
                        },
                        "netbox_device_primary_ip": {
                            "block": {
                                "attributes": {
                                    "device_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "ip_address_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "ip_address_version": {
                                        "type": "number",
                                        "optional": True,
                                    },
                                }
                            }
                        },
                        "netbox_device_interface_primary_mac_address": {
                            "block": {
                                "attributes": {
                                    "interface_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "mac_address_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                }
                            }
                        },
                        "netbox_virtual_machine_interface_primary_mac_address": {
                            "block": {
                                "attributes": {
                                    "interface_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                    "mac_address_id": {
                                        "type": "number",
                                        "required": True,
                                    },
                                }
                            }
                        },
                    }
                }
            }
        })

    def _openapi_path(self):
        crud = {
            "get": {"responses": {"200": {"description": "ok"}}},
            "post": {
                "requestBody": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "type": "object",
                                "properties": {
                                    "name": {"type": "string"},
                                    "primary_mac_address": {"type": "integer"},
                                },
                            }
                        }
                    }
                },
                "responses": {"200": {"description": "ok"}},
            },
        }
        detail = {
            "get": {
                "responses": {
                    "200": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "type": "object",
                                    "properties": {
                                        "id": {"type": "integer"},
                                        "name": {"type": "string"},
                                        "primary_mac_address": {
                                            "type": "integer",
                                            "readOnly": True,
                                        },
                                    },
                                }
                            }
                        }
                    }
                }
            },
            "put": crud["post"],
            "patch": crud["post"],
        }
        device_detail = {
            "get": {
                "responses": {
                    "200": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "type": "object",
                                    "properties": {
                                        "id": {"type": "integer"},
                                        "primary_ip4": {
                                            "type": "integer",
                                            "readOnly": True,
                                        },
                                    },
                                }
                            }
                        }
                    }
                }
            },
            "patch": {
                "requestBody": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "type": "object",
                                "properties": {
                                    "primary_ip4": {"type": "integer"},
                                    "primary_ip6": {"type": "integer"},
                                },
                            }
                        }
                    }
                },
                "responses": {"200": {"description": "ok"}},
            },
        }
        available_ip_action = {
            "get": {"responses": {"200": {"description": "ok"}}},
            "post": {
                "requestBody": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "type": "array",
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "description": {"type": "string"},
                                    },
                                },
                            }
                        }
                    }
                },
                "responses": {"201": {"description": "created"}},
            },
        }
        available_prefix_action = {
            "post": {
                "requestBody": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "type": "array",
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "prefix_length": {"type": "integer"},
                                    },
                                },
                            }
                        }
                    }
                },
                "responses": {"201": {"description": "created"}},
            },
        }
        return self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/dcim/devices/": crud,
                "/api/dcim/devices/{id}/": device_detail,
                "/api/dcim/interfaces/": crud,
                "/api/dcim/interfaces/{id}/": detail,
                "/api/ipam/ip-addresses/": crud,
                "/api/ipam/ip-addresses/{id}/": detail,
                "/api/ipam/ip-ranges/{id}/available-ips/": available_ip_action,
                "/api/ipam/prefixes/{id}/available-prefixes/": available_prefix_action,
                "/api/ipam/prefixes/{id}/available-ips/": available_ip_action,
                "/api/virtualization/interfaces/": crud,
                "/api/virtualization/interfaces/{id}/": detail,
            },
            "components": {"schemas": {}},
        })

    def test_maps_and_disambiguates_with_schema_hints(self):
        report = openapi_resource_map.build_report(
            self._schema_path(),
            self._openapi_path(),
            provider_source="registry.terraform.io/example/netbox",
            resource_prefix="netbox",
            api_prefix="/api/",
        )
        by_resource = dict((r["resource"], r) for r in report["resources"])

        self.assertEqual(
            by_resource["netbox_device_interface"]["collection_path"],
            "/api/dcim/interfaces/")
        self.assertEqual(
            by_resource["netbox_interface"]["collection_path"],
            "/api/virtualization/interfaces/")
        self.assertEqual(
            by_resource["netbox_available_ip_address"]["status"],
            "special")
        self.assertEqual(
            by_resource["netbox_available_ip_address"]["special_type"],
            "allocation_action")
        self.assertEqual(
            sorted(a["path"] for a in by_resource[
                "netbox_available_ip_address"]["actions"]),
            [
                "/api/ipam/ip-ranges/{id}/available-ips/",
                "/api/ipam/prefixes/{id}/available-ips/",
            ])
        self.assertEqual(
            by_resource["netbox_device_primary_ip"]["status"],
            "special")
        self.assertEqual(
            by_resource["netbox_device_primary_ip"]["special_type"],
            "derived_relationship")
        self.assertEqual(
            by_resource["netbox_device_primary_ip"][
                "assignments"][0]["write_fields"],
            ["primary_ip4", "primary_ip6"])
        self.assertEqual(
            by_resource[
                "netbox_device_interface_primary_mac_address"]["status"],
            "special")
        self.assertEqual(
            by_resource[
                "netbox_device_interface_primary_mac_address"][
                "special_type"],
            "derived_relationship")
        self.assertEqual(
            by_resource[
                "netbox_device_interface_primary_mac_address"][
                "assignments"][0]["parent_detail_path"],
            "/api/dcim/interfaces/{id}/")
        self.assertEqual(
            by_resource[
                "netbox_virtual_machine_interface_primary_mac_address"][
                "assignments"][0]["parent_detail_path"],
            "/api/virtualization/interfaces/{id}/")
        self.assertEqual(
            by_resource[
                "netbox_device_interface_primary_mac_address"][
                "assignments"][0]["write_fields"],
            ["primary_mac_address"])
        self.assertEqual(
            by_resource["netbox_available_prefix"]["actions"][0][
                "parent_id_fields"],
            ["parent_prefix_id"])
        self.assertEqual(
            by_resource["netbox_available_ip_address"]["static_contract"][
                "write_top_level_paths"],
            ["description"])
        self.assertEqual(
            by_resource["netbox_available_prefix"]["static_contract"][
                "write_top_level_paths"],
            ["prefix_length"])
        self.assertEqual(report["summary"]["matched"], 2)
        self.assertEqual(report["summary"]["special"], 5)
        self.assertEqual(report["summary"]["unmatched"], 0)

    def test_maps_slashless_openapi_collections(self):
        schema_path = self._write_json("grafana-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/grafana": {
                    "resource_schemas": {
                        "grafana_folder": {
                            "block": {
                                "attributes": {
                                    "title": {
                                        "type": "string",
                                        "required": True,
                                    },
                                    "uid": {
                                        "type": "string",
                                        "optional": True,
                                    },
                                }
                            }
                        },
                    }
                }
            }
        })
        openapi_path = self._write_json("grafana-openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/folders": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "post": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "title": {"type": "string"},
                                            "uid": {"type": "string"},
                                        },
                                    }
                                }
                            }
                        },
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/folders/{folder_uid}": {
                    "put": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "title": {"type": "string"},
                                        },
                                    }
                                }
                            }
                        },
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/example/grafana",
            resource_prefix="grafana",
            api_prefix="/",
        )
        item = report["resources"][0]

        self.assertEqual(item["status"], "matched")
        self.assertEqual(item["collection_path"], "/folders")
        self.assertEqual(item["detail_path"], "/folders/{folder_uid}")
        self.assertEqual(
            item["static_contract"]["write_top_level_paths"],
            ["title", "uid"])

    def test_maps_camel_case_parent_scoped_collections(self):
        schema_path = self._write_json("zpa-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/zpa": {
                    "provider": {
                        "block": {
                            "attributes": {
                                "zpa_cloud": {"type": "string", "optional": True},
                                "client_id": {"type": "string", "optional": True},
                                "client_secret": {
                                    "type": "string",
                                    "optional": True,
                                    "sensitive": True,
                                },
                            },
                        },
                    },
                    "resource_schemas": {
                        "zpa_app_connector_group": {
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
        openapi_path = self._write_json("zpa-openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Zscaler Private Access API"},
            "servers": [{"url": "https://api.example.test/zpa"}],
            "paths": {
                "/mgmtconfig/v1/admin/customers/{customerId}/appConnectorGroup": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "post": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "name": {"type": "string"},
                                        },
                                    },
                                },
                            },
                        },
                        "responses": {"201": {"description": "created"}},
                    },
                },
                "/mgmtconfig/v1/admin/customers/{customerId}/appConnectorGroup/{appConnectorGroupId}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "put": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "name": {"type": "string"},
                                        },
                                    },
                                },
                            },
                        },
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/zscaler/zpa",
            resource_prefix="zpa",
            api_prefix="/",
        )
        item = report["resources"][0]

        self.assertEqual(item["status"], "matched")
        self.assertEqual(
            item["collection_path"],
            "/mgmtconfig/v1/admin/customers/{customerId}/appConnectorGroup")
        self.assertEqual(
            item["detail_path"],
            "/mgmtconfig/v1/admin/customers/{customerId}/appConnectorGroup/{appConnectorGroupId}")
        self.assertEqual(
            report["openapi"]["profile"]["top_collection_segments"][0],
            {"segment": "app-connector-group", "paths": 2})
        self.assertEqual(report["coverage"]["coverage_ratio"], 1.0)

    def test_surfaces_low_coverage_as_wrong_or_partial_spec(self):
        schema_path = self._write_json("grafana-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/grafana/grafana": {
                    "provider": {
                        "block": {
                            "attributes": {
                                "url": {"type": "string", "optional": True},
                                "auth": {
                                    "type": "string",
                                    "optional": True,
                                    "sensitive": True,
                                },
                                "cloud_access_policy_token": {
                                    "type": "string",
                                    "optional": True,
                                    "sensitive": True,
                                },
                                "oncall_url": {
                                    "type": "string",
                                    "optional": True,
                                },
                            },
                        },
                    },
                    "resource_schemas": {
                        "grafana_folder": {
                            "block": {
                                "attributes": {
                                    "title": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                        "grafana_cloud_stack": {
                            "block": {
                                "attributes": {
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                        "grafana_oncall_integration": {
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
        openapi_path = self._write_json("core-openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Grafana HTTP API"},
            "servers": [{"url": "/api"}],
            "paths": {
                "/folders": {
                    "post": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "title": {"type": "string"},
                                        },
                                    },
                                },
                            },
                        },
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/folders/{folder_uid}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "put": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "title": {"type": "string"},
                                        },
                                    },
                                },
                            },
                        },
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/grafana/grafana",
            resource_prefix="grafana",
            api_prefix="/",
        )
        warnings = [warning["code"] for warning in report["coverage"]["warnings"]]

        self.assertEqual(report["summary"]["matched"], 1)
        self.assertEqual(report["coverage"]["family_coverage"]["cloud"], {
            "unmatched": 1,
        })
        self.assertIn("provider_config_suggests_multiple_surfaces", warnings)
        self.assertIn("uncovered_resource_families", warnings)
        self.assertEqual(
            [hint["name"] for hint in report["provider_config_hints"]],
            ["auth", "cloud_access_policy_token", "oncall_url", "url"])

    def test_maps_ztc_openapi_aliases_and_activation_action(self):
        schema_path = self._write_json("ztc-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/ztc": {
                    "resource_schemas": {
                        "ztc_activation_status": {
                            "block": {
                                "attributes": {
                                    "admin_activate_status": {
                                        "type": "string",
                                        "optional": True,
                                    },
                                },
                            },
                        },
                        "ztc_forwarding_gateway": {
                            "block": {
                                "attributes": {
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                    "primary_type": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                        "ztc_ip_pool_groups": {
                            "block": {
                                "attributes": {
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                    "ip_addresses": {
                                        "type": ["set", "string"],
                                        "required": True,
                                    },
                                },
                            },
                        },
                        "ztc_traffic_forwarding_dns_rule": {
                            "block": {
                                "attributes": {
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                    "order": {
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
        openapi_path = self._write_json("ztw-openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Zscaler Cloud & Branch Connector API"},
            "servers": [{"url": "https://api.zsapi.net/ztw/api/v1"}],
            "paths": {
                "/ecAdminActivateStatus": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
                "/ecAdminActivateStatus/activate": {
                    "put": {
                        "requestBody": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "type": "object",
                                        "properties": {
                                            "adminActivateStatus": {
                                                "type": "string",
                                            },
                                        },
                                    },
                                },
                            },
                        },
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/gateways": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "post": {"responses": {"201": {"description": "ok"}}},
                },
                "/gateways/{gatewayId}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "put": {"responses": {"200": {"description": "ok"}}},
                },
                "/ipGroups": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "post": {"responses": {"201": {"description": "ok"}}},
                },
                "/ipGroups/{ipGroupId}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "put": {"responses": {"200": {"description": "ok"}}},
                },
                "/ecRules/ecDns": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "post": {"responses": {"201": {"description": "ok"}}},
                },
                "/ecRules/ecDns/{ruleId}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "put": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/zscaler/ztc",
            resource_prefix="ztc",
            api_prefix="/",
        )
        by_resource = {r["resource"]: r for r in report["resources"]}

        self.assertEqual(report["summary"]["matched"], 3)
        self.assertEqual(report["summary"]["special"], 1)
        self.assertEqual(
            by_resource["ztc_forwarding_gateway"]["collection_path"],
            "/gateways")
        self.assertEqual(
            by_resource["ztc_ip_pool_groups"]["collection_path"],
            "/ipGroups")
        self.assertEqual(
            by_resource["ztc_traffic_forwarding_dns_rule"]["collection_path"],
            "/ecRules/ecDns")
        self.assertEqual(
            by_resource["ztc_activation_status"]["special_type"],
            "aliased_action")
        self.assertEqual(
            by_resource["ztc_activation_status"]["write_operations"],
            ["PUT:/ecAdminActivateStatus/activate"])

    def test_registry_fetch_coverage_catches_parent_scoped_zpa_path(self):
        schema_path = self._write_json("zpa-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/zpa": {
                    "resource_schemas": {
                        "zpa_application_segment": {
                            "block": {
                                "attributes": {
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                        "zpa_policy_access_rule": {
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
        openapi_path = self._write_json("zpa-openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/mgmtconfig/v1/admin/customers/{customerId}/application": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "post": {"responses": {"200": {"description": "ok"}}},
                },
                "/mgmtconfig/v1/admin/customers/{customerId}/application/{applicationId}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                    "put": {"responses": {"200": {"description": "ok"}}},
                },
                "/mgmtconfig/v1/admin/customers/{customerId}/policySet/rules/policyType/{policyType}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/zscaler/zpa",
            resource_prefix="zpa",
            api_prefix="/",
            registry_data={
                "zpa_application_segment": {
                    "product": "zpa",
                    "fetch": {
                        "pagination": "zpa",
                        "path": "application",
                    },
                },
                "zpa_policy_access_rule": {
                    "product": "zpa",
                    "fetch": {
                        "pagination": "zpa",
                        "path": "policySet/rules/policyType/ACCESS_POLICY",
                    },
                },
            },
        )

        self.assertEqual(report["summary"]["matched"], 0)
        self.assertEqual(
            report["registry_fetch_coverage"]["summary"],
            {
                "coverage_ratio": 1.0,
                "fetch_resources": 2,
                "matched": 2,
                "unmatched": 0,
            })
        by_resource = {
            r["resource"]: r
            for r in report["registry_fetch_coverage"]["resources"]
        }
        self.assertEqual(by_resource["zpa_application_segment"]["match"], "suffix")
        self.assertEqual(by_resource["zpa_policy_access_rule"]["match"], "suffix")

    def test_registry_fetch_coverage_strips_zcc_product_prefix(self):
        schema_path = self._write_json("zcc-schema.json", {
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
        openapi_path = self._write_json("zcc-openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/papi/public/v1/webForwardingProfile/listByCompany": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/zscaler/zcc",
            resource_prefix="zcc",
            api_prefix="/",
            registry_data={
                "zcc_forwarding_profile": {
                    "product": "zcc",
                    "fetch": {
                        "pagination": "zia",
                        "path": "zcc/papi/public/v1/webForwardingProfile/listByCompany",
                    },
                },
            },
        )

        self.assertEqual(report["summary"]["matched"], 0)
        self.assertEqual(
            report["registry_fetch_coverage"]["summary"]["coverage_ratio"],
            1.0)
        self.assertEqual(
            report["registry_fetch_coverage"]["resources"][0]["variant"],
            "product_prefix_stripped")

    def test_cli_registry_fetch_coverage_strips_api_prefix(self):
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
            "openapi": "3.1.0",
            "paths": {
                "/api/v1/projects/{id}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })
        registry_path = self._write_json("registry.json", {
            "example_project": {
                "product": "example",
                "fetch": {
                    "pagination": "single",
                    "path": "/api/v1/projects/{id}",
                },
            },
        })
        out_path = os.path.join(self.tmp, "report.json")

        rc = openapi_resource_map.main([
            "--schema", schema_path,
            "--openapi", openapi_path,
            "--provider-source", "registry.terraform.io/example/example",
            "--resource-prefix", "example",
            "--api-prefix", "/api/v1/",
            "--registry", registry_path,
            "--out", out_path,
        ])
        self.assertEqual(rc, 0)

        with open(out_path, encoding="utf-8") as f:
            report = json.load(f)
        self.assertEqual(
            report["registry_fetch_coverage"]["summary"]["coverage_ratio"],
            1.0)
        self.assertEqual(
            report["registry_fetch_coverage"]["resources"][0]["variant"],
            "api_prefix_stripped")

    def test_registry_read_coverage_accepts_source_derived_read_paths(self):
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
            "openapi": "3.1.0",
            "paths": {
                "/api/v1/projects/{id}": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
            api_prefix="/api/v1/",
            registry_data={
                "example_project": {
                    "product": "example",
                    "status": "mapped",
                    "read": {
                        "method": "GET",
                        "operation_id": "ProjectsRetrieve",
                        "path": "/api/v1/projects/{id}",
                        "path_kind": "detail",
                    },
                },
            },
        )

        self.assertEqual(
            report["registry_read_coverage"]["summary"],
            {
                "coverage_ratio": 1.0,
                "read_resources": 1,
                "matched": 1,
                "ambiguous": 0,
                "unmatched": 0,
            })
        resource = report["registry_read_coverage"]["resources"][0]
        self.assertEqual(resource["read_path"], "/api/v1/projects/{id}")
        self.assertEqual(resource["operation_id"], "ProjectsRetrieve")
        self.assertEqual(resource["path_kind"], "detail")

    def test_registry_fetch_coverage_rejects_wrong_known_product_spec(self):
        schema_path = self._write_json("ztc-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/ztc": {
                    "resource_schemas": {
                        "ztc_dns_gateway": {
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
        openapi_path = self._write_json("zia-openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Zscaler Internet Access API"},
            "servers": [{"url": "https://api.zsapi.net/zia/api/v1"}],
            "paths": {
                "/dnsGateways": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/zscaler/ztc",
            resource_prefix="ztc",
            api_prefix="/",
            registry_data={
                "ztc_dns_gateway": {
                    "product": "ztc",
                    "fetch": {
                        "pagination": "ztc",
                        "path": "dnsGateways",
                    },
                },
            },
        )

        self.assertEqual(
            report["registry_fetch_coverage"]["summary"]["coverage_ratio"],
            0.0)
        self.assertEqual(
            report["registry_fetch_coverage"]["resources"][0]["reason"],
            "openapi_product_mismatch")
        self.assertEqual(
            [w["code"] for w in report["registry_fetch_coverage"]["warnings"]],
            [
                "registry_openapi_product_mismatch",
                "registry_fetch_paths_missing_from_openapi",
            ])

    def test_registry_read_coverage_rejects_wrong_known_product_spec(self):
        schema_path = self._write_json("ztc-schema.json", {
            "provider_schemas": {
                "registry.terraform.io/zscaler/ztc": {
                    "resource_schemas": {
                        "ztc_dns_gateway": {
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
        openapi_path = self._write_json("zia-openapi.json", {
            "openapi": "3.0.3",
            "info": {"title": "Zscaler Internet Access API"},
            "servers": [{"url": "https://api.zsapi.net/zia/api/v1"}],
            "paths": {
                "/dnsGateways": {
                    "get": {"responses": {"200": {"description": "ok"}}},
                },
            },
        })

        report = openapi_resource_map.build_report(
            schema_path,
            openapi_path,
            provider_source="registry.terraform.io/zscaler/ztc",
            resource_prefix="ztc",
            api_prefix="/",
            registry_data={
                "ztc_dns_gateway": {
                    "product": "ztc",
                    "status": "mapped",
                    "read": {
                        "method": "GET",
                        "operation_id": "GetDnsGateway",
                        "path": "dnsGateways",
                        "path_kind": "list",
                    },
                },
            },
        )

        self.assertEqual(
            report["registry_read_coverage"]["summary"]["coverage_ratio"],
            0.0)
        self.assertEqual(
            report["registry_read_coverage"]["resources"][0]["reason"],
            "openapi_product_mismatch")
        self.assertEqual(
            [w["code"] for w in report["registry_read_coverage"]["warnings"]],
            [
                "registry_read_openapi_product_mismatch",
                "registry_read_paths_missing_from_openapi",
            ])


if __name__ == "__main__":
    unittest.main()
