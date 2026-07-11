import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  validateZccPullRefreshArtifactSet,
} from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  parseGeneratedImports,
  renderGeneratedImports,
  renderMovedBlocks,
} from "../node-src/domain/import-moves.js";
import {
  compileZccPullArtifactSet,
  compileZccPullRefreshCandidateArtifactSet,
  ZCC_TRANSFORM_CATALOG_SHA256,
} from "../node-src/domain/zcc-pull-artifacts.js";
import {
  zccRefreshBaselineFingerprint,
  zccRefreshTransitionFingerprint,
} from "../node-src/domain/zcc-pull-refresh-fingerprints.js";
import {
  compileZccPullRefreshArtifactSet,
  type ZccPullRefreshArtifactSet,
} from "../node-src/domain/zcc-pull-refresh.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";

function candidate(
  rawItems: readonly unknown[] = [{ id: "1", active: "1" }],
) {
  return compileZccPullArtifactSet({
    catalog: loadZccTransformCatalog(),
    catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
    rawItems,
    target: {
      tenant: "refresh",
      resourceType: "zcc_device_cleanup",
      rootLabel: "zcc_device_cleanup",
      rootMembers: ["zcc_device_cleanup"],
      variableName: "items",
      configPath: "config/refresh/zcc_device_cleanup.auto.tfvars.json",
      importsPath: "imports/refresh/zcc_device_cleanup_imports.tf",
      lookupPath: null,
    },
    source: {
      path: "pulls/refresh/zcc_device_cleanup.json",
      sha256: "a".repeat(64),
      size_bytes: 1,
    },
  });
}

function refresh(): ZccPullRefreshArtifactSet {
  const compiled = candidate();
  return compileZccPullRefreshArtifactSet({
    candidate: compiled,
    baselineImports: {
      path: compiled.artifacts.imports.path,
      content: compiled.artifacts.imports.content,
    },
    baselineTfvars: {
      path: compiled.artifacts.tfvars.path,
      state: "present",
      content: Buffer.from("prior tfvars bytes\n"),
    },
    baselineLookup: {
      path: "config/refresh/zcc_device_cleanup.lookup.json",
      state: "absent",
    },
    movesPath: "imports/refresh/zcc_device_cleanup_moves.tf",
    pendingMovesPath:
      "imports/refresh/zcc_device_cleanup_moves.pending.json",
    alternateHclPath:
      "config/refresh/zcc_device_cleanup.auto.tfvars",
    generatedBindingsPath:
      "config/refresh/zcc_device_cleanup.generated.expressions.json",
  });
}

function duplicateTrustedRefresh(): ZccPullRefreshArtifactSet {
  const compiled = compileZccPullRefreshCandidateArtifactSet({
    catalog: loadZccTransformCatalog(),
    catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
    rawItems: [
      { id: "duplicate-id", networkName: "After Alpha" },
      { id: "duplicate-id", networkName: "After Beta" },
    ],
    target: {
      tenant: "refresh",
      resourceType: "zcc_trusted_network",
      rootLabel: "zcc_trusted_network",
      rootMembers: ["zcc_trusted_network"],
      variableName: "items",
      configPath: "config/refresh/zcc_trusted_network.auto.tfvars.json",
      importsPath: "imports/refresh/zcc_trusted_network_imports.tf",
      lookupPath: "config/refresh/zcc_trusted_network.lookup.json",
    },
    source: {
      path: "pulls/refresh/zcc_trusted_network.json",
      sha256: "b".repeat(64),
      size_bytes: 1,
    },
  });
  return compileZccPullRefreshArtifactSet({
    candidate: compiled,
    baselineImports: {
      path: compiled.artifacts.imports.path,
      content: renderGeneratedImports("zcc_trusted_network", [{
        key: "before",
        importId: "duplicate-id",
      }]),
    },
    baselineTfvars: {
      path: compiled.artifacts.tfvars.path,
      state: "absent",
    },
    baselineLookup: {
      path: "config/refresh/zcc_trusted_network.lookup.json",
      state: "absent",
    },
    movesPath: "imports/refresh/zcc_trusted_network_moves.tf",
    pendingMovesPath:
      "imports/refresh/zcc_trusted_network_moves.pending.json",
    alternateHclPath:
      "config/refresh/zcc_trusted_network.auto.tfvars",
    generatedBindingsPath:
      "config/refresh/zcc_trusted_network.generated.expressions.json",
  });
}

