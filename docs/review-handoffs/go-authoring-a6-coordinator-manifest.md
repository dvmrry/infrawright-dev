# Go authoring A6 coordinator manifest

Status: implementation contract. This manifest freezes A6 before builder work.
It does not authorize singleton-state degrouping, release packaging, a global
operator cutover, Node deletion, network access, provider credentials, or live
Terraform execution.

## Scope and authority

A6 exposes these six retained authoring commands through the existing `iw`
binary: `reconcile`, `openapi-map`, `source-operation-map`,
`source-evidence-eval`, `provider-probe`, and `transform-adopt-parity`.
Darwin and Linux are the supported platforms. The version-specific
`zpa-provider-evidence` command is removed from active Go help and routing; its
frozen Node behavior and checked generic corpus remain historical evidence.

Frozen Node v1 behavior is the exact authority for overlapping legacy inputs.
Source-first v2 has no Node equivalent and is governed by the accepted typed
contracts and independently reviewed source-bound goldens. A CLI may compose
accepted package APIs but must not reconstruct evidence, readiness counts,
OpenAPI conclusions, provider-probe artifacts, or Transform/Adopt parity from
unsealed maps.

## Compatibility matrix

| Surface | Exact compatibility | Semantic compatibility | Intentional A6 behavior |
|---|---|---|---|
| Shared parsing/help | full help bytes; usage messages; status 2 | none | help lists exactly the retained six |
| `reconcile` | legacy report/stdout/stderr bytes; post-write status 4 | input read order and `INFRAWRIGHT_PACKS || default` | none |
| `openapi-map` | legacy report bytes and destination behavior | strict failure leaves `--out` untouched | none |
| `source-operation-map` legacy | registry, diagnostics, comparison, stdout order, exits | preparation must complete before each corresponding write | none |
| `source-operation-map` v2 | accepted six sealed artifact byte streams | verified/unverified capability separation and OpenAPI isolation | complete-set directory publication |
| `source-evidence-eval` legacy | five artifacts, Markdown, stdout, exits | regression status only after successful publication | complete-set publication may eliminate Node partial-write states |
| `source-evidence-eval` v2 | accepted sealed source-first artifacts | same trust/capability rules as source-operation v2 | complete-set directory publication |
| `provider-probe` legacy | five artifacts, copies, stdout order, status 2 | debug stack precedes concise error; exact stack bytes are platform-sensitive | complete-set publisher owns public artifact replacement |
| `provider-probe` v2 | sealed six-file core and optional map bytes | unavailable/absent OpenAPI cannot suppress source core | stale optional map disappears only on successful whole-set replacement |
| `transform-adopt-parity` | report bytes and exits | `Build` then typed `ResultClassification` only | none |

`INFRAWRIGHT_PERFORMANCE_REPORT` remains the already-recorded Go-runtime
deferral in `go-runtime-plan.md`: current Go dispatch does not create that
Node-compatible telemetry artifact for any command. A6 does not selectively
invent it for authoring commands. Differential vectors scrub the variable;
the eventual shared recorder must separately restore the Node post-command
write/failure behavior across the whole CLI.

## v2 command roots

`--source-manifest` selects qualified source-first mode and
`--allow-unverified-source` selects diagnostic-only source-first mode. They are
mutually exclusive. Either selection requires the command's bundle directory
(`--artifact-dir` for `source-operation-map`, `--out-dir` for
`source-evidence-eval`) and is incompatible with legacy destinations and
legacy source-facts flags.

Qualified mode uses:

- `--source-root DIR` as the provider root;
- `--schema FILE` as the explicit schema file named by the manifest;
- repeatable `--sdk-root MODULE=DIR` values for the manifest's exact SDK set;
- optional `--openapi FILE` as the locally supplied manifest-bound OpenAPI
  root; and
- `--source-manifest FILE` as the canonical provenance manifest.

Legacy mode continues to accept one raw `--sdk-root DIR`. The parser may
collect repeated values, but legacy validation rejects more than one.

Unverified mode uses the same explicit local roots and requires
`--provider-module MODULE`, one or more repeatable `--provider-file RELATIVE`,
and, for every SDK, repeatable `--sdk-root MODULE@VERSION=DIR` and
`--sdk-file MODULE=RELATIVE` values. `--schema FILE` supplies both the schema
root and its selected basename. Lists are sorted before the existing
`LoadUnverified` boundary; duplicates, missing module/version/root/file
bindings, and a provider or SDK file outside its explicit root are usage
errors. These flags are command input, not a qualification manifest; they must
never mint verified trust. No implicit directory walk or module/version guess
is permitted.

