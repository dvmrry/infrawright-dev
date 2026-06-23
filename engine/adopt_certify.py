"""Fixture-driven adoption certification advisory CLI."""

import argparse
import json
import sys

from engine import transform
from engine.adoption_meta import (
    adoption_entry,
    derive_key_from_identity,
    identity_item,
    skip_identity_item,
)
from engine.advisory_report import build_report
from engine.drift_policy import DriftPolicy


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _raw_items_by_key(resource_type, raw):
    if isinstance(raw, dict):
        return dict(
            (str(key), transform.snake_keys(value))
            for key, value in sorted(raw.items()))
    if not isinstance(raw, list):
        raise ValueError("--raw must be a JSON object or list")

    meta = adoption_entry(resource_type)
    out = {}
    for raw_item in raw:
        if not isinstance(raw_item, dict):
            raise ValueError("--raw list items must be objects")
        ident = identity_item(raw_item, resource_type)
        if skip_identity_item(ident, meta):
            continue
        key = derive_key_from_identity(ident, meta)
        if key in out:
            raise ValueError("duplicate derived raw key %r" % key)
        out[key] = transform.snake_keys(raw_item)
    return out


def _oracle_state_by_key(raw):
    if not isinstance(raw, dict):
        raise ValueError("--oracle-state must be a JSON object keyed by item")
    out = {}
    for key, value in sorted(raw.items()):
        if not isinstance(value, dict):
            raise ValueError("oracle_state[%r] must be an object" % key)
        if "values" not in value:
            raise ValueError("oracle_state[%r] missing values" % key)
        if not isinstance(value.get("values"), dict):
            raise ValueError("oracle_state[%r].values must be an object" % key)
        out[str(key)] = value
    return out


def _projected_items_by_key(raw):
    if not isinstance(raw, dict) or not isinstance(raw.get("items"), dict):
        raise ValueError("--projected must be tfvars JSON with an items object")
    return dict((str(key), value) for key, value in sorted(raw["items"].items()))


def _require_same_keys(raw_items, oracle_state, projected_items):
    expected = set(raw_items)
    for label, data in (
            ("oracle-state", oracle_state),
            ("projected", projected_items)):
        keys = set(data)
        missing = sorted(expected - keys)
        extra = sorted(keys - expected)
        if missing or extra:
            detail = []
            if missing:
                detail.append("missing keys: %s" % ", ".join(missing))
            if extra:
                detail.append("extra keys: %s" % ", ".join(extra))
            raise ValueError("%s key mismatch (%s)" % (label, "; ".join(detail)))


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Build a fixture-driven adoption advisory report.")
    parser.add_argument("--resource-type", required=True)
    parser.add_argument("--raw", required=True)
    parser.add_argument("--oracle-state", required=True)
    parser.add_argument("--projected", required=True)
    parser.add_argument("--policy")
    args = parser.parse_args(argv)

    try:
        raw_items = _raw_items_by_key(args.resource_type, _read_json(args.raw))
        oracle_state = _oracle_state_by_key(_read_json(args.oracle_state))
        projected_items = _projected_items_by_key(_read_json(args.projected))
        _require_same_keys(raw_items, oracle_state, projected_items)
        policy = DriftPolicy.load(args.policy)
        report = build_report(
            args.resource_type,
            raw_items,
            oracle_state,
            projected_items,
            policy,
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1

    sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
