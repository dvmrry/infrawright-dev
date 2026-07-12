# ZCC Adoption Oracle Parity Contract v1

This document is normative for
`infrawright.zcc_adoption_oracle_parity` schema version `1`. The JSON Schema
closes the report shape; this document freezes the value encoding, commitment
framing, digest, trust boundary, and qualification rules that JSON Schema
cannot express.

## Trust and authority

The report is produced and consumed inside one trusted process or through an
authenticated CI artifact channel. The semantic validator and the plain
report digest detect malformed structure and accidental corruption. They do
not authenticate the evidence, its producer, or the pipeline that transported
it. Version 1 defines no report-signing or commitment-key distribution system.

HMAC commitments keep tenant-derived values and low-entropy artifact contents
out of the report while still permitting equality comparisons within one run.
The HMAC key is not report authentication: it is not published to report
consumers, and the report's unkeyed digest can be recomputed by anyone who can
replace the report.

The current builder is a private in-process seam. Any future public operation
MUST derive `evidence_class` and the three public build hashes from the
execution it actually controls. It MUST generate a fresh random 32-byte key
internally for each report and MUST NOT accept the key, evidence class, or
build bindings as caller assertions across the public request boundary.

## Build-binding bytes

The three build fields are hashes of immutable runner artifacts, not Git refs,
image tags, paths, version strings, or caller-authored metadata:

- `node_sha256` is SHA-256 of the exact `dist/infrawright.mjs` file bytes used
  to execute the Node side of the comparison.
- `python_before_sha256` and `python_after_sha256` are SHA-256 of the exact same
  immutable Python runner archive bytes mounted for the corresponding pass.
  That archive MUST contain the Python executable and standard library, every
  Infrawright Python module, each pack/catalog input, and every locked runtime
  dependency used by the oracle. A mutable checkout or an archive assembled
  differently for the two passes is not eligible for
  `live_independent_executor` evidence.

The future host owns creation and retention of the Python runner archive and
MUST bind each digest before starting its executor. Version 1 does not define a
portable archive-construction format; byte-identical archives are the required
identity within one report. This intentionally makes live qualification
unavailable until the protected host can supply immutable runner artifacts.

## Input cardinality and aggregate disclosure

For each snapshot, `survivors` and `observations` MUST each be an array or a
JSON record so its cardinality can be counted; array cardinality is array
length and record cardinality is enumerable own-key count. Their cardinalities
MUST be equal. A mismatch is invalid input and MUST abort report construction;
it is never converted to empty coverage.

An equal cardinality of zero is absent input and an equal positive cardinality
is present input. Version 1 reports no per-resource presence. For live evidence
it discloses only one aggregate bit as `summary.live_input_coverage`:

- `complete` means every Python-before, Node, and, when required,
  Python-after snapshot for all five resources had equal positive cardinality;
- `incomplete` means at least one otherwise valid snapshot had equal zero
  cardinality; and
- `not_applicable` is required for simulation evidence.

This aggregate bit is the minimum presence disclosure required to derive live
qualification without revealing which resource was empty. Semantic validation
can check that qualification follows the aggregate claim, but, without tenant
inputs or the commitment key, cannot independently prove that the trusted
producer derived the bit honestly.

## Canonical value encoding

The `value` media payload is the UTF-8 encoding of the following compact
canonical JSON value. Inputs MUST first pass the inert, acyclic, well-formed
JSON graph snapshot and its 128-container depth limit.

- `null`, `true`, and `false` use those exact ASCII tokens.
- Strings use ECMAScript `JSON.stringify` string encoding. Input Unicode MUST
  be well formed. Non-ASCII characters are not normalized; canonically
  equivalent Unicode strings with different code points remain different.
- Array order is retained. Elements are comma-separated with no whitespace.
- Object keys are sorted by Unicode code point, recursively. Keys and values
  use the same string/value encoding. Members are comma-separated with no
  whitespace.
- A native JavaScript number MUST be finite. Native negative zero is encoded
  as the exact token `-0`, while native positive zero is `0`; signed zero is
  intentionally distinct. Every other native number uses ECMAScript
  `JSON.stringify` number serialization.
- A `LosslessNumber` MUST contain a valid JSON number token. Its token is
  emitted verbatim: `1`, `1.0`, `1e0`, `-0`, and `-0.0` are distinct lexical
  values except where they are literally the same token.

Python JSON entering the parity builder MUST remain losslessly parsed so its
numeric spelling is represented by `LosslessNumber`. This preserves Python's
emitted float spellings, including `-0.0`, rather than silently converting
them through a native JavaScript number. Native Node values follow the native
rules above. Consequently native `-0` and `LosslessNumber("-0")` have the same
payload, but native `0`, `LosslessNumber("-0.0")`, and
`LosslessNumber("1e0")` have different payloads.

