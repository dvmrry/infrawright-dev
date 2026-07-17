# Adversarial review handoff: Python reconcile/OpenAPI archive

## Scope

This slice archives the retired Python schema/API reconciler and OpenAPI
resource mapper after preserving their exact authority in versioned fixtures.
It changes no production Node behavior or public artifact schema.

Base: `bfaf46159f7209fdc58dbc4b85d820442aacaad4`

Authority commit: `538d34c80d2d3503e0e76d758e34009c62e3bf6b`

## Deleted Python

- `engine/reconcile_schema_api.py`
- `engine/openapi_resource_map.py`
- `tests/test_reconcile_schema_api.py`
- `tests/test_openapi_resource_map.py`

The four files contain exactly 4,499 lines. The Node library and CLI were
already the production paths.

## Frozen evidence

- Reconcile fixture: 45,068 bytes, SHA-256
  `e44663ac77b8bc7be8b2af65f2bf39e7f6dbca12b7d79805b9fa133e99f7c9ff`
- OpenAPI fixture: 771,787 bytes, SHA-256
  `fc730c4adda0fb599f37d712adc75c1b9132350a5e714511e3f2c6e81581bd8a`
- Generator: 28,883 bytes, SHA-256
  `28b9235d1e8e9164a9c4d41fba1721779da30d96ebac27e966a173067c78fe90`

The generator pins baseline `bfaf461…`, CPython 3.13.13, UCD 15.1, and 41
exact source inputs. The locks include every material default-registry file,
pack manifest, and loader/validator source. The fixture-recorded producing
command was executed verbatim from a clean clone and reproduced both files
byte-for-byte.

## Preserved corpus

- Reconcile: nine unittest runs, seven complete reports, five helper results,
  one retained CLI case, two former live differential reports, and one former
  live CLI comparison.
- OpenAPI: thirteen complete unittest reports, one retained CLI case, six
  former live differential reports, and one former live CLI comparison.
- Valid former live CLI evidence includes exact status, stdout, and stderr.
- One retired OpenAPI unittest CLI input lacks required `info`; its Python
  output remains frozen, while current Node intentionally rejects it at the
  stricter Swagger validation boundary before producing a report.
- Current Node tests replay all recorded inputs; no Node output is stored as
  Python authority and parsed-JSON equality does not replace byte contracts.

## Initial review and remediation

The initial fresh review requested changes for two gaps:

1. Material default-registry inputs were not source-locked.
2. The recorded producing command could not run from a clean checkout because
   the generator did not exist at the retired baseline.

The remediation added 15 material locks, a registry-mutation rejection test,
and an exact detached-worktree resurrection command. Patch-focused re-review
approved both corrections.

## Review focus for the deletion patch

- Every frozen case is actually replayed by Node after the Python files are
  deleted.
- Report objects and helper values remain complete and exact; valid CLI cases
  retain exact status/stdout/stderr, and the historical invalid OpenAPI CLI
  case retains its explicit validation-divergence test.
- Reduced profiles select the converted tests whose replay inputs are
  self-contained. The OpenAPI replay is selected only when all four Zscaler
  registries recorded by its frozen authority are physically present.
- Historical source hashes in earlier frozen authorities remain unchanged.
- Vendor allowances exist only for files that remain.
- No new runtime, provider, pack, Terraform, credential, or network behavior
  appears.

## Pruned-checkout CI remediation

The first exact-head run exposed that
`authoring-openapi-resource-map.test.js` was still selected in the physically
pruned empty checkout. Its frozen full-pack reports include registry evidence
from ZCC, ZIA, ZPA, and ZTC, so replaying them without those recorded inputs
correctly produced a different report. The test now declares those four packs
and the shared Zscaler metadata as requirements. The suite selector excludes
it with `missing-pack-requirements` in reduced checkouts and still selects it
in the complete checkout. Neither production code nor frozen authority bytes
changed.

The rerun then exposed the same physical-input rule in the retained Python
generator mutation test: it deliberately copies `packs/zia/registry.json`, so
an empty checkout cannot execute it. That single test now declares ZIA and the
shared Zscaler metadata in the legacy test selector. The empty checkout omits
it while the ZIA and complete profiles still execute the mutation guard.
