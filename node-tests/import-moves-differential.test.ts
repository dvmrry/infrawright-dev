import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  deriveImportMoves,
  parseGeneratedImports,
  parseHclQuotedString,
  renderGeneratedImports,
  renderHclQuotedString,
  renderMovedBlocks,
  type GeneratedImportPair,
  type ImportMove,
  type ImportMoveDerivation,
} from "../node-src/domain/import-moves.js";

const RESOURCE_TYPE = "zia_rule_labels";

interface DifferentialCase {
  readonly name: string;
  readonly old: readonly GeneratedImportPair[];
  readonly next: readonly GeneratedImportPair[];
}

interface PythonDifferentialResult {
  readonly name: string;
  readonly oldText: string;
  readonly newText: string;
  readonly oldPairs: readonly GeneratedImportPair[];
  readonly newPairs: readonly GeneratedImportPair[];
  readonly derivation: ImportMoveDerivation;
  readonly movesText: string;
}

interface PythonImportMovesAuthority {
  readonly authority: {
    readonly implementation: string;
    readonly python: string;
    readonly unicode: string;
  };
  readonly baseline: string;
  readonly kind: string;
  readonly resourceType: string;
  readonly results: readonly PythonDifferentialResult[];
  readonly version: number;
}

const AUTHORITY_SHA256 =
  "5dc5a25751c2990ee36c550180b0516eea3e40ab4e7405a846bdc05d8d25387d";
const AUTHORITY_RESULTS_BYTES = 11_001;
const AUTHORITY_RESULTS_SHA256 =
  "8300db560009cdff81a3f8385dc02d3e142029f32c334345dbe83cec8882b8e0";

const CASES: readonly DifferentialCase[] = [
  { name: "empty", old: [], next: [] },
  {
    name: "unchanged",
    old: [
      { key: "alpha", importId: "101" },
      { key: "stable", importId: "102" },
    ],
    next: [
      { key: "alpha", importId: "101" },
      { key: "stable", importId: "102" },
    ],
  },
  {
    name: "rename",
    old: [{ key: "old_name", importId: "101" }],
    next: [{ key: "new_name", importId: "101" }],
  },
  {
    name: "multiple-renames",
    old: [
      { key: "zulu", importId: "3" },
      { key: "alpha", importId: "1" },
      { key: "middle", importId: "2" },
      { key: "stable", importId: "4" },
    ],
    next: [
      { key: "renamed_zulu", importId: "3" },
      { key: "renamed_alpha", importId: "1" },
      { key: "renamed_middle", importId: "2" },
      { key: "stable", importId: "4" },
    ],
  },
  {
    name: "add-remove-not-rename",
    old: [
      { key: "removed", importId: "101" },
      { key: "stable", importId: "102" },
    ],
    next: [
      { key: "stable", importId: "102" },
      { key: "added", importId: "103" },
    ],
  },
  {
    name: "unicode-and-escapes",
    old: [
      {
        key: "old\" }\nresource \"x\" \"y\" {\\\t東京${name}%{x}",
        importId: "id\"\\\n\r\t識別子${id}%{id}",
      },
      { key: "😀", importId: "astral" },
      { key: "", importId: "bmp" },
    ],
    next: [
      {
        key: "new\" }\nresource \"x\" \"y\" {\\\t大阪${name}%{x}",
        importId: "id\"\\\n\r\t識別子${id}%{id}",
      },
      { key: "😀-renamed", importId: "astral" },
      { key: "-renamed", importId: "bmp" },
    ],
  },
  {
    name: "exact-string-import-ids",
    old: [
      { key: "decimal", importId: "1.0" },
      { key: "leading_zero", importId: "01" },
      { key: "plain", importId: "1" },
    ],
    next: [
      { key: "decimal_new", importId: "1.0" },
      { key: "leading_zero_new", importId: "01" },
      { key: "plain_new", importId: "1" },
    ],
  },
  {
    name: "key-swap",
    old: [
      { key: "a", importId: "101" },
      { key: "b", importId: "102" },
    ],
    next: [
      { key: "a", importId: "102" },
      { key: "b", importId: "101" },
    ],
  },
  {
    name: "destination-occupied",
    old: [
      { key: "a", importId: "101" },
      { key: "b", importId: "102" },
    ],
    next: [{ key: "b", importId: "101" }],
  },
  {
    name: "duplicate-from",
    old: [{ key: "a", importId: "101" }],
    next: [
      { key: "b", importId: "101" },
      { key: "c", importId: "101" },
    ],
  },
  {
    name: "ambiguous-old-id",
    old: [
      { key: "a", importId: "101" },
      { key: "b", importId: "101" },
    ],
    next: [{ key: "c", importId: "101" }],
  },
  {
    name: "three-cycle-destinations-occupied",
    old: [
      { key: "a", importId: "101" },
      { key: "b", importId: "102" },
      { key: "c", importId: "103" },
    ],
    next: [
      { key: "a", importId: "103" },
      { key: "b", importId: "101" },
      { key: "c", importId: "102" },
    ],
  },
];

