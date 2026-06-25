import unittest

from engine import guidance_paths


class GuidanceCandidatePathsTest(unittest.TestCase):
    def _plan(self, changes=None, drift=None):
        return {
            "format_version": "1.0",
            "resource_changes": changes or [],
            "resource_drift": drift or [],
        }

    def _change(self, before, after, resource_type="sample_resource",
                actions=None, address=None, **extra):
        change = {
            "actions": actions or ["update"],
            "before": before,
            "after": after,
        }
        change.update(extra)
        return {
            "address": address or ("%s.this" % resource_type),
            "type": resource_type,
            "change": change,
        }

    def _records(self, plan, resource_type="sample_resource"):
        return list(guidance_paths.guidance_candidate_paths(plan, resource_type))

    def test_includes_actual_value_diff_path(self):
        plan = self._plan(changes=[
            self._change(
                {"data": {"flags": "old"}},
                {"data": {"flags": "new"}},
            )
        ])

        records = self._records(plan)

        self.assertEqual([r["formatted_path"] for r in records], ["data.flags"])
        self.assertEqual(records[0]["path"], ("data", "flags"))

    def test_includes_after_unknown_path(self):
        plan = self._plan(changes=[
            self._change(
                {"data": {"flags": "known"}},
                {"data": {"flags": "known"}},
                after_unknown={"data": {"flags": True}},
            )
        ])

        self.assertEqual(
            [r["formatted_path"] for r in self._records(plan)],
            ["data.flags"],
        )

    def test_excludes_sensitivity_only_paths(self):
        plan = self._plan(changes=[
            self._change(
                {"data": {"flags": "same"}},
                {"data": {"flags": "same"}},
                before_sensitive={"data": {"flags": True}},
                after_sensitive={"data": {"flags": True}},
            )
        ])

        self.assertEqual(self._records(plan), [])

    def test_includes_diff_path_even_when_also_sensitive(self):
        plan = self._plan(changes=[
            self._change(
                {"data": {"flags": "old"}},
                {"data": {"flags": "new"}},
                before_sensitive={"data": {"flags": True}},
                after_sensitive={"data": {"flags": True}},
            )
        ])

        self.assertEqual(
            [r["formatted_path"] for r in self._records(plan)],
            ["data.flags"],
        )

    def test_filters_wrong_resource_type(self):
        plan = self._plan(changes=[
            self._change(
                {"data": {"flags": "old"}},
                {"data": {"flags": "new"}},
                resource_type="other_resource",
            )
        ])

        self.assertEqual(self._records(plan), [])

    def test_ignores_non_update_actions(self):
        for action in ("create", "delete", "no-op"):
            with self.subTest(action=action):
                plan = self._plan(changes=[
                    self._change(
                        {"data": {"flags": "old"}},
                        {"data": {"flags": "new"}},
                        actions=[action],
                    )
                ])

                self.assertEqual(self._records(plan), [])

    def test_handles_resource_drift(self):
        plan = self._plan(drift=[
            self._change(
                {"data": {"flags": "old"}},
                {"data": {"flags": "new"}},
                address="sample_resource.drifted",
            )
        ])

        records = self._records(plan)

        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["source"], "resource_drift")
        self.assertEqual(records[0]["address"], "sample_resource.drifted")
        self.assertEqual(records[0]["formatted_path"], "data.flags")

    def test_ordering_is_deterministic(self):
        plan = self._plan(
            changes=[
                self._change({"b": "old"}, {"b": "new"}, address="z.this"),
                self._change({"a": "old"}, {"a": "new"}, address="a.this"),
            ],
            drift=[
                self._change({"a": "old"}, {"a": "new"}, address="a.this"),
            ],
        )

        self.assertEqual(
            [
                (r["source"], r["address"], r["formatted_path"])
                for r in self._records(plan)
            ],
            [
                ("resource_changes", "a.this", "a"),
                ("resource_changes", "z.this", "b"),
                ("resource_drift", "a.this", "a"),
            ],
        )


if __name__ == "__main__":
    unittest.main()
