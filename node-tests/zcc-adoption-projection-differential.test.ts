import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import {
  stringify as stringifyLosslessJson,
} from "lossless-json";

import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import {
  compileZccAdoptionProjection,
} from "../node-src/domain/zcc-adoption-projection.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import { terraformJsonEqual } from "../node-src/json/python-equality.js";
import {
  renderPythonLosslessArtifactJson,
} from "../node-src/json/python-lossless-artifact.js";

const WORKSPACE = process.cwd();
const CORPUS_PATH = path.join(
  WORKSPACE,
  "node-tests/fixtures/zcc-adoption-projection-corpus.v1.json",
);
const SOURCE_DERIVED_FAILOPEN_PATH = path.join(
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
type ResourceType = typeof RESOURCE_TYPES[number];
type ExpectedOutcome =
  | "success"
  | "identity_error"
  | "state_join_error"
  | "projection_error";

interface Observation {
  readonly address: string;
  readonly key: string;
  readonly import_id: string;
  readonly provider_name: string;
  readonly resource_type: ResourceType;
  readonly values: unknown;
  readonly sensitive_values?: unknown;
}

interface DifferentialCase {
  readonly name: string;
  readonly resource_type: ResourceType;
  readonly expected: ExpectedOutcome;
  readonly raw_items: readonly unknown[];
  readonly observed_states: readonly Observation[];
  readonly secret_tokens?: readonly string[];
  readonly provenance_status: "synthetic_sanitized" | "source_derived";
}

interface Corpus {
  readonly fixture_version: 1;
  readonly provenance: {
    readonly status: "synthetic_sanitized";
    readonly note: string;
  };
  readonly cases: readonly Omit<DifferentialCase, "provenance_status">[];
}

interface PythonSuccess {
  readonly ok: true;
  readonly items: unknown;
  readonly identities: unknown;
  readonly import_ids: unknown;
  readonly loader_calls: number;
  readonly rendered: {
    readonly items: string;
    readonly identities: string;
    readonly import_ids: string;
  };
}

interface PythonFailure {
  readonly ok: false;
  readonly phase: Exclude<ExpectedOutcome, "success">;
  readonly error_type: string;
  readonly message: string;
  readonly loader_calls: number;
}

interface PythonCaseResult {
  readonly name: string;
  readonly result: PythonSuccess | PythonFailure;
}

// Credential-free parity only: this runs Python identity and projection with
// injected sanitized observations, not Terraform or a live provider oracle.
// Node-only resource/provider/scratch-address binding is covered separately
// by zcc-adoption-projection-security.test.ts.
const PYTHON_IDENTITY_PROJECTION_ORACLE = String.raw`
import json
import sys

from engine import adopt
from engine.state_project import ProjectionError


class StateJoinError(ValueError):
    pass


def render(value):
    return json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n"


payload = json.load(sys.stdin)
results = []
for case in payload["cases"]:
    loader_calls = [0]
    requested_import_ids = {}

    def state_loader(resource_type, key_to_import_id, policy=None, raw_items=None):
        del resource_type, policy, raw_items
        loader_calls[0] += 1
        requested_import_ids.update(
            (key, str(import_id))
            for key, import_id in key_to_import_id.items()
        )
        observations = case["observed_states"]
        by_key = {}
        seen_import_ids = set()
        for observation in observations:
            key = observation["key"]
            import_id = observation["import_id"]
            if key in by_key:
                raise StateJoinError("duplicate observed key")
            if import_id in seen_import_ids:
                raise StateJoinError("duplicate observed import id")
            by_key[key] = observation
            seen_import_ids.add(import_id)
        if set(by_key) != set(key_to_import_id):
            raise StateJoinError("observed keys do not exactly match requested keys")
        for key, expected_import_id in key_to_import_id.items():
            if by_key[key]["import_id"] != str(expected_import_id):
                raise StateJoinError("observed import id does not match requested key")
        return {
            key: {
                "values": by_key[key]["values"],
                "sensitive_values": by_key[key].get("sensitive_values") or {},
            }
            for key in key_to_import_id
        }

    try:
        items, identities = adopt.adopt_items(
            case["raw_items"],
            case["resource_type"],
            state_loader=state_loader,
        )
        result = {
            "ok": True,
            "items": items,
            "identities": identities,
            "import_ids": requested_import_ids,
            "loader_calls": loader_calls[0],
            "rendered": {
                "items": render(items),
                "identities": render(identities),
                "import_ids": render(requested_import_ids),
            },
        }
    except Exception as exc:
        if isinstance(exc, StateJoinError):
            phase = "state_join_error"
        elif isinstance(exc, ProjectionError):
            phase = "projection_error"
        else:
            phase = "identity_error"
        result = {
            "ok": False,
            "phase": phase,
            "error_type": type(exc).__name__,
            "message": str(exc),
            "loader_calls": loader_calls[0],
        }
    results.append({"name": case["name"], "result": result})

json.dump(results, sys.stdout, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
`;

function isResourceType(value: string): value is ResourceType {
  return (RESOURCE_TYPES as readonly string[]).includes(value);
}

async function loadCases(): Promise<readonly DifferentialCase[]> {
  const corpus = parseDataJsonLosslessly(
    await readFile(CORPUS_PATH, "utf8"),
  ) as Corpus;
  assert.equal(String(corpus.fixture_version), "1");
  assert.equal(corpus.provenance.status, "synthetic_sanitized");
  assert.match(corpus.provenance.note, /synthetic and sanitized/i);
  const synthetic = corpus.cases.map((fixture): DifferentialCase => {
    assert.ok(isResourceType(fixture.resource_type));
    return { ...fixture, provenance_status: "synthetic_sanitized" };
  });

  const sourceFixture = parseDataJsonLosslessly(
    await readFile(SOURCE_DERIVED_FAILOPEN_PATH, "utf8"),
  ) as {
    readonly resource_type: string;
    readonly raw_items: readonly unknown[];
    readonly provider_state: Readonly<Record<string, {
      readonly values: unknown;
      readonly sensitive_values?: unknown;
    }>>;
    readonly provenance: {
      readonly status: string;
      readonly sanitized: boolean;
    };
  };
  assert.equal(sourceFixture.resource_type, "zcc_failopen_policy");
  assert.equal(sourceFixture.provenance.status, "source_derived");
  assert.equal(sourceFixture.provenance.sanitized, true);
  assert.deepEqual(Object.keys(sourceFixture.provider_state), ["policy-001"]);
  const state = sourceFixture.provider_state["policy-001"];
  assert.notEqual(state, undefined);
  const sourceDerived: DifferentialCase = {
    name: "source-derived-sanitized-failopen-inversion-control",
    resource_type: "zcc_failopen_policy",
    expected: "success",
    raw_items: sourceFixture.raw_items,
    observed_states: [{
      address: "zcc_failopen_policy.iw_a535a60194bc40a4",
      key: "policy_001",
      import_id: "policy-001",
      provider_name: "registry.terraform.io/zscaler/zcc",
      resource_type: "zcc_failopen_policy",
      values: state?.values,
      sensitive_values: state?.sensitive_values,
    }],
    provenance_status: "source_derived",
  };
  return [sourceDerived, ...synthetic];
}

function pythonResults(
  cases: readonly DifferentialCase[],
): readonly PythonCaseResult[] {
  const child = spawnSync("python3", ["-c", PYTHON_IDENTITY_PROJECTION_ORACLE], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: stringifyLosslessJson({ cases: cases.map((fixture) => ({
      name: fixture.name,
      resource_type: fixture.resource_type,
      raw_items: fixture.raw_items,
      observed_states: fixture.observed_states,
    })) }),
    maxBuffer: 32 * 1024 * 1024,
  });
  assert.equal(child.status, 0, child.stderr);
  assert.equal(child.stderr, "");
  return parseDataJsonLosslessly(child.stdout) as readonly PythonCaseResult[];
}

