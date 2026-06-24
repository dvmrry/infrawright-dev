#!/usr/bin/env python3
"""Read-only CLI for the adoption metadata inventory report.

This script only aggregates validated pack metadata. It does not project, omit,
change drift policy, alter assert-adoptable, render provider configuration, or
run Terraform/OpenTofu.
"""
import argparse
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from engine import adoption_inventory_report


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Read-only adoption metadata inventory report."
    )
    parser.add_argument(
        "--provider",
        help="Filter to one provider short name.",
    )
    parser.add_argument(
        "--resource-type",
        help="Filter to one resource type.",
    )
    parser.add_argument(
        "--class",
        dest="metadata_class",
        choices=["provider_config", "absent_default", "dynamic_schema"],
        help="Filter to one metadata class.",
    )
    parser.add_argument(
        "--format",
        choices=["json", "markdown"],
        default="json",
        help="Output format (default: json).",
    )
    args = parser.parse_args(argv)

    report = adoption_inventory_report.build_report(
        provider=args.provider,
        resource_type=args.resource_type,
        metadata_class=args.metadata_class,
    )

    if args.format == "json":
        sys.stdout.write(adoption_inventory_report.to_json(report))
        sys.stdout.write("\n")
    else:
        sys.stdout.write(adoption_inventory_report.to_markdown(report))

    return 0


if __name__ == "__main__":
    sys.exit(main())