function recompute(value: ZccPullRefreshArtifactSet): void {
  const mutable = value as unknown as Record<string, unknown>;
  const baseline = mutable.baseline as Record<string, unknown>;
  baseline.fingerprint_sha256 = zccRefreshBaselineFingerprint({
    tfvars: baseline.tfvars,
    imports: baseline.imports,
    lookup: baseline.lookup,
    moves: baseline.moves,
    pending_moves: baseline.pending_moves,
    alternate_hcl: baseline.alternate_hcl,
    generated_bindings: baseline.generated_bindings,
  });
  mutable.transition_sha256 = zccRefreshTransitionFingerprint({
    product: mutable.product,
    resource_type: mutable.resource_type,
    tenant: mutable.tenant,
    source: mutable.source,
    catalog: mutable.catalog,
    root: mutable.root,
    baseline,
    unexpected_drops: mutable.unexpected_drops,
    moves: mutable.moves,
    desired: mutable.desired,
  });
}

function prefixArtifactPaths(
  value: ZccPullRefreshArtifactSet,
  prefix: string,
): void {
  const baseline = value.baseline as unknown as Record<string, { path: string }>;
  for (const name of [
    "tfvars",
    "imports",
    "lookup",
    "moves",
    "pending_moves",
    "alternate_hcl",
    "generated_bindings",
  ] as const) {
    const state = baseline[name];
    assert.notEqual(state, undefined);
    if (state !== undefined) {
      state.path = `${prefix}${state.path}`;
    }
  }
  const desired = value.desired as unknown as Record<
    string,
    { path?: string; artifact?: { path: string } }
  >;
  for (const name of ["tfvars", "imports", "lookup", "moves"] as const) {
    const state = desired[name];
    assert.notEqual(state, undefined);
    if (state === undefined) {
      continue;
    }
    if (state.artifact === undefined) {
      state.path = `${prefix}${String(state.path)}`;
    } else {
      state.artifact.path = `${prefix}${state.artifact.path}`;
    }
  }
  recompute(value);
}

function setMoveEvidence(
  value: ZccPullRefreshArtifactSet,
  safe: readonly { readonly from_key: string; readonly to_key: string }[],
  suppressed: readonly {
    readonly from_key: string;
    readonly to_key: string;
    readonly reason:
      | "ambiguous"
      | "duplicate_from"
      | "key_swap"
      | "destination_occupied";
  }[] = [],
): void {
  const mutable = value as unknown as Record<string, unknown>;
  const moves = mutable.moves as Record<string, unknown>;
  moves.safe = safe;
  moves.suppressed = suppressed;
  mutable.status = suppressed.length === 0 ? "ready" : "review_required";
  const desired = mutable.desired as Record<string, unknown>;
  if (safe.length === 0) {
    desired.moves = {
      path: value.baseline.moves.path,
      state: "absent",
    };
  } else {
    const content = renderMovedBlocks(
      value.resource_type,
      safe.map((move) => ({
        oldKey: move.from_key,
        newKey: move.to_key,
      })),
    );
    const bytes = Buffer.from(content, "utf8");
    desired.moves = {
      state: "present",
      artifact: {
        path: value.baseline.moves.path,
        media_type: "text/x-hcl",
        encoding: "utf-8",
        sha256: createHash("sha256").update(bytes).digest("hex"),
        size_bytes: bytes.length,
        content,
      },
    };
  }
  recompute(value);
}

function rules(value: unknown): readonly string[] {
  assert.equal(validateZccPullRefreshArtifactSet(value), false);
  return (validateZccPullRefreshArtifactSet.errors ?? []).map((error) => {
    const params = error.params as { readonly rule?: unknown };
    return String(params.rule ?? error.keyword);
  });
}

test("refresh contract is immutable, closed, and has fixed fingerprint vectors", () => {
  const value = refresh();
  assert.equal(
    validateZccPullRefreshArtifactSet(value),
    true,
    JSON.stringify(validateZccPullRefreshArtifactSet.errors),
  );
  assert.equal(value.kind, "infrawright.zcc_pull_refresh_artifact_set");
  assert.equal(value.mode, "refresh");
  assert.equal(value.status, "ready");
  assert.equal(value.baseline.imports.state, "present");
  assert.equal(value.baseline.lookup.state, "absent");
  assert.equal(value.desired.lookup.state, "absent");
  assert.equal(value.desired.moves.state, "absent");
  assert.equal(Object.isFrozen(value), true);
  assert.equal(Object.isFrozen(value.baseline), true);
  assert.equal(Object.isFrozen(value.desired), true);
  assert.equal(
    value.baseline.fingerprint_sha256,
    "f4ea88924b39040e8822e44217dee7738b274a6fb792caf0a1c1ba8419b21ef9",
  );
  assert.equal(
    value.transition_sha256,
    "bd0295e133f52578b9942c944b7b24e44c2c4d3aefef190b2052273dc1e9aa6f",
  );
});