function nodeError(error: unknown): ProcessFailure {
  assert.ok(error instanceof ProcessFailure, String(error));
  return error;
}

function errorText(error: ProcessFailure): string {
  return JSON.stringify({
    name: error.name,
    code: error.code,
    category: error.category,
    message: error.message,
    details: error.details,
  });
}

const ERROR_CODES: Readonly<Record<Exclude<ExpectedOutcome, "success">, string>> = {
  identity_error: "ZCC_ADOPTION_IDENTITY_FAILED",
  state_join_error: "ZCC_ADOPTION_STATE_JOIN_FAILED",
  projection_error: "ZCC_ADOPTION_PROJECTION_FAILED",
};

test("credential-free Node identity/projection matches Python adopt_items", async () => {
  const cases = await loadCases();
  const expected = pythonResults(cases);
  assert.equal(expected.length, cases.length);
  const catalog = loadZccAdoptionCatalog();
  const successes = new Map<string, ReturnType<typeof compileZccAdoptionProjection>>();

  for (const [index, fixture] of cases.entries()) {
    const oracle = expected[index];
    assert.notEqual(oracle, undefined, fixture.name);
    assert.equal(oracle?.name, fixture.name);
    const oracleResult = oracle?.result;
    assert.notEqual(oracleResult, undefined, fixture.name);
    assert.equal(
      fixture.provenance_status === "source_derived"
        ? fixture.name.startsWith("source-derived-")
        : fixture.name.startsWith("synthetic-"),
      true,
      `${fixture.name}: evidence class must remain explicit`,
    );
    const rawBefore = stringifyLosslessJson(fixture.raw_items);
    const statesBefore = stringifyLosslessJson(fixture.observed_states);

    if (fixture.expected === "success") {
      assert.equal(oracleResult?.ok, true, fixture.name);
      if (oracleResult?.ok !== true) {
        assert.fail(`${fixture.name}: Python unexpectedly failed`);
      }
      const actual = compileZccAdoptionProjection({
        catalog,
        resourceType: fixture.resource_type,
        rawItems: fixture.raw_items,
        observedStates: fixture.observed_states,
      });
      assert.equal(actual.kind, "infrawright.zcc_adoption_projection");
      assert.equal(actual.schema_version, 1);
      assert.equal(actual.product, "zcc");
      assert.equal(actual.resource_type, fixture.resource_type);
      assert.equal(actual.catalog.kind, "infrawright.adoption_catalog");
      assert.equal(actual.catalog.schema_version, 1);
      assert.equal(actual.catalog.sources_sha256, catalog.sources_sha256);
      assert.ok(Object.isFrozen(actual.catalog));
      successes.set(fixture.name, actual);
      assert.ok(terraformJsonEqual(actual.items, oracleResult.items), fixture.name);
      assert.ok(
        terraformJsonEqual(actual.identities, oracleResult.identities),
        `${fixture.name}: identities`,
      );
      assert.ok(
        terraformJsonEqual(actual.import_ids, oracleResult.import_ids),
        `${fixture.name}: import IDs`,
      );
      assert.equal(
        renderPythonLosslessArtifactJson(actual.items),
        oracleResult.rendered.items,
        `${fixture.name}: projected map bytes`,
      );
      assert.equal(
        renderPythonLosslessArtifactJson(actual.identities),
        oracleResult.rendered.identities,
        `${fixture.name}: identity map bytes`,
      );
      assert.equal(
        renderPythonLosslessArtifactJson(actual.import_ids),
        oracleResult.rendered.import_ids,
        `${fixture.name}: import-ID map bytes`,
      );
      assert.equal(
        String(oracleResult.loader_calls),
        fixture.raw_items.length === 0 ? "0" : "1",
        `${fixture.name}: Python state-loader calls`,
      );
    } else {
      assert.equal(oracleResult?.ok, false, fixture.name);
      if (oracleResult?.ok !== false) {
        assert.fail(`${fixture.name}: Python unexpectedly succeeded`);
      }
      assert.equal(oracleResult.phase, fixture.expected, fixture.name);
      let actualError: ProcessFailure | undefined;
      try {
        compileZccAdoptionProjection({
          catalog,
          resourceType: fixture.resource_type,
          rawItems: fixture.raw_items,
          observedStates: fixture.observed_states,
        });
      } catch (error: unknown) {
        actualError = nodeError(error);
      }
      assert.notEqual(actualError, undefined, `${fixture.name}: Node must fail closed`);
      assert.equal(actualError?.code, ERROR_CODES[fixture.expected], fixture.name);
      const nodeText = errorText(actualError as ProcessFailure);
      const pythonText = JSON.stringify(oracleResult);
      for (const secret of fixture.secret_tokens ?? []) {
        assert.equal(nodeText.includes(secret), false, `${fixture.name}: Node leaked secret`);
        assert.equal(
          pythonText.includes(secret),
          false,
          `${fixture.name}: Python harness leaked secret`,
        );
      }
    }

    assert.equal(
      stringifyLosslessJson(fixture.raw_items),
      rawBefore,
      `${fixture.name}: raw inputs mutated`,
    );
    assert.equal(
      stringifyLosslessJson(fixture.observed_states),
      statesBefore,
      `${fixture.name}: provider-state observations mutated`,
    );
  }

  const successfulTypes = new Set(
    cases.filter((fixture) => fixture.expected === "success")
      .map((fixture) => fixture.resource_type),
  );
  assert.deepEqual(successfulTypes, new Set(RESOURCE_TYPES));

  const device = successes.get(
    "synthetic-device-cleanup-big-integral-null-and-computed-only",
  );
  assert.notEqual(device, undefined);
  const deviceItem = (device?.items as Record<string, Record<string, unknown>>)[
    "900719925474099312345678901"
  ];
  assert.notEqual(deviceItem, undefined);
  assert.equal(String(deviceItem?.auto_purge_days), "900719925474099312345678902");
  assert.equal(Object.hasOwn(deviceItem ?? {}, "id"), false);
  assert.equal(Object.hasOwn(deviceItem ?? {}, "force_remove_type"), false);

  const forwarding = successes.get(
    "synthetic-forwarding-nested-list-and-single-shapes-html-unicode",
  );
  const forwardingItems = forwarding?.items as Record<string, Record<string, unknown>>;
  assert.equal(forwardingItems.a_amp_b?.name, "A & B 東京 😀");
  assert.deepEqual(
    (forwardingItems.shape_two?.forwarding_profile_actions as readonly unknown[])
      .length,
    1,
  );

  const trusted = successes.get(
    "synthetic-trusted-network-raw-identity-versus-provider-name",
  );
  const trustedItems = trusted?.items as Record<string, Record<string, unknown>>;
  const trustedIdentities = trusted?.identities as Record<string, Record<string, unknown>>;
  assert.equal(trustedIdentities.raw_identity_amp?.name, "Raw Identity &amp; 東京 😀");
  assert.equal(trustedItems.raw_identity_amp?.name, "Provider & Identity 東京 😀");

  const privacy = successes.get(
    "synthetic-web-privacy-optional-null-and-absent",
  );
  const privacyItem = (privacy?.items as Record<string, Record<string, unknown>>)
    .privacy_1;
  assert.ok(terraformJsonEqual(privacyItem, { collect_user_info: false }));
});
