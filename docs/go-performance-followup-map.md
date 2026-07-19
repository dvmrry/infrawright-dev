# Go performance follow-up map

Status: PARKED INVENTORY — the safe in-process P0/P1 remediations are landed.
The remaining work changes filesystem observability, freshness, Terraform
transactions, provider behavior, concurrency, or rate-limit exposure and needs
separate measurement and authorization.

This document is the short index for the earlier performance deep dive. The
detailed evidence and qualification procedures remain in:

- [performance-benchmark.md](performance-benchmark.md)
- [go-p1-performance-remediation.md](review-handoffs/go-p1-performance-remediation.md)
- [performance-accepted-plan-oracle.md](review-handoffs/performance-accepted-plan-oracle.md)
- [performance-telemetry-fetch-concurrency.md](review-handoffs/performance-telemetry-fetch-concurrency.md)

## 1. Completed safe work

| Commit | Slice | Recorded effect |
|---|---|---|
| `672322c` | In-process HCL formatting | Replaced one `terraform fmt` subprocess per generated file with token-only `hclwrite.Format`; 604-file format work measured at about 23 ms rather than about 118 seconds of subprocess spawn overhead, with byte gates green |
| `7963f74` | Right-sized stable-file and command-capture buffers | Small paths fell from multi-megabyte reservation to approximately 69 KiB and 1.3 KiB per operation respectively |
| `5d5e98c` | Provider-schema cache per pack root | Removed repeated schema decoding; approximately 50 times fewer allocations in the measured path and eliminated the projected multi-gigabyte adoption amplification |
| `747f613` | P1 staging/envgen/snapshot remediation | Terraform state observation is once per logical root rather than once per member; envgen allocation fell from about 61.7 MB to 45.7 MB; tiny snapshot verification fell from a 1 MiB buffer to about 768 B/op |

All were accepted with the standing artifact byte gates green. The exact
measurements belong to their review handoffs; the numbers above are navigation
summaries, not new benchmarks.

## 2. Low-complexity candidates still parked

### No-op generated-file write suppression

Avoid rewriting a generated module or artifact when its bytes are unchanged.
This was excluded from P1 because mtime, file-watcher, atomic-replacement, and
diagnostic behavior are observable. Reopen only with an explicit contract for
those effects and tests proving that permissions, identity, diagnostics, and
failure ordering remain correct.

### Skip generation work that downstream selection cannot consume

Measure module/profile selection after singleton-state topology v2. Degrouping
may remove much of the amplification by itself. Only then identify generation
that is provably unreachable from the selected output; retain collect-then-emit
barriers and exact diagnostics.

## 3. Live-measurement candidates

### Fetch concurrency and in-flight byte budget

The existing experimental worker-pool work retains a serial default. Run the
documented concurrency 1/2/4/8 comparison with identical selectors and repeated
runs. Before raising the default, add or prove a global in-flight response-byte
budget so concurrency cannot multiply peak memory independently of item-count
limits. Abort on artifact drift, increased request count, materially worse
429/retry behavior, or latency regression.

### Accepted-plan Oracle state

The opt-in accepted-plan mode can remove scratch Apply plus state show when the
accepted import-only plan contains complete provider observations and identical
sensitivity masks. It is fixture-qualified but not live-qualified. Compare it
against applied-state on the same bounded cohort and retain fail-closed
rejection for missing, unknown, extra, tainted, deposed, or mismatched values.

### Terraform plugin cache

A job-local `TF_PLUGIN_CACHE_DIR` can reduce provider download/unpack work, but
does not reduce provider API reads. Measure cold and warm runs, protect the
cache from concurrent corruption, and keep provider-lock/checksum verification
authoritative.

### Phase-level timing

Use the sanitized performance reports to separate Go compute, Terraform init,
provider startup, provider reads, retries, and artifact I/O. Structural command
counts and fixture timings must not be presented as live speed claims.

## 4. Per-resource Terraform/provider API reads

The observed import/apply bottleneck is mostly outside the Go process:

- Terraform imports and refreshes managed addresses through the provider.
- The provider may perform a fresh detail read for every object.
- Those reads accumulate API/proxy latency and may be serial or only narrowly
  parallel inside Terraform/provider execution.
- Infrawright's Fetch output is normalized inventory, not the exact provider
  transport response, so it cannot safely be substituted as provider state.

Do not add an Infrawright-level response cache in front of Terraform merely to
hide this cost. It would weaken the freshness property unless the provider
consumes the exact cached response through a supported, qualified interface.

The investigation order remains:

1. Capture the exact provider binary provenance and phase/request telemetry on
   the work machine.
2. Determine whether the costly family can be read completely from a provider
   list response inside one provider process.
3. If source and live traces support it, prototype a provider-local list-backed
   read cache with real detail-read misses and normal provider mapping.
4. Consider a private job-local read-through cache only after proving the
   per-process approach insufficient and defining freshness, scope, redaction,
   invalidation, and failure behavior.
5. Consider exact immutable response replay only if the provider exposes a
   complete provider-consumable snapshot seam. Never treat normalized Fetch
   files as that snapshot.

URL Categories is a poor first target because its Read already uses a bulk
`GetAll` path with detail fallback. Location Management was identified as a
more credible candidate from source inspection, but live request accounting is
required before selecting it.

## 5. Batching and concurrency boundary

Provider-wide Oracle batching could amortize Terraform init and provider
startup, but it does not eliminate one-detail-read-per-object behavior. It also
changes transaction containment, failure attribution, cancellation, generated
configuration policy, and artifact publication boundaries.

Do not prototype batching or parallel plan/apply merely because individual
operations are slow. Reconsider bounded batching only if live timing still
shows init/provider startup as material after:

- singleton-state topology v2;
- accepted-plan qualification;
- plugin-cache measurement; and
- any validated provider-local request reduction.

No test or benchmark may make live Apply reachable without its separate human
authorization.

## 6. Reopening criteria

A parked optimization becomes a candidate only when it has:

- a measured dominant cost on the current topology and Go binary;
- an explicit freshness, cancellation, failure-order, and side-effect contract;
- exact artifact and operator-decision parity where those remain required;
- fixture/local-Terraform qualification before live measurement;
- repeated same-cohort live evidence where provider/API behavior is involved;
- a default-off or rollback path for any behavior-changing experiment.

Singleton-state topology v2 is expected to change the performance shape. Re-run
the profile after its qualification gates before selecting the next item from
this map.