test("fingerprint vectors fix Unicode and control-character canonical bytes", () => {
  const unicodePreimage =
    '{"kind":"infrawright.zcc_refresh_baseline_fingerprint",'
    + '"schema_version":1,"states":{"alternate_hcl":0,'
    + '"generated_bindings":[],"imports":"café","lookup":"雪",'
    + '"moves":null,"pending_moves":false,"tfvars":"東京"}}';
  const unicodeDigest =
    "4b0098aeaa708a17c8598b47c1e976eb33ecd85326f470609ecc333f4377f0a9";
  assert.equal(
    createHash("sha256").update(unicodePreimage, "utf8").digest("hex"),
    unicodeDigest,
  );
  assert.equal(zccRefreshBaselineFingerprint({
    tfvars: "東京",
    imports: "café",
    lookup: "雪",
    moves: null,
    pending_moves: false,
    alternate_hcl: 0,
    generated_bindings: [],
  }), unicodeDigest);

  const controlPreimage =
    '{"baseline":{},"catalog":null,"desired":{"imports":'
    + '{"path":"imports\\r","state":"absent"},"lookup":'
    + '{"path":"lookup\\b","state":"absent"},"moves":'
    + '{"path":"moves\\f","state":"absent"},"tfvars":'
    + '{"path":"line\\n\\t\\u0001","state":"absent"}},'
    + '"kind":"infrawright.zcc_refresh_transition","moves":'
    + '{"safe":[],"suppressed":[]},"product":"zcc",'
    + '"resource_type":"line\\nbreak","root":{},"schema_version":1,'
    + '"source":"\\u0001","tenant":"tab\\tvalue",'
    + '"unexpected_drops":[]}';
  const controlDigest =
    "26738d6cb2e6a12b02bb2b3ff86a1019f278f832793c75f9d981de0bd2436234";
  assert.equal(
    createHash("sha256").update(controlPreimage, "utf8").digest("hex"),
    controlDigest,
  );
  assert.equal(zccRefreshTransitionFingerprint({
    product: "zcc",
    resource_type: "line\nbreak",
    tenant: "tab\tvalue",
    source: "\u0001",
    catalog: null,
    root: {},
    baseline: {},
    unexpected_drops: [],
    moves: { safe: [], suppressed: [] },
    desired: {
      tfvars: { path: "line\n\t\u0001", state: "absent" },
      imports: { path: "imports\r", state: "absent" },
      lookup: { path: "lookup\b", state: "absent" },
      moves: { path: "moves\f", state: "absent" },
    },
  }), controlDigest);
});

test("refresh semantics pin Python's deterministic collapsed lookup winner", () => {
  const value = duplicateTrustedRefresh();
  assert.equal(
    validateZccPullRefreshArtifactSet(value),
    true,
    JSON.stringify(validateZccPullRefreshArtifactSet.errors),
  );
  assert.equal(value.desired.lookup.state, "present");
  if (value.desired.lookup.state === "present") {
    assert.match(value.desired.lookup.artifact.content, /"after_beta"/u);
  }

  const forged = structuredClone(value);
  assert.equal(forged.desired.lookup.state, "present");
  if (forged.desired.lookup.state === "present") {
    const content = forged.desired.lookup.artifact.content.replace(
      '"duplicate-id": "after_beta"',
      '"duplicate-id": "after_alpha"',
    );
    const artifact = forged.desired.lookup.artifact as {
      content: string;
      sha256: string;
      size_bytes: number;
    };
    artifact.content = content;
    artifact.sha256 = createHash("sha256").update(content, "utf8").digest("hex");
    artifact.size_bytes = Buffer.byteLength(content, "utf8");
  }
  recompute(forged);
  assert.ok(rules(forged).includes("lookup_join"));

  const forgedDisplay = structuredClone(value);
  assert.equal(forgedDisplay.desired.lookup.state, "present");
  if (forgedDisplay.desired.lookup.state === "present") {
    const content = forgedDisplay.desired.lookup.artifact.content.replace(
      '"duplicate-id": "After Beta"',
      '"duplicate-id": "After Alpha"',
    );
    const artifact = forgedDisplay.desired.lookup.artifact as {
      content: string;
      sha256: string;
      size_bytes: number;
    };
    artifact.content = content;
    artifact.sha256 = createHash("sha256").update(content, "utf8").digest("hex");
    artifact.size_bytes = Buffer.byteLength(content, "utf8");
  }
  recompute(forgedDisplay);
  assert.ok(rules(forgedDisplay).includes("lookup_join"));
});

