"""ZPA collector URL/auth seams.

Stdlib-only, Python 3.6-floor.
"""
from urllib.parse import urlencode

from packs._shared.zscaler import collector as zscaler


# ZPA legacy config base per ZPA_CLOUD (zscaler-sdk-go). The Terraform
# provider derives the SAME base from ZPA_CLOUD via the SDK, so deriving it
# here keeps fetch and provider on the same host; hardcoding the production
# base ignored ZPA_CLOUD and broke every non-production tenant (the ZS2 ZPA
# 401). PRODUCTION (and empty) and ZPATWO are confirmed; BETA/GOV/GOVUS use
# Zscaler's documented public hosts - confirm against the SDK constants and
# pin with ZPA_LEGACY_BASE_URL if any is wrong for your tenant.
_ZPA_LEGACY_BASES = {
    "": "https://config.private.zscaler.com",
    "PRODUCTION": "https://config.private.zscaler.com",
    "ZPATWO": "https://config.zpatwo.net",
    "BETA": "https://config.zpabeta.net",
    "GOV": "https://config.zpagov.net",
    "GOVUS": "https://config.zpagov.us",
}


def _zpa_legacy_base_or_none(cloud):
    """Mapped ZPA legacy base for `cloud`, or None if the cloud is unlisted
    (callers decide whether that is fatal - fetch fails, --diag shows a
    placeholder)."""
    return _ZPA_LEGACY_BASES.get((cloud or "").strip().upper())


def _legacy_zpa_base(cloud):
    """ZPA legacy config base for ZPA_CLOUD; raises if the cloud is unlisted.

    For a private/unlisted cloud, set ZPA_LEGACY_BASE_URL to override.
    """
    base = _zpa_legacy_base_or_none(cloud)
    if base is None:
        raise SystemExit(
            "unknown ZPA_CLOUD %r for the legacy config base — set "
            "ZPA_LEGACY_BASE_URL to the correct https://config.<cloud> host "
            "(known clouds: %s)"
            % (cloud, ", ".join(k for k in _ZPA_LEGACY_BASES if k)))
    return base


def _zpa_legacy_base_for(ctx):
    """Legacy ZPA base: ZPA_LEGACY_BASE_URL override wins over derivation."""
    return ctx.get("zpa_legacy_base") or _legacy_zpa_base(ctx.get("zpa_cloud", ""))


def compose_url(auth_mode, path, ctx):
    if auth_mode == "oneapi":
        return zscaler.compose_url(auth_mode, "zpa", path, ctx)
    if auth_mode == "legacy":
        return "%s/mgmtconfig/v1/admin/customers/%s/%s" % (
            _zpa_legacy_base_for(ctx), ctx["customer_id"], path
        )
    raise ValueError("unknown auth_mode/product: %r/%r" % (auth_mode, "zpa"))


def acquire(auth_mode, env, ctx, opener, now_ms=None):
    if auth_mode == "oneapi":
        return zscaler.acquire(auth_mode, env, ctx, opener, now_ms=now_ms)
    if auth_mode == "legacy":
        # signin must hit the same config base as the data calls (ctx
        # override wins over ZPA_CLOUD derivation).
        url = "%s/signin" % _zpa_legacy_base_for(ctx)
        body = urlencode({
            "client_id": zscaler._require(env, "ZPA_CLIENT_ID"),
            "client_secret": zscaler._require(env, "ZPA_CLIENT_SECRET"),
        }).encode()
        status, raw = opener(
            "POST", url,
            {"Content-Type": "application/x-www-form-urlencoded"}, body,
        )
        if status != 200:
            raise SystemExit("ZPA signin failed: HTTP %d" % status)
        return zscaler._token_field(raw, "access_token", "ZPA signin")
    raise SystemExit("unknown auth mode %r" % auth_mode)
