"""ZIA collector URL/auth seams.

Stdlib-only, Python 3.6-floor.
"""
import json
import time

from packs._shared.zscaler import collector as zscaler


def obfuscate_api_key(api_key, timestamp):
    """Port of the ZIA legacy key-obfuscation algorithm (public SDK).

    timestamp is milliseconds-since-epoch as a string. Raises ValueError
    on inputs too short to index, matching the SDK guard.
    """
    if len(timestamp) < 6 or len(api_key) < 12:
        raise ValueError("timestamp or api key below required length")
    high = timestamp[-6:]
    low = "%06d" % (int(high) >> 1)
    obfuscated = ""
    for ch in high:
        obfuscated += api_key[int(ch)]
    for ch in low:
        obfuscated += api_key[int(ch) + 2]
    return obfuscated


def _legacy_zia_base(cloud):
    """ZIA legacy base for ZIA_CLOUD: https://zsapi.<cloud>.net.

    An empty cloud would build https://zsapi..net — a malformed host that
    surfaces downstream as an opaque provider crash, so fail loud here.
    """
    if not cloud:
        raise SystemExit(
            "ZIA_CLOUD is required in legacy mode (e.g. zscalertwo) — it "
            "selects the ZIA host https://zsapi.<cloud>.net")
    return "https://zsapi.%s.net" % cloud


def _zia_legacy_base_for(ctx):
    """Legacy ZIA base: ZIA_LEGACY_BASE_URL override wins over derivation."""
    return ctx.get("zia_legacy_base") or _legacy_zia_base(ctx.get("cloud", ""))


def compose_url(auth_mode, path, ctx):
    if auth_mode == "oneapi":
        return zscaler.compose_url(auth_mode, "zia", path, ctx)
    if auth_mode == "legacy":
        return "%s/api/v1/%s" % (_zia_legacy_base_for(ctx), path)
    raise ValueError("unknown auth_mode/product: %r/%r" % (auth_mode, "zia"))


def acquire(auth_mode, env, ctx, opener, now_ms=None):
    if auth_mode == "oneapi":
        return zscaler.acquire(auth_mode, env, ctx, opener, now_ms=now_ms)
    if auth_mode == "legacy":
        ts = str(now_ms if now_ms is not None else int(time.time() * 1000))
        url = "%s/api/v1/authenticatedSession" % _zia_legacy_base_for(ctx)
        payload = json.dumps({
            "apiKey": obfuscate_api_key(zscaler._require(env, "ZIA_API_KEY"), ts),
            "username": zscaler._require(env, "ZIA_USERNAME"),
            "password": zscaler._require(env, "ZIA_PASSWORD"),
            "timestamp": ts,
        }).encode()
        status, raw = opener(
            "POST", url, {"Content-Type": "application/json"}, payload
        )
        if status != 200:
            raise SystemExit("ZIA session auth failed: HTTP %d" % status)
        return None
    raise SystemExit("unknown auth mode %r" % auth_mode)
