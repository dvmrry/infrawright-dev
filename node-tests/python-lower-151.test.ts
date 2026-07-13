import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  PYTHON_LOWER_151_RUNTIME_DELTAS,
  PYTHON_LOWER_151_UCD_SOURCES,
} from "../node-src/generated/python-lower-15.1.js";
import { pythonLower151 } from "../node-src/json/python-lower-151.js";
import {
  snakeName,
  slugifyTransformKey,
  transformPullItems,
} from "../node-src/domain/pull-transform.js";
import {
  compileZccPullArtifactSet,
  ZCC_TRANSFORM_CATALOG_SHA256,
} from "../node-src/domain/zcc-pull-artifacts.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import {
  deriveZccAdoptionIdentities,
} from "../node-src/domain/zcc-adoption-projection.js";

const HASH_CONTRACT = "infrawright-python-lower-15.1-exhaustive-v1\0";
const HASH_VECTORS = [
  (character: string): string => character,
  (character: string): string => `A\u03a3${character}`,
  (character: string): string => `A\u03a3${character}A`,
  (character: string): string => `${character}\u03a3`,
  (character: string): string => `A${character}\u03a3`,
] as const;

const PYTHON_EXHAUSTIVE_ORACLE = String.raw`
import hashlib
import json
import struct
import sys
import unicodedata

supported = {
    (3, 12): "15.0.0",
    (3, 13): "15.1.0",
}
runtime = tuple(sys.version_info[:2])
expected_ucd = supported.get(runtime)
if expected_ucd is None or unicodedata.unidata_version != expected_ucd:
    sys.stderr.write(
        "unsupported Python lowercase oracle: Python %d.%d / UCD %s\n"
        % (runtime[0], runtime[1], unicodedata.unidata_version)
    )
    sys.exit(3)

digest = hashlib.sha256()
digest.update(b"infrawright-python-lower-15.1-exhaustive-v1\0")
for code_point in range(0x110000):
    if 0xD800 <= code_point <= 0xDFFF:
        continue
    character = chr(code_point)
    vectors = (
        character,
        "A\u03A3" + character,
        "A\u03A3" + character + "A",
        character + "\u03A3",
        "A" + character + "\u03A3",
    )
    digest.update(("%06x" % code_point).encode("ascii"))
    for vector in vectors:
        payload = vector.lower().encode("utf-8")
        digest.update(struct.pack(">I", len(payload)))
        digest.update(payload)

json.dump({
    "digest": digest.hexdigest(),
    "python": "%d.%d" % runtime,
    "ucd": unicodedata.unidata_version,
}, sys.stdout, sort_keys=True, separators=(",", ":"))
`;

const PYTHON_ADOPTION_KEY_ORACLE = String.raw`
import json
import sys

from engine.adoption_meta import (
    adoption_entry,
    derive_import_id_from_identity,
    derive_key_from_identity,
    identity_item,
)

payload = json.load(sys.stdin)
meta = adoption_entry("zcc_forwarding_profile")
results = []
for raw in payload["items"]:
    item = identity_item(raw, "zcc_forwarding_profile")
    key = derive_key_from_identity(item, meta)
    results.append({
        "key": key,
        "import_id": derive_import_id_from_identity(
            item, meta, "zcc_forwarding_profile", key
        ),
    })

collision = None
seen = set()
try:
    for raw in payload["collision"]:
        item = identity_item(raw, "zcc_forwarding_profile")
        key = derive_key_from_identity(item, meta)
        if key in seen:
            raise ValueError("duplicate derived key %r" % key)
        seen.add(key)
except ValueError as exc:
    collision = str(exc)

json.dump(
    {"results": results, "collision": collision},
    sys.stdout,
    ensure_ascii=False,
    sort_keys=True,
    separators=(",", ":"),
)
`;

function expanded(ranges: readonly number[]): number[] {
  const output: number[] = [];
  for (let index = 0; index < ranges.length; index += 2) {
    const first = ranges[index];
    const last = ranges[index + 1];
    assert.notEqual(first, undefined);
    assert.notEqual(last, undefined);
    for (
      let codePoint = first ?? 0;
      codePoint <= (last ?? -1);
      codePoint += 1
    ) {
      output.push(codePoint);
    }
  }
  return output;
}

