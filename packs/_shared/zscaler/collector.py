"""Shared Zscaler OneAPI collector helpers.

Stdlib-only, Python 3.6-floor.
"""
import json
import re
from urllib.parse import urlencode
from urllib.parse import urlsplit
from urllib.parse import urlunsplit


# The OAuth audience is NOT a dialable host - api.zscaler.com serves no
# valid cert and exists only as the token-request audience value. The real
# OneAPI gateway is api.zsapi.net (api.<cloud>.zsapi.net off production).
_ONEAPI_AUDIENCE = "https://api.zscaler.com"
_DNS_LABEL_RE = re.compile(r"^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$")


def _validate_label(value, label, allow_placeholder=False):
    text = (value or "").strip().lower()
    if allow_placeholder and text == "<vanity>":
        return text
    if not text or not _DNS_LABEL_RE.match(text):
        raise SystemExit(
            "%s must be a DNS label (letters, digits, hyphen; no dots, "
            "slashes, or empty labels)" % label)
    return text


def _cloud_label(cloud, label="ZSCALER_CLOUD"):
    text = (cloud or "").strip().lower()
    if text in ("", "production"):
        return ""
    return _validate_label(text, label)


def _normalize_https_base_url(name, value):
    if not value:
        return ""
    parts = urlsplit(value)
    if parts.scheme.lower() != "https" or not parts.netloc:
        raise SystemExit("%s must be an https:// host URL" % name)
    if parts.username or parts.password:
        raise SystemExit("%s must not contain username or password" % name)
    if parts.path not in ("", "/") or parts.query or parts.fragment:
        raise SystemExit("%s must not contain path, query, or fragment" % name)
    host = (parts.hostname or "").lower()
    for segment in host.split("."):
        _validate_label(segment, "%s host segment" % name)
    netloc = host
    try:
        port = parts.port
    except ValueError:
        raise SystemExit("%s must contain a valid port" % name)
    if port is not None:
        netloc += ":%d" % port
    return urlunsplit(("https", netloc, "", "", ""))


def _require(env, name):
    value = env.get(name)
    if not value:
        raise SystemExit("missing required env var %s" % name)
    return value


def _token_field(raw, key, label):
    """Extract a token field from an auth response body, LOUDLY."""
    try:
        doc = json.loads(raw.decode())
    except ValueError:
        raise SystemExit(
            "%s: HTTP 200 but the response is not JSON (maintenance page? "
            "proxy interception?) — re-try, then check the auth endpoint "
            "with make fetch-diag" % label)
    if not isinstance(doc, dict) or key not in doc:
        raise SystemExit(
            "%s: HTTP 200 but no %r in the response — check the API "
            "client's permissions/credentials for this product" % (label, key))
    return doc[key]


def _oneapi_gateway(cloud):
    norm = _cloud_label(cloud)
    if not norm:
        return "https://api.zsapi.net"
    return "https://api.%s.zsapi.net" % norm


def _gateway_for(ctx):
    """OneAPI gateway base, derived from the cloud."""
    return _oneapi_gateway(ctx.get("cloud", ""))


def _zslogin_host(vanity, cloud):
    """OneAPI token host. Production (empty/PRODUCTION cloud) has no suffix;
    other clouds lowercase into the host, per the SDK."""
    vanity = _validate_label(vanity, "ZSCALER_VANITY_DOMAIN",
                             allow_placeholder=True)
    suffix = _cloud_label(cloud)
    return "https://%s.zslogin%s.net" % (vanity, suffix)


def host_overrides(env):
    """The host-override ctx keys resolved from the environment (empty value
    == derive from the cloud).
    """
    return {
        "zpa_cloud": env.get("ZPA_CLOUD", ""),
        "zia_legacy_base": _normalize_https_base_url(
            "ZIA_LEGACY_BASE_URL", env.get("ZIA_LEGACY_BASE_URL", "")),
        "zpa_legacy_base": _normalize_https_base_url(
            "ZPA_LEGACY_BASE_URL", env.get("ZPA_LEGACY_BASE_URL", "")),
    }


def compose_url(auth_mode, product, path, ctx):
    """Compose OneAPI product URL branches."""
    if auth_mode != "oneapi":
        raise ValueError("unknown auth_mode/product: %r/%r" % (auth_mode, product))
    gateway = _gateway_for(ctx)
    if product == "zia":
        return "%s/zia/api/v1/%s" % (gateway, path)
    if product == "zpa":
        return "%s/zpa/mgmtconfig/v1/admin/customers/%s/%s" % (
            gateway, ctx["customer_id"], path
        )
    if product == "zcc":
        return "%s/%s" % (gateway, path)
    if product == "ztc":
        if path.startswith("/"):
            return "%s%s" % (gateway, path)
        return "%s/%s" % (gateway, path)
    raise ValueError("unknown auth_mode/product: %r/%r" % (auth_mode, product))


def build_headers(token):
    """Bearer header for OneAPI."""
    return {"Authorization": "Bearer " + token, "Accept": "application/json"}


def acquire(auth_mode, env, ctx, opener, now_ms=None):
    """Acquire a shared OneAPI bearer token."""
    if auth_mode != "oneapi":
        raise SystemExit("unknown auth mode %r" % auth_mode)
    # The token host derives from the vanity + cloud, matching the gateway
    # the data calls use.
    token_url = _zslogin_host(
        _require(env, "ZSCALER_VANITY_DOMAIN"), env.get("ZSCALER_CLOUD", "")
    ) + "/oauth2/v1/token"
    body = urlencode({
        "grant_type": "client_credentials",
        "client_id": _require(env, "ZSCALER_CLIENT_ID"),
        "client_secret": _require(env, "ZSCALER_CLIENT_SECRET"),
        "audience": _ONEAPI_AUDIENCE,
    }).encode()
    status, raw = opener(
        "POST", token_url,
        {"Content-Type": "application/x-www-form-urlencoded"}, body,
    )
    if status != 200:
        raise SystemExit("OneAPI token request failed: HTTP %d" % status)
    return _token_field(raw, "access_token", "OneAPI token")
