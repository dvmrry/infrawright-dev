# Transform/Adopt Parity Diagnostic

The transform/adopt parity diagnostic compares the two maintained ways this
repository can render configuration for the same logical provider object:

```text
sanitized raw API item -> transform overrides -> canonical JSON tfvars
same import identity -> post-Read provider state -> oracle projection -> canonical JSON tfvars
```

A clean first oracle plan is not parity evidence. Oracle projection mirrors
post-Read state, so a wrong-but-self-consistent provider representation can
plan clean. Conversely, a transform/adopt byte difference can be harmless,
validation-sensitive, or semantically important. This diagnostic exposes the
difference without automatically copying policy between the two paths.

## Run The Committed Fixtures

```sh
iw transform-adopt-parity tests/fixtures/parity/*.json \
  > transform-adopt-parity.json
```

The command writes deterministic JSON to stdout. Exit status is:

- `0` when all fixtures are equal or every difference is explicitly accepted;
- `1` when an evidence gate, unclassified difference, stale classification, or
  unacknowledged transform drop remains;
- `2` for an invalid fixture or invocation.

This is a versioned maintainer diagnostic format, not one of the stable
downstream contracts in `docs/schemas/`. Consumers must not build delivery or
drift orchestration against it.

The committed baseline intentionally exits `1`: its remaining DLP name
difference is an evidence gate. Unit tests pin the exact baseline and fail if a
new difference appears, an expected value changes, a classification becomes
stale, or a transform drop is introduced.

## Fixture Contract

Each `tests/fixtures/parity/*.json` file contains:

- `fixture_version` and a stable, path-independent `name`;
- the exact generated `resource_type`;
- explicit provenance, including the pinned provider version, provider-source
  links, any separately pinned SDK dependency sources, local pack/schema
  inputs, and whether the data is sanitized;
- sanitized `raw_items`;
- `provider_state` keyed by the import IDs derived through the real adoption
  metadata; and
- `expected_differences` using RFC 6901 JSON pointers.

Fixture validation requires the provider version to equal the active pack pin,
requires provider and dependency sources to use exact GitHub blob refs,
requires every local source to exist inside the repository, and requires every
classification evidence reference to be declared by fixture provenance.

The diagnostic calls the production transform path for the raw side and
the production Adopt path with an injected fixture state loader for the oracle
side.
The injected loader replaces only the network/provider import step; identity,
skip behavior, pack drift policy, state projection, and raw-item projection
fills remain the production code paths.

An expected difference is bound to all three of:

- its exact JSON pointer;
- its exact transform presence/value; and
- its exact adopt presence/value.

Changing either value makes the old expectation stale and the actual
difference unclassified. A broad label therefore cannot conceal a future
provider, pack, or projection change at the same path.

Classifications describe the mechanism (`semantic_mismatch`,
`validation_asymmetry`, `representational_difference`,
`provider_normalization`, or `other`). Disposition is separate:

- `evidence_gate` remains fail-closed until its named evidence or design
  decision exists;
- `accepted` records a reviewed difference that is intentional and does not
  make the diagnostic fail.

Every classification requires a reason and evidence references.

## Initial Zscaler Baseline

| Fixture | Result | What it proves or exposes |
|---|---|---|
| `zcc_failopen_policy_inversion` | byte-equal | The five inverted booleans and the non-inverted strict-enforcement boolean agree across the two local paths for the source-derived values. The three string-backed API flags use the ZCC provider's pinned SDK model types. |
| `zia_dlp_engines_predefined_name` | evidence gate | Transform promotes `predefined_engine_name`; provider Read stores `resp.Name`, producing a wrong-or-right semantic choice that a first clean oracle plan cannot settle. |
| `zia_url_filtering_rules_zero_quota` | byte-equal | Provider Read stores zero quotas, and Adopt now applies the same pack `drop_if_default` omissions as Transform. The same fixture confirms empty URL categories normalize to `["ANY"]` on both paths. |

These are minimal source-derived fixtures, not retained tenant observations.
They prove the behavior of the pinned local code against the authored provider
state shape. They do not replace a provider-framework test or controlled live
run where the remaining DLP gate calls for one, nor do equal artifacts alone
prove later-plan stability.

## Safety And Scope

Committed fixtures must be sanitized; validation rejects
`provenance.sanitized=false`. Reports include differing configuration values,
so do not feed private Terraform state, raw tenant exports, secrets, or
identifiers into this first-slice command.

This slice compares canonical JSON tfvars payloads only. It does not compare
HCL formatting, imports, moved blocks, lookup sidecars, generated expression
bindings, plan results, apply/refresh behavior, or later-plan stability. Those
are separate extensions and must not be inferred from an equal result here.
Scalar comparison uses canonical JSON encoding rather than JavaScript numeric
equality, so representations such as `-0.0` and `0.0` cannot report equal. A
second completeness check reconstructs the adopt payload from the transform
payload plus every reported difference and requires exact rendered-byte
equality. A total or partial comparator miss is therefore fail-closed as an
unaccounted render difference.

## Adding A Case

1. Start with one smallest sanitized logical object.
2. Pin the provider version and source lines that justify the post-Read state
   shape. Use `source_derived` unless a retained sanitized live record exists.
3. Include every provider-state import ID derived from the raw survivor set and
   no extras.
4. Run the diagnostic with no expectation to observe the exact difference.
5. Add an exact classification with evidence and an honest disposition.
6. Run the focused parity tests and the full repository gate.

Do not change transform or projection behavior merely to make the report
equal. A behavior fix requires its own provider-backed decision, regression
test, and adversarial review.