No command clones, downloads, scrapes, discovers a public fallback, or invokes
a second authoring executable in source-first mode. The legacy evaluator's
automatic `go run tools/source-evidence-ast` convenience retires at A6: legacy
mode requires an explicit `--source-facts` file, while v2 uses the reusable Go
source analyzer in-process. Existing legacy vectors with supplied facts remain
exact. `--ast-tool-dir` is rejected and is absent from active help/Make routing.

## Publisher contract

The complete-set publisher supports local Darwin/Linux filesystems and
cooperative publishers. The caller grants the invocation exclusive ownership
of the destination directory and the sibling lock, staging, and backup names
for the complete transaction. A non-cooperating same-UID writer, remote/NFS
lock semantics, and power-loss durability are outside the guarantee.

The publisher:

1. validates the fixed relative artifact vocabulary and detached bytes;
2. acquires a sibling lock atomically and never steals an existing lock;
3. writes and validates a private sibling stage directory;
4. renames an existing destination to a sibling backup;
5. promotes the staged directory by rename;
6. restores the old directory if promotion fails and rollback is possible;
7. removes the backup only after the new complete directory is committed; and
8. reports committed-cleanup failure distinctly without rolling back a
   committed new directory.

Under this ownership contract readers may observe the old complete directory,
the new complete directory, or a failed lookup during the two-rename window;
they must never observe a mixed bundle. Successful replacement publishes
exactly the supplied set, so an omitted optional artifact removes a stale
copy. A preparation or staging failure leaves the old complete destination
unchanged. Rollback/cleanup failure never becomes success and preserves
recovery evidence.

## Failure and invariant matrix

| Invariant | Required proof |
|---|---|
| Usage failures do not run domain logic or mutate outputs | direct command tests for missing/duplicate/unknown/mixed-mode inputs |
| Legacy post-publication statuses remain observable | reconcile unknown status 4; evaluation regression status 1; probe contained status 2 |
| Source trust cannot be upgraded at CLI composition | qualified and unverified tests exercise only their distinct sealed capability chains |
| OpenAPI cannot suppress or rewrite source evidence | absent/unavailable/degraded/conflict source-first bundle goldens |
| No mixed bundle on ordinary failure | injection at lock/preflight/stage/write/validate/backup/promote/rollback/cleanup boundaries |
| Stale optional artifacts disappear only after success | qualified probe replacement with and without `openapi-map.json` plus failed replacement |
| No Node or second authoring executable | PATH-scrubbed six-command smoke through the built `iw` binary |
| No live behavior | fixture-local inputs; injected HTTP/Git/Terraform seams; no credentials or network |
| Frozen v1 remains exact | CLI differential corpus compares stdout, stderr, exits, and every legacy artifact byte |
| Active surface is six commands | help/routing/Make tests reject `zpa-provider-evidence` while corpus gates remain green |

## Build parcels and ownership

1. `go/internal/authoring/artifactpublish/**`: cooperative complete-set
   publisher and focused failure-injection tests. It owns no CLI files.
2. `go/cmd/iw/commands_authoring_core*.go`: shared authoring I/O plus
   `reconcile`, `openapi-map`, and `transform-adopt-parity` composition. It
   owns no source/probe command file and no central dispatch.
3. `go/cmd/iw/commands_authoring_source*.go` plus only a minimal bounded
   sourcebind helper if required: legacy and v2 source-operation/evaluation.
4. `go/cmd/iw/commands_authoring_probe*.go`: provider-probe command composition
   and publication/copy behavior.
5. Coordinator-owned integration: `go/cmd/iw/main.go`, `usage.txt`, authoring
   differential/Node-free corpus, `Makefile`, lifecycle records, and central
   documentation reconciliation.

Builders do not commit or push. The coordinator integrates once, runs the full
gates, writes the review handoff, and sends the consolidated A6 candidate to a
fresh read-only adversarial reviewer. One bounded remediation pass follows the
review; discovery of a second independent defect class stops serial patching
and returns the slice to architecture triage.

## Deferred work

Global `IW_OPERATOR` routing, release binaries/signing/checksums, stable-tag
default switching, rollback-window removal, Node archive, and singleton-state
degrouping remain governed by their later roadmaps. The final A6 evidence may
unlock the authority-handoff gate, but it does not silently implement those
later phases.
