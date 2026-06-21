"""Cloudflare collector URL/auth seams.

Stdlib-only, Python 3.6-floor.
"""
from urllib.parse import quote


_BASE = "https://api.cloudflare.com/client/v4"


def _require(env, name):
    value = env.get(name)
    if not value:
        raise SystemExit("missing required env var %s" % name)
    return value


def _resolve_path(path, ctx):
    resolved = path
    if "{account_id}" in resolved:
        resolved = resolved.replace(
            "{account_id}", quote(ctx["account_id"], safe=""))
    if "{zone_id}" in resolved:
        resolved = resolved.replace(
            "{zone_id}", quote(ctx["_current_zone_id"], safe=""))
    if "{list_id}" in resolved:
        resolved = resolved.replace(
            "{list_id}", quote(ctx["list_id"], safe=""))
    return resolved


def compose_url(auth_mode, path, ctx):
    return "%s/%s" % (_BASE, _resolve_path(path, ctx).lstrip("/"))


def acquire(auth_mode, env, ctx, opener, now_ms=None):
    return _require(env, "CLOUDFLARE_API_TOKEN")


def _inject(item, field, value):
    if not isinstance(item, dict):
        raise RuntimeError("Cloudflare item for %s injection is not an object" % field)
    item[field] = value
    return item


def _zone_ids(auth_mode, ctx, token, opener):
    from collectors import rest
    return rest._fetch_paths(
        rest.manifest_entry("cloudflare_zone"), auth_mode, ctx, token, opener)


def _fetch_zone_scoped(entry, auth_mode, ctx, token, opener):
    from collectors import rest
    items = []
    previous = ctx.get("_current_zone_id")
    had_previous = "_current_zone_id" in ctx
    try:
        for zone in _zone_ids(auth_mode, ctx, token, opener):
            zone_id = zone.get("id")
            if not zone_id:
                raise RuntimeError("Cloudflare zone page returned a zone without id")
            ctx["_current_zone_id"] = zone_id
            for item in rest._fetch_paths(entry, auth_mode, ctx, token, opener):
                items.append(_inject(item, "zone_id", zone_id))
    finally:
        if had_previous:
            ctx["_current_zone_id"] = previous
        else:
            ctx.pop("_current_zone_id", None)
    return items


def _fetch_list_items(entry, auth_mode, ctx, token, opener):
    from collectors import rest
    parent_type = entry["parent"]
    parent_entry = rest.manifest_entry(parent_type)
    items = []
    for parent in rest._fetch_paths(parent_entry, auth_mode, ctx, token, opener):
        list_id = parent.get("id")
        if not list_id:
            raise RuntimeError("Cloudflare list page returned a list without id")
        child_ctx = dict(ctx)
        child_ctx["list_id"] = list_id
        for item in rest._fetch_paths(entry, auth_mode, child_ctx, token, opener):
            items.append(_inject(item, "list_id", list_id))
    return items


def fetch_resource(resource_type, entry, auth_mode, ctx, token, opener):
    """Cloudflare two-pass fetches; default entries use the shared REST path."""
    from collectors import rest
    if entry.get("zone_scoped"):
        return _fetch_zone_scoped(entry, auth_mode, ctx, token, opener)
    if resource_type == "cloudflare_list_item":
        return _fetch_list_items(entry, auth_mode, ctx, token, opener)
    return rest._fetch_paths(entry, auth_mode, ctx, token, opener)