function currentRuntimeDelta(): (
  typeof PYTHON_LOWER_151_RUNTIME_DELTAS
)[keyof typeof PYTHON_LOWER_151_RUNTIME_DELTAS] {
  const version = process.versions.unicode;
  assert.ok(version === "16.0" || version === "17.0");
  return PYTHON_LOWER_151_RUNTIME_DELTAS[version];
}

function nodeExhaustiveDigest(): string {
  const digest = createHash("sha256");
  const length = Buffer.allocUnsafe(4);
  digest.update(HASH_CONTRACT, "utf8");
  for (let codePoint = 0; codePoint <= 0x10ffff; codePoint += 1) {
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) {
      continue;
    }
    const character = String.fromCodePoint(codePoint);
    digest.update(codePoint.toString(16).padStart(6, "0"), "ascii");
    for (const vector of HASH_VECTORS) {
      const payload = Buffer.from(pythonLower151(vector(character)), "utf8");
      length.writeUInt32BE(payload.length);
      digest.update(length);
      digest.update(payload);
    }
  }
  return digest.digest("hex");
}

test("generated runtime deltas are closed over the reviewed Node 24 Unicode tables", () => {
  assert.deepEqual(Object.keys(PYTHON_LOWER_151_UCD_SOURCES), [
    "15.1.0",
    "16.0.0",
    "17.0.0",
  ]);
  assert.deepEqual(Object.keys(PYTHON_LOWER_151_RUNTIME_DELTAS), [
    "16.0",
    "17.0",
  ]);
  const unicode16 = PYTHON_LOWER_151_RUNTIME_DELTAS["16.0"];
  const unicode17 = PYTHON_LOWER_151_RUNTIME_DELTAS["17.0"];
  assert.deepEqual(unicode16.runtime_only_lowercase_source_ranges, [
    0x1c89, 0x1c89,
    0xa7cb, 0xa7cc,
    0xa7da, 0xa7da,
    0xa7dc, 0xa7dc,
    0x10d50, 0x10d65,
  ]);
  assert.deepEqual(unicode17.runtime_only_lowercase_source_ranges, [
    0x1c89, 0x1c89,
    0xa7cb, 0xa7cc,
    0xa7ce, 0xa7ce,
    0xa7d2, 0xa7d2,
    0xa7d4, 0xa7d4,
    0xa7da, 0xa7da,
    0xa7dc, 0xa7dc,
    0x10d50, 0x10d65,
    0x16ea0, 0x16eb8,
  ]);
  for (const [delta, expected] of [
    [unicode16, [27, 52, 0, 43]],
    [unicode17, [55, 107, 1, 88]],
  ] as const) {
    assert.equal(
      expanded(delta.runtime_only_lowercase_source_ranges).length,
      expected[0],
    );
    assert.deepEqual(delta.python_only_lowercase_source_ranges, []);
    assert.deepEqual(delta.changed_common_lowercase_source_ranges, []);
    assert.equal(expanded(delta.runtime_only_cased_ranges).length, expected[1]);
    assert.equal(expanded(delta.python_only_cased_ranges).length, expected[2]);
    assert.equal(
      expanded(delta.runtime_only_case_ignorable_ranges).length,
      expected[3],
    );
    assert.deepEqual(delta.python_only_case_ignorable_ranges, [
      0x1171e,
      0x1171e,
    ]);
  }
  assert.deepEqual(unicode16.python_only_cased_ranges, []);
  assert.deepEqual(unicode17.python_only_cased_ranges, [0x295, 0x295]);
});

