import json
import unittest

from engine.drift_policy import DriftPolicy, DriftPolicyError, parse_path


def _entry(**overrides):
    entry = {
        "path": "status",
        "reason": "test",
        "approved_by": "unit",
    }
    entry.update(overrides)
    return entry


def _sync_entry(**overrides):
    entry = {
        "target_path": "res_categories",
        "source_path": "dest_ip_categories",
        "reason": "test",
        "approved_by": "unit",
    }
    entry.update(overrides)
    return entry


def _omit_if_entry(**overrides):
    entry = {
        "path": "ports[*].end",
        "values": [0],
        "reason": "test",
        "approved_by": "unit",
    }
    entry.update(overrides)
    return entry


def _policy(mode, entry):
    return {
        "version": 1,
        "resource_types": {
            "sample_resource": {
                mode: [entry],
            },
        },
    }


def _tolerating_policy_data():
    return {
        "version": 1,
        "resource_types": {
            "t_x": {
                "plan_tolerate": [{
                    "path": "a[].b",
                    "reason": "r",
                    "approved_by": "me",
                }],
            },
        },
    }


class StatelessDataTest(unittest.TestCase):
    def test_matching_does_not_mutate_policy_data(self):
        data = _tolerating_policy_data()
        snapshot = json.loads(json.dumps(data))
        policy = DriftPolicy(data)
        self.assertTrue(policy.tolerates_plan_path("t_x", ("a", 0, "b"), "update"))
        self.assertEqual(policy.data, snapshot)

    def test_used_policy_data_revalidates(self):
        data = _tolerating_policy_data()
        policy = DriftPolicy(data)
        policy.tolerates_plan_path("t_x", ("a", 0, "b"), "update")
        DriftPolicy(policy.data)  # must not raise on unknown key _matched

    def test_stale_entries_still_tracks_matches(self):
        policy = DriftPolicy(_tolerating_policy_data())
        self.assertEqual(
            policy.stale_entries(modes=("plan_tolerate",)),
            [("t_x", "plan_tolerate", "a[].b")],
        )
        policy.tolerates_plan_path("t_x", ("a", 0, "b"), "update")
        self.assertEqual(policy.stale_entries(modes=("plan_tolerate",)), [])

    def test_match_state_is_per_instance(self):
        data = _tolerating_policy_data()
        first = DriftPolicy(data)
        second = DriftPolicy(data)
        first.tolerates_plan_path("t_x", ("a", 0, "b"), "update")
        self.assertEqual(second.stale_entries(modes=("plan_tolerate",)),
                         [("t_x", "plan_tolerate", "a[].b")])

    def test_entries_public_accessor(self):
        policy = DriftPolicy(_tolerating_policy_data())
        self.assertEqual(len(policy.entries("t_x", "plan_tolerate")), 1)
        self.assertEqual(policy.entries("missing", "plan_tolerate"), [])

    def test_new_policy_match_state_does_not_mutate_policy_data(self):
        data = {
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [_sync_entry()],
                    "projection_omit_if": [_omit_if_entry()],
                },
            },
        }
        snapshot = json.loads(json.dumps(data))
        policy = DriftPolicy(data)
        policy.mark_matched(policy.entries(
            "sample_resource", "projection_sync")[0])
        policy.mark_matched(policy.entries(
            "sample_resource", "projection_omit_if")[0])
        self.assertEqual(policy.data, snapshot)
        DriftPolicy(policy.data)

    def test_new_policy_modes_are_stale_until_marked(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [_sync_entry()],
                    "projection_omit_if": [_omit_if_entry()],
                },
            },
        })
        self.assertEqual(policy.stale_entries(), [
            ("sample_resource", "projection_sync", "res_categories"),
            ("sample_resource", "projection_omit_if", "ports[*].end"),
        ])
        policy.mark_matched(policy.entries(
            "sample_resource", "projection_sync")[0])
        self.assertEqual(policy.stale_entries(modes=("projection_sync",)), [])


