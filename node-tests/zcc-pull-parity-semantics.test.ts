import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  validateProcessResponse,
  validateZccPullArtifactParity,
} from "../node-src/contracts/validators.js";
import type { ZccPullArtifactSet } from "../node-src/domain/zcc-pull-artifacts.js";
import { compareZccPullArtifactDigests } from "../node-src/domain/zcc-pull-parity.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";

const SHA_A = "a".repeat(64);
const SHA_B = "b".repeat(64);

function digest(content: string): string {
  return createHash("sha256").update(Buffer.from(content, "utf8")).digest("hex");
}

function candidate(): ZccPullArtifactSet {
  const tfvarsContent = renderPythonLosslessArtifactJson({ items: {} });
  const importsContent = "";
  return {
    kind: "infrawright.zcc_pull_artifact_set",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: "zcc_failopen_policy",
    tenant: "tenant",
    source: {
      path: "pulls/tenant/zcc_failopen_policy.json",
      sha256: SHA_A,
      size_bytes: 2,
    },
    catalog: {
      kind: "infrawright.transform_catalog",
      schema_version: 1,
      sha256: "3900a4d12cd49af7bc8d80248b9c184fa8047ca1987654965a81de87c600937a",
      sources_sha256: "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11",
    },
    root: {
      label: "zcc_failopen_policy",
      members: ["zcc_failopen_policy"],
      variable_name: "items",
    },
    status: "ready",
    unexpected_drops: [],
    artifacts: {
      tfvars: {
        path: "config/tenant/zcc_failopen_policy.auto.tfvars.json",
        media_type: "application/json",
        encoding: "utf-8",
        sha256: digest(tfvarsContent),
        size_bytes: Buffer.byteLength(tfvarsContent, "utf8"),
        content: tfvarsContent,
      },
      imports: {
        path: "imports/tenant/zcc_failopen_policy_imports.tf",
        media_type: "text/x-hcl",
        encoding: "utf-8",
        sha256: digest(importsContent),
        size_bytes: 0,
        content: importsContent,
      },
      lookup: null,
    },
  };
}

function validReport(): Record<string, unknown> {
  return structuredClone(compareZccPullArtifactDigests({
    candidate: candidate(),
    materialized: {
      tfvars: {
        sha256: candidate().artifacts.tfvars.sha256,
        size_bytes: candidate().artifacts.tfvars.size_bytes,
      },
      imports: {
        sha256: candidate().artifacts.imports.sha256,
        size_bytes: candidate().artifacts.imports.size_bytes,
      },
      lookup: null,
    },
  })) as unknown as Record<string, unknown>;
}

function errorsFor(value: unknown): readonly string[] {
  assert.equal(validateZccPullArtifactParity(value), false);
  return (validateZccPullArtifactParity.errors ?? []).map((error) => {
    const params = error.params as { readonly rule?: unknown } | undefined;
    return String(params?.rule ?? error.keyword);
  });
}

test("parity semantic schema accepts the exact digest-only report", () => {
  const report = validReport();
  assert.equal(
    validateZccPullArtifactParity(report),
    true,
    JSON.stringify(validateZccPullArtifactParity.errors),
  );
  assert.equal(JSON.stringify(report).includes("content"), false);
});

