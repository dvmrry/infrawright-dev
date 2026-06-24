"""Stable dotted-path inventory for JSON-like values."""

LIST_MARKER = "[]"


def leaf_paths(value, normalize_lists=True):
    """Return sorted leaf paths for a JSON-like value.

    Object and list container paths are intentionally omitted. List indexes are
    normalized to [] by default so repeated elements compare by shape instead
    of position.
    """
    out = set()
    _walk(value, (), out, normalize_lists)
    return sorted(out)


def _walk(value, path, out, normalize_lists):
    if isinstance(value, dict):
        for key in sorted(value, key=lambda item: str(item)):
            _walk(value[key], path + (str(key),), out, normalize_lists)
        return
    if isinstance(value, list):
        for idx, child in enumerate(value):
            segment = LIST_MARKER if normalize_lists else idx
            _walk(child, path + (segment,), out, normalize_lists)
        return
    out.add(format_path(path))


def format_path(path):
    if isinstance(path, str):
        return path
    if not path:
        return "<root>"
    parts = []
    for segment in path:
        if segment == LIST_MARKER:
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
