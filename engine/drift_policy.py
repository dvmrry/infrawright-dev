"""Consumer-owned drift/projection policy."""
import json
import re


class DriftPolicyError(ValueError):
    pass


_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


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
