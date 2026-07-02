"""Audit vendor-specific tokens in engine source files.

This command is intentionally diagnostic-only. It does not move code, load
provider packs, run collectors, generate Terraform, or change adoption
behavior. The allowlist documents transitional or intentional vendor-specific
engine references so new references fail loudly.
"""
import argparse
import json
import os
import re
import sys


VENDOR_TOKENS = (
    "zscaler",
    "zia",
    "zpa",
    "zcc",
    "cloudflare",
    "netbox",
    "aws",
    "google",
)

DEFAULT_ALLOWLIST = os.path.join(
    os.path.dirname(os.path.abspath(__file__)),
    "vendor_boundary_allowlist.json",
)

SKIP_BASENAMES = {
    "audit_vendor_boundary.py",
}


class AuditError(ValueError):
    pass


def _repo_root():
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _rel(path, root):
    return os.path.relpath(path, root).replace(os.sep, "/")


def _compile_token_regex(tokens):
    # Treat underscores, dashes, slashes, quotes, and dots as separators while
    # avoiding false positives inside ordinary words such as "awesome".
    pattern = r"(?<![A-Za-z0-9])(%s)(?![A-Za-z0-9])" % "|".join(
        re.escape(t) for t in sorted(tokens, key=len, reverse=True)
    )
    return re.compile(pattern, re.IGNORECASE)


def iter_engine_files(root):
    engine_dir = os.path.join(root, "engine")
    if not os.path.isdir(engine_dir):
        return
    for dirpath, dirnames, filenames in os.walk(engine_dir):
        dirnames[:] = sorted(
            d for d in dirnames
            if d != "__pycache__" and not d.startswith(".")
        )
        for filename in sorted(filenames):
            if not filename.endswith(".py"):
                continue
            if filename in SKIP_BASENAMES:
                continue
            yield os.path.join(dirpath, filename)


def scan_engine(root, tokens=VENDOR_TOKENS):
    token_re = _compile_token_regex(tokens)
    matches = []
    for path in iter_engine_files(root):
        relpath = _rel(path, root)
        with open(path, encoding="utf-8") as f:
            for line_no, line in enumerate(f, 1):
                seen = set()
                for match in token_re.finditer(line):
                    token = match.group(1).lower()
                    if token in seen:
                        continue
                    seen.add(token)
                    matches.append({
                        "path": relpath,
                        "line": line_no,
                        "token": token,
                        "excerpt": line.rstrip("\n"),
                    })
    return matches


def load_allowlist(path):
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
    except (IOError, OSError, ValueError) as exc:
        raise AuditError("failed to read allowlist %s: %s" % (path, exc))

    if not isinstance(data, dict):
        raise AuditError("%s: allowlist must be a JSON object" % path)
    entries = data.get("allow")
    if not isinstance(entries, list):
        raise AuditError("%s: allow must be a list" % path)

    out = []
    for i, entry in enumerate(entries):
        prefix = "%s: allow[%d]" % (path, i)
        if not isinstance(entry, dict):
            raise AuditError("%s must be an object" % prefix)
        unknown = sorted(set(entry) - {"path", "token", "pattern", "reason"})
        if unknown:
            raise AuditError("%s unknown keys: %s" % (prefix, ", ".join(unknown)))
        for key in ("path", "token", "pattern", "reason"):
            value = entry.get(key)
            if not isinstance(value, str) or not value:
                raise AuditError("%s.%s must be a non-empty string" % (prefix, key))
        out.append({
            "path": entry["path"],
            "token": entry["token"].lower(),
            "pattern": entry["pattern"],
            "reason": entry["reason"],
        })
    return out


def _is_allowed(match, allowlist):
    for entry in allowlist:
        if entry["path"] != match["path"]:
            continue
        if entry["token"] != match["token"]:
            continue
        if entry["pattern"] not in match["excerpt"]:
            continue
        return True
    return False


def classify_matches(matches, allowlist):
    allowed = []
    violations = []
    for match in matches:
        if _is_allowed(match, allowlist):
            allowed.append(match)
        else:
            violations.append(match)
    return allowed, violations


def audit(root=None, allowlist_path=None):
    root = os.path.abspath(root or _repo_root())
    allowlist_path = os.path.abspath(allowlist_path or DEFAULT_ALLOWLIST)
    allowlist = load_allowlist(allowlist_path)
    matches = scan_engine(root)
    allowed, violations = classify_matches(matches, allowlist)
    return {
        "root": root,
        "allowlist": allowlist_path,
        "matches": matches,
        "allowed": allowed,
        "violations": violations,
    }


def _print_match(out, match):
    out.write(
        "%s:%d: %s: %s\n"
        % (match["path"], match["line"], match["token"], match["excerpt"].strip())
    )


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="python -m engine.audit_vendor_boundary",
        description="Audit vendor-specific tokens in engine source files.",
    )
    parser.add_argument("--root", default=None, help=argparse.SUPPRESS)
    parser.add_argument("--allowlist", default=None, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    try:
        result = audit(root=args.root, allowlist_path=args.allowlist)
    except AuditError as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2

    sys.stdout.write("vendor boundary audit\n")
    sys.stdout.write("tokens: %s\n" % ", ".join(VENDOR_TOKENS))
    sys.stdout.write("allowed matches: %d\n" % len(result["allowed"]))
    sys.stdout.write("violations: %d\n" % len(result["violations"]))
    if result["violations"]:
        sys.stdout.write("\nviolations:\n")
        for match in result["violations"]:
            _print_match(sys.stdout, match)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
