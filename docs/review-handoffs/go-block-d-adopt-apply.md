# Block D dependency plan — Adopt/import/oracle/apply (not authorized)

Status: dependency-plan correction only, recorded 2026-07-18 from
`feature/go-canonjson-foundation` at `9fc3f30`. This document does **not**
authorize Block D, add a dependency, or implement any Adopt/import/oracle/apply
behavior.

Block D remains closed until the §5 live Node → Go → Node read-only comparison
passes, its evidence receives the required review, and the user makes a
separate explicit Block D authorization decision. Those governance gates are
unchanged.

## 1. Corrected dependency rule

Use libraries for consuming and orchestrating; hand-roll only renderers whose
bytes are committed infrastructure artifacts. In Block D that means:

| Surface | Dependency posture | Boundary |
|---|---|---|
| Terraform scratch flow | `github.com/hashicorp/terraform-exec` | Run the oracle's `init` → `import` → `plan` → `apply` → `show` flow against the project-generated provider configuration. Preserve Infrawright's bounded execution, environment, redaction, and fail-closed decisions around the library. |
| Terraform plan/state decode | `github.com/hashicorp/terraform-json` | Decode plan/state into typed structures. Project validation layers on top; typed decoding is not authorization. |
| Schema validation | `github.com/santhosh-tekuri/jsonschema/v6` | Preferred for validation added or revisited in future work, behind deterministic project error mapping. Do not redo Block C's accepted hand-port merely to adopt it. |
| Provider access | `github.com/zscaler/zscaler-sdk-go/v3` | Use for provider authentication and transport. Evidence readback remains the raw provider response; do not route evidence through SDK model normalization or reserialization. |
| Canonical JSON artifacts | Existing `internal/canonjson` | Keep the byte-exact hand renderer. Generated JSON must continue to match committed state and goldens. |
| Import/config/moved HCL artifacts | Existing `internal/tfrender` | Keep the byte-exact hand renderer. Do not adopt `hclwrite`; its bytes differ from the committed goldens. |

### Complete-field gate layers on top

The oracle scratch flow is built on `terraform-exec` plus `terraform-json`.
`terraform-json` supplies typed plan/state structures; it does not make the
Infrawright safety decision. The Node contract at
`node-src/domain/plan-contract.ts:463` accepts only literal
`complete === true`. The Go adapter must enforce the equivalent fail-closed
gate **after** typed decode and before classification or Apply. Missing, nil,
false, or otherwise unusable completeness never passes. A decoding library not
enforcing that product gate is not a reason to reject the library.

Current `terraform-json v0.28.0` exposes `Plan.Complete` as `*bool`; the
required project check is still explicit: the pointer must be non-nil and the
value must be true. The surrounding existing contract checks remain in force,
including errored/deferred/non-applyable and non-import-effect refusal.

## 2. Block D behavior carried forward

- Generated import, configuration, and moved artifacts are rendered only by
  the existing `canonjson`/`tfrender` byte-exact paths and remain covered by
  differential artifact gates.
- Provider evidence is captured from raw readback bytes. The Zscaler SDK may
  establish authentication and transport, but its resource structs must not
  normalize, omit, reorder, or re-encode evidence.
- The pack/user adoption-policy merge deferred from PR 247 lands in Block D.
  Its prerequisite `isSupportedDriftPolicyVersion` building block is already
  ported; Block D must preserve the Node merge/version behavior rather than
  inventing a second validator.

## 3. Dependency version revalidation — 2026-07-18

This process has Go `1.26.3`, but no usable enterprise Artifactory/JFrog
client, credentials, repository URL, or Artifactory-backed `GOPROXY` exposed to
it. Its configured proxy is the public Go proxy followed by `direct`.
Therefore the checks below prove current upstream availability and
module/toolchain compatibility only; **none is a current Artifactory
availability claim**. The user's earlier Artifactory validation is retained as
historical context, but it did not establish that today's exact candidates and
their transitives are still mirrored.

Commands used, without changing `go.mod` or `go.sum`:

```sh
GOWORK=off GOPROXY=https://proxy.golang.org \
  go list -m -versions -json <module>@latest
GOWORK=off GOPROXY=https://proxy.golang.org \
  go mod download -json <module>@<version>
```

| Library | Exact upstream candidate | Upstream result | Declared Go version | Enterprise Artifactory status |
|---|---:|---|---:|---|
| `github.com/hashicorp/terraform-exec` | `v0.25.2` | **Verified**: listed and downloaded; `h1:fFLAVEtAjKdGfawGUXDnKooCnqJi+TuohT3W99AGbhk=` | `1.25.0` (`toolchain go1.25.8`) | Earlier availability reported; this exact version/transitives **UNVERIFIED now** |
| `github.com/hashicorp/terraform-json` | `v0.28.0` | **Verified**: listed and downloaded; `h1:dOkJT55rWfU6T1/VklHde51ym4LfNP+9xYR3ZizAJe4=` | `1.21` | Earlier availability reported; this exact version/transitives **UNVERIFIED now** |
| `github.com/santhosh-tekuri/jsonschema/v6` | `v6.0.2` | **Verified**: listed and downloaded; `h1:KRzFb2m7YtdldCEkzs6KqmJw4nqEVZGK7IN2kJkjTuQ=` | `1.21` | Earlier availability reported; this exact version/transitives **UNVERIFIED now** |
| `github.com/zscaler/zscaler-sdk-go/v3` | `v3.8.40` | **Verified**: listed and downloaded; `h1:0ca+Hm0VRT8sG8WOTQrG6XAcmOI/uCnHKr4H7GBqREw=` | `1.25.8` | Earlier availability reported; this exact version/transitives **UNVERIFIED now** |

All four declared Go requirements are compatible with this repository's Go
1.26.3 toolchain. That is not an integration test: no dependency was added and
no Block D code was compiled. Before choosing pins in an authorized Block D
parcel, rerun the exact-version downloads through the enterprise `GOPROXY` and
record which candidates and transitives the mirror actually serves. If the
mirror lacks a candidate, select a reviewed mirrored version rather than
silently falling back to the public network.

`terraform-exec v0.25.2` currently requires `terraform-json v0.27.2`; selecting
`terraform-json v0.28.0` directly would cause minimal-version selection to use
`v0.28.0`. Compatibility of that pair belongs in the authorized dependency
spike and must be tested rather than assumed.

## 4. Sunk work and one open decision

The existing `internal/terraformcmd` Terraform invocation and
`internal/canonjson` strict decoder are complete, working, and low-ROI to
replace. Do not rewrite either as dependency cleanup. `canonjson` remains the
artifact renderer regardless.

Open decision for an authorized Block D dependency spike: if adopting
`terraform-exec` for the oracle makes a single Terraform invocation path
clearly simpler while preserving Infrawright's bounds, redaction, environment,
timeouts, process cleanup, and error classifications, evaluate unifying it with
`terraformcmd`. Otherwise keep the two paths. This document authorizes neither
choice and no migration now.

## 5. Authorization boundary

- No Go source or generated artifact changes are authorized here.
- Do not add these modules to `go.mod` until Block D is separately authorized.
- The module remains zero-dependency today.
- The §5 live comparison and evidence review remain prerequisites.
- Block D implementation remains a separate explicit user decision.