class DriftPolicyTest(unittest.TestCase):
    def test_parse_supported_paths(self):
        self.assertEqual(parse_path("foo.bar"), ("foo", "bar"))
        self.assertEqual(parse_path("foo[*].bar"), ("foo", "*", "bar"))
        self.assertEqual(parse_path("foo[].bar"), ("foo", "*", "bar"))
        self.assertEqual(parse_path("foo[0].bar"), ("foo", 0, "bar"))
        self.assertEqual(parse_path('tags["Name"]'), ("tags", "Name"))
        self.assertEqual(
            parse_path('terraform_labels["goog-terraform-provisioned"]'),
            ("terraform_labels", "goog-terraform-provisioned"),
        )

    def test_validation_requires_reason_and_approver(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "projection_omit": [{"path": "name", "approved_by": "unit"}]
                    }
                },
            })
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "plan_tolerate": [{"path": "name", "reason": "test"}]
                    }
                },
            })
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy(
                "projection_sync",
                _sync_entry(reason=""),
            ))
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy(
                "projection_omit_if",
                _omit_if_entry(approved_by=""),
            ))

    def test_unsupported_version_fails(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({"version": 2, "resource_types": {}})

    def test_default_policy_is_empty_version_one(self):
        policy = DriftPolicy(None)
        self.assertFalse(policy.projection_omits("sample_resource", ("name",)))
        self.assertEqual(policy.stale_entries(), [])

    def test_rejects_unknown_top_level_key(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {},
                "unexpected": True,
            })

    def test_rejects_missing_top_level_shape(self):
        for data in (
                {},
                {"version": 1},
                {"resource_types": {}},
                [],
                "not a policy",
        ):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy(data)

    def test_rejects_malformed_resource_types(self):
        for resource_types in ([], "sample_resource"):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy({"version": 1, "resource_types": resource_types})
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {"bad-resource": {}},
            })
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {"sample_resource": []},
            })

    def test_rejects_unknown_per_resource_key(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {"other_mode": []},
                },
            })

    def test_rejects_non_list_policy_collections(self):
        for mode in (
                "projection_omit",
                "projection_sync",
                "projection_omit_if",
                "plan_tolerate",
        ):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy({
                    "version": 1,
                    "resource_types": {
                        "sample_resource": {mode: _entry()},
                    },
                })

    def test_rejects_unknown_entry_key(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy("plan_tolerate", _entry(unexpected=True)))
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy("projection_omit", _entry(actions=["update"])))
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy("projection_sync", _sync_entry(path="status")))
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy(
                "projection_omit_if",
                _omit_if_entry(actions=["update"]),
            ))

    def test_accepts_projection_sync_and_omit_if_shapes(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [_sync_entry(ticket="NET-1")],
                    "projection_omit_if": [
                        _omit_if_entry(values=["x", 0, 0.5, False, None])
                    ],
                },
            },
        })

        self.assertEqual(
            policy.entries("sample_resource", "projection_sync")[0]
            ["target_path"],
            "res_categories",
        )
        self.assertEqual(
            policy.entries("sample_resource", "projection_omit_if")[0]
            ["values"],
            ["x", 0, 0.5, False, None],
        )

    def test_rejects_invalid_projection_sync_shape(self):
        for entry in (
                _sync_entry(target_path=""),
                _sync_entry(source_path=""),
                _sync_entry(target_path="same", source_path="same"),
                _sync_entry(target_path="rules[*].name"),
                _sync_entry(source_path="rules[0].name"),
                _sync_entry(target_path="bad..path"),
        ):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy(_policy("projection_sync", entry))

    def test_rejects_invalid_projection_omit_if_values(self):
        for values in (None, 0, [], [{}], [[]]):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy(_policy(
                    "projection_omit_if",
                    _omit_if_entry(values=values),
                ))

    def test_rejects_duplicate_projection_sync_target_entries(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "projection_sync": [
                            _sync_entry(source_path="dest_ip_categories"),
                            _sync_entry(source_path="other_categories"),
                        ],
                    },
                },
            })

    def test_rejects_duplicate_projection_omit_if_entries(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "projection_omit_if": [
                            _omit_if_entry(values=[0]),
                            _omit_if_entry(values=[0]),
                        ],
                    },
                },
            })

    def test_projection_omit_if_duplicate_scope_uses_json_scalar_types(self):
        DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit_if": [
                        _omit_if_entry(values=[0]),
                        _omit_if_entry(values=[False]),
                    ],
                },
            },
        })

    def test_rejects_unsafe_plan_actions(self):
        for actions in (
                ["delete"],
                ["create"],
                ["delete", "create"],
                ["no-op"],
                ["import-only"],
        ):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy(_policy("plan_tolerate", _entry(actions=actions)))

    def test_rejects_malformed_actions(self):
        for actions in ("update", [], ["update", "update"], [1], [None]):
            with self.assertRaises(DriftPolicyError):
                DriftPolicy(_policy("plan_tolerate", _entry(actions=actions)))

    def test_rejects_duplicate_tolerated_path_entries(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "plan_tolerate": [
                            _entry(path="status", reason="one"),
                            _entry(path="status", reason="two"),
                        ],
                    },
                },
            })

    def test_rejects_duplicate_projection_omission_entries(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "projection_omit": [
                            _entry(path="description", reason="one"),
                            _entry(path="description", reason="two"),
                        ],
                    },
                },
            })

    def test_rejects_invalid_path(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy("plan_tolerate", _entry(path="bad..path")))

    def test_ticket_is_allowed_but_must_be_string(self):
        DriftPolicy(_policy("plan_tolerate", _entry(ticket="NET-1842")))
        with self.assertRaises(DriftPolicyError):
            DriftPolicy(_policy("plan_tolerate", _entry(ticket=1842)))

    def test_implicit_update_action_preserves_existing_behavior(self):
        policy = DriftPolicy(_policy("plan_tolerate", _entry(path="status")))
        self.assertTrue(
            policy.tolerates_plan_path(
                "sample_resource",
                ("status",),
                "update",
            )
        )
        self.assertFalse(
            policy.tolerates_plan_path(
                "sample_resource",
                ("status",),
                "delete",
            )
        )

    def test_matching_and_stale_entries(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {"path": "description", "reason": "test", "approved_by": "unit"}
                    ],
                    "plan_tolerate": [
                        {
                            "path": "rules[*].status",
                            "actions": ["update"],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                }
            },
        })
        self.assertTrue(policy.projection_omits("sample_resource", ("description",)))
        self.assertTrue(
            policy.tolerates_plan_path("sample_resource", ("rules", 0, "status"), "update")
        )
        self.assertFalse(
            policy.tolerates_plan_path("sample_resource", ("rules", "0", "status"), "update")
        )
        self.assertEqual(policy.stale_entries(), [])

    def test_diagnostic_list_path_can_be_copied_into_policy(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [
                        {
                            "path": "rules[].status",
                            "actions": ["update"],
                            "reason": "copied from diagnostic output",
                            "approved_by": "unit",
                        }
                    ],
                    "projection_omit": [
                        {
                            "path": 'terraform_labels["goog-terraform-provisioned"]',
                            "reason": "quoted map selector",
                            "approved_by": "unit",
                        }
                    ],
                }
            },
        })

        self.assertTrue(
            policy.tolerates_plan_path(
                "sample_resource",
                ("rules", 0, "status"),
                "update",
            )
        )
        self.assertTrue(
            policy.projection_omits(
                "sample_resource",
                ("terraform_labels", "goog-terraform-provisioned"),
            )
        )

    def test_stale_entries_reports_unmatched_policy(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "description",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                    "plan_tolerate": [
                        {
                            "path": "rules[*].status",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                },
                "other_resource": {
                    "plan_tolerate": [
                        {
                            "path": "status",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })
        self.assertEqual(
            policy.stale_entries(),
            [
                ("other_resource", "plan_tolerate", "status"),
                ("sample_resource", "projection_omit", "description"),
                ("sample_resource", "plan_tolerate", "rules[*].status"),
            ],
        )
        self.assertEqual(
            policy.stale_entries(
                resource_types={"sample_resource"}, modes=("plan_tolerate",)
            ),
            [("sample_resource", "plan_tolerate", "rules[*].status")],
        )


if __name__ == "__main__":
    unittest.main()
