"""Extract HTTP path templates from vendored Go SDK source.

SDKs such as DigitalOcean `godo` build request paths from const base paths
(``const domainsBasePath = "v2/domains"``) composed with ``fmt.Sprintf`` and
then handed to ``client.NewRequest(ctx, http.MethodGet, path, nil)``. This
module recovers ``(client_symbol, method, path_template)`` triples from that
shape so ``source_operation_map`` can resolve OpenAPI operations by path
structure before falling back to fuzzy name scoring.

The extractor is intentionally narrow. It handles the simple, common cases:

- ``const <name>BasePath = "v2/foo"``
- ``path := <baseVar>``
- ``path := fmt.Sprintf("%s/%s", <baseVar>, <arg>)`` (and ``%d``/``%v`` verbs)
- ``s.client.NewRequest(ctx, http.MethodGet, path, nil)`` for method detection

Anything it cannot trace is reported as ``sdk_path_unresolved`` rather than
silently dropped. Non-GET extracted paths are returned separately as action
evidence so callers can surface them without confusing them with read paths.

Stdlib-only, Python 3.6-floor.
"""
import os
import re


GO_FILE_SUFFIX = ".go"

HTTP_METHOD_CONSTS = {
    "http.MethodGet": "GET",
    "http.MethodPost": "POST",
    "http.MethodPut": "PUT",
    "http.MethodPatch": "PATCH",
    "http.MethodDelete": "DELETE",
    "http.MethodHead": "HEAD",
    "http.MethodOptions": "OPTIONS",
}

SERVICE_TYPE_SUFFIXES = ("ServiceOp", "Service", "Client", "API")

VERB_RE = re.compile(r"%[a-zA-Z]")


def _go_code_without_comments(text):
    """Return Go code with comments removed but string/rune literals kept."""
    parts = []
    index = 0
    while index < len(text):
        char = text[index]
        nxt = text[index + 1] if index + 1 < len(text) else ""
        if char == "/" and nxt == "/":
            end = text.find("\n", index + 2)
            if end == -1:
                break
            parts.append("\n")
            index = end + 1
            continue
        if char == "/" and nxt == "*":
            end = text.find("*/", index + 2)
            removed = text[index:] if end == -1 else text[index:end + 2]
            parts.append("\n" * removed.count("\n"))
            index = len(text) if end == -1 else end + 2
            continue
        parts.append(char)
        index += 1
    return "".join(parts)


def _skip_string(text, index, quote):
    """Return the index past the closing quote of a string starting at index."""
    index += 1
    while index < len(text):
        char = text[index]
        if char == "\\" and quote != "`":
            index += 2
            continue
        if char == quote:
            return index + 1
        index += 1
    return index


def _sdk_files(sdk_root):
    for root, dirs, files in os.walk(sdk_root):
        dirs[:] = [d for d in dirs if d not in (".git", "test", "testdata")]
        for filename in files:
            if not filename.endswith(GO_FILE_SUFFIX):
                continue
            if filename.endswith("_test.go"):
                continue
            yield os.path.join(root, filename)


def _extract_base_paths(code):
    """Return ``{const_name: path_value}`` for ``const fooBasePath = "v2/foo"``."""
    base_paths = {}
    for match in re.finditer(
            r'\bconst\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"', code):
        name, value = match.groups()
        if name.endswith("BasePath"):
            base_paths[name] = value
    for block in re.finditer(r'\bconst\s*\(([^)]*)\)', code, re.S):
        for line in block.group(1).splitlines():
            match = re.search(
                r'([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"', line)
            if match and match.group(1).endswith("BasePath"):
                base_paths[match.group(1)] = match.group(2)
    return base_paths


def _receiver_service_name(receiver_type):
    for suffix in SERVICE_TYPE_SUFFIXES:
        if receiver_type.endswith(suffix) and len(receiver_type) > len(suffix):
            return receiver_type[:-len(suffix)]
    return receiver_type


