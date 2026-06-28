"""Consumer-owned drift/projection policy."""
import json
import re


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
    parts = []
    for raw in _split_dotted(text):
        parts.extend(_parse_segment(raw, text))
    return tuple(parts)


def _split_dotted(text):
    parts = []
    buf = []
    in_quote = False
    escaped = False
    for char in text:
        if escaped:
            buf.append(char)
            escaped = False
            continue
        if char == "\\" and in_quote:
            buf.append(char)
            escaped = True
            continue
        if char == '"':
            in_quote = not in_quote
            buf.append(char)
            continue
        if char == "." and not in_quote:
            parts.append("".join(buf))
            buf = []
            continue
        buf.append(char)
    if in_quote:
        raise DriftPolicyError("unterminated quoted selector in %r" % text)
    parts.append("".join(buf))
    return parts


def _parse_segment(raw, full_path):
    m = _NAME_RE.match(raw)
    if not m:
        raise DriftPolicyError(
            "invalid policy path segment %r in %r" % (raw, full_path)
        )
    out = [m.group(0)]
    pos = m.end()
    while pos < len(raw):
        if raw[pos] != "[":
            raise DriftPolicyError(
                "invalid policy path segment %r in %r" % (raw, full_path)
            )
        end = _selector_end(raw, pos, full_path)
        selector = raw[pos + 1:end]
        if selector in ("", "*"):
            out.append("*")
        elif selector.isdigit():
            out.append(int(selector))
        elif len(selector) >= 2 and selector[0] == '"' and selector[-1] == '"':
            out.append(_unquote_selector(selector[1:-1]))
        else:
            raise DriftPolicyError(
                "invalid policy path selector %r in %r" % (selector, full_path)
            )
        pos = end + 1
    return out


def _selector_end(raw, start, full_path):
    in_quote = False
    escaped = False
    for idx in range(start + 1, len(raw)):
        char = raw[idx]
        if escaped:
            escaped = False
            continue
        if char == "\\" and in_quote:
            escaped = True
            continue
        if char == '"':
            in_quote = not in_quote
            continue
        if char == "]" and not in_quote:
            return idx
    raise DriftPolicyError("unterminated policy path selector in %r" % full_path)


def _unquote_selector(text):
    return text.replace(r'\"', '"').replace(r"\\", "\\")


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