test("Python lowercase preserves every direct mapping source added by this runtime", () => {
  const additions = expanded(
    currentRuntimeDelta().runtime_only_lowercase_source_ranges,
  );
  assert.ok(additions.includes(0xa7cb));
  for (const codePoint of additions) {
    const character = String.fromCodePoint(codePoint);
    assert.equal(pythonLower151(character), character, `U+${codePoint.toString(16)}`);
    assert.notEqual(
      character.toLowerCase(),
      character,
      `Node must expose the reviewed runtime delta at U+${codePoint.toString(16)}`,
    );
  }
  assert.equal(pythonLower151("\ua7ce"), "\ua7ce");
  assert.equal(pythonLower151("\u{16ea0}"), "\u{16ea0}");
  assert.equal(pythonLower151("\u0130"), "i\u0307");
});

test("Final Sigma uses Unicode 15.1 Cased and Case_Ignorable context", () => {
  for (const [input, expected] of [
    ["\u1c89\u03a3", "\u1c89\u03c3"],
    ["A\u03a3\u1c89", "a\u03c2\u1c89"],
    ["A\u0897\u03a3", "a\u0897\u03c3"],
    ["A\u03a3\u0897A", "a\u03c2\u0897a"],
    ["\ua7ce\u03a3", "\ua7ce\u03c3"],
    ["A\u03a3\ua7ce", "a\u03c2\ua7ce"],
    ["\u0295\u03a3", "\u0295\u03c2"],
    ["A\u03a3\u0295", "a\u03c3\u0295"],
    ["A\u1acf\u03a3", "a\u1acf\u03c3"],
    ["A\u03a3\u1acfA", "a\u03c2\u1acfa"],
    ["A\u{1171e}\u03a3", "a\u{1171e}\u03c2"],
    ["A\u03a3\u{1171e}A", "a\u03c3\u{1171e}a"],
  ] as const) {
    assert.equal(pythonLower151(input), expected);
  }

  // U+02B0 is both Cased and Case_Ignorable. Final_Sigma must ignore it
  // before considering whether the next significant character is Cased.
  assert.equal(pythonLower151("1\u02b0\u03a3"), "1\u02b0\u03c3");
  assert.equal(pythonLower151("A\u03a3\u02b01A"), "a\u03c2\u02b01a");
});

test("snake regex matches Python dot boundaries and Unicode code points", () => {
  for (const [input, expected] of [
    ["\rFoo", "\r_foo"],
    ["\nFoo", "\nfoo"],
    ["\u2028Foo", "\u2028_foo"],
    ["\u2029Foo", "\u2029_foo"],
    ["\u{1f600}Foo", "\u{1f600}_foo"],
    ["\ud800Foo", "\ud800_foo"],
  ] as const) {
    assert.equal(snakeName(input), expected);
  }
  assert.equal(snakeName("\ua7cbName"), "\ua7cb_name");
});

test("slug output and collisions retain Python expansion behavior", () => {
  assert.equal(slugifyTransformKey("\u0130"), "i");
  assert.equal(slugifyTransformKey("A\u03a3C"), "a_c");
  assert.equal(slugifyTransformKey("\ua7cb"), "");

  const catalog = loadZccTransformCatalog();
  assert.throws(
    () => transformPullItems({
      catalog,
      rawItems: [
        { id: "one", name: "I" },
        { id: "two", name: "\u0130" },
      ],
      resourceType: "zcc_forwarding_profile",
    }),
    /duplicate derived key "i"/,
  );
});

