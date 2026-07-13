import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import { stringify as stringifyLosslessJson } from "lossless-json";

import {
  compileZccAdoptionArtifactSet,
  ZCC_ADOPTION_CATALOG_SHA256,
  type ZccAdoptionArtifactSet,
} from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  type ZccAdoptionStateObservation,
} from "../node-src/domain/zcc-adoption-projection.js";
import type {
  ZccArtifactTarget,
  ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";

const WORKSPACE = process.cwd();
const SOURCE_DIGEST = "7".repeat(64);
const CORPUS_PATH = path.join(
  WORKSPACE,
  "node-tests/fixtures/zcc-adoption-projection-corpus.v1.json",
);
const FAILOPEN_PATH = path.join(
  WORKSPACE,
  "tests/fixtures/parity/zcc_failopen_policy_inversion.json",
);
const RESOURCE_TYPES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

interface SuccessCase {
  readonly name: string;
  readonly resource_type: ZccPullResourceType;
  readonly raw_items: readonly unknown[];
  readonly observed_states: readonly ZccAdoptionStateObservation[];
}

interface PythonArtifacts {
  readonly name: string;
  readonly tfvars: string;
  readonly imports: string;
  readonly lookup: string | null;
}

// Credential-free artifact parity runs Python identity, projection, and
// renderers over injected sanitized observations. It is not a live import;
// observation resource/provider/address binding has separate Node-only tests.
const PYTHON_ARTIFACT_ORACLE = String.raw`
import json
import sys

from engine import adopt
from engine import adoption_meta
from engine import lookup
from engine import transform


payload = json.load(sys.stdin)
results = []
for case in payload["cases"]:
    observations = dict((entry["key"], entry) for entry in case["observed_states"])

    def state_loader(resource_type, key_to_import_id, policy=None, raw_items=None):
        del resource_type, policy, raw_items
        if set(observations) != set(key_to_import_id):
            raise ValueError("oracle state keys do not match")
        for key, import_id in key_to_import_id.items():
            if observations[key]["import_id"] != str(import_id):
                raise ValueError("oracle import id does not match")
        return dict(
            (key, {
                "values": observations[key]["values"],
                "sensitive_values": observations[key].get("sensitive_values") or {},
            })
            for key in key_to_import_id
        )

    resource_type = case["resource_type"]
    items, identities = adopt.adopt_items(
        case["raw_items"], resource_type, state_loader=state_loader)
    tfvars = transform.render_tfvars(items, var_name=case["variable_name"])
    imports = transform.render_imports(
        resource_type,
        identities,
        {"import_id": adoption_meta.adoption_entry(resource_type)["import_id"]},
    )
    lookup_text = None
    source = lookup.lookup_sources().get(resource_type)
    if source is not None:
        merged_items = {}
        for key in sorted(items):
            merged = dict(identities.get(key) or {})
            merged.update(items[key])
            merged_items[key] = merged
        lookup_text = lookup.render_lookup(
            lookup.build_lookup(merged_items, source["name_field"]),
            key_mapping=lookup.build_lookup_key_map(merged_items),
        )
    results.append({
        "name": case["name"],
        "tfvars": tfvars,
        "imports": imports,
        "lookup": lookup_text,
    })

json.dump(results, sys.stdout, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
`;

function target(resourceType: ZccPullResourceType): ZccArtifactTarget {
  return {
    tenant: "demo",
    resourceType,
    rootLabel: resourceType,
    rootMembers: [resourceType],
    variableName: "items",
    configPath: `overlay/config/demo/${resourceType}.auto.tfvars.json`,
    importsPath: `overlay/imports/demo/${resourceType}_imports.tf`,
    lookupPath: resourceType === "zcc_trusted_network"
      ? `overlay/config/demo/${resourceType}.lookup.json`
      : null,
  };
}

function compile(fixture: SuccessCase): ZccAdoptionArtifactSet {
  const catalog = loadZccAdoptionCatalog();
  return compileZccAdoptionArtifactSet({
    catalog,
    catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
    observedStates: fixture.observed_states,
    rawItems: fixture.raw_items,
    source: {
      path: `pulls/demo/${fixture.resource_type}.json`,
      sha256: SOURCE_DIGEST,
      size_bytes: 123,
    },
    target: target(fixture.resource_type),
  });
}

function verifyDescriptor(
  artifact: ZccAdoptionArtifactSet["artifacts"]["tfvars"],
): void {
  const bytes = Buffer.from(artifact.content, "utf8");
  assert.equal(artifact.size_bytes, bytes.length);
  assert.equal(
    artifact.sha256,
    createHash("sha256").update(bytes).digest("hex"),
  );
  assert.equal(artifact.encoding, "utf-8");
}

function isResourceType(value: string): value is ZccPullResourceType {
  return (RESOURCE_TYPES as readonly string[]).includes(value);
}

async function loadSuccessCases(): Promise<readonly SuccessCase[]> {
  const corpus = parseDataJsonLosslessly(await readFile(CORPUS_PATH, "utf8")) as {
    readonly cases: readonly {
      readonly name: string;
      readonly resource_type: string;
      readonly raw_items: readonly unknown[];
      readonly observed_states: readonly ZccAdoptionStateObservation[];
      readonly expected: string;
    }[];
  };
  const cases = corpus.cases.flatMap((fixture): readonly SuccessCase[] => {
    if (fixture.expected !== "success" || fixture.raw_items.length === 0) {
      return [];
    }
    assert.ok(isResourceType(fixture.resource_type));
    return [{
      name: fixture.name,
      resource_type: fixture.resource_type,
      raw_items: fixture.raw_items,
      observed_states: fixture.observed_states,
    }];
  });

  const failopen = parseDataJsonLosslessly(await readFile(FAILOPEN_PATH, "utf8")) as {
    readonly raw_items: readonly unknown[];
    readonly provider_state: Readonly<Record<string, {
      readonly values: unknown;
      readonly sensitive_values?: unknown;
    }>>;
  };
  const state = failopen.provider_state["policy-001"];
  assert.notEqual(state, undefined);
  return [{
    name: "source-derived-sanitized-failopen-inversion-control",
    resource_type: "zcc_failopen_policy",
    raw_items: failopen.raw_items,
    observed_states: [{
      address: "zcc_failopen_policy.iw_a535a60194bc40a4",
      key: "policy_001",
      import_id: "policy-001",
      provider_name: "registry.terraform.io/zscaler/zcc",
      resource_type: "zcc_failopen_policy",
      values: state?.values,
      sensitive_values: state?.sensitive_values,
    }],
  }, ...cases];
}

function pythonArtifacts(cases: readonly SuccessCase[]): readonly PythonArtifacts[] {
  const result = spawnSync(PYTHON_ORACLE, ["-c", PYTHON_ARTIFACT_ORACLE], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: stringifyLosslessJson({
      cases: cases.map((fixture) => ({ ...fixture, variable_name: "items" })),
    }),
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout) as readonly PythonArtifacts[];
}

test("all five ZCC adoption resources render byte-identically to Python", async () => {
  const cases = await loadSuccessCases();
  assert.deepEqual(
    [...new Set(cases.map((fixture) => fixture.resource_type))].sort(),
    [...RESOURCE_TYPES].sort(),
  );
  const expected = new Map(
    pythonArtifacts(cases).map((entry) => [entry.name, entry] as const),
  );
  for (const fixture of cases) {
    const result = compile(fixture);
    const oracle = expected.get(fixture.name);
    assert.notEqual(oracle, undefined, fixture.name);
    assert.equal(result.artifacts.tfvars.content, oracle?.tfvars, fixture.name);
    assert.equal(result.artifacts.imports.content, oracle?.imports, fixture.name);
    assert.equal(result.artifacts.lookup?.content ?? null, oracle?.lookup, fixture.name);
    assert.equal(result.resource_type, fixture.resource_type);
    assert.equal(result.catalog.sha256, ZCC_ADOPTION_CATALOG_SHA256);
    assert.equal(
      result.catalog.sources_sha256,
      loadZccAdoptionCatalog().sources_sha256,
    );
    verifyDescriptor(result.artifacts.tfvars);
    verifyDescriptor(result.artifacts.imports);
    if (result.artifacts.lookup !== null) {
      verifyDescriptor(result.artifacts.lookup);
    }
    assert.equal(Object.isFrozen(result), true);
    assert.equal(Object.isFrozen(result.artifacts), true);
  }
});

test("exported adoption catalog digest matches the committed bytes", async () => {
  const bytes = await readFile(
    path.join(WORKSPACE, "catalogs/zcc-adoption-catalog.v1.json"),
  );
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    ZCC_ADOPTION_CATALOG_SHA256,
  );
});

