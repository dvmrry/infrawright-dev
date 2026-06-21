"""Summarize Terraform saved-plan JSON for review gates."""
import json
import sys


def counts(plan):
    """Return (imports, adds, changes, destroys) for a plan JSON object."""
    if not isinstance(plan, dict) or "format_version" not in plan:
        raise ValueError(
            "stdin is not plan JSON (no format_version - terraform version skew?)"
        )
    imports = adds = changes = destroys = 0
    for resource_change in plan.get("resource_changes") or []:
        change = resource_change.get("change") or {}
        actions = set(change.get("actions") or [])
        if change.get("importing") or resource_change.get("importing"):
            imports += 1
        if "create" in actions:
            adds += 1
        if "update" in actions:
            changes += 1
        if "delete" in actions:
            destroys += 1
    return imports, adds, changes, destroys


def summarize(plan, label):
    imports, adds, changes, destroys = counts(plan)
    return "| %s | %d | %d | %d | %d |" % (
        label, imports, adds, changes, destroys), destroys


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    if len(argv) != 1:
        sys.stderr.write(
            "usage: terraform show -json tfplan | "
            "python -m engine.plan_summary <label> | --counts\n"
        )
        return 2
    try:
        plan = json.load(sys.stdin)
        if argv[0] == "--counts":
            sys.stdout.write("%d %d %d %d\n" % counts(plan))
            return 0
        row, destroys = summarize(plan, argv[0])
    except ValueError as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1
    sys.stdout.write(row + "\n")
    sys.stdout.write("%d\n" % destroys)
    return 0


if __name__ == "__main__":
    sys.exit(main())
