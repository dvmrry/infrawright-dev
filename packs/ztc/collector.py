"""ZTC collector URL/auth seams.

Stdlib-only, Python 3.6-floor.
"""
from packs._shared.zscaler import collector as zscaler


def compose_url(auth_mode, path, ctx):
    if auth_mode == "oneapi":
        return zscaler.compose_url(auth_mode, "ztc", path, ctx)
    if auth_mode == "legacy":
        raise SystemExit(
            "ZTC legacy auth is not wired in the collector yet. Use OneAPI, "
            "or scope ZTC out of legacy runs with RESOURCE=\"zia zpa\".")
    raise SystemExit("unknown auth mode %r" % auth_mode)


def acquire(auth_mode, env, ctx, opener, now_ms=None):
    if auth_mode == "oneapi":
        return zscaler.acquire(auth_mode, env, ctx, opener, now_ms=now_ms)
    if auth_mode == "legacy":
        raise SystemExit(
            "ZTC legacy auth is not wired in the collector yet. Use OneAPI, "
            "or scope ZTC out of legacy runs with RESOURCE=\"zia zpa\".")
    raise SystemExit("unknown auth mode %r" % auth_mode)