test("artifact compilation rejects a proxied catalog before traps", () => {
  let trapCalls = 0;
  const catalog = loadZccAdoptionCatalog();
  const proxy = new Proxy(catalog, {
    get(targetValue, property, receiver) {
      trapCalls += 1;
      return Reflect.get(targetValue, property, receiver);
    },
    getPrototypeOf(targetValue) {
      trapCalls += 1;
      return Reflect.getPrototypeOf(targetValue);
    },
  });
  assert.throws(
    () => compileZccAdoptionArtifactSet({
      catalog: proxy,
      catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
      observedStates: [],
      rawItems: [],
      source: {
        path: "pulls/demo/zcc_device_cleanup.json",
        sha256: SOURCE_DIGEST,
        size_bytes: 0,
      },
      target: target("zcc_device_cleanup"),
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_ADOPTION_CATALOG",
  );
  assert.equal(trapCalls, 0);
});

test("trusted-network lookup keeps raw identity key and provider display name", async () => {
  const fixture = (await loadSuccessCases()).find((entry) => {
    return entry.resource_type === "zcc_trusted_network";
  });
  assert.notEqual(fixture, undefined);
  if (fixture === undefined) {
    return;
  }
  const result = compile(fixture);
  assert.match(result.artifacts.tfvars.content, /Provider & Identity/);
  assert.match(result.artifacts.tfvars.content, /\\u6771\\u4eac/);
  assert.equal(
    result.artifacts.lookup?.content,
    "{\n"
      + "  \"by_id\": {\n"
      + "    \"tn-1\": \"Provider & Identity \\u6771\\u4eac \\ud83d\\ude00\"\n"
      + "  },\n"
      + "  \"key_by_id\": {\n"
      + "    \"tn-1\": \"raw_identity_amp\"\n"
      + "  }\n"
      + "}\n",
  );
  assert.doesNotMatch(result.artifacts.lookup?.content ?? "", /Raw Identity/);
  assert.doesNotMatch(result.artifacts.lookup?.content ?? "", /&amp;/);
});

test("adoption imports preserve Python HCL escaping and unbounded integer IDs", async () => {
  const importId = 'quote"\\line\nrow\rcol\t${name}%{ if true }';
  const escaped = compile({
    name: "escaped-import-id",
    resource_type: "zcc_forwarding_profile",
    raw_items: [{ id: importId, name: "Escaping 東京" }],
    observed_states: [{
      address: "zcc_forwarding_profile.iw_bef0d9f5eae9666e",
      key: "escaping",
      import_id: importId,
      provider_name: "registry.terraform.io/zscaler/zcc",
      resource_type: "zcc_forwarding_profile",
      values: { name: "Escaping 東京" },
    }],
  });
  assert.equal(
    escaped.artifacts.imports.content,
    "import {\n"
      + "  to = module.zcc_forwarding_profile.zcc_forwarding_profile.this[\"escaping\"]\n"
      + "  id = \"quote\\\"\\\\line\\nrow\\rcol\\t$${name}%%{ if true }\"\n"
      + "}\n",
  );
  assert.match(escaped.artifacts.tfvars.content, /\\u6771\\u4eac/);

  const device = (await loadSuccessCases()).find((entry) => {
    return entry.resource_type === "zcc_device_cleanup";
  });
  assert.notEqual(device, undefined);
  if (device !== undefined) {
    assert.match(
      compile(device).artifacts.imports.content,
      /id = "900719925474099312345678901"/,
    );
  }
});

test("empty adoption emits complete bootstrap artifacts for all five resources", () => {
  for (const resourceType of RESOURCE_TYPES) {
    const result = compile({
      name: `empty-${resourceType}`,
      resource_type: resourceType,
      raw_items: [],
      observed_states: [],
    });
    assert.equal(result.artifacts.tfvars.content, '{\n  "items": {}\n}\n');
    assert.equal(result.artifacts.imports.content, "");
    assert.equal(
      result.artifacts.lookup?.content ?? null,
      resourceType === "zcc_trusted_network" ? "{}\n" : null,
    );
  }
});
