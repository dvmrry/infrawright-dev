# Unicode Lowercase Compatibility

`go/internal/textcompat.Lower151` is a narrow compatibility seam for keys and
paths that must match Python 3.12/3.13 `str.lower()` byte-for-byte. Those
Python releases use Unicode 15.0/15.1; the lowercase mappings and Final Sigma
behavior relevant here are equivalent. This is not a general-purpose Unicode
case-mapping API.

The production implementation is self-contained Go:

- `go/internal/textcompat/lower.go` implements full lowercase mapping and the
  locale-independent Final Sigma rule.
- `go/internal/textcompat/tables.go` contains the reviewed Unicode runtime
  deltas and the pinned source-file metadata.
- `go/internal/textcompat/lower_test.go` exhaustively hashes every Unicode
  scalar value (excluding surrogates) across five context vectors. The fixed
  digest is
  `93acb44d32a0d2dffc6d8151c78420d4f35aea2764a74cfa939b315eb68f5db1`.

Normal builds and tests do not require Python, Node, network access, or the
multi-megabyte Unicode source files.

## Pinned Unicode sources

The archived generator verified these official inputs before producing the
compact delta tables.

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

Against the Unicode 15.1 target, the reviewed deltas contain:

- Unicode 16.0: 27 runtime-only lowercase sources, 52 runtime-only `Cased`
  points, 43 runtime-only `Case_Ignorable` points, and U+1171E as the sole
  target-only `Case_Ignorable` point.
- Unicode 17.0: 55 runtime-only lowercase sources, 107 runtime-only `Cased`
  points, 88 runtime-only `Case_Ignorable` points, U+0295 as the sole
  target-only `Cased` point, and U+1171E as the sole target-only
  `Case_Ignorable` point.
- Both: no target-only lowercase sources and no changed common lowercase
  mappings.

## Archived source recovery

The retired generator is intentionally absent from the production tree. It is
recoverable from immutable tag `node-oracle-v1-final`, which resolves to commit
`e4b78aa0944654df8c6a428f07424e62f4b652cb`:

| Archived path | SHA-256 |
| --- | --- |
| `tools/generate-python-lower-151.mjs` | `3d2abeec00af86a288ff908630ad7815d70c3b2a3d1c776c89f15042bfd6b254` |
| `node-src/generated/python-lower-15.1.ts` | `f09c942ba0b273973d0ea56094c6cdd51b92b2a6e19ee4bdbca960860fe1e2fb` |
| `node-src/json/python-lower-151.ts` | `9515e4704495b6dabbd5af1f3b2779f98e94c71891240af21e45653c8b890240` |

For a table update, recover and verify those files from the tag, acquire the
nine Unicode inputs in `<ucd-root>/<version>/<file>` layout, then run the
archived generator with `--write` and `--check`. Mechanically port the reviewed
table delta to `tables.go`; do not restore a runtime dependency or hand-edit
the ranges.

Any Unicode source, runtime version, expected cardinality, or table change
requires review of the generated delta and an updated exhaustive Go digest.