function pythonAuthority(): PythonImportMovesAuthority {
  const bytes = readFileSync(path.join(
    process.cwd(),
    "node-tests",
    "fixtures",
    "python-import-moves-v1.json",
  ));
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    AUTHORITY_SHA256,
    "frozen Python import/move authority changed without re-adjudication",
  );
  return JSON.parse(bytes.toString("utf8")) as PythonImportMovesAuthority;
}

test("generated imports and safe move derivation match frozen Python bytes and semantics", () => {
  const authority = pythonAuthority();
  assert.deepEqual(
    {
      authority: authority.authority,
      baseline: authority.baseline,
      kind: authority.kind,
      resourceType: authority.resourceType,
      version: authority.version,
    },
    {
      authority: {
        implementation: "cpython",
        python: "3.13.13",
        unicode: "15.1.0",
      },
      baseline: "71da6c267119c8f8531accce4906414a8c7c1e84",
      kind: "infrawright.python-import-moves-authority",
      resourceType: RESOURCE_TYPE,
      version: 1,
    },
  );
  const canonicalResults = JSON.stringify(authority.results);
  assert.equal(Buffer.byteLength(canonicalResults, "utf8"), AUTHORITY_RESULTS_BYTES);
  assert.equal(
    createHash("sha256").update(canonicalResults, "utf8").digest("hex"),
    AUTHORITY_RESULTS_SHA256,
  );
  const expected = authority.results;
  assert.equal(expected.length, CASES.length);

  for (const [index, item] of expected.entries()) {
    const input = CASES[index];
    assert.notEqual(input, undefined);
    assert.equal(item.name, input?.name);
    assert.equal(
      renderGeneratedImports(RESOURCE_TYPE, input?.old ?? []),
      item.oldText,
      `${item.name}: old imports bytes`,
    );
    assert.equal(
      renderGeneratedImports(RESOURCE_TYPE, input?.next ?? []),
      item.newText,
      `${item.name}: new imports bytes`,
    );
    assert.deepEqual(
      parseGeneratedImports(RESOURCE_TYPE, item.oldText),
      item.oldPairs,
      `${item.name}: old parse`,
    );
    assert.deepEqual(
      parseGeneratedImports(RESOURCE_TYPE, item.newText),
      item.newPairs,
      `${item.name}: new parse`,
    );

    const actual = deriveImportMoves(
      RESOURCE_TYPE,
      item.oldText,
      item.newText,
    );
    assert.deepEqual(actual, item.derivation, `${item.name}: derivation`);
    assert.equal(
      renderMovedBlocks(RESOURCE_TYPE, actual.moves),
      item.movesText,
      `${item.name}: moves bytes`,
    );
  }
});

test("all four unsafe move classes remain explicitly suppressed", () => {
  const expected = new Map(
    pythonAuthority().results.map((item) => [item.name, item.derivation]),
  );
  assert.deepEqual(
    expected.get("key-swap")?.suppressed.map((item) => item.reason),
    ["key_swap", "key_swap"],
  );
  assert.deepEqual(
    expected.get("destination-occupied")?.suppressed.map((item) => item.reason),
    ["destination_occupied"],
  );
  assert.deepEqual(
    expected.get("duplicate-from")?.suppressed.map((item) => item.reason),
    ["duplicate_from", "duplicate_from"],
  );
  assert.deepEqual(
    expected.get("ambiguous-old-id")?.suppressed.map((item) => item.reason),
    ["ambiguous", "ambiguous"],
  );
});

