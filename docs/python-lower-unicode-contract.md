# Python Lowercase Compatibility Contract

The Node transform and private adoption kernels must preserve the key and path
bytes produced by Python `str.lower()`. Python 3.12 uses Unicode 15.0 and
Python 3.13 uses Unicode 15.1; the lowercase mappings and Final Sigma property
behavior used here are equivalent across those two runtimes. Node 24 patch
releases have shipped more than one Unicode table: Node 24.15.0 reports Unicode
16.0/ICU 76.1, while Node 24.18.0 reports Unicode 17.0/ICU 78.3. Both added or
changed characters and properties that affect whether U+03A3 lowercases to
U+03C2 or U+03C3.

`node-src/json/python-lower-151.ts` is therefore a deliberately narrow
compatibility helper, not a general case-mapping library. It:

- selects a closed generated delta only when `process.versions.unicode` is
  exactly `16.0` or `17.0`, and fails every other value;
- preserves every newer-runtime lowercase source that Unicode 15.1 leaves
  unchanged;
- applies unconditional full lowercase mappings one code point at a time;
- implements Final Sigma explicitly with Unicode 15.1 `Cased` and
  `Case_Ignorable` properties; and
- tests `Case_Ignorable` before `Cased`, as required for points that have both
  properties.

The snake-case first pass uses `([^\n])([A-Z][a-z]+)` with Unicode code-point
matching. This is Python regex-dot behavior: LF is excluded, while CR, U+2028,
and U+2029 are included. Both snake-case and transform/adoption slug generation
then use the compatibility helper.

## Pinned Source Evidence

The compact production ranges are generated from these official Unicode
inputs. The generator verifies every digest before parsing a file.

| Version | File | SHA-256 |
| --- | --- | --- |
| 15.1.0 | [UnicodeData.txt](https://www.unicode.org/Public/15.1.0/ucd/UnicodeData.txt) | `2fc713e6a31a87c4850a37fe2caffa4218180fadb5de86b43a143ddb4581fb86` |
| 15.1.0 | [SpecialCasing.txt](https://www.unicode.org/Public/15.1.0/ucd/SpecialCasing.txt) | `55a477efd933a52cd27e6a9bf70265bb2d8814af31aab07767abc8eb421f27ef` |
| 15.1.0 | [DerivedCoreProperties.txt](https://www.unicode.org/Public/15.1.0/ucd/DerivedCoreProperties.txt) | `f55d0db69123431a7317868725b1fcbf1eab6b265d756d1bd7f0f6d9f9ee108b` |
| 16.0.0 | [UnicodeData.txt](https://www.unicode.org/Public/16.0.0/ucd/UnicodeData.txt) | `ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f` |
| 16.0.0 | [SpecialCasing.txt](https://www.unicode.org/Public/16.0.0/ucd/SpecialCasing.txt) | `8d5de354eef79f2395a54c9c7dcebbaf3d30fc962d0f85611ea97aa973a0c451` |
| 16.0.0 | [DerivedCoreProperties.txt](https://www.unicode.org/Public/16.0.0/ucd/DerivedCoreProperties.txt) | `39d35161f2954497f69e08bdb9e701493f476a3d30222de20028feda36c1dabd` |
| 17.0.0 | [UnicodeData.txt](https://www.unicode.org/Public/17.0.0/ucd/UnicodeData.txt) | `2e1efc1dcb59c575eedf5ccae60f95229f706ee6d031835247d843c11d96470c` |
| 17.0.0 | [SpecialCasing.txt](https://www.unicode.org/Public/17.0.0/ucd/SpecialCasing.txt) | `efc25faf19de21b92c1194c111c932e03d2a5eaf18194e33f1156e96de4c9588` |
| 17.0.0 | [DerivedCoreProperties.txt](https://www.unicode.org/Public/17.0.0/ucd/DerivedCoreProperties.txt) | `24c7fed1195c482faaefd5c1e7eb821c5ee1fb6de07ecdbaa64b56a99da22c08` |

The generated artifact is a closed record keyed exactly by the runtime's
`process.versions.unicode`. Against Python Unicode 15.1, the reviewed deltas
are:

- Unicode 16.0: 27 runtime-only direct lowercase sources, 52 runtime-only
  `Cased` points, 43 runtime-only `Case_Ignorable` points, no Python-only
  `Cased` points, and U+1171E as the sole Python-only `Case_Ignorable` point;
- Unicode 17.0: 55 runtime-only direct lowercase sources, 107 runtime-only
  `Cased` points, 88 runtime-only `Case_Ignorable` points, U+0295 as the sole
  Python-only `Cased` point, and U+1171E as the sole Python-only
  `Case_Ignorable` point; and
- both runtimes: zero Python-only lowercase sources and zero changed common
  lowercase mappings. The generator refuses either class until target mappings
  are explicitly represented.

The current generated artifact is `go/internal/pyunicode/tables.go`. The nine
multi-megabyte source files are not vendored and no runtime Unicode package is
installed.

## Regeneration And Check

Acquire the nine files separately and put them under a local root with this
layout:

```text
<ucd-root>/15.1.0/{UnicodeData.txt,SpecialCasing.txt,DerivedCoreProperties.txt}
<ucd-root>/16.0.0/{UnicodeData.txt,SpecialCasing.txt,DerivedCoreProperties.txt}
<ucd-root>/17.0.0/{UnicodeData.txt,SpecialCasing.txt,DerivedCoreProperties.txt}
```

The original generator was archived with the Node source. Recover it from the
immutable `node-oracle-v1-final` tag when changing the pinned tables, then port
and review any resulting table update in Go. The historical commands were:

```sh
node tools/generate-python-lower-151.mjs --ucd-root <ucd-root> --write
node tools/generate-python-lower-151.mjs --ucd-root <ucd-root> --check
```

The archived tool contains no downloader and performs no network access.
Normal build and test runs consume only the compact committed Go artifact. A
Unicode source, runtime, or expected-cardinality change requires a reviewed
regeneration and updated exhaustive digests.

## Node 24 Patch-Level Coverage

The initial implementation guarded exactly Unicode 16.0 because that was the
table exposed by the local Node 24.15.0 runtime. GitHub `setup-node` correctly
resolved the newer Node 24.18.0 patch for the repository's broad Node 24 engine
contract; that runtime exposes Unicode 17.0, and CI failed at the exact guard.
The guard worked, but the supported-runtime evidence was incomplete.

The remediation keeps the package contract at all Node 24 releases and adds a
separately source-derived Unicode 17.0 delta. It does not pin an older Node
patch or pretend one runtime has another table. The exhaustive test is run
under the real Node 24.15.0/Unicode 16.0 and Node 24.18.0/Unicode 17.0 binaries;
all unreviewed Unicode versions remain terminal.

## Exhaustive Differential Evidence

The Node test hashes every Unicode scalar value (surrogates excluded) against a
live, version-checked Python 3.12/UCD 15.0 or Python 3.13/UCD 15.1 oracle. For
each scalar `c`, the fixed vector order is `c`, `AΣc`, `AΣcA`, `cΣ`, `AcΣ`.
The SHA-256 stream begins with
`infrawright-python-lower-15.1-exhaustive-v1\0`; each scalar contributes its
six-digit lowercase hexadecimal code point followed by each lowercased UTF-8
result prefixed with an unsigned four-byte big-endian byte length.

That explicit framing produces
`93acb44d32a0d2dffc6d8151c78420d4f35aea2764a74cfa939b315eb68f5db1`.
Both implementations compute the digest independently during the test; the
test also requires equality rather than accepting the known-answer value by
itself.