The `bytes` media payload is the exact supplied byte sequence. It is not
decoded, normalized, or JSON-parsed. Identity, observation, and projection use
`value`; tfvars, imports, and applicable lookup artifacts use `bytes`.

## Commitment framing

Every applicable comparison commitment is:

```text
HMAC-SHA256(
  key,
  UTF8("infrawright.zcc-adoption-oracle-parity") || 0x00 ||
  UTF8("v1")                                      || 0x00 ||
  UTF8(evidence_class)                            || 0x00 ||
  UTF8(resource_type)                             || 0x00 ||
  UTF8(role)                                      || 0x00 ||
  UTF8(media)                                     || 0x00 ||
  payload
)
```

All framed selectors are closed enums in the implementation and schema, so a
NUL byte cannot occur inside them. The terminal NUL separates the media token
from an arbitrary byte payload. The output is 64 lowercase hexadecimal
characters.

## Report digest and semantic validation

`report_sha256` is SHA-256 over the UTF-8 canonical value encoding of the
complete report body with `report_sha256` omitted. It is an unkeyed integrity
checksum, not a signature. The semantic validator additionally re-derives the
closed five-resource order, lookup applicability, comparison and resource
statuses, role counts, Python build stability, aggregate status, and
qualification.

Projection qualification requires live evidence, complete aggregate input
coverage, no mismatch or unstable role, and a stable Python reference when an
after pass is present. Executor qualification additionally requires
`live_independent_executor`. This report never qualifies downstream cutover.

## Fixed known-answer vectors

All vectors use this 32-byte key:

```text
000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
```

They use evidence class `simulation` and resource type
`zcc_device_cleanup`. The table freezes both canonical payloads and the full
domain framing:

| Role / media | Input | Canonical or exact payload | HMAC-SHA256 |
| --- | --- | --- | --- |
| `identity` / `value` | `[ {"😀":"𝄞","é":"café","a":"雪"} ]` in any object insertion order | `[{"a":"雪","é":"café","😀":"𝄞"}]` | `4448b82d8ee04a1e8f934508f1960a96b88e10adaaabe7eb8739a8b08dfd6dbb` |
| `observation` / `value` | `[LosslessNumber("1e0")]` | `[1e0]` | `78560b006d6c28056b481b7ae93707714909f53f9665c9c54afb96ab0a6ae64e` |
| `projection` / `value` | native `-0` | `-0` | `2cf99c544cd1ff72ec5ea32c3ab1f9758cd3af1c5140788a80cce720e8e5b488` |
| `projection` / `value` | native `0` | `0` | `ed30c84bf34528f3e4ea9f8c60ecb20855e6ba51aa0bfe57c5c38335f81bc833` |
| `projection` / `value` | `LosslessNumber("-0")` | `-0` | `2cf99c544cd1ff72ec5ea32c3ab1f9758cd3af1c5140788a80cce720e8e5b488` |
| `projection` / `value` | `LosslessNumber("-0.0")` | `-0.0` | `60e53b99e550cc23eadf8a1093f6dc6e4dcddedb127e863b60ba9005542dbecb` |
| `projection` / `value` | `LosslessNumber("1e0")` | `1e0` | `154965d19783f0df39bbf0cc37061bb21a7b040d41db39da6b6d08a330ddcf0b` |
| `tfvars` / `bytes` | UTF-8 bytes of the identity canonical payload above | `[{"a":"雪","é":"café","😀":"𝄞"}]` | `b05cf553487ebdede9f7e71cf5d32ddf756dd80e88831207e03ec495d1c92b6d` |

The different identity and tfvars commitments for identical payload bytes pin
the role and `value`/`bytes` media separation.

The report-digest vector uses the equal variants from the table: Unicode
identity value, `LosslessNumber("1e0")` observation, native `-0` projection,
the shown tfvars bytes, imports bytes `["1"]`, and no lookup for
`zcc_device_cleanup`. For each of the other four resources, survivors,
observations, and projection are the value `["1"]`; tfvars and imports are the
UTF-8 bytes `["1"]`; only `zcc_trusted_network` has lookup bytes `["1"]`.
Python-before and Node are equal, every Python-after value is null, the Python
before build is 64 `a` characters, the Node build is 64 `b` characters, and
the versioned catalog bindings come from the committed ZCC catalog. The
resulting report digest is:

```text
3e9d030b18bb9813b3a450dff23ec2adce976580ee3799f3952d90fccf6c0782
```