test("HCL quoted strings round-trip only the generated escape grammar", () => {
  const values = [
    "",
    "plain",
    "quote\"slash\\line\nreturn\rtab\t",
    "${name} %{ if true } $${already} %%{already}",
    "東京😀",
  ];
  for (const value of values) {
    const rendered = renderHclQuotedString(value);
    assert.deepEqual(parseHclQuotedString(rendered), {
      value,
      end: rendered.length,
    });
  }
  assert.throws(() => parseHclQuotedString('"bad\\u0020escape"'), (error) => {
    assert.ok(error instanceof ProcessFailure);
    return error.code === "INVALID_HCL_QUOTED_STRING";
  });
  assert.throws(() => renderHclQuotedString("bad\0value"), (error) => {
    assert.ok(error instanceof ProcessFailure);
    return error.code === "INVALID_HCL_QUOTED_STRING";
  });
});

test("parser accepts empty output and rejects noncanonical or incomplete import evidence", () => {
  assert.deepEqual(parseGeneratedImports(RESOURCE_TYPE, ""), []);
  const alpha = renderGeneratedImports(RESOURCE_TYPE, [
    { key: "alpha", importId: "secret-alpha-id" },
  ]);
  const beta = renderGeneratedImports(RESOURCE_TYPE, [
    { key: "beta", importId: "secret-beta-id" },
  ]);
  const canonicalTwo = renderGeneratedImports(RESOURCE_TYPE, [
    { key: "alpha", importId: "secret-alpha-id" },
    { key: "beta", importId: "secret-beta-id" },
  ]);
  const alphaBlock = alpha;
  const betaBlock = beta;

  const malformed = [
    `# comment\n${alpha}`,
    ` ${alpha}`,
    `${alpha}\n`,
    alpha.replaceAll("\n", "\r\n"),
    alpha.replace("import {", "import  {"),
    alpha.replace("  to =", "  from ="),
    alpha.replace(`module.${RESOURCE_TYPE}`, "module.zia_other"),
    alpha.replace(`.${RESOURCE_TYPE}.this`, ".zia_other.this"),
    alpha.replace("\n  id =", "\n  unexpected = \"x\"\n  id ="),
    alpha.replace("\n  id =", "\n  id = \"duplicate\"\n  id ="),
    alpha.replace("\n  id =", ""),
    alpha.slice(0, -1),
    alpha.replace("secret-alpha-id", "bad\\u0020id"),
    alpha.replace("secret-alpha-id", "${raw_interpolation}"),
    `${alphaBlock}\n${alphaBlock}`,
    `${betaBlock}\n${alphaBlock}`,
    canonicalTwo.replace("\n\nimport {", "\nimport {"),
    `${canonicalTwo}unexpected`,
  ];

  for (const text of malformed) {
    assert.throws(() => parseGeneratedImports(RESOURCE_TYPE, text), (error) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.category, "domain");
      assert.equal(error.retryable, false);
      assert.ok(
        error.code === "INVALID_GENERATED_IMPORTS"
        || error.code === "INVALID_HCL_QUOTED_STRING",
      );
      assert.doesNotMatch(error.message, /secret-alpha-id|secret-beta-id/);
      assert.deepEqual(error.details, []);
      return true;
    });
  }
});

test("duplicate addresses and unsafe resource interpolation fail value-safely", () => {
  assert.throws(
    () => renderGeneratedImports(RESOURCE_TYPE, [
      { key: "private-address", importId: "first-private-id" },
      { key: "private-address", importId: "second-private-id" },
    ]),
    (error) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "INVALID_GENERATED_IMPORTS");
      assert.doesNotMatch(error.message, /private|first|second/);
      return true;
    },
  );
  assert.throws(
    () => renderMovedBlocks(
      "zia_rule_labels] malicious-private-text",
      [{ oldKey: "private-old", newKey: "private-new" }],
    ),
    (error) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "INVALID_IMPORT_RESOURCE_TYPE");
      assert.doesNotMatch(error.message, /malicious|private/);
      return true;
    },
  );
});

