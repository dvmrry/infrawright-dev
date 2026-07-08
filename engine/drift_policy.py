"""Consumer-owned drift/projection policy."""
import json
import re

from engine import paths


class DriftPolicyError(ValueError):
    pass


_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
_TOP_LEVEL_KEYS = frozenset(("version", "resource_types"))
_RESOURCE_KEYS = frozenset((
    "projection_omit",
    "projection_sync",
    "projection_omit_if",
    "plan_tolerate",
))
_COMMON_ENTRY_KEYS = frozenset(("path", "reason", "approved_by", "ticket"))
_PROJECTION_SYNC_ENTRY_KEYS = frozenset((
    "target_path", "source_path", "reason", "approved_by", "ticket"
))
_PROJECTION_OMIT_IF_ENTRY_KEYS = (
    _COMMON_ENTRY_KEYS | frozenset(("values",))
)
_PLAN_TOLERATE_ENTRY_KEYS = _COMMON_ENTRY_KEYS | frozenset(("actions",))
_SAFE_PLAN_ACTIONS = frozenset(("update",))
_DEFAULT_STALE_MODES = (
    "projection_omit",
    "projection_sync",
    "projection_omit_if",
    "plan_tolerate",
)


class DriftPolicy(object):
    def __init__(self, data, source="<memory>"):
        self.data = (
            {"version": 1, "resource_types": {}}
            if data is None else data
        )
        self.source = source
        self._validate()
        self._matched_ids = set()

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
                self._matched_ids.add(id(entry))
                return True
        return False

    def stale_entries(self, resource_types=None, modes=None):
        resource_types = set(resource_types or [])
        modes = tuple(modes or _DEFAULT_STALE_MODES)
        stale = []
        for rt, cfg in sorted((self.data.get("resource_types") or {}).items()):
            if resource_types and rt not in resource_types:
                continue
            for mode in modes:
                for entry in cfg.get(mode) or []:
                    if id(entry) not in self._matched_ids:
                        stale.append((rt, mode, _entry_display_path(entry)))
        return stale

    def _matches(self, resource_type, mode, path_tuple):
        for entry in self._entries(resource_type, mode):
            if paths.selector_matches(parse_path(entry["path"]), path_tuple):
                self._matched_ids.add(id(entry))
                return True
        return False

    def entries(self, resource_type, mode):
        """Public read accessor for policy entries. Do not mutate the result."""
        return list(self._entries(resource_type, mode))

    def mark_matched(self, entry):
        self._matched_ids.add(id(entry))

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
            for mode in _DEFAULT_STALE_MODES:
                entries = cfg.get(mode, [])
                if not isinstance(entries, list):
                    raise DriftPolicyError(
                        "%s %s entries for %s must be a list"
                        % (self.source, mode, rt)
                    )
                seen = {}
                for entry in entries:
                    path, scope = self._validate_entry(rt, mode, entry)
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
        allowed = _entry_keys_for_mode(mode)
        _reject_unknown_keys(
            entry,
            allowed,
            "%s %s entry for %s" % (self.source, mode, rt),
        )
        required_fields = (
            ("target_path", "source_path", "reason", "approved_by")
            if mode == "projection_sync" else
            ("path", "values", "reason", "approved_by")
            if mode == "projection_omit_if" else
            ("path", "reason", "approved_by")
        )
        for required in required_fields:
            if required == "values":
                continue
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
        if mode == "projection_sync":
            target_path = entry["target_path"]
            source_path = entry["source_path"]
            target = parse_path(target_path)
            source = parse_path(source_path)
            if target == source:
                raise DriftPolicyError(
                    "%s projection_sync entry for %s target_path and "
                    "source_path must differ" % (self.source, rt)
                )
            if _has_wildcard_or_index(target):
                raise DriftPolicyError(
                    "%s projection_sync entry for %s target_path must not "
                    "contain wildcard or index selectors" % (self.source, rt)
                )
            if _has_wildcard_or_index(source):
                raise DriftPolicyError(
                    "%s projection_sync entry for %s source_path must not "
                    "contain wildcard or index selectors" % (self.source, rt)
                )
            return target_path, ("projection_sync", target_path)

        path = entry["path"]
        parse_path(path)
        if mode == "projection_omit_if":
            values = entry.get("values")
            if not isinstance(values, list) or not values:
                raise DriftPolicyError(
                    "%s projection_omit_if entry for %s values must be a "
                    "non-empty JSON list" % (self.source, rt)
                )
            for value in values:
                if not _is_json_scalar(value):
                    raise DriftPolicyError(
                        "%s projection_omit_if entry for %s values must "
                        "contain only JSON scalars" % (self.source, rt)
                    )
            return path, (
                "projection_omit_if",
                path,
                tuple(_json_scalar_key(value) for value in values),
            )
        if mode != "plan_tolerate":
            return path, ("projection_omit", path)
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
        return path, ("plan_tolerate", path, tuple(sorted(actions)))


def _entry_keys_for_mode(mode):
    if mode == "plan_tolerate":
        return _PLAN_TOLERATE_ENTRY_KEYS
    if mode == "projection_sync":
        return _PROJECTION_SYNC_ENTRY_KEYS
    if mode == "projection_omit_if":
        return _PROJECTION_OMIT_IF_ENTRY_KEYS
    return _COMMON_ENTRY_KEYS


def _entry_display_path(entry):
    return entry.get("path", entry.get("target_path"))


def _has_wildcard_or_index(path):
    return any(
        isinstance(segment, int)
        or (type(segment) is str and segment == paths.WILDCARD)
        for segment in path
    )


def _is_json_scalar(value):
    return value is None or type(value) in (str, int, float, bool)


def _json_scalar_key(value):
    if value is None:
        return ("null", None)
    if isinstance(value, bool):
        return ("bool", value)
    if isinstance(value, (int, float)):
        return ("number", value)
    return ("string", value)


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
