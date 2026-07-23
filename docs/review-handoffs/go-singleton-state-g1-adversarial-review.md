# Singleton-state topology G1 adversarial review

## Blocking findings from first pass

1. `dist/iw` freshness omitted compiled Darwin assembly inputs because the
   Make prerequisite allow-list covered only Go/JSON/module files.
2. The first replacement for the retired Generation/Topology differential was
   weaker: it dropped a topology-independent two-resource module comparison,
   did not preserve an implicit-singleton env comparison, and asserted only
   selected v2 fields instead of complete bytes.
3. The Go-v2 authority CLI helper assumed the ignored `dist/` directory was
   already present, so the tests failed in a pristine clone.

The first-pass verdict was **Request changes**. Catalog exactness, field
retirement, singleton identity, same-root unreachability, consumer isolation,
C4/D5 parity, and frozen-oracle verification had no findings.

## Remediation verification

- `GO_BUILD_INPUTS` now conservatively covers every local file below `go/`,
  including both platform assembly sources and future embedded inputs.
  `dist/iw` creates its parent directory. An assembly `make -n -W` probe
  scheduled the Go build.
- Exact committed v2 stdout/stderr/tree goldens now cover roots (default and
  selected), scope-paths, plan-roots, and singleton gen-env. The complete
  frozen-Node module generate/validate comparison for `zcc_web_privacy` and
  `zia_rule_labels` is restored, as is the exact same-workspace
  implicit-singleton gen-env comparison. `make v2-authority` executes every
  replacement before the retained differential lane.
- The test helper provisions absent `dist/`, removes only its own candidate,
  and removes the directory only when it created it and it remains empty. The
  Go-only authority tests passed with `dist/` absent; the restored oracle
  remained at bundle SHA `ce48c2c6...` and checksum-file SHA `b955f56a...`.

## Source evidence review

- Base/head diff: inspected directly from `93f04b36728755c55567b0915b804cacb4cd3a65`.
- Root catalog schema/artifacts: 151 unique generated types; provider counts
  ZCC 7, ZIA 74, ZPA 54, ZTC 16; v1/v2 provider/resource/source projection
  identical after removing version, digest, and v1 slug fields.
- Frozen v1 catalog: byte-unchanged; SHA-256 `844c6c4b...`.
- V2 catalog: schema-valid; SHA-256 `66eb8da2...`.
- Pack metadata: exactly 18 ZIA `slug_group` inputs removed; the validator
  rejects any reintroduction with the roadmap pointer.
- Live/provider/backend/Kubernetes evidence: not part of G1 and not run.

## Verdict

**Approve.** The same fresh reviewer rechecked only the three remediated
surfaces and found each resolved. The review and recheck were read-only.