test("exported fingerprint helpers reject cyclic and over-depth graphs", () => {
  const cycle: Record<string, unknown> = {};
  cycle.self = cycle;
  const deep: Record<string, unknown> = {};
  let cursor = deep;
  for (let index = 0; index < 20_000; index += 1) {
    const child: Record<string, unknown> = {};
    cursor.child = child;
    cursor = child;
  }
  for (const tfvars of [cycle, deep]) {
    assert.throws(
      () => zccRefreshBaselineFingerprint({
        tfvars,
        imports: null,
        lookup: null,
        moves: null,
        pending_moves: null,
        alternate_hcl: null,
        generated_bindings: null,
      }),
      (error: unknown) => error instanceof TypeError && !(error instanceof RangeError),
    );
  }
});

test("direct constructor validates and snapshots every typed boundary", () => {
  const compiled = candidate();
  const tfvarsBytes = Buffer.from("mutable baseline\n");
  const value = compileZccPullRefreshArtifactSet({
    candidate: compiled,
    baselineImports: {
      path: compiled.artifacts.imports.path,
      content: compiled.artifacts.imports.content,
    },
    baselineTfvars: {
      path: compiled.artifacts.tfvars.path,
      state: "present",
      content: tfvarsBytes,
    },
    baselineLookup: {
      path: "config/refresh/zcc_device_cleanup.lookup.json",
      state: "absent",
    },
    movesPath: "imports/refresh/zcc_device_cleanup_moves.tf",
    pendingMovesPath:
      "imports/refresh/zcc_device_cleanup_moves.pending.json",
    alternateHclPath:
      "config/refresh/zcc_device_cleanup.auto.tfvars",
    generatedBindingsPath:
      "config/refresh/zcc_device_cleanup.generated.expressions.json",
  });
  tfvarsBytes.fill(0);
  assert.equal(value.baseline.tfvars.state, "present");
  assert.notEqual(
    value.baseline.tfvars.state === "present"
      ? value.baseline.tfvars.sha256
      : "",
    "0".repeat(64),
  );

  const invalidCandidate = structuredClone(compiled) as unknown as Record<
    string,
    unknown
  >;
  invalidCandidate.status = "review_required";
  assert.throws(
    () => compileZccPullRefreshArtifactSet({
      candidate: invalidCandidate as never,
      baselineImports: {
        path: compiled.artifacts.imports.path,
        content: compiled.artifacts.imports.content,
      },
      baselineTfvars: {
        path: compiled.artifacts.tfvars.path,
        state: "absent",
      },
      baselineLookup: {
        path: "config/refresh/zcc_device_cleanup.lookup.json",
        state: "absent",
      },
      movesPath: "imports/refresh/zcc_device_cleanup_moves.tf",
      pendingMovesPath:
        "imports/refresh/zcc_device_cleanup_moves.pending.json",
      alternateHclPath:
        "config/refresh/zcc_device_cleanup.auto.tfvars",
      generatedBindingsPath:
        "config/refresh/zcc_device_cleanup.generated.expressions.json",
    }),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_REFRESH_CANDIDATE",
  );
  assert.throws(
    () => compileZccPullRefreshArtifactSet({
      candidate: compiled,
      baselineImports: {
        path: compiled.artifacts.imports.path,
        content: compiled.artifacts.imports.content,
      },
      baselineTfvars: {
        path: compiled.artifacts.tfvars.path,
        state: "absent",
      },
      baselineLookup: {
        path: "config/refresh/zcc_device_cleanup.lookup.json",
        state: "absent",
      },
      movesPath: "imports/refresh/not-the-sibling_moves.tf",
      pendingMovesPath:
        "imports/refresh/zcc_device_cleanup_moves.pending.json",
      alternateHclPath:
        "config/refresh/zcc_device_cleanup.auto.tfvars",
      generatedBindingsPath:
        "config/refresh/zcc_device_cleanup.generated.expressions.json",
    }),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_REFRESH_BASELINE",
  );

  for (const invalid of [
    null,
    {},
    { candidate: compiled },
    {
      candidate: compiled,
      baselineImports: { path: 1, content: "" },
      baselineTfvars: {},
      baselineLookup: {},
      movesPath: 1,
      pendingMovesPath: 1,
      alternateHclPath: 1,
      generatedBindingsPath: 1,
    },
  ]) {
    assert.throws(
      () => compileZccPullRefreshArtifactSet(invalid as never),
      (error) => error instanceof ProcessFailure
        && error.code === "INVALID_ZCC_REFRESH_BASELINE",
    );
  }
  let candidateGetterCalled = false;
  const accessor = Object.defineProperty({
    baselineImports: { path: "x", content: "" },
    baselineTfvars: {},
    baselineLookup: {},
    movesPath: "x",
    pendingMovesPath: "x",
    alternateHclPath: "x",
    generatedBindingsPath: "x",
  }, "candidate", {
    enumerable: true,
    get: () => {
      candidateGetterCalled = true;
      return compiled;
    },
  });
  assert.throws(
    () => compileZccPullRefreshArtifactSet(accessor as never),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_REFRESH_CANDIDATE",
  );
  assert.equal(candidateGetterCalled, false);
  let baselineGetterCalled = false;
  const baselineAccessor = Object.defineProperty({
    path: compiled.artifacts.tfvars.path,
    state: "present",
  }, "content", {
    enumerable: true,
    get: () => {
      baselineGetterCalled = true;
      return Buffer.from("private accessor bytes");
    },
  });
  assert.throws(
    () => compileZccPullRefreshArtifactSet({
      candidate: compiled,
      baselineImports: {
        path: compiled.artifacts.imports.path,
        content: compiled.artifacts.imports.content,
      },
      baselineTfvars: baselineAccessor as never,
      baselineLookup: {
        path: "config/refresh/zcc_device_cleanup.lookup.json",
        state: "absent",
      },
      movesPath: "imports/refresh/zcc_device_cleanup_moves.tf",
      pendingMovesPath:
        "imports/refresh/zcc_device_cleanup_moves.pending.json",
      alternateHclPath:
        "config/refresh/zcc_device_cleanup.auto.tfvars",
      generatedBindingsPath:
        "config/refresh/zcc_device_cleanup.generated.expressions.json",
    }),
    (error) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_REFRESH_BASELINE",
  );
  assert.equal(baselineGetterCalled, false);
});