test("digest comparison refuses a content-inconsistent candidate", () => {
  const forged = structuredClone(candidate());
  (forged.artifacts.tfvars as unknown as Record<string, unknown>).content = "{}\n";
  assert.throws(
    () => compareZccPullArtifactDigests({
      candidate: forged,
      materialized: {
        tfvars: {
          sha256: forged.artifacts.tfvars.sha256,
          size_bytes: forged.artifacts.tfvars.size_bytes,
        },
        imports: {
          sha256: forged.artifacts.imports.sha256,
          size_bytes: forged.artifacts.imports.size_bytes,
        },
        lookup: null,
      },
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_ARTIFACT_CANDIDATE",
  );
});

test("parity schema rejects artifact sizes above the comparison read limit", () => {
  const report = validReport();
  const parity = report.parity as Record<string, unknown>;
  const artifacts = parity.artifacts as Record<string, unknown>;
  const tfvars = artifacts.tfvars as Record<string, unknown>;
  (tfvars.expected as Record<string, unknown>).size_bytes = 33554433;
  (tfvars.observed as Record<string, unknown>).size_bytes = 33554433;
  assert.equal(validateZccPullArtifactParity(report), false);
});

test("parity semantic schema derives digest statuses, counts, and top status", () => {
  const badDigest = validReport();
  const parity = badDigest.parity as Record<string, unknown>;
  const artifacts = parity.artifacts as Record<string, unknown>;
  const tfvars = artifacts.tfvars as Record<string, unknown>;
  (tfvars.observed as Record<string, unknown>).sha256 = SHA_B;
  assert.ok(errorsFor(badDigest).includes("artifact_status"));

  const badCounts = validReport();
  (badCounts.parity as Record<string, unknown>).matched = 1;
  assert.ok(errorsFor(badCounts).includes("parity_counts"));

  const badParity = validReport();
  (badParity.parity as Record<string, unknown>).status = "different";
  assert.ok(errorsFor(badParity).includes("parity_status"));

  const badTop = validReport();
  badTop.status = "review_required";
  assert.ok(errorsFor(badTop).includes("report_status"));
});

test("parity semantic schema binds source, root, sorted drops, and layout", () => {
  const badSource = validReport();
  (badSource.source as Record<string, unknown>).path =
    "pulls/other/zcc_failopen_policy.json";
  assert.ok(errorsFor(badSource).includes("source_path"));

  const badRoot = validReport();
  (badRoot.root as Record<string, unknown>).variable_name = "other";
  assert.ok(errorsFor(badRoot).includes("variable_name"));

  const generatedLabelGroup = validReport();
  generatedLabelGroup.root = {
    label: "zcc_failopen_policy",
    members: ["zcc_failopen_policy", "zcc_trusted_network"],
    variable_name: "items",
  };
  assert.ok(errorsFor(generatedLabelGroup).includes("root_label"));
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "impossible-root",
    operation: "compare_pull_artifacts",
    status: "ok",
    diagnostics: [],
    result: generatedLabelGroup,
    error: null,
  }), false);

  const crossProviderGeneratedLabel = validReport();
  crossProviderGeneratedLabel.root = {
    label: "zia_admin_users",
    members: ["zcc_failopen_policy"],
    variable_name: "zcc_failopen_policy_items",
  };
  assert.ok(errorsFor(crossProviderGeneratedLabel).includes("root_label"));

  const unknownMember = validReport();
  unknownMember.root = {
    label: "zcc_group",
    members: ["zcc_failopen_policy", "zcc_totally_fake"],
    variable_name: "zcc_failopen_policy_items",
  };
  assert.ok(errorsFor(unknownMember).includes("root_members"));

  const badDrops = validReport();
  badDrops.candidate = {
    status: "review_required",
    unexpected_drops: ["z", "a"],
  };
  badDrops.status = "review_required";
  assert.ok(errorsFor(badDrops).includes("unexpected_drops"));

  const badLayout = validReport();
  const parity = badLayout.parity as Record<string, unknown>;
  const artifacts = parity.artifacts as Record<string, unknown>;
  (artifacts.imports as Record<string, unknown>).path =
    "other/imports/tenant/zcc_failopen_policy_imports.tf";
  assert.ok(errorsFor(badLayout).includes("artifact_layout"));
});

test("parity semantic schema preserves lexical root overlays", () => {
  for (const prefix of ["/", "//", "///"]) {
    const report = validReport();
    const parity = report.parity as Record<string, unknown>;
    const artifacts = parity.artifacts as Record<string, unknown>;
    (artifacts.tfvars as Record<string, unknown>).path =
      `${prefix}config/tenant/zcc_failopen_policy.auto.tfvars.json`;
    (artifacts.imports as Record<string, unknown>).path =
      `${prefix}imports/tenant/zcc_failopen_policy_imports.tf`;
    assert.equal(
      validateZccPullArtifactParity(report),
      true,
      `${prefix}: ${JSON.stringify(validateZccPullArtifactParity.errors)}`,
    );
  }

  const mixed = validReport();
  const parity = mixed.parity as Record<string, unknown>;
  const artifacts = parity.artifacts as Record<string, unknown>;
  (artifacts.tfvars as Record<string, unknown>).path =
    "//config/tenant/zcc_failopen_policy.auto.tfvars.json";
  (artifacts.imports as Record<string, unknown>).path =
    "/imports/tenant/zcc_failopen_policy_imports.tf";
  assert.ok(errorsFor(mixed).includes("artifact_layout"));

  const mixedLookup = validReport();
  mixedLookup.resource_type = "zcc_trusted_network";
  (mixedLookup.source as Record<string, unknown>).path =
    "pulls/tenant/zcc_trusted_network.json";
  mixedLookup.root = {
    label: "zcc_trusted_network",
    members: ["zcc_trusted_network"],
    variable_name: "items",
  };
  const lookupParity = mixedLookup.parity as Record<string, unknown>;
  lookupParity.matched = 3;
  const lookupArtifacts = lookupParity.artifacts as Record<string, unknown>;
  (lookupArtifacts.tfvars as Record<string, unknown>).path =
    "//config/tenant/zcc_trusted_network.auto.tfvars.json";
  (lookupArtifacts.imports as Record<string, unknown>).path =
    "//imports/tenant/zcc_trusted_network_imports.tf";
  const expected = structuredClone(
    (lookupArtifacts.tfvars as Record<string, unknown>).expected,
  );
  lookupArtifacts.lookup = {
    path: "/config/tenant/zcc_trusted_network.lookup.json",
    expected,
    observed: structuredClone(expected),
    status: "match",
  };
  assert.ok(errorsFor(mixedLookup).includes("artifact_layout"));
});