def _split_functions(code):
    """Yield ``(service_name, method_name, body)`` for each receiver method.

    Only functions with a receiver (``func (s *Type) Name(...)``) are yielded;
    package-level functions do not map to ``client.Service.Method`` call shapes.
    """
    func_re = re.compile(
        r'\bfunc\s*\(([^)]*)\)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(')
    for match in func_re.finditer(code):
        receiver = match.group(1).strip()
        method_name = match.group(2)
        type_match = re.search(r'\*?([A-Za-z_][A-Za-z0-9_]*)\s*$', receiver)
        if not type_match:
            continue
        service = _receiver_service_name(type_match.group(1))
        brace = code.find("{", match.end())
        if brace == -1:
            continue
        body, _end = _extract_braced(code, brace)
        if body is None:
            continue
        yield service, method_name, body


def _extract_braced(code, brace_index):
    """Return ``(body, end_index)`` for the ``{...}`` block starting at brace_index."""
    depth = 0
    body_start = brace_index + 1
    index = brace_index
    while index < len(code):
        char = code[index]
        if char in ('"', "'", "`"):
            index = _skip_string(code, index, char)
            continue
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return code[body_start:index], index + 1
        index += 1
    return None, len(code)


def _render_fmt_template(fmt_string, base_value, extra_args):
    """Render a ``fmt.Sprintf`` format string into a path template.

    The first verb is substituted with the base path value; remaining verbs
    become ``{arg}`` placeholders using the argument identifier when available.
    """
    parts = []
    cursor = 0
    arg_index = 0
    base_consumed = False
    for match in VERB_RE.finditer(fmt_string):
        parts.append(fmt_string[cursor:match.start()])
        if not base_consumed:
            parts.append(base_value)
            base_consumed = True
        else:
            arg = extra_args[arg_index] if arg_index < len(extra_args) else ""
            parts.append(_arg_placeholder(arg))
            arg_index += 1
        cursor = match.end()
    parts.append(fmt_string[cursor:])
    return "".join(parts)


def _arg_placeholder(arg):
    arg = arg.strip()
    if not arg:
        return "{param}"
    if arg.startswith('"') and arg.endswith('"'):
        return arg[1:-1]
    if re.match(r'^[A-Za-z_][A-Za-z0-9_]*$', arg):
        return "{%s}" % arg
    return "{param}"


def _parse_path_expr(expr, base_paths, current_path):
    """Resolve a ``path :=`` right-hand side to a path template string.

    Returns ``(template, used_base_var)`` or ``(None, None)`` when the
    expression is not one of the simple supported shapes.
    """
    expr = expr.strip()
    # Direct base var reference: path := domainsBasePath
    if expr in base_paths:
        return base_paths[expr], expr
    # Reassignment chaining: path = fmt.Sprintf("%s/...", path, ...)
    if current_path is not None and expr.startswith("fmt.Sprintf("):
        rendered = _try_render_sprintf(expr, base_paths, current_path)
        if rendered is not None:
            return rendered, None
    if expr.startswith("fmt.Sprintf("):
        rendered = _try_render_sprintf(expr, base_paths, None)
        if rendered is not None:
            return rendered, None
    return None, None


def _try_render_sprintf(expr, base_paths, current_path):
    """Parse ``fmt.Sprintf("fmt", args...)`` and render a path template."""
    inner = expr[len("fmt.Sprintf("):]
    if not inner.endswith(")"):
        return None
    inner = inner[:-1]
    parsed = _split_call_args(inner)
    if not parsed:
        return None
    fmt_string = parsed[0].strip()
    if not (fmt_string.startswith('"') and fmt_string.endswith('"')):
        return None
    fmt_string = fmt_string[1:-1]
    args = [arg.strip() for arg in parsed[1:]]
    if not args:
        return None
    first = args[0]
    if first in base_paths:
        return _render_fmt_template(fmt_string, base_paths[first], args[1:])
    if current_path is not None and first == "path":
        return _render_fmt_template(fmt_string, current_path, args[1:])
    return None


def _split_call_args(text):
    """Split top-level comma-separated call arguments, respecting parens/quotes."""
    args = []
    depth = 0
    index = 0
    start = 0
    while index < len(text):
        char = text[index]
        if char in ('"', "'", "`"):
            index = _skip_string(text, index, char)
            continue
        if char in "([{":
            depth += 1
        elif char in ")]}":
            depth -= 1
        elif char == "," and depth == 0:
            args.append(text[start:index])
            start = index + 1
        index += 1
    args.append(text[start:])
    if len(args) == 1 and not args[0].strip():
        return []
    return args


