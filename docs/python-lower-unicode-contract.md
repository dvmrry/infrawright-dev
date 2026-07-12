# Python Lowercase Compatibility Contract

The Node transform and private adoption kernels must preserve the key and path
bytes produced by Python `str.lower()`. Python 3.12 uses Unicode 15.0 and
Python 3.13 uses Unicode 15.1; the lowercase mappings and Final Sigma property
behavior used here are equivalent across those two runtimes. Node 24 currently
uses Unicode 16.0, which added cased characters and changed the properties that
decide whether U+03A3 lowercases to U+03C2 or U+03C3.

`node-src/json/python-lower-151.ts` is therefore a deliberately narrow
compatibility helper, not a general case-mapping library. It:

- fails closed unless `process.versions.unicode` is exactly `16.0`;
- preserves the 27 Unicode 16 lowercase sources that Unicode 15.1 leaves
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

The reviewed derivation has four nonempty deltas:

- 27 Unicode 16-only direct lowercase sources;
- 52 Unicode 16-only `Cased` points;
- 43 Unicode 16-only `Case_Ignorable` points; and
- U+1171E as the sole Unicode 15.1-only `Case_Ignorable` point.

The generated artifact is
`node-src/generated/python-lower-15.1.ts`. The six multi-megabyte source files
are not vendored and no runtime Unicode package is installed.

## Regeneration And Check

Acquire the six files separately and put them under a local root with this
layout:

```text
<ucd-root>/15.1.0/{UnicodeData.txt,SpecialCasing.txt,DerivedCoreProperties.txt}
<ucd-root>/16.0.0/{UnicodeData.txt,SpecialCasing.txt,DerivedCoreProperties.txt}
```

Then regenerate or compare the committed bytes:

```sh
node tools/generate-python-lower-151.mjs --ucd-root <ucd-root> --write
node tools/generate-python-lower-151.mjs --ucd-root <ucd-root> --check
```

The tool contains no downloader and performs no network access. Normal build
and test runs consume only the compact committed artifact. A Unicode source,
runtime, or expected-cardinality change fails closed and requires a reviewed
regeneration.

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
