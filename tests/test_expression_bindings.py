import ast
import json
import unittest

from engine import expression_bindings


class ExpressionBindingsTest(unittest.TestCase):
    def _valid_data(self):
        return {
            "resources": {
                "zpa_application_segment.example": {
                    "clientless_app_config.password": {
                        "expression": "var.zpa_client_secret",
                        "sensitive": True,
                    },
                    "clientless_app_config.username": {
                        "expression": "local.zpa_client_username",
                    },
                },
            },
        }

    def test_parse_valid_resource_path_bindings(self):
        bindings = expression_bindings.parse_bindings(
            self._valid_data(), "zpa_application_segment")

        self.assertEqual(len(bindings), 2)
        self.assertEqual(bindings[0]["key"], "example")
        self.assertEqual(bindings[0]["path"], "clientless_app_config.password")
        self.assertEqual(bindings[0]["expression"], "var.zpa_client_secret")
        self.assertEqual(bindings[0]["sensitive"], True)
        self.assertEqual(
            bindings[0]["path_parts"],
            ("clientless_app_config", "password"),
        )

    def test_render_hcl_generates_sensitive_var_and_nested_merge(self):
        bindings = expression_bindings.parse_bindings(
            self._valid_data(), "zpa_application_segment")
        text = expression_bindings.render_hcl(bindings)

        self.assertIn('variable "zpa_client_secret" {', text)
        self.assertIn("  type = string", text)
        self.assertIn("  sensitive = true", text)
        self.assertIn(
            'infrawright_expression_bound_items = merge(var.items, {',
            text,
        )
        self.assertIn(
            '"example" = merge(var.items["example"], {',
            text,
        )
        self.assertIn(
            "clientless_app_config = merge("
            'try(var.items["example"].clientless_app_config, null) == null ? '
            '{} : var.items["example"].clientless_app_config, {',
            text,
        )
        self.assertIn("password = var.zpa_client_secret", text)
        self.assertIn("username = local.zpa_client_username", text)

    def test_render_hcl_rejects_conflicting_parent_and_child_bindings(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "sample_resource.prod": {
                    "settings": {"expression": "local.settings"},
                    "settings.password": {"expression": "var.secret"},
                },
            },
        }, "sample_resource")

        with self.assertRaises(ValueError):
            expression_bindings.render_hcl(bindings)

    def test_expression_sentinel_renders_unquoted_in_native_hcl(self):
        value = {
            "username": "svc-user",
            "password": expression_bindings.HclExpression(
                "var.zpa_client_secret"),
        }

        text = expression_bindings.render_hcl_value(value)

        self.assertIn('username = "svc-user"', text)
        self.assertIn("password = var.zpa_client_secret", text)
        self.assertNotIn('"var.zpa_client_secret"', text)

    def test_hcl_value_renderer_quotes_non_identifier_map_keys(self):
        text = expression_bindings.render_hcl_value({
            "normal": "yes",
            "has-hyphen": "quoted",
        })

        self.assertIn('normal = "yes"', text)
        self.assertIn('"has-hyphen" = "quoted"', text)

    def test_expression_sentinel_renders_interpolation_in_tf_json(self):
        value = {
            "literal": "var.zpa_client_secret",
            "password": expression_bindings.HclExpression(
                "var.zpa_client_secret"),
        }

        rendered = expression_bindings.to_tf_json_value(value)

        self.assertEqual(rendered["literal"], "var.zpa_client_secret")
        self.assertEqual(rendered["password"], "${var.zpa_client_secret}")
        self.assertEqual(
            json.dumps(rendered, sort_keys=True),
            '{"literal": "var.zpa_client_secret", '
            '"password": "${var.zpa_client_secret}"}',
        )

    def test_expression_binding_module_stays_isolated_from_adoption_logic(self):
        with open(expression_bindings.__file__, encoding="utf-8") as f:
            tree = ast.parse(f.read())

        imports = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imports.update(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom):
                module = node.module or ""
                if module == "engine":
                    imports.update("engine.%s" % alias.name for alias in node.names)
                elif module.startswith("engine."):
                    imports.add(module)

        forbidden = {
            "engine.adoption_guidance",
            "engine.drift_policy",
            "engine.plan_eval",
            "engine.state_project",
            "engine.sensitive_required",
            "engine.sensitive_required_validator",
        }
        forbidden_imports = sorted(
            name for name in imports
            if name in forbidden or name.startswith("engine.sensitive_required")
        )

        self.assertEqual(forbidden_imports, [])

    def test_expression_sentinel_renders_in_tf_json_lists(self):
        value = [
            expression_bindings.HclExpression("var.secret"),
            "literal",
        ]

        self.assertEqual(
            expression_bindings.to_tf_json_value(value),
            ["${var.secret}", "literal"],
        )

    def test_expression_sentinel_renders_in_native_hcl_lists(self):
        value = [
            expression_bindings.HclExpression("var.secret"),
            "literal",
        ]

        self.assertEqual(
            expression_bindings.render_hcl_value(value),
            '[var.secret, "literal"]',
        )

    def test_apply_bindings_replaces_nested_object_leaf(self):
        bindings = expression_bindings.parse_bindings(
            self._valid_data(), "zpa_application_segment")
        projected = {
            "example": {
                "clientless_app_config": {
                    "username": "svc-user",
                    "password": None,
                },
            },
        }

        bound = expression_bindings.apply_bindings(projected, bindings)

        self.assertEqual(
            bound["example"]["clientless_app_config"]["password"],
            expression_bindings.HclExpression("var.zpa_client_secret"),
        )
        self.assertEqual(
            bound["example"]["clientless_app_config"]["username"],
            expression_bindings.HclExpression("local.zpa_client_username"),
        )
        self.assertIsNone(
            projected["example"]["clientless_app_config"]["password"])

    def test_apply_bindings_missing_leaf_under_parent_fails_closed(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "zpa_application_segment.example": {
                    "clientless_app_config.password": {
                        "expression": "var.zpa_client_secret",
                    },
                },
            },
        }, "zpa_application_segment")

        with self.assertRaises(ValueError):
            expression_bindings.apply_bindings({
                "example": {"clientless_app_config": {"username": "svc-user"}},
            }, bindings)

    def test_apply_bindings_missing_parent_fails_closed(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "zpa_application_segment.example": {
                    "clientless_app_config.password": {
                        "expression": "var.zpa_client_secret",
                    },
                },
            },
        }, "zpa_application_segment")

        with self.assertRaises(ValueError):
            expression_bindings.apply_bindings({"example": {}}, bindings)

    def test_apply_bindings_unknown_address_fails_closed(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "zpa_application_segment.missing": {
                    "password": {"expression": "var.zpa_client_secret"},
                },
            },
        }, "zpa_application_segment")

        with self.assertRaises(ValueError):
            expression_bindings.apply_bindings({"example": {}}, bindings)

    def test_existing_literal_string_remains_literal_without_exact_binding(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "zpa_application_segment.example": {
                    "clientless_app_config.username": {
                        "expression": "local.zpa_client_username",
                    },
                },
            },
        }, "zpa_application_segment")
        projected = {
            "example": {
                "clientless_app_config": {
                    "username": "svc-user",
                    "password": "var.zpa_client_secret",
                },
            },
        }

        bound = expression_bindings.apply_bindings(projected, bindings)

        self.assertEqual(
            bound["example"]["clientless_app_config"]["username"],
            expression_bindings.HclExpression("local.zpa_client_username"),
        )
        self.assertEqual(
            bound["example"]["clientless_app_config"]["password"],
            "var.zpa_client_secret",
        )

    def test_var_declarations_only_for_exact_var_references(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "sample_resource.prod": {
                    "password": {
                        "expression": "var.secret",
                        "sensitive": True,
                    },
                    "username": {
                        "expression": 'data.vault_kv_secret_v2.zpa.data["username"]',
                        "sensitive": True,
                    },
                },
            },
        }, "sample_resource")

        self.assertEqual(
            expression_bindings.variable_declarations(bindings),
            {"secret": True},
        )

    def test_variable_declarations_deduplicate_sensitive_or(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "sample_resource.one": {
                    "password": {
                        "expression": "var.shared_secret",
                        "sensitive": False,
                    },
                },
                "sample_resource.two": {
                    "password": {
                        "expression": "var.shared_secret",
                        "sensitive": True,
                    },
                },
            },
        }, "sample_resource")

        self.assertEqual(
            expression_bindings.variable_declarations(bindings),
            {"shared_secret": True},
        )

    def test_render_hcl_honors_explicit_sensitive_false(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "sample_resource.prod": {
                    "password": {
                        "expression": "var.non_secret",
                        "sensitive": False,
                    },
                },
            },
        }, "sample_resource")

        text = expression_bindings.render_hcl(bindings)

        self.assertIn('variable "non_secret" {', text)
        self.assertIn("  type = string", text)
        self.assertNotIn("sensitive = true", text)

    def test_rejects_non_resource_address(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "other.example": {
                        "password": {"expression": "var.secret"},
                    },
                },
            }, "sample_resource")

    def test_rejects_non_object_resources(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": [],
            }, "sample_resource")

    def test_rejects_path_list_selectors_in_v1(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "blocks[].password": {"expression": "var.secret"},
                    },
                },
            }, "sample_resource")

    def test_rejects_unapproved_expression_root(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "password": {"expression": "file(\"secret\")"},
                    },
                },
            }, "sample_resource")

    def test_rejects_empty_expression(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "password": {"expression": ""},
                    },
                },
            }, "sample_resource")

    def test_rejects_interpolation_wrapped_expression_input(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "password": {"expression": "${var.secret}"},
                    },
                },
            }, "sample_resource")

    def test_rejects_expression_with_newline(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "password": {"expression": "var.secret\n"},
                    },
                },
            }, "sample_resource")

    def test_rejects_unknown_binding_keys(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "password": {
                            "expression": "var.secret",
                            "secret_manager": "ado",
                        },
                    },
                },
            }, "sample_resource")

    def test_rejects_secret_value_field(self):
        with self.assertRaises(ValueError):
            expression_bindings.parse_bindings({
                "resources": {
                    "sample_resource.prod": {
                        "password": {
                            "expression": "var.secret",
                            "value": "actual-secret",
                        },
                    },
                },
            }, "sample_resource")

    def test_rejects_secretish_metadata_fields(self):
        for key in ("secret", "password_value"):
            with self.subTest(key=key):
                with self.assertRaises(ValueError):
                    expression_bindings.parse_bindings({
                        "resources": {
                            "sample_resource.prod": {
                                "password": {
                                    "expression": "var.secret",
                                    key: "actual-secret",
                                },
                            },
                        },
                    }, "sample_resource")

    def test_accepts_reason_metadata(self):
        bindings = expression_bindings.parse_bindings({
            "resources": {
                "sample_resource.prod": {
                    "password": {
                        "expression": "var.secret",
                        "sensitive": True,
                        "reason": "supplied by CI/CD",
                    },
                },
            },
        }, "sample_resource")

        self.assertEqual(bindings[0]["reason"], "supplied by CI/CD")


if __name__ == "__main__":
    unittest.main()
