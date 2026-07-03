"""Consumer-owned drift/projection policy."""
import json
import re

from engine import paths


class DriftPolicyError(ValueError):
    pass


_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
_TOP_LEVEL_KEYS = frozenset(("version", "resource_types"))
_RESOURCE_KEYS = frozenset(("projection_omit", "plan_tolerate"))
_COMMON_ENTRY_KEYS = frozenset(("path", "reason", "approved_by", "ticket"))
_PLAN_TOLERATE_ENTRY_KEYS = _COMMON_ENTRY_KEYS | frozenset(("actions",))
_SAFE_PLAN_ACTIONS = frozenset(("update",))


class DriftPolicy(object):
    def __init__(self, data, source="<memory>"):
        self.data = (
            {"version": 1, "resource_types": {}}
            if data is None else data
        )
        self.source = source
        self._validate()

    @classmethod
    def load(cls, path):
        if not path:
            return cls(None)
        with open(path, encoding="utf-8") as f:
            return cls(json.load(f), source=path)

    def projection_omits(self, resource_type, path_tuple):
        return self._matches(resource_type, "projection_omit", path_tuple)

    def tolerates_plan_path(self, resource_type, path_tuple, action):
        for entry in self._entries(resource_type, "plan_tolerate"):
            if action not in entry.get("actions", ["update"]):
                continue
            if paths.selector_matches(parse_path(entry["path"]), path_tuple):
                entry["_matched"] = True
                return True
        return False

    def stale_entries(self, resource_types=None, modes=None):
        resource_types = set(resource_types or [])
        modes = tuple(modes or ("projection_omit", "plan_tolerate"))
        stale = []
        for rt, cfg in sorted((self.data.get("resource_types") or {}).items()):
            if resource_types and rt not in resource_types:
                continue
            for mode in modes:
                for entry in cfg.get(mode) or []:
                    if not entry.get("_matched"):
                        stale.append((rt, mode, entry["path"]))
        return stale

    def _matches(self, resource_type, mode, path_tuple):
        for entry in self._entries(resource_type, mode):
            if paths.selector_matches(parse_path(entry["path"]), path_tuple):
                entry["_matched"] = True
                return True
        return False

    def _entries(self, resource_type, mode):
        return (
            (self.data.get("resource_types") or {})
            .get(resource_type, {})
            .get(mode, [])
        )

    def _validate(self):
        if not isinstance(self.data, dict):
            raise DriftPolicyError("%s: drift policy must be an object" % self.source)
        _reject_unknown_keys(
            self.data, _TOP_LEVEL_KEYS, "%s top-level drift policy" % self.source
        )
        if "version" not in self.data:
            raise DriftPolicyError("%s: drift policy missing version" % self.source)
        if self.data.get("version") != 1:
            raise DriftPolicyError(
                "%s: unsupported drift policy version" % self.source
            )
        if "resource_types" not in self.data:
            raise DriftPolicyError(
                "%s: drift policy missing resource_types" % self.source
            )
        if not isinstance(self.data.get("resource_types"), dict):
            raise DriftPolicyError("%s: resource_types must be an object" % self.source)
        for rt, cfg in sorted(self.data.get("resource_types").items()):
            if not isinstance(rt, str) or not _NAME_RE.fullmatch(rt):
                raise DriftPolicyError(
                    "%s: invalid resource type %r" % (self.source, rt)
                )
            if not isinstance(cfg, dict):
                raise DriftPolicyError(
                    "%s: policy for %s must be an object" % (self.source, rt)
                )
            _reject_unknown_keys(
                cfg, _RESOURCE_KEYS, "%s policy for %s" % (self.source, rt)
            )
            for mode in ("projection_omit", "plan_tolerate"):
                entries = cfg.get(mode, [])
                if not isinstance(entries, list):
                    raise DriftPolicyError(
                        "%s %s entries for %s must be a list"
                        % (self.source, mode, rt)
                    )
                seen = {}
                for entry in entries:
                    path, actions = self._validate_entry(rt, mode, entry)
                    scope = (path, tuple(actions))
                    prior = seen.get(scope)
                    if prior is not None:
                        raise DriftPolicyError(
                            "%s duplicate %s entry for %s path %s"
                            % (self.source, mode, rt, path)
                        )
                    seen[scope] = entry

    def _validate_entry(self, rt, mode, entry):
        if not isinstance(entry, dict):
            raise DriftPolicyError(
                "%s %s entry for %s must be an object" % (self.source, mode, rt)
            )
        allowed = (
            _PLAN_TOLERATE_ENTRY_KEYS
            if mode == "plan_tolerate" else _COMMON_ENTRY_KEYS
        )
        _reject_unknown_keys(
            entry,
            allowed,
            "%s %s entry for %s" % (self.source, mode, rt),
        )
        for required in ("path", "reason", "approved_by"):
            if not isinstance(entry.get(required), str) or not entry.get(required):
                raise DriftPolicyError(
                    "%s %s entry for %s missing %s"
                    % (self.source, mode, rt, required)
                )
        if "ticket" in entry and (
                not isinstance(entry.get("ticket"), str) or not entry.get("ticket")):
            raise DriftPolicyError(
                "%s %s entry for %s has invalid ticket"
                % (self.source, mode, rt)
            )
        path = entry["path"]
        parse_path(path)
        if mode != "plan_tolerate":
            return path, ("projection_omit",)
        actions = entry.get("actions", ["update"])
        if not isinstance(actions, list):
            raise DriftPolicyError(
                "%s plan_tolerate entry for %s actions must be a list"
                % (self.source, rt)
            )
        if not actions:
            raise DriftPolicyError(
                "%s plan_tolerate entry for %s actions must not be empty"
                % (self.source, rt)
            )
        seen_actions = set()
        for action in actions:
            if not isinstance(action, str) or not action:
                raise DriftPolicyError(
                    "%s plan_tolerate entry for %s has invalid action"
                    % (self.source, rt)
                )
            if action not in _SAFE_PLAN_ACTIONS:
                raise DriftPolicyError(
                    "%s plan_tolerate entry for %s has unsupported action %r"
                    % (self.source, rt, action)
                )
            if action in seen_actions:
                raise DriftPolicyError(
                    "%s plan_tolerate entry for %s has duplicate action %r"
                    % (self.source, rt, action)
                )
            seen_actions.add(action)
        return path, tuple(sorted(actions))


def _reject_unknown_keys(obj, allowed, where):
    unknown = sorted((key for key in obj if key not in allowed), key=str)
    if unknown:
        raise DriftPolicyError("%s has unknown key %s" % (where, unknown[0]))


def parse_path(text):
    if not isinstance(text, str) or not text:
        raise DriftPolicyError("policy path must be a non-empty string")
    try:
        return paths.parse_path(text, strict_names=True, what="policy path")
    except ValueError as exc:
        raise DriftPolicyError(str(exc))
