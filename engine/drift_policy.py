"""Consumer-owned drift/projection policy."""
import json
import re


class DriftPolicyError(ValueError):
    pass


_SEG_RE = re.compile(
    r"""
    (?P<name>[A-Za-z_][A-Za-z0-9_]*)
    (?:
      \[
        (?:
          (?P<wild>\*) |
          (?P<idx>\d+) |
          "(?P<key>[^"]+)"
        )
      \]
    )?
    """,
    re.VERBOSE,
)


class DriftPolicy(object):
    def __init__(self, data, source="<memory>"):
        self.data = data or {"version": 1, "resource_types": {}}
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
            if _selector_matches(parse_path(entry["path"]), path_tuple):
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
            if _selector_matches(parse_path(entry["path"]), path_tuple):
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
        if self.data.get("version") != 1:
            raise DriftPolicyError(
                "%s: unsupported drift policy version" % self.source
            )
        if not isinstance(self.data.get("resource_types") or {}, dict):
            raise DriftPolicyError("%s: resource_types must be an object" % self.source)
        for rt, cfg in sorted((self.data.get("resource_types") or {}).items()):
            for mode in ("projection_omit", "plan_tolerate"):
                entries = cfg.get(mode) or []
                if not isinstance(entries, list):
                    raise DriftPolicyError(
                        "%s %s entries for %s must be a list"
                        % (self.source, mode, rt)
                    )
                for entry in entries:
                    for required in ("path", "reason", "approved_by"):
                        if not entry.get(required):
                            raise DriftPolicyError(
                                "%s %s entry for %s missing %s"
                                % (self.source, mode, rt, required)
                            )
                    parse_path(entry["path"])


def parse_path(text):
    parts = []
    for raw in text.split("."):
        m = _SEG_RE.fullmatch(raw)
        if not m:
            raise DriftPolicyError(
                "invalid policy path segment %r in %r" % (raw, text)
            )
        parts.append(m.group("name"))
        if m.group("wild"):
            parts.append("*")
        elif m.group("idx") is not None:
            parts.append(int(m.group("idx")))
        elif m.group("key") is not None:
            parts.append(m.group("key"))
    return tuple(parts)


def _selector_matches(selector, actual):
    if len(selector) != len(actual):
        return False
    for s, a in zip(selector, actual):
        if s == "*":
            if not isinstance(a, int):
                return False
            continue
        if s != a:
            return False
    return True