test("public ZCC artifacts expose Python snake bytes for Unicode edge drops", () => {
  const result = compileZccPullArtifactSet({
    catalog: loadZccTransformCatalog(),
    catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
    rawItems: [{
      id: "edge-lower-1",
      name: "\u0130",
      "A\u0897\u03a3Noise": true,
      "A\u1acf\u03a3Noise": true,
      "\u0295\u03a3Noise": true,
      "\ua7ceNoise": true,
      "\ua7cbNoise": true,
    }],
    source: {
      path: "pulls/demo/zcc_forwarding_profile.json",
      sha256: "5".repeat(64),
      size_bytes: 0,
    },
    target: {
      tenant: "demo",
      resourceType: "zcc_forwarding_profile",
      rootLabel: "zcc_forwarding_profile",
      rootMembers: ["zcc_forwarding_profile"],
      variableName: "items",
      configPath: "config/demo/zcc_forwarding_profile.auto.tfvars.json",
      importsPath: "imports/demo/zcc_forwarding_profile_imports.tf",
      lookupPath: null,
    },
  });
  assert.equal(result.status, "review_required");
  assert.deepEqual(result.unexpected_drops, [
    "a\u0897\u03c3_noise",
    "a\u1acf\u03c3_noise",
    "\u0295\u03c2_noise",
    "\ua7cb_noise",
    "\ua7ce_noise",
  ]);
  assert.match(result.artifacts.tfvars.content, /"i": \{/);
  assert.match(result.artifacts.tfvars.content, /"name": "\\u0130"/);
  assert.match(result.artifacts.imports.content, /this\["i"\]/);
});

test("private ZCC adoption identities match live Python slug semantics", () => {
  const items = [
    { id: "edge-i", name: "\u0130" },
    { id: "edge-sigma", name: "A\u03a3C" },
    { id: "edge-a7cb", name: "\ua7cb" },
    { id: "edge-ignorable", name: "A\u{1171e}\u03a3" },
  ] as const;
  const collision = [
    { id: "collision-one", name: "I" },
    { id: "collision-two", name: "\u0130" },
  ] as const;
  const python = spawnSync(PYTHON_ORACLE, ["-c", PYTHON_ADOPTION_KEY_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: JSON.stringify({ collision, items }),
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  const expected = JSON.parse(python.stdout) as {
    readonly collision: string | null;
    readonly results: readonly {
      readonly import_id: string;
      readonly key: string;
    }[];
  };
  const catalog = loadZccAdoptionCatalog();
  const actual = items.map((raw) => {
    const result = deriveZccAdoptionIdentities({
      catalog,
      rawItems: [raw],
      resourceType: "zcc_forwarding_profile",
    });
    const key = Object.keys(result.import_ids)[0];
    assert.notEqual(key, undefined);
    return { import_id: result.import_ids[key ?? ""], key };
  });
  assert.deepEqual(actual, expected.results);
  assert.deepEqual(actual.map((entry) => entry.key), [
    "i",
    "a_c",
    "id_edge_a7cb",
    "a",
  ]);
  assert.match(expected.collision ?? "", /duplicate derived key 'i'/);
  assert.throws(
    () => deriveZccAdoptionIdentities({
      catalog,
      rawItems: collision,
      resourceType: "zcc_forwarding_profile",
    }),
    /duplicate derived adoption key "i"/,
  );
});

test("lowercase helper rejects Node Unicode runtime drift", () => {
  const descriptor = Object.getOwnPropertyDescriptor(process.versions, "unicode");
  assert.notEqual(descriptor, undefined);
  try {
    Object.defineProperty(process.versions, "unicode", {
      ...descriptor,
      value: "18.0",
    });
    assert.throws(
      () => pythonLower151("A"),
      /requires a reviewed Node Unicode 16\.0 or 17\.0 runtime delta/,
    );
  } finally {
    Object.defineProperty(process.versions, "unicode", descriptor ?? {});
  }
  assert.equal(pythonLower151("A"), "a");
});

test("all Unicode scalars match the supported live Python oracle", {
  timeout: 120_000,
}, () => {
  const python = spawnSync(PYTHON_ORACLE, ["-c", PYTHON_EXHAUSTIVE_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    maxBuffer: 1024 * 1024,
    timeout: 120_000,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  const oracle = JSON.parse(python.stdout) as {
    readonly digest: string;
    readonly python: string;
    readonly ucd: string;
  };
  assert.ok(oracle.python === "3.12" || oracle.python === "3.13");
  assert.ok(oracle.ucd === "15.0.0" || oracle.ucd === "15.1.0");
  const actual = nodeExhaustiveDigest();
  assert.equal(actual, oracle.digest);
  assert.equal(actual, "93acb44d32a0d2dffc6d8151c78420d4f35aea2764a74cfa939b315eb68f5db1");
});
