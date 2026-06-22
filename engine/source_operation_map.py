"""Derive read-path registry entries from provider source and OpenAPI.

This is a best-effort bridge for Go Terraform providers that call generated
OpenAPI clients. It scans provider source files for Terraform resource type
strings, then looks for OpenAPI operation IDs referenced as Go identifiers in
those files. GET operations become source/read entries that
`openapi_resource_map` can use for registry-backed coverage.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import re
import sys

from engine import openapi_resource_map


GO_FILE_SUFFIX = ".go"
AMBIGUITY_SCORE_DELTA = 5
READ_DETAIL_AMBIGUITY_DELTA = 25
SDK_READ_METHODS = set(("Get", "Read", "Fetch"))
SDK_LIST_METHODS = set(("List", "Search"))
SDK_CALL_SCORE_FLOOR = 35
PACKAGE_CALL_SCORE_FLOOR = 35
RAW_REST_CALL_SCORE_FLOOR = 70
SDK_RECEIVER_NAMES = set(("api", "client"))


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _write_json(data, path=None):
    text = json.dumps(data, indent=2, sort_keys=True) + "\n"
    if path:
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
    else:
        sys.stdout.write(text)


def _canonical(value):
    return re.sub(r"[^a-z0-9]", "", value.lower())


def _operation_aliases(operation_id):
    raw_aliases = set([operation_id])
    for pattern, replacement in (
            ("retrieve", "read"),
            ("retrieve", "get"),
            ("read", "retrieve"),
            ("get", "retrieve"),
    ):
        raw_aliases.add(re.sub(
            pattern, replacement, operation_id, flags=re.I))
    for alias in list(raw_aliases):
        if alias.lower().startswith("route"):
            raw_aliases.add(alias[5:])
    aliases = set(_canonical(alias) for alias in raw_aliases)
    for alias in list(aliases):
        aliases.add(alias + "withresponse")
    return sorted(a for a in aliases if a)


def _synthetic_operation_id(method, path):
    return "%s %s" % (method.upper(), path)


def _operation_index(spec):
    operations = []
    for path, path_obj in sorted((spec.get("paths") or {}).items()):
        for method, operation in sorted((path_obj or {}).items()):
            if not isinstance(operation, dict):
                continue
            method = method.upper()
            operation_id = (
                operation.get("operationId")
                or _synthetic_operation_id(method, path)
            )
            operations.append({
                "method": method,
                "path": path,
                "operation_id": operation_id,
                "operation_id_source": (
                    "openapi" if operation.get("operationId")
                    else "synthetic_path"
                ),
                "aliases": _operation_aliases(operation_id),
            })
    return operations


def _source_files(source_root):
    for root, dirs, files in os.walk(source_root):
        dirs[:] = [
            d for d in dirs
            if d not in (
                ".git", "acceptance", "vendor", ".terraform", "node_modules")
        ]
        for filename in files:
            if not filename.endswith(GO_FILE_SUFFIX):
                continue
            if filename.endswith("_test.go"):
                continue
            if filename == "sweep.go":
                continue
            yield os.path.join(root, filename)


def _service_dir_files(source_root, resource, resource_prefix):
    if not resource_prefix:
        return []
    prefix = resource_prefix + "_"
    if not resource.startswith(prefix):
        return []
    service_name = resource[len(prefix):]
    service_dir = os.path.join(
        source_root, "internal", "services", service_name)
    if not os.path.isdir(service_dir):
        return []
    return sorted(_source_files(service_dir))


def _framework_resource_files(source_root, resource, resource_prefix):
    if not resource_prefix:
        return []
    prefix = resource_prefix + "_"
    if not resource.startswith(prefix):
        return []
    resource_name = resource[len(prefix):]
    candidates = [
        os.path.join(source_root, "internal", "framework", "resources",
                     "%s.go" % resource_name),
        os.path.join(source_root, "internal", "framework", "datasources",
                     "%s.go" % resource_name),
    ]
    return sorted(path for path in candidates if os.path.isfile(path))


def _convention_resource_files(source_root, resource):
    candidates = [
        os.path.join(source_root, "resource_%s.go" % resource),
        os.path.join(source_root, resource.split("_", 1)[0],
                     "resource_%s.go" % resource),
    ]
    return sorted(path for path in candidates if os.path.isfile(path))


def _bare_resource_name(resource, resource_prefix):
    prefix = resource_prefix + "_"
    if resource_prefix and resource.startswith(prefix):
        return resource[len(prefix):]
    return resource


def _basename_resource_files(source_root, resource, resource_prefix):
    filenames = set([
        "resource_%s.go" % resource,
        "resource_%s.go" % _bare_resource_name(resource, resource_prefix),
    ])
    return sorted(
        path for path in _source_files(source_root)
        if os.path.basename(path) in filenames
    )


def _exact_resource_files(source_root, resource, resource_prefix):
    paths = []
    paths.extend(_convention_resource_files(source_root, resource))
    paths.extend(_basename_resource_files(source_root, resource,
                                          resource_prefix))
    paths.extend(_service_dir_files(source_root, resource, resource_prefix))
    paths.extend(_framework_resource_files(source_root, resource,
                                           resource_prefix))
    return sorted(set(paths))


def _function_definition_index(source_root):
    functions = {}
    for path in _source_files(source_root):
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except UnicodeDecodeError:
            continue
        code = _go_code_without_comments_and_strings(text)
        for match in re.finditer(
                r"\bfunc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(", code):
            functions.setdefault(match.group(1), set()).add(path)
    return functions


def _registration_resource_files(source_root, resource, function_definitions):
    paths = set()
    function_pattern = re.compile(
        r'"%s"\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(' %
        re.escape(resource))
    package_pattern = re.compile(
        r'"%s"\s*:\s*([A-Za-z_][A-Za-z0-9_]*)'
        r"\.([A-Za-z_][A-Za-z0-9_]*)\s*\(" % re.escape(resource))
    for path in _source_files(source_root):
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except UnicodeDecodeError:
            continue
        code = _go_code_without_comments(text)
        for match in function_pattern.finditer(code):
            function_name = match.group(1)
            if _canonical(function_name).startswith("datasource"):
                continue
            for function_path in function_definitions.get(function_name, []):
                paths.add(function_path)
        import_aliases = _go_import_aliases(text)
        for match in package_pattern.finditer(code):
            package_name, function_name = match.groups()
            if _canonical(function_name).startswith("datasource"):
                continue
            import_path = import_aliases.get(package_name)
            package_dir = _local_import_dir(source_root, import_path)
            if not package_dir:
                continue
            for package_path in _package_resource_files(package_dir):
                paths.add(package_path)
    return sorted(paths)


def _local_import_dir(source_root, import_path):
    if not import_path:
        return None
    parts = import_path.split("/")
    for index in range(len(parts)):
        candidate = os.path.join(source_root, *parts[index:])
        if os.path.isdir(candidate):
            return candidate
    return None


def _package_resource_files(package_dir):
    paths = []
    for filename in sorted(os.listdir(package_dir)):
        if not filename.endswith(GO_FILE_SUFFIX):
            continue
        if filename.endswith("_test.go") or filename == "sweep.go":
            continue
        if "datasource" in filename or filename.startswith("data_source_"):
            continue
        paths.append(os.path.join(package_dir, filename))
    return paths


def _read_callback_files(paths, function_definitions):
    callback_paths = set()
    pattern = re.compile(
        r"\bRead(?:Context|WithoutTimeout)?\s*:\s*"
        r"([A-Za-z_][A-Za-z0-9_]*)")
    for path in paths:
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except UnicodeDecodeError:
            continue
        for match in pattern.finditer(_go_code_without_comments_and_strings(
                text)):
            for function_path in function_definitions.get(match.group(1), []):
                if os.path.dirname(function_path) != os.path.dirname(path):
                    continue
                callback_paths.add(function_path)
    return sorted(callback_paths)


def _is_broad_registration_file(path):
    return os.path.basename(path) in ("provider.go", "main.go")


def _is_loose_resource_match_file(path):
    basename = os.path.basename(path)
    return _is_broad_registration_file(path) or basename.startswith(
        "data_source_")


def _resource_files(source_root, resource_names, resource_prefix=""):
    resources = dict((name, []) for name in resource_names)
    function_definitions = _function_definition_index(source_root)
    for path in _source_files(source_root):
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except UnicodeDecodeError:
            continue
        for resource in resource_names:
            if '"%s"' % resource in text:
                resources[resource].append(path)
    for resource, paths in resources.items():
        exact_paths = _exact_resource_files(
            source_root, resource, resource_prefix)
        registration_paths = _registration_resource_files(
            source_root, resource, function_definitions)
        if exact_paths or registration_paths:
            paths = [
                path for path in paths
                if not _is_loose_resource_match_file(path)
            ]
        paths.extend(exact_paths)
        paths.extend(registration_paths)
        paths.extend(_read_callback_files(paths, function_definitions))
        resources[resource] = sorted(set(paths))
    return resources


def _is_identifier_start(char):
    return char == "_" or char.isalpha()


def _is_identifier_part(char):
    return char == "_" or char.isalnum()


def _skip_quoted(text, index, quote):
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


def _go_identifier_tokens(text):
    """Return Go identifiers while ignoring comments and string/rune literals."""
    tokens = set()
    index = 0
    while index < len(text):
        char = text[index]
        nxt = text[index + 1] if index + 1 < len(text) else ""
        if char == "/" and nxt == "/":
            index = text.find("\n", index + 2)
            if index == -1:
                break
            continue
        if char == "/" and nxt == "*":
            end = text.find("*/", index + 2)
            index = len(text) if end == -1 else end + 2
            continue
        if char in ('"', "'", "`"):
            index = _skip_quoted(text, index, char)
            continue
        if _is_identifier_start(char):
            start = index
            index += 1
            while index < len(text) and _is_identifier_part(text[index]):
                index += 1
            tokens.add(_canonical(text[start:index]))
            continue
        index += 1
    return tokens


def _go_code_without_comments_and_strings(text):
    """Return Go code with comments and string/rune literals replaced."""
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
        if char in ('"', "'", "`"):
            end = _skip_quoted(text, index, char)
            removed = text[index:end]
            parts.append("\n" * removed.count("\n"))
            index = end
            continue
        parts.append(char)
        index += 1
    return "".join(parts)


def _go_code_without_comments(text):
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


def _sdk_method_role(method):
    lowered = method.lower()
    if method in SDK_READ_METHODS or lowered.startswith(
            ("get", "read", "fetch")) or lowered.endswith(
                ("get", "read", "retrieve")):
        return "read"
    if method in SDK_LIST_METHODS or lowered.startswith(("list", "search")):
        return "list"
    if lowered.endswith(("list", "search")):
        return "list"
    return None


def _sdk_client_calls(text):
    """Return SDK calls such as client.DNS.Records.Get or api.Ipam.FooRead."""
    code = _go_code_without_comments_and_strings(text)
    calls = {}
    pattern = re.compile(
        r"\b((?:[A-Za-z_][A-Za-z0-9_]*\.)*(?:api|client)"
        r"(?:\.[A-Za-z_][A-Za-z0-9_]*){1,})\s*\(")
    for match in pattern.finditer(code):
        parts = match.group(1).split(".")
        receiver_indexes = [
            index for index, part in enumerate(parts)
            if part in SDK_RECEIVER_NAMES
        ]
        if not receiver_indexes:
            continue
        client_index = receiver_indexes[-1]
        suffix = parts[client_index + 1:]
        if not suffix:
            continue
        method = suffix[-1]
        role = _sdk_method_role(method)
        if not role:
            continue
        chain = suffix[:-1]
        symbol = ".".join(chain + [method])
        calls[symbol] = {
            "client_symbol": symbol,
            "chain": chain,
            "method": method,
            "source_role": role,
        }
    return [calls[key] for key in sorted(calls)]


def _go_import_aliases(text):
    code = _go_code_without_comments(text)
    aliases = {}
    import_blocks = []
    for match in re.finditer(r"\bimport\s*\((.*?)\)", code, re.S):
        import_blocks.append(match.group(1))
    for match in re.finditer(
            r"\bimport\s+([A-Za-z_][A-Za-z0-9_]*\s+)?\"([^\"]+)\"", code):
        alias, import_path = match.groups()
        name = (alias or "").strip() or import_path.rsplit("/", 1)[-1]
        aliases[name] = import_path
    for block in import_blocks:
        for line in block.splitlines():
            match = re.search(
                r"^\s*([A-Za-z_][A-Za-z0-9_]*\s+)?\"([^\"]+)\"", line)
            if not match:
                continue
            alias, import_path = match.groups()
            name = (alias or "").strip() or import_path.rsplit("/", 1)[-1]
            aliases[name] = import_path
    return aliases


def _package_method_role(method):
    lowered = method.lower()
    if lowered.startswith(("get", "read", "fetch")):
        if "all" in lowered or "list" in lowered:
            return "list"
        return "read"
    if lowered.startswith(("list", "search")):
        return "list"
    return None


def _is_external_import_path(import_path):
    first = import_path.split("/", 1)[0]
    return "." in first


def _sdk_package_calls(text, source_root=None):
    """Return package-level SDK calls such as locationmanagement.Get."""
    code = _go_code_without_comments_and_strings(text)
    import_aliases = _go_import_aliases(text)
    calls = {}
    if not import_aliases:
        return []
    pattern = re.compile(
        r"\b([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\(")
    for match in pattern.finditer(code):
        package, method = match.groups()
        import_path = import_aliases.get(package)
        if not import_path:
            continue
        if not _is_external_import_path(import_path):
            continue
        if source_root and _local_import_dir(source_root, import_path):
            continue
        role = _package_method_role(method)
        if not role:
            continue
        symbol = "%s.%s" % (package, method)
        calls[symbol] = {
            "client_symbol": symbol,
            "package": package,
            "package_path": import_path,
            "method": method,
            "source_role": role,
        }
    return [calls[key] for key in sorted(calls)]


def _go_string_literal(pattern):
    return (
        r"(?:(?P<%s_double>\"(?:\\.|[^\"\\])*\")|"
        r"(?P<%s_raw>`[^`]*`))"
    ) % (pattern, pattern)


def _decode_go_string_literal(value):
    if not value:
        return ""
    if value.startswith("`") and value.endswith("`"):
        return value[1:-1]
    if value.startswith('"') and value.endswith('"'):
        value = value[1:-1]
    return bytes(value, "utf-8").decode("unicode_escape")


def _normalize_raw_rest_path(path):
    path = path.strip()
    path = re.sub(
        r"%[#0 +\-]*[0-9]*(?:\.[0-9]+)?[bcdefgosqxXUvT]",
        "{arg}",
        path,
    )
    if not path.startswith("/"):
        path = "/" + path
    path = re.sub(r"/+", "/", path)
    return path


def _raw_rest_calls(text):
    """Return direct REST requests such as client.NewRequest("GET", "...")."""
    code = _go_code_without_comments(text)
    path_literal = _go_string_literal("path")
    patterns = [
        re.compile(
            r"\b(?P<symbol>(?:[A-Za-z_][A-Za-z0-9_]*\.)+NewRequest)"
            r"\s*\(\s*(?:\"GET\"|http\.MethodGet)\s*,\s*"
            r"fmt\.Sprintf\s*\(\s*" + path_literal,
            re.S,
        ),
        re.compile(
            r"\b(?P<symbol>(?:[A-Za-z_][A-Za-z0-9_]*\.)+NewRequest)"
            r"\s*\(\s*(?:\"GET\"|http\.MethodGet)\s*,\s*"
            + path_literal,
            re.S,
        ),
    ]
    calls = {}
    for pattern in patterns:
        for match in pattern.finditer(code):
            literal = match.group("path_double") or match.group("path_raw")
            path = _normalize_raw_rest_path(
                _decode_go_string_literal(literal))
            symbol = match.group("symbol")
            key = (symbol, path)
            calls[key] = {
                "client_symbol": "%s GET %s" % (symbol, path),
                "method": "GET",
                "path": path,
                "source_role": "read",
            }
    return [calls[key] for key in sorted(calls)]


def _is_graphql_source(text):
    code = _go_code_without_comments(text)
    return bool(
        re.search(r"\bgithubv4\b", code)
        or re.search(r"\bgraphql\s*:", code)
        or "github.com/shurcooL/githubv4" in code
    )


def _base_tokens(resource_type, resource_prefix):
    tokens = openapi_resource_map._base_tokens(resource_type, resource_prefix)
    drop = set(("cloud", "apps", "asserts", "k6", "machine", "learning",
                "monitoring", "oncall", "synthetic", "trust", "zero"))
    return [token for token in tokens if token not in drop]


def _path_parts(path):
    return [part for part in path.strip("/").split("/") if part]


def _is_path_parameter(part):
    return part.startswith("{") and part.endswith("}")


def _operation_words(operation_id):
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", operation_id)
    value = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1 \2", value)
    value = re.sub(r"[^A-Za-z0-9]+", " ", value)
    return [part.lower() for part in value.split() if part]


def _identifier_words(value):
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", value)
    value = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1 \2", value)
    value = re.sub(r"[^A-Za-z0-9]+", " ", value)
    return [part.lower() for part in value.split() if part]


def _is_list_operation(operation_id):
    words = _operation_words(operation_id)
    if "list" in words or "search" in words:
        return True
    return "get" in words and "all" in words


def _operation_mentions_token(operation, token):
    token = _canonical(token)
    if not token:
        return False
    if (token in _canonical(operation["path"])
            or token in _canonical(operation["operation_id"])):
        return True
    for word in _path_words(operation["path"]):
        if _word_matches_token(word, token):
            return True
    for word in _operation_words(operation["operation_id"]):
        if _word_matches_token(word, token):
            return True
    return False


def _sdk_chain_tokens(call):
    drop = set(("cloudflare", "zerotrust"))
    tokens = [
        token for token in call["chain"]
        if _canonical(token) not in drop
    ]
    return tokens or call["chain"]


def _sdk_method_tokens(call):
    drop = set(("by", "fetch", "get", "list", "read", "search", "with"))
    return [
        token for token in _identifier_words(call["method"])
        if token not in drop and len(_canonical(token)) >= 3
    ]


def _static_path_parts(path):
    return [
        part for part in _path_parts(path)
        if not _is_path_parameter(part)
    ]


def _path_words(path):
    words = []
    for part in _static_path_parts(path):
        words.extend(
            word.lower() for word in re.split(r"[^A-Za-z0-9]+", part)
            if word)
    return words


def _word_matches_token(word, token):
    word = _canonical(word)
    token = _canonical(token)
    aliases = set((token,))
    if token.endswith("y"):
        aliases.add(token[:-1] + "ies")
    aliases.add(token + "s")
    if token.endswith("s"):
        aliases.add(token[:-1])
    aliases.update({
        "app" if token == "application" else token,
        "apps" if token == "application" else token,
        "application" if token in ("app", "apps") else token,
        "applications" if token in ("app", "apps") else token,
    })
    if token in ("repo", "repos", "repository", "repositories"):
        aliases.update(("repo", "repos", "repository", "repositories"))
    if token in ("org", "orgs", "organization", "organizations"):
        aliases.update(("org", "orgs", "organization", "organizations"))
    return word in aliases


def _resource_path_sequence_score(resource_type, resource_prefix, operation):
    tokens = _base_tokens(resource_type, resource_prefix)
    words = _path_words(operation["path"])
    if not tokens or len(tokens) > len(words):
        return 0
    best = 0
    for start in range(0, len(words) - len(tokens) + 1):
        if not all(
                _word_matches_token(words[start + offset], token)
                for offset, token in enumerate(tokens)):
            continue
        ends_at_terminal = start + len(tokens) == len(words)
        if len(tokens) == 1 and not ends_at_terminal:
            continue
        best = max(best, 60 if ends_at_terminal else 40)
    return best


def _resource_terminal_score(resource_type, resource_prefix, operation):
    tokens = _base_tokens(resource_type, resource_prefix)
    if not tokens:
        return 0
    parts = _static_path_parts(operation["path"])
    if not parts:
        return 0
    terminal = _canonical(parts[-1])
    last_token = _canonical(tokens[-1])
    if last_token and last_token in terminal:
        return 35
    return 0


def _resource_prefix_score(resource_type, resource_prefix, operation):
    if not resource_prefix:
        return 0
    tokens = _base_tokens(resource_type, resource_prefix)
    if not tokens:
        return 0
    parts = _static_path_parts(operation["path"])
    if len(parts) < 2:
        return 0
    if not _word_matches_token(parts[0], resource_prefix):
        return 0
    if _word_matches_token(parts[1], tokens[0]):
        return 30
    return 0


def _scope_hints(resource_schema):
    attrs = (resource_schema.get("block") or {}).get("attributes") or {}
    scopes = {}
    scope_fields = {
        "account": ("account_id", "account_identifier", "account_tag"),
        "user": ("user_id",),
        "zone": ("zone_id", "zone_identifier", "zone_tag"),
    }
    for scope, names in scope_fields.items():
        present = [
            attrs[name] for name in names
            if name in attrs
        ]
        if not present:
            continue
        if any(item.get("required") for item in present):
            scopes[scope] = "required"
        else:
            scopes[scope] = "optional"
    return scopes


def _operation_scopes(operation):
    scopes = set()
    for part in _path_parts(operation["path"]):
        cleaned = part.strip("{}").lower()
        if cleaned in ("account_id", "account_identifier", "account_tag"):
            scopes.add("account")
        elif cleaned in ("zone_id", "zone_identifier", "zone_tag"):
            scopes.add("zone")
        elif cleaned == "user_id":
            scopes.add("user")
        elif cleaned == "accounts":
            scopes.add("account")
        elif cleaned == "zones":
            scopes.add("zone")
        elif cleaned == "user":
            scopes.add("user")
    return scopes


def _scope_score(operation, scopes):
    if not scopes:
        return 0
    operation_scopes = _operation_scopes(operation)
    required = set(
        scope for scope, state in scopes.items()
        if state == "required")
    if required:
        if operation_scopes.intersection(required):
            return 80
        if operation_scopes:
            return -80
    if len(scopes) == 1:
        only_scope = next(iter(scopes))
        if only_scope in operation_scopes:
            return 40
        if operation_scopes:
            return -40
    if operation_scopes and not operation_scopes.intersection(scopes):
        return -40
    return 0


def _package_call_tokens(call):
    tokens = []
    package_path = call.get("package_path") or ""
    for part in package_path.split("/"):
        if part in ("services", "zscaler", "v3"):
            continue
        tokens.extend(_identifier_words(part))
    tokens.extend(_identifier_words(call["package"]))
    tokens.extend(_identifier_words(call["method"]))
    drop = set(("by", "get", "id", "list", "or", "read", "search"))
    return [
        token for token in tokens
        if token not in drop and len(_canonical(token)) >= 3
    ]


def _method_path_kind_hint(method):
    lowered = method.lower()
    if "byid" in lowered or "details" in lowered or "detail" in lowered:
        return "detail"
    if lowered.startswith(("list", "search")) or "all" in lowered:
        return "list"
    return None


def _action_shaped_path(path):
    action_parts = set((
        "batch", "bulk", "export", "import", "preview", "review", "scan",
        "search", "trigger", "usage",
    ))
    return bool(action_parts.intersection(_path_parts(path)))


def _path_kind(operation):
    if _is_list_operation(operation["operation_id"]):
        return "list"
    parts = _path_parts(operation["path"])
    if parts and _is_path_parameter(parts[-1]):
        return "detail"
    return "list"


def _sdk_call_score(resource_type, resource_prefix, operation, call,
                    resource_schema):
    if operation["method"] != "GET":
        return None
    score = 0
    matched_chain_tokens = 0
    base_tokens = _base_tokens(resource_type, resource_prefix)
    matched_method_tokens = 0
    resource_token_hits = 0
    chain_tokens = _sdk_chain_tokens(call)
    for token in chain_tokens:
        if _operation_mentions_token(operation, token):
            matched_chain_tokens += 1
            score += 30
    method_tokens = _sdk_method_tokens(call)
    for token in method_tokens:
        if _operation_mentions_token(operation, token):
            matched_method_tokens += 1
            score += 22
    if chain_tokens and not matched_chain_tokens:
        return None
    path_sequence_score = _resource_path_sequence_score(
        resource_type, resource_prefix, operation)
    terminal_score = _resource_terminal_score(
        resource_type, resource_prefix, operation)
    for token in base_tokens:
        if _operation_mentions_token(operation, token):
            resource_token_hits += 1
    has_resource_evidence = bool(
        resource_token_hits or path_sequence_score or terminal_score)
    method_exact = _canonical(call["method"]) in operation["aliases"]
    if not chain_tokens:
        if not method_exact and not matched_method_tokens:
            return None
        if base_tokens and not has_resource_evidence:
            return None
        if method_exact:
            score += 110
        else:
            score += 35 + matched_method_tokens * 18
            score -= (len(method_tokens) - matched_method_tokens) * 20
    elif method_exact:
        score += 80
    score -= (len(chain_tokens) - matched_chain_tokens) * 35
    score += resource_token_hits * 8
    score += path_sequence_score
    score += terminal_score
    score += _resource_prefix_score(resource_type, resource_prefix, operation)
    score += _scope_score(operation, _scope_hints(resource_schema))
    path_kind = _path_kind(operation)
    words = _operation_words(operation["operation_id"])
    if call["source_role"] == "read":
        if path_kind == "detail":
            score += 30
        else:
            score += 5
        if "detail" in words or "details" in words or "get" in words:
            score += 10
        if _is_list_operation(operation["operation_id"]):
            score -= 20
        if _action_shaped_path(operation["path"]):
            score -= 25
    elif call["source_role"] == "list":
        if path_kind == "list":
            score += 30
        else:
            score -= 20
        if _is_list_operation(operation["operation_id"]):
            score += 15
        if _action_shaped_path(operation["path"]):
            score -= 20
    return score if score >= SDK_CALL_SCORE_FLOOR else None


def _package_call_score(resource_type, resource_prefix, operation, call,
                        resource_schema):
    if operation["method"] != "GET":
        return None
    score = 0
    matched_tokens = 0
    for token in _package_call_tokens(call):
        if _operation_mentions_token(operation, token):
            matched_tokens += 1
            score += 18
    if not matched_tokens:
        return None
    for token in _base_tokens(resource_type, resource_prefix):
        if _operation_mentions_token(operation, token):
            score += 10
    score += _resource_path_sequence_score(
        resource_type, resource_prefix, operation)
    score += _resource_terminal_score(resource_type, resource_prefix, operation)
    score += _resource_prefix_score(resource_type, resource_prefix, operation)
    score += _scope_score(operation, _scope_hints(resource_schema))
    path_kind = _path_kind(operation)
    hint = _method_path_kind_hint(call["method"])
    if call["source_role"] == "read":
        if hint == "detail" and path_kind == "detail":
            score += 45
        elif hint == "list" and path_kind == "list":
            score += 30
        elif path_kind == "detail":
            score += 20
        if _is_list_operation(operation["operation_id"]):
            score -= 20
        if _action_shaped_path(operation["path"]):
            score -= 20
    elif call["source_role"] == "list":
        if path_kind == "list":
            score += 35
        else:
            score -= 25
        if _is_list_operation(operation["operation_id"]):
            score += 10
    return score if score >= PACKAGE_CALL_SCORE_FLOOR else None


def _path_word_sequence_matches(haystack, needle):
    if not needle or len(needle) > len(haystack):
        return False
    for start in range(0, len(haystack) - len(needle) + 1):
        if all(
                _word_matches_token(haystack[start + offset], token)
                for offset, token in enumerate(needle)):
            return True
    return False


def _raw_rest_call_score(resource_type, resource_prefix, operation, call,
                         resource_schema):
    if operation["method"] != call["method"]:
        return None
    call_words = _path_words(call["path"])
    operation_words = _path_words(operation["path"])
    if not call_words or not operation_words:
        return None
    if not (
            _path_word_sequence_matches(operation_words, call_words)
            or _path_word_sequence_matches(call_words, operation_words)):
        return None
    score = 120 + len(call_words) * 12
    if len(call_words) == len(operation_words):
        score += 50
    score += _resource_path_sequence_score(
        resource_type, resource_prefix, operation)
    score += _resource_terminal_score(resource_type, resource_prefix, operation)
    score += _resource_prefix_score(resource_type, resource_prefix, operation)
    score += _scope_score(operation, _scope_hints(resource_schema))
    if _path_kind(operation) == "detail":
        score += 20
    if _action_shaped_path(operation["path"]):
        score -= 10
    return score if score >= RAW_REST_CALL_SCORE_FLOOR else None


def _candidate_score(resource_type, resource_prefix, operation):
    path = operation["path"]
    operation_id = operation["operation_id"]
    score = 0
    for token in _base_tokens(resource_type, resource_prefix):
        if _canonical(token) in _canonical(path):
            score += 5
    if "{" in path:
        score += 30
    lowered = operation_id.lower()
    if _is_list_operation(operation_id):
        score -= 10
    if lowered.startswith(("get", "retrieve", "read", "routeget")):
        score += 10
    if path.endswith("/search") or "/search/" in path:
        score -= 20
    return score


def _list_candidate_score(resource_type, resource_prefix, operation):
    path = operation["path"]
    operation_id = operation["operation_id"]
    score = 0
    for token in _base_tokens(resource_type, resource_prefix):
        if _canonical(token) in _canonical(path):
            score += 5
    lowered = operation_id.lower()
    if _is_list_operation(operation_id):
        score += 20
    parts = _path_parts(path)
    if parts and _is_path_parameter(parts[-1]):
        score -= 20
    else:
        score += 15
    if lowered.startswith(("get", "retrieve", "read", "routeget")):
        score += 5
    return score


def _operation_entry(hit, evidence_kind, source_files):
    openapi_operation = {
        "kind": "openapi_operation",
        "operation_id": hit["operation_id"],
        "method": hit["method"],
        "path": hit["path"],
    }
    if hit.get("operation_id_source") != "openapi":
        openapi_operation["operation_id_source"] = hit.get(
            "operation_id_source")
    provider_call = {
        "kind": "provider_call",
        "client_symbol": hit.get("client_symbol", hit["operation_id"]),
        "matched_aliases": hit.get("matched_aliases", []),
        "source_files": source_files,
    }
    if hit.get("sdk_method"):
        provider_call["sdk_method"] = hit["sdk_method"]
    if hit.get("sdk_package"):
        provider_call["sdk_package"] = hit["sdk_package"]
    if hit.get("sdk_package_path"):
        provider_call["sdk_package_path"] = hit["sdk_package_path"]
    if hit.get("raw_rest_path"):
        provider_call["raw_rest_path"] = hit["raw_rest_path"]
    if hit.get("source_role"):
        provider_call["source_role"] = hit["source_role"]
    if hit.get("alternate_client_symbols"):
        provider_call["alternate_client_symbols"] = (
            hit["alternate_client_symbols"])
    entry = {
        "evidence_kind": evidence_kind,
        "confidence": "high",
        "method": hit["method"],
        "operation_id": hit["operation_id"],
        "path": hit["path"],
        "path_kind": hit["path_kind"],
        "hops": [
            provider_call,
            openapi_operation,
        ],
    }
    if hit.get("operation_id_source") != "openapi":
        entry["operation_id_source"] = hit.get("operation_id_source")
    return entry


def _candidate_entry(hit):
    entry = {
        "method": hit["method"],
        "path": hit["path"],
        "operation_id": hit["operation_id"],
        "path_kind": hit["path_kind"],
        "read_score": hit["read_score"],
        "list_score": hit["list_score"],
    }
    if hit.get("operation_id_source") != "openapi":
        entry["operation_id_source"] = hit.get("operation_id_source")
    if hit.get("client_symbol"):
        entry["client_symbol"] = hit["client_symbol"]
    if hit.get("source_role"):
        entry["source_role"] = hit["source_role"]
    if hit.get("alternate_client_symbols"):
        entry["alternate_client_symbols"] = hit["alternate_client_symbols"]
    return entry


def _candidate_operation_entry(hit, evidence_kind, source_files):
    entry = _operation_entry(hit, evidence_kind, source_files)
    entry["confidence"] = "low"
    entry["read_score"] = hit["read_score"]
    entry["list_score"] = hit["list_score"]
    return entry


def _dedupe_hits(hits):
    grouped = {}
    for hit in hits:
        key = (
            hit["method"],
            hit["path"],
            hit["operation_id"],
            hit["path_kind"],
            hit.get("source_role"),
        )
        existing = grouped.get(key)
        if existing is None:
            grouped[key] = dict(hit)
            continue
        existing["read_score"] = max(existing["read_score"], hit["read_score"])
        existing["list_score"] = max(existing["list_score"], hit["list_score"])
        aliases = set(existing.get("matched_aliases", []))
        aliases.update(hit.get("matched_aliases", []))
        existing["matched_aliases"] = sorted(aliases)
        symbols = set(existing.get("alternate_client_symbols", []))
        for item in (existing.get("client_symbol"), hit.get("client_symbol")):
            if item:
                symbols.add(item)
        if symbols:
            existing["alternate_client_symbols"] = sorted(symbols)
    return list(grouped.values())


def _select_hit(hits, role):
    role_hits = [
        hit for hit in hits
        if hit.get("source_role") in (None, role)
    ]
    if role == "list":
        candidates = [hit for hit in role_hits if hit["path_kind"] == "list"]
        score_key = "list_score"
    else:
        candidates = list(role_hits)
        score_key = "read_score"
    candidates.sort(key=lambda hit: (
        -hit[score_key],
        hit["path"],
        hit["operation_id"],
    ))
    if not candidates:
        return None, []
    best = candidates[0]
    if role == "read" and best["path_kind"] != "detail":
        detail_close = [
            hit for hit in candidates[1:]
            if (hit["path_kind"] == "detail"
                and best[score_key] - hit[score_key]
                <= READ_DETAIL_AMBIGUITY_DELTA)
        ]
        if detail_close:
            return None, [best] + detail_close[:4]
    ambiguous = [
        hit for hit in candidates[1:]
        if (hit["path_kind"] == best["path_kind"]
            and best[score_key] - hit[score_key] <= AMBIGUITY_SCORE_DELTA)
    ]
    if ambiguous:
        return None, [best] + ambiguous[:4]
    return best, []


def _relationship_resource(resource_type, resource_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    if len(tokens) < 2:
        return False
    joined = "_".join(tokens)
    phrases = (
        "secret_repositories",
        "variable_repositories",
        "role_team",
        "role_user",
        "team_assignment",
        "user_assignment",
        "repository_topics",
        "repository_collaborator",
        "sync_group_mapping",
    )
    if any(phrase in joined for phrase in phrases):
        return True
    relationship_tokens = set((
        "assignment",
        "collaborator",
        "collaborators",
        "mapping",
        "members",
        "membership",
        "repositories",
        "topics",
    ))
    return bool(relationship_tokens.intersection(tokens))


def _select_relationship_list_hit(hits, resource_type, resource_prefix):
    if not _relationship_resource(resource_type, resource_prefix):
        return None, []
    return _select_hit(hits, "list")


def _load_resource_schemas(schema_path, provider_source=None):
    provider = openapi_resource_map._provider_from_schema(
        _read_json(schema_path), provider_source=provider_source)
    return provider.get("resource_schemas") or {}


def derive_registry(schema_path, openapi_path, source_root,
                    provider_source=None, resource_prefix=""):
    resource_schemas = _load_resource_schemas(
        schema_path, provider_source=provider_source)
    resource_names = sorted(resource_schemas)
    files_by_resource = _resource_files(
        source_root, resource_names, resource_prefix)
    operations = _operation_index(_read_json(openapi_path))

    registry = {}
    diagnostics = []
    resources_with_source_files = 0
    for resource in resource_names:
        source_paths = files_by_resource.get(resource) or []
        source_files = [
            os.path.relpath(path, source_root)
            for path in source_paths
        ]
        if source_paths:
            resources_with_source_files += 1
        source_text = []
        for path in source_paths:
            with open(path, encoding="utf-8") as f:
                source_text.append(f.read())
        source_identifiers = _go_identifier_tokens("\n".join(source_text))
        joined_source_text = "\n".join(source_text)
        sdk_calls = _sdk_client_calls(joined_source_text)
        package_calls = _sdk_package_calls(joined_source_text, source_root)
        raw_rest_calls = _raw_rest_calls(joined_source_text)
        graphql_source = _is_graphql_source(joined_source_text)

        hits = []
        for operation in operations:
            if operation["method"] != "GET":
                continue
            matched_aliases = sorted(
                alias for alias in operation["aliases"]
                if alias in source_identifiers)
            if matched_aliases:
                hit = dict(operation)
                hit["matched_aliases"] = matched_aliases
                hit["path_kind"] = _path_kind(operation)
                hit["read_score"] = _candidate_score(
                    resource, resource_prefix, operation)
                hit["list_score"] = _list_candidate_score(
                    resource, resource_prefix, operation)
                hits.append(hit)
            for call in sdk_calls:
                sdk_score = _sdk_call_score(
                    resource, resource_prefix, operation, call,
                    resource_schemas[resource])
                if sdk_score is None:
                    continue
                hit = dict(operation)
                hit["client_symbol"] = call["client_symbol"]
                hit["matched_aliases"] = [call["client_symbol"]]
                hit["path_kind"] = _path_kind(operation)
                hit["read_score"] = (
                    _candidate_score(resource, resource_prefix, operation)
                    + sdk_score)
                hit["list_score"] = (
                    _list_candidate_score(resource, resource_prefix, operation)
                    + sdk_score)
                hit["sdk_method"] = call["method"]
                hit["source_role"] = call["source_role"]
                hits.append(hit)
            for call in package_calls:
                sdk_score = _package_call_score(
                    resource, resource_prefix, operation, call,
                    resource_schemas[resource])
                if sdk_score is None:
                    continue
                hit = dict(operation)
                hit["client_symbol"] = call["client_symbol"]
                hit["matched_aliases"] = [call["client_symbol"]]
                hit["path_kind"] = _path_kind(operation)
                hit["read_score"] = (
                    _candidate_score(resource, resource_prefix, operation)
                    + sdk_score)
                hit["list_score"] = (
                    _list_candidate_score(resource, resource_prefix, operation)
                    + sdk_score)
                hit["sdk_method"] = call["method"]
                hit["sdk_package"] = call["package"]
                hit["sdk_package_path"] = call["package_path"]
                hit["source_role"] = call["source_role"]
                hits.append(hit)
            for call in raw_rest_calls:
                raw_score = _raw_rest_call_score(
                    resource, resource_prefix, operation, call,
                    resource_schemas[resource])
                if raw_score is None:
                    continue
                hit = dict(operation)
                hit["client_symbol"] = call["client_symbol"]
                hit["matched_aliases"] = [call["path"]]
                hit["path_kind"] = _path_kind(operation)
                hit["read_score"] = (
                    _candidate_score(resource, resource_prefix, operation)
                    + raw_score)
                hit["list_score"] = (
                    _list_candidate_score(resource, resource_prefix, operation)
                    + raw_score)
                hit["raw_rest_path"] = call["path"]
                hit["source_role"] = call["source_role"]
                hits.append(hit)

        hits.sort(key=lambda hit: (
            -hit["read_score"],
            hit["path"],
            hit["operation_id"],
        ))
        hits = _dedupe_hits(hits)
        hits.sort(key=lambda hit: (
            -hit["read_score"],
            hit["path"],
            hit["operation_id"],
        ))

        read_hit, read_ambiguous = _select_hit(hits, "read")
        list_hit, list_ambiguous = _select_hit(hits, "list")
        relationship_read_hit, relationship_read_ambiguous = (
            _select_relationship_list_hit(hits, resource, resource_prefix))
        status = "unmapped"
        reason = None
        entry = {
            "product": resource_prefix,
            "surface": resource_prefix,
            "status": status,
            "source": {
                "candidate_count": len(hits),
                "files": source_files,
            },
            "reason": None,
        }
        if sdk_calls:
            entry["source"]["client_call_count"] = len(sdk_calls)
            entry["source"]["client_calls"] = [
                call["client_symbol"] for call in sdk_calls[:20]
            ]
        if package_calls:
            entry["source"]["package_call_count"] = len(package_calls)
            entry["source"]["package_calls"] = [
                call["client_symbol"] for call in package_calls[:20]
            ]
        if raw_rest_calls:
            entry["source"]["raw_rest_call_count"] = len(raw_rest_calls)
            entry["source"]["raw_rest_calls"] = [
                call["client_symbol"] for call in raw_rest_calls[:20]
            ]
        if graphql_source:
            entry["source"]["graphql"] = True
        if read_ambiguous:
            status = "ambiguous_source_operation"
            reason = "ambiguous_source_operation"
            entry["status"] = status
            entry["reason"] = reason
            entry["candidates"] = [
                _candidate_operation_entry(hit, "read", source_files)
                for hit in read_ambiguous
            ]
        elif read_hit:
            status = "mapped"
            entry["status"] = status
            entry["read"] = _operation_entry(read_hit, "read", source_files)
            if list_hit and list_hit["path"] != read_hit["path"]:
                entry["list"] = _operation_entry(
                    list_hit, "list", source_files)
            if list_ambiguous:
                entry["source"]["list_ambiguous"] = [
                    _candidate_entry(hit) for hit in list_ambiguous
                ]
        elif relationship_read_ambiguous:
            status = "ambiguous_source_operation"
            reason = "ambiguous_source_operation"
            entry["status"] = status
            entry["reason"] = reason
            entry["candidates"] = [
                _candidate_operation_entry(
                    hit, "relationship_list_read", source_files)
                for hit in relationship_read_ambiguous
            ]
        elif relationship_read_hit:
            status = "mapped"
            entry["status"] = status
            entry["read"] = _operation_entry(
                relationship_read_hit, "relationship_list_read", source_files)
            entry["source"]["relationship_list_read"] = True
        elif graphql_source:
            status = "graphql_source"
            reason = "graphql_source"
            entry["status"] = status
            entry["reason"] = reason
        else:
            reason = (
                "resource_file_not_found"
                if not source_paths else "no_source_operation_match"
            )
            entry["reason"] = reason
        registry[resource] = entry

        diagnostics.append({
            "resource": resource,
            "status": status,
            "reason": reason,
            "files": source_files,
            "ambiguous": [
                _candidate_entry(hit) for hit in read_ambiguous
            ],
            "hits": [
                _candidate_entry(hit)
                for hit in hits[:10]
            ],
        })

    ambiguous = sum(
        1 for item in diagnostics
        if item["status"] == "ambiguous_source_operation")
    graphql_source = sum(
        1 for item in diagnostics
        if item["status"] == "graphql_source")
    mapped = sum(
        1 for item in registry.values()
        if item["status"] == "mapped")
    return {
        "summary": {
            "resources": len(resource_names),
            "resources_with_source_files": resources_with_source_files,
            "resources_without_source_files": (
                len(resource_names) - resources_with_source_files),
            "mapped": mapped,
            "ambiguous": ambiguous,
            "graphql_source": graphql_source,
            "unmapped": (
                len(resource_names) - mapped - ambiguous - graphql_source),
        },
        "registry": registry,
        "diagnostics": diagnostics,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Derive read registry from provider source operations")
    parser.add_argument("--schema", required=True, help="Terraform provider schema JSON")
    parser.add_argument("--openapi", required=True, help="OpenAPI/Swagger JSON")
    parser.add_argument("--source-root", required=True, help="Provider source root")
    parser.add_argument("--provider-source", help="Provider source address")
    parser.add_argument("--resource-prefix", default="", help="Resource name prefix/product")
    parser.add_argument("--out", help="Write source/read registry JSON to this file")
    parser.add_argument("--diagnostics", help="Write diagnostics JSON to this file")
    args = parser.parse_args(argv)
    try:
        report = derive_registry(
            args.schema,
            args.openapi,
            args.source_root,
            provider_source=args.provider_source,
            resource_prefix=args.resource_prefix,
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2

    if args.diagnostics:
        _write_json({
            "summary": report["summary"],
            "diagnostics": report["diagnostics"],
        }, path=args.diagnostics)
    _write_json(report["registry"], path=args.out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
