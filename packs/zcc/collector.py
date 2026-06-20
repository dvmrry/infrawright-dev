"""ZCC collector URL/auth seams.

Stdlib-only, Python 3.6-floor.
"""
from packs._shared.zscaler import collector as zscaler


def compose_url(auth_mode, path, ctx):
    if auth_mode == "oneapi":
        return zscaler.compose_url(auth_mode, "zcc", path, ctx)
    raise ValueError("unknown auth_mode/product: %r/%r" % (auth_mode, "zcc"))


def acquire(auth_mode, env, ctx, opener, now_ms=None):
    if auth_mode == "oneapi":
        return zscaler.acquire(auth_mode, env, ctx, opener, now_ms=now_ms)
    if auth_mode == "legacy":
        # ZCC is OneAPI-only - there is no legacy mobile-portal path here.
        # Caught per-product by fetch_all; the message names the fix.
        raise SystemExit(
            "ZCC has no legacy auth path — it is OneAPI-only. Use OneAPI, "
            "or scope ZCC out of legacy runs with RESOURCE=\"zia zpa\".")
    raise SystemExit("unknown auth mode %r" % auth_mode)