test("move rendering preserves caller order while derived moves are sorted", () => {
  const unsorted: readonly ImportMove[] = [
    { oldKey: "z", newKey: "z-new" },
    { oldKey: "a", newKey: "a-new" },
  ];
  const rendered = renderMovedBlocks(RESOURCE_TYPE, unsorted);
  assert.ok(rendered.indexOf('this["z"]') < rendered.indexOf('this["a"]'));

  const oldText = renderGeneratedImports(RESOURCE_TYPE, [
    { key: "z", importId: "1" },
    { key: "a", importId: "2" },
  ]);
  const newText = renderGeneratedImports(RESOURCE_TYPE, [
    { key: "z-new", importId: "1" },
    { key: "a-new", importId: "2" },
  ]);
  assert.deepEqual(deriveImportMoves(RESOURCE_TYPE, oldText, newText).moves, [
    { oldKey: "a", newKey: "a-new" },
    { oldKey: "z", newKey: "z-new" },
  ]);
});

test("duplicate-id candidate amplification fails at a value-safe fixed bound", () => {
  const count = 225;
  const oldText = renderGeneratedImports(
    RESOURCE_TYPE,
    Array.from({ length: count }, (_, index) => ({
      key: `old-${String(index).padStart(3, "0")}`,
      importId: "private-repeated-id",
    })),
  );
  const newText = renderGeneratedImports(
    RESOURCE_TYPE,
    Array.from({ length: count }, (_, index) => ({
      key: `new-${String(index).padStart(3, "0")}`,
      importId: "private-repeated-id",
    })),
  );
  assert.throws(
    () => deriveImportMoves(RESOURCE_TYPE, oldText, newText),
    (error) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "IMPORT_MOVE_LIMIT_EXCEEDED");
      assert.doesNotMatch(error.message, /private|old-|new-/);
      assert.deepEqual(error.details, []);
      return true;
    },
  );
});

test("typed boundary is prototype-safe, immutable, and rejects ill-typed values", () => {
  const oldPairs = Object.freeze([
    Object.freeze({ key: "__proto__", importId: "constructor" }),
    Object.freeze({ key: "constructor", importId: "__proto__" }),
  ]);
  const newPairs = Object.freeze([
    Object.freeze({ key: "prototype", importId: "constructor" }),
    Object.freeze({ key: "toString", importId: "__proto__" }),
  ]);
  const oldText = renderGeneratedImports(RESOURCE_TYPE, oldPairs);
  const newText = renderGeneratedImports(RESOURCE_TYPE, newPairs);
  assert.deepEqual(parseGeneratedImports(RESOURCE_TYPE, oldText), oldPairs);
  const result = deriveImportMoves(RESOURCE_TYPE, oldText, newText);
  assert.deepEqual(result.moves, [
    { oldKey: "__proto__", newKey: "prototype" },
    { oldKey: "constructor", newKey: "toString" },
  ]);
  assert.equal(Object.isFrozen(result), true);
  assert.equal(Object.isFrozen(result.moves), true);
  assert.equal(Object.isFrozen(result.moves[0]), true);
  assert.equal(Object.isFrozen(parseGeneratedImports(RESOURCE_TYPE, oldText)), true);
  assert.deepEqual(oldPairs, [
    { key: "__proto__", importId: "constructor" },
    { key: "constructor", importId: "__proto__" },
  ]);

  for (const start of [-1, 0.5, Number.NaN, Number.POSITIVE_INFINITY, 3]) {
    assert.throws(
      () => parseHclQuotedString('"x"', start),
      (error) => error instanceof ProcessFailure
        && error.code === "INVALID_HCL_QUOTED_STRING",
    );
  }
  assert.throws(
    () => parseHclQuotedString(null as unknown as string),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_HCL_QUOTED_STRING",
  );
  assert.throws(
    () => renderGeneratedImports(
      RESOURCE_TYPE,
      null as unknown as readonly GeneratedImportPair[],
    ),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_GENERATED_IMPORTS",
  );
  assert.throws(
    () => renderMovedBlocks(
      RESOURCE_TYPE,
      null as unknown as readonly ImportMove[],
    ),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_IMPORT_MOVES",
  );
});
