# ZPA v4.4.6 endpoint-evidence fixture

This fixture is the independent, source-first qualification corpus for the 16
selected ZPA resources. It is intentionally separate from
`docs/evidence/zpa-provider-v4.4.6.json`: that older matrix contains reviewed
import grammar, identity, state-shape, sensitivity, and exception claims that
this AST corpus neither derives nor promotes.

`source-provenance-v1.json` pins the following local-only inputs:

- `zscaler/terraform-provider-zpa` tag `v4.4.6`, commit
  `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`;
- the provider entry point and every tracked, non-test, top-level `zpa/*.go`
  file, which closes same-package registration, Read, flattening, and helper
  references without pulling test files or subpackages into the authority;
- `zscaler-sdk-go/v3` module version `v3.8.40`, pinned by every analyzed file
  hash and the subset tree digest because the normal module cache has no Git
  metadata;
- the selected SDK service files, shared service/client edge,
  `zparequests.go`, and the generic paging helper source; and
- the checked-in ZPA provider schema used by the selected pack.

The bound SDK subset deliberately stops at Zscaler's `NewRequestDo` wrapper.
A1 certifies raw HTTP only when a statically reached declaration contains a
direct exact `net/http.NewRequest` or `net/http.NewRequestWithContext` sink.
`zscaler/oneapiconfig.go`, where the lower-level transport continues, is not in
this bounded A1 fixture. Consequently, reaching a pinned SDK symbol here is not
an HTTP endpoint claim; no path or method may be inferred from the wrapper's
name or signature.

The expected report is hand-authored from the registration, Read callback, and
SDK declaration/call-site anchors in these exact source bytes. It is not copied
from analyzer output. Where a pinned SDK `Get` body directly calls
`(*zscaler.Client).NewRequestDo`, the report retains that factual terminal call
step while still recording `endpoint_not_recovered`; the wrapper is not
promoted to raw HTTP. The policy-access Read has both a same-package prerequisite
policy-set lookup and the object lookup; both remain visible because
`zpa/common.go` is bound, so the fixture must fail closed if both are viable.

The optional external-source test reads only the two explicit environment
variables below. It never clones, fetches, downloads, contacts a provider, or
uses credentials:

```sh
ZPA_PROVIDER_SOURCE=/absolute/path/to/terraform-provider-zpa \
ZPA_SDK_SOURCE=/absolute/path/to/zscaler-sdk-go-v3.8.40 \
go test ./internal/authoring/zpacorpus -run EndpointFixture
```

Both roots must already match the manifest. Leaving both variables unset runs
the ordinary committed fixture checks and skips only the external binding and
analyzer qualification leg.