test("semantic validator rejects forged baselines, moves, joins, and Unicode", () => {
  const oversized = structuredClone(refresh());
  assert.equal(oversized.baseline.tfvars.state, "present");
  if (oversized.baseline.tfvars.state === "present") {
    (oversized.baseline.tfvars as { size_bytes: number }).size_bytes =
      32 * 1024 * 1024 + 1;
  }
  recompute(oversized);
  assert.ok(rules(oversized).includes("baseline_bounds"));

  const badBaselineHash = structuredClone(refresh());
  (badBaselineHash.baseline as { fingerprint_sha256: string })
    .fingerprint_sha256 = "0".repeat(64);
  assert.ok(rules(badBaselineHash).includes("baseline_fingerprint"));

  const badTransition = structuredClone(refresh());
  (badTransition as { transition_sha256: string }).transition_sha256 =
    "0".repeat(64);
  assert.ok(rules(badTransition).includes("transition_fingerprint"));

  const badJoin = structuredClone(refresh());
  (badJoin.baseline.tfvars as { path: string }).path = "wrong/path";
  recompute(badJoin);
  assert.ok(rules(badJoin).includes("baseline_join"));

  const forgedLookup = structuredClone(refresh());
  (forgedLookup.baseline.lookup as { path: string }).path =
    "config/refresh/forged.lookup.json";
  assert.equal(forgedLookup.desired.lookup.state, "absent");
  if (forgedLookup.desired.lookup.state === "absent") {
    (forgedLookup.desired.lookup as { path: string }).path =
      "config/refresh/forged.lookup.json";
  }
  recompute(forgedLookup);
  assert.ok(rules(forgedLookup).includes("adjacent_paths"));

  const externalLookup = structuredClone(refresh());
  prefixArtifactPaths(externalLookup, "/external-overlay/");
  assert.equal(
    validateZccPullRefreshArtifactSet(externalLookup),
    true,
    JSON.stringify(validateZccPullRefreshArtifactSet.errors),
  );
  (externalLookup.baseline.lookup as { path: string }).path =
    "/external-overlay/forged/zcc_device_cleanup.lookup.json";
  assert.equal(externalLookup.desired.lookup.state, "absent");
  if (externalLookup.desired.lookup.state === "absent") {
    (externalLookup.desired.lookup as { path: string }).path =
      externalLookup.baseline.lookup.path;
  }
  recompute(externalLookup);
  assert.ok(rules(externalLookup).includes("adjacent_paths"));

  const badUnicode = structuredClone(refresh());
  assert.equal(badUnicode.desired.tfvars.state, "present");
  if (badUnicode.desired.tfvars.state === "present") {
    (badUnicode.desired.tfvars.artifact as { content: string }).content =
      "\ud800";
  }
  assert.ok(rules(badUnicode).includes("well_formed_unicode"));

  for (const [field, badPath] of [
    ["alternate_hcl", "config/refresh/wrong.auto.tfvars"],
    ["generated_bindings", "config/refresh/wrong.generated.json"],
  ] as const) {
    const badAdjacent = structuredClone(refresh());
    (badAdjacent.baseline[field] as { path: string }).path = badPath;
    recompute(badAdjacent);
    assert.ok(rules(badAdjacent).includes("adjacent_paths"), field);
  }

  const nulPath = structuredClone(refresh());
  (nulPath.baseline.moves as { path: string }).path =
    "imports/refresh/\0zcc_device_cleanup_moves.tf";
  assert.equal(nulPath.desired.moves.state, "absent");
  if (nulPath.desired.moves.state === "absent") {
    (nulPath.desired.moves as { path: string }).path =
      nulPath.baseline.moves.path;
  }
  recompute(nulPath);
  assert.ok(rules(nulPath).includes("path_characters"));
});

