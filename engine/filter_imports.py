"""Filter import blocks down to addresses not already present in state."""
import re
import sys

_BLOCK_START_RE = re.compile(r"(?m)^[ \t]*import\s*\{")
_TO_RE = re.compile(r"(?m)^[ \t]*to\s*=\s*(.*?)\s*$")


def filter_imports(imports_text, state_addresses):
    """Return (filtered_text, kept_count, skipped_count)."""
    managed = set(state_addresses)
    parts = []
    pos = 0
    kept = 0
    skipped = 0
    for start, end in _iter_import_blocks(imports_text):
        parts.append(imports_text[pos:start])
        block = imports_text[start:end]
        match = _TO_RE.search(block)
        address = match.group(1).strip() if match else None
        if address is not None and address in managed:
            skipped += 1
            pos = end
            continue
        parts.append(block)
        kept += 1
        pos = end
    parts.append(imports_text[pos:])
    text = "".join(parts)
    if kept == 0 and not text.strip():
        text = ""
    return text, kept, skipped


def _iter_import_blocks(text):
    """Yield (start, end) for top-level generated import blocks.

    This intentionally scans only line-start `import { ... }` blocks and is not
    a general HCL parser. Block end detection is HCL-string-aware so generated
    import IDs or keys containing braces do not truncate the block.
    """
    pos = 0
    while True:
        match = _BLOCK_START_RE.search(text, pos)
        if match is None:
            return
        open_brace = text.rfind("{", match.start(), match.end())
        end = _scan_block_end(text, open_brace)
        yield match.start(), end
        pos = end


def _scan_block_end(text, open_brace):
    depth = 0
    i = open_brace
    while i < len(text):
        ch = text[i]
        if ch == '"':
            try:
                _, i = _parse_hcl_string_literal(text, i)
            except ValueError as exc:
                raise ValueError("malformed generated import block: %s" % exc)
            continue
        if ch == "{":
            depth += 1
            i += 1
            continue
        if ch == "}":
            depth -= 1
            i += 1
            if depth == 0:
                if text.startswith("\r\n", i):
                    return i + 2
                if i < len(text) and text[i] in ("\n", "\r"):
                    return i + 1
                return i
            continue
        i += 1
    raise ValueError("unterminated generated import block")


def _parse_hcl_string_literal(text, start):
    from engine.transform import parse_hcl_string_literal

    return parse_hcl_string_literal(text, start)


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    if len(argv) != 2:
        sys.stderr.write(
            "usage: python -m engine.filter_imports <imports.tf> <state-list-file>\n"
        )
        return 2
    with open(argv[0], encoding="utf-8") as f:
        imports_text = f.read()
    with open(argv[1], encoding="utf-8") as f:
        state_addresses = [line.strip() for line in f if line.strip()]
    text, kept, skipped = filter_imports(imports_text, state_addresses)
    sys.stdout.write(text)
    sys.stderr.write(
        "%d import(s) kept, %d already managed (skipped)\n" % (kept, skipped)
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
