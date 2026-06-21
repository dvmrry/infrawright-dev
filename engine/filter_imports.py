"""Filter import blocks down to addresses not already present in state."""
import re
import sys

_BLOCK_RE = re.compile(r"import \{[^}]*\}\n?", re.DOTALL)
_TO_RE = re.compile(r"to\s*=\s*(\S+)")


def filter_imports(imports_text, state_addresses):
    """Return (filtered_text, kept_count, skipped_count)."""
    managed = set(state_addresses)
    kept_blocks = []
    skipped = 0
    for block in _BLOCK_RE.findall(imports_text):
        match = _TO_RE.search(block)
        address = match.group(1) if match else None
        if address is not None and address in managed:
            skipped += 1
            continue
        kept_blocks.append(block.strip("\n"))
    text = "\n\n".join(kept_blocks)
    if text:
        text += "\n"
    return text, len(kept_blocks), skipped


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