test("semantic move joins reject missing, duplicate, chain, and cross-chain targets", () => {
  const base = refresh();
  assert.equal(base.desired.imports.state, "present");
  const target = parseGeneratedImports(
    base.resource_type,
    base.desired.imports.state === "present"
      ? base.desired.imports.artifact.content
      : "",
  )[0]?.key;
  assert.notEqual(target, undefined);

  for (const [name, safe] of [
    ["missing", [{ from_key: "old", to_key: "missing" }]],
    [
      "duplicate",
      [
        { from_key: "old-a", to_key: target ?? "" },
        { from_key: "old-b", to_key: target ?? "" },
      ],
    ],
    [
      "chain",
      [
        { from_key: "old", to_key: target ?? "" },
        { from_key: target ?? "", to_key: "missing" },
      ],
    ],
  ] as const) {
    const forged = structuredClone(base);
    setMoveEvidence(forged, safe);
    assert.ok(rules(forged).includes("move_join"), name);
  }

  const compiled = candidate([
    { id: "1", active: "1" },
    { id: "2", active: "1" },
  ]);
  const two = compileZccPullRefreshArtifactSet({
    candidate: compiled,
    baselineImports: {
      path: compiled.artifacts.imports.path,
      content: compiled.artifacts.imports.content,
    },
    baselineTfvars: {
      path: compiled.artifacts.tfvars.path,
      state: "absent",
    },
    baselineLookup: {
      path: "config/refresh/zcc_device_cleanup.lookup.json",
      state: "absent",
    },
    movesPath: "imports/refresh/zcc_device_cleanup_moves.tf",
    pendingMovesPath:
      "imports/refresh/zcc_device_cleanup_moves.pending.json",
    alternateHclPath:
      "config/refresh/zcc_device_cleanup.auto.tfvars",
    generatedBindingsPath:
      "config/refresh/zcc_device_cleanup.generated.expressions.json",
  });
  assert.equal(two.desired.imports.state, "present");
  const keys = parseGeneratedImports(
    two.resource_type,
    two.desired.imports.state === "present"
      ? two.desired.imports.artifact.content
      : "",
  ).map((entry) => entry.key);
  assert.equal(keys.length, 2);
  const crossChain = structuredClone(two);
  setMoveEvidence(
    crossChain,
    [{ from_key: "old", to_key: keys[0] ?? "" }],
    [{
      from_key: keys[0] ?? "",
      to_key: keys[1] ?? "",
      reason: "destination_occupied",
    }],
  );
  assert.ok(rules(crossChain).includes("move_join"));
});
