"""Single path grammar, matcher, and formatter for engine path strings.

One tokenizer serves both dialects:
  - report paths (schema_paths): arbitrary plain segments allowed,
    ``[0]``/``[*]``/``[]`` all normalize to the ``[]`` list marker.
  - policy paths (drift_policy): plain segments must be identifiers
    (strict_names=True); ``[3]`` stays an exact int index and ``[]``/``[*]``
    stay the ``*`` wildcard, which matches int indexes ONLY (never map keys).

Parsed segments are: str (name or quoted map key), int (explicit index),
WILDCARD ("*"). normalize() collapses int/WILDCARD to LIST_MARKER ("[]").
"""
import re

LIST_MARKER = "[]"
WILDCARD = "*"
_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


class _PlainWildcard(str):
    """A bare ``*`` segment (no brackets), as distinct from the ``[*]``/``[]``
    collection selector. It compares equal to ``"*"`` so lane validators can
    keep rejecting bare wildcards, but normalize() must NOT collapse it to
    LIST_MARKER the way it collapses real collection selectors."""


def parse_path(text, strict_names=False, what="path"):
    parts = []
    for raw in _split_dotted(str(text), what):
        if raw == "" and not strict_names:
            raise ValueError("empty %s segment in %r" % (what, text))
        parts.extend(_parse_segment(raw, text, strict_names, what))
    return tuple(parts)


def _split_dotted(text, what):
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
        raise ValueError("unterminated quoted %s selector in %r" % (what, text))
    parts.append("".join(buf))
    return parts


def _parse_segment(raw, full_path, strict_names, what):
    if not strict_names and "[" not in raw and "]" not in raw:
        if raw == WILDCARD:
            return [_PlainWildcard(raw)]
        return [raw]
    match = _NAME_RE.match(raw)
    if not match:
        raise ValueError(
            "invalid %s segment %r in %r" % (what, raw, full_path)
        )
    out = [match.group(0)]
    pos = match.end()
    while pos < len(raw):
        if raw[pos] != "[":
            raise ValueError(
                "invalid %s segment %r in %r" % (what, raw, full_path)
            )
        end = _selector_end(raw, pos, full_path, what)
        selector = raw[pos + 1:end]
        if selector in ("", "*"):
            out.append(WILDCARD)
        elif selector.isdigit():
            out.append(int(selector))
        elif len(selector) >= 2 and selector[0] == '"' and selector[-1] == '"':
            out.append(_unquote_selector(selector[1:-1]))
        else:
            raise ValueError(
                "invalid %s selector %r in %r" % (what, selector, full_path)
            )
        pos = end + 1
    return out


def _selector_end(raw, start, full_path, what):
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
    raise ValueError("unterminated %s selector in %r" % (what, full_path))


def _unquote_selector(text):
    return text.replace(r'\"', '"').replace(r"\\", "\\")


def selector_matches(selector, actual):
    """Policy-path matching: WILDCARD matches int indexes only."""
    if len(selector) != len(actual):
        return False
    for s, a in zip(selector, actual):
        if _is_wildcard(s):
            if not isinstance(a, int):
                return False
            continue
        if s != a:
            return False
    return True


def normalize(path):
    """Collapse int indexes and wildcards to LIST_MARKER; parse str input."""
    if isinstance(path, str):
        path = parse_path(path)
    return tuple(
        LIST_MARKER
        if isinstance(segment, int) or _is_wildcard(segment) else segment
        for segment in path
    )


def format_path(path):
    """Faithful display: ints keep their index, LIST_MARKER/WILDCARD render []."""
    if isinstance(path, str):
        return path
    if not path:
        return "<root>"
    parts = []
    for segment in path:
        if segment == LIST_MARKER or _is_wildcard(segment):
            if parts:
                parts[-1] = parts[-1] + LIST_MARKER
            else:
                parts.append(LIST_MARKER)
        elif isinstance(segment, int):
            if parts:
                parts[-1] = "%s[%d]" % (parts[-1], segment)
            else:
                parts.append("[%d]" % segment)
        else:
            parts.append(str(segment))
    return ".".join(parts)


def format_report_path(path):
    """Normalized display: every index/wildcard collapses to []."""
    return format_path(normalize(path))


def _is_wildcard(segment):
    return type(segment) is str and segment == WILDCARD