def _find_path_assignments(body, base_paths):
    """Return ordered list of ``(template, var_name)`` for ``path`` assignments."""
    assignments = []
    current = None
    for match in re.finditer(
            r'\b([A-Za-z_][A-Za-z0-9_]*)\s*:?=\s*([^;\n]+)', body):
        var, expr = match.groups()
        if var != "path":
            continue
        template, _used = _parse_path_expr(expr, base_paths, current)
        if template is None:
            current = None
            continue
        current = template
        assignments.append(template)
    return assignments


def _detect_method(body):
    """Return the HTTP method string for the ``NewRequest`` call in body."""
    for match in re.finditer(r'\bNewRequest\s*\(([^)]*)\)', body):
        args = _split_call_args(match.group(1))
        if len(args) < 2:
            continue
        method_expr = args[1].strip()
        if method_expr in HTTP_METHOD_CONSTS:
            return HTTP_METHOD_CONSTS[method_expr]
        if method_expr.startswith('"') and method_expr.endswith('"'):
            return method_expr[1:-1].upper()
    return None


def _method_role(method_name):
    lowered = method_name.lower()
    if method_name in ("Get", "Read", "Fetch") or lowered.startswith(
            ("get", "read", "fetch")):
        return "read"
    if method_name in ("List", "Search") or lowered.startswith(
            ("list", "search")):
        return "list"
    return None


def extract_sdk_paths(sdk_root):
    """Extract SDK path evidence from a vendored Go SDK source root.

    Returns a tuple ``(evidence, unresolved)``:

    - ``evidence``: ``{client_symbol: {client_symbol, method, path_template,
      sdk_file, source_role}}`` for successfully extracted read/list methods.
    - ``unresolved``: ``{client_symbol: {sdk_file, reason}}`` for methods that
      looked like path-building calls but could not be fully resolved.
    """
    evidence = {}
    unresolved = {}
    if not sdk_root or not os.path.isdir(sdk_root):
        return evidence, unresolved
    for path in _sdk_files(sdk_root):
        try:
            with open(path, encoding="utf-8") as handle:
                text = handle.read()
        except UnicodeDecodeError:
            continue
        code = _go_code_without_comments(text)
        base_paths = _extract_base_paths(code)
        rel = os.path.relpath(path, sdk_root)
        for service, method_name, body in _split_functions(code):
            symbol = "%s.%s" % (service, method_name)
            assignments = _find_path_assignments(body, base_paths)
            method = _detect_method(body)
            if not assignments and method is None:
                continue
            if not assignments:
                unresolved[symbol] = {
                    "sdk_file": rel,
                    "reason": "path_template_not_found",
                }
                continue
            if method is None:
                unresolved[symbol] = {
                    "sdk_file": rel,
                    "reason": "method_not_detected",
                }
                continue
            template = assignments[-1]
            evidence[symbol] = {
                "client_symbol": symbol,
                "method": method,
                "path_template": template,
                "sdk_file": rel,
                "source_role": _method_role(method_name),
            }
    return evidence, unresolved


def normalize_path_segments(path):
    """Split a path into comparable segments; params collapse to a sentinel."""
    segments = []
    for part in path.strip("/").split("/"):
        if not part:
            continue
        if part.startswith("{") and part.endswith("}"):
            segments.append("{param}")
        else:
            segments.append(part.lower())
    return segments


def segments_match(template_segments, op_segments):
    if len(template_segments) != len(op_segments):
        return False
    for left, right in zip(template_segments, op_segments):
        if left == right:
            continue
        if left == "{param}" or right == "{param}":
            continue
        return False
    return True


def match_openapi_by_path(operations, path_template, method="GET"):
    """Return the OpenAPI operation whose path structure matches the template."""
    template_segments = normalize_path_segments(path_template)
    matches = []
    for op in operations:
        if op["method"] != method:
            continue
        if segments_match(template_segments, normalize_path_segments(op["path"])):
            matches.append(op)
    if len(matches) == 1:
        return matches[0], []
    if not matches:
        return None, []
    return None, matches
