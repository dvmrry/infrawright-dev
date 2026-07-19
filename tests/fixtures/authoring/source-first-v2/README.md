# Source-first v2 analyzer inputs

This directory is a synthetic, offline-only input corpus for the source-first
provider-to-SDK analyzer. `provider/` and `sdk/` are independent Go modules;
the provider uses a local `replace` directive, so both build with
`GOWORK=off` and no network access.

The fixture deliberately selects eight Terraform resource names:

| Resource | Intended review case |
|---|---|
| `sourcefirst_direct_http` | Read-rooted provider helper chain ending in a direct raw request. |
| `sourcefirst_sdk_http` | Read-rooted SDK package function and receiver-method chain ending in a raw request; the chain contains a terminating helper cycle. |
| `sourcefirst_sdk_symbol` | Read reaches a pinned SDK symbol whose transport is intentionally opaque. |
| `sourcefirst_ambiguous` | Read reaches same-named `Get` methods in two distinct SDK packages and therefore exposes two endpoint candidates. |
| `sourcefirst_dynamic` | Read issues a request whose path is produced by an unsupported runtime callback. |
| `sourcefirst_unresolved` | Read stops at an interface call; a raw HTTP call exists only on the Create path as a decoy. |
| `sourcefirst_no_source` | Selected in the provider schema but intentionally has no provider registration/source. |
| `sourcefirst_not_applicable` | Selected in the provider schema but reserved for an independently reviewed not-applicable reason. |

These labels state fixture intent only. They are not analyzer output and do not
constitute expected evidence. Expected classifications, provenance manifests,
and hashes belong in separately authored and independently reviewed files.

The source shapes also cover a top-level SDK package function, SDK receiver
methods, same-named methods from distinct packages, direct provider helpers,
and an actual bounded runtime cycle. The unresolved resource's Create-only
request must never support a Read claim merely because it shares a source file.

Offline compilation checks:

```sh
(cd sdk && GOWORK=off GOPROXY=off go test ./...)
(cd provider && GOWORK=off GOPROXY=off go test ./...)
```
