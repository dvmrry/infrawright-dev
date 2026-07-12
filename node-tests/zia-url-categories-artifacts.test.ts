import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { getCACertificates } from "node:tls";
import {
  access,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  truncate,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { buildSync } from "esbuild";
import { LosslessNumber } from "lossless-json";

import {
  compileZiaUrlCategoryArtifacts,
  deriveZiaUrlCategoryIdentities,
  ZIA_PROVIDER_SOURCE,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
  type ZiaUrlCategoryStateObservation,
} from "../node-src/domain/zia-url-categories.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { runZiaUrlCategoryArtifactWorkflow } from "../node-src/io/zia-url-categories-artifacts.js";
import { collectZiaUrlCategories } from "../node-src/io/zia-url-categories-fetch.js";
import { observeZiaUrlCategories } from "../node-src/io/zia-url-categories-oracle.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";

function wire(value: string): Uint8Array {
  return Buffer.from(value, "utf8");
}

const RAW = Object.freeze([{
  configuredName: "Raw Category Name",
  customCategory: true,
  id: "CUSTOM_01",
  urls: ["two.example", "one.example"],
}]);

const ONEAPI_ENVIRONMENT = Object.freeze({
  ZSCALER_CLIENT_ID: "client",
  ZSCALER_CLIENT_SECRET: "secret",
  ZSCALER_CLOUD: "production",
  ZSCALER_VANITY_DOMAIN: "tenant",
});

const PYTHON_ARTIFACT_ORACLE = String.raw`
import json
import sys

from engine import lookup
from engine import transform
from engine.adoption_meta import adoption_entry, derive_key_from_identity, identity_item
from engine.state_project import project_item

payload = json.load(sys.stdin)
resource_type = "zia_url_categories"
identity = identity_item(payload["raw_items"][0], resource_type)
key = derive_key_from_identity(identity, adoption_entry(resource_type))
item = project_item(
    resource_type,
    payload["values"],
    sensitive_values=payload["sensitive_values"],
)
items = {key: item}
originals = {key: identity}
merged = dict(identity)
merged.update(item)
lookup_items = {key: merged}
result = {
    "imports": transform.render_imports(
        resource_type,
        originals,
        {"import_id": adoption_entry(resource_type)["import_id"]},
    ),
    "lookup": lookup.render_lookup(
        lookup.build_lookup(lookup_items, "configured_name"),
        key_mapping=lookup.build_lookup_key_map(lookup_items),
    ),
    "pull": json.dumps(payload["raw_items"], indent=2, sort_keys=True) + "\n",
    "tfvars": transform.render_tfvars(items),
}
json.dump(result, sys.stdout, indent=2, sort_keys=True)
sys.stdout.write("\n")
`;

function observed(
  rawItems: readonly unknown[] = RAW,
): readonly ZiaUrlCategoryStateObservation[] {
  return deriveZiaUrlCategoryIdentities(rawItems).map((identity) => {
    return Object.freeze({
      address: identity.address,
      importId: identity.importId,
      key: identity.key,
      providerName: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
      resourceType: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
      sensitiveValues: {},
      values: {
        category_id: "CUSTOM_01",
        configured_name: "Provider Normalized Name",
        custom_category: true,
        custom_urls_count: 2,
        id: "provider-state-id-is-not-the-import-contract",
        scopes: [{
          scope_entities: [{ id: [10] }],
          scope_group_member_entities: [],
          type: "ORGANIZATION",
        }],
        url_keyword_counts: [{
          retain_parent_url_count: 0,
          total_url_count: 2,
        }],
        urls: ["one.example", "two.example"],
        val: 101,
      },
    });
  });
}

function exactPlan(identity: ReturnType<typeof deriveZiaUrlCategoryIdentities>[number]): Record<string, unknown> {
  return {
    applyable: true,
    complete: true,
    errored: false,
    format_version: "1.2",
    resource_changes: [{
      address: identity.address,
      change: {
        actions: ["no-op"],
        importing: { id: identity.importId },
      },
      mode: "managed",
      provider_name: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
      type: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    }],
    resource_drift: [],
    terraform_version: "1.15.4",
  };
}

function exactState(identity: ReturnType<typeof deriveZiaUrlCategoryIdentities>[number]): Record<string, unknown> {
  return {
    checks: [],
    format_version: "1.0",
    terraform_version: "1.15.4",
    values: {
      outputs: {},
      root_module: {
        child_modules: [],
        resources: [{
          address: identity.address,
          mode: "managed",
          provider_name: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
          sensitive_values: {},
          type: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
          values: observed()[0]?.values,
        }],
      },
    },
  };
}

function failureCode(code: string): (error: unknown) => boolean {
  return (error) => error instanceof ProcessFailure && error.code === code;
}

test("provider observations render real tfvars, imports, and lookup bytes", () => {
  const artifacts = compileZiaUrlCategoryArtifacts({
    observations: observed(),
    rawItems: RAW,
  });
  assert.equal(
    artifacts.imports,
    'import {\n'
      + '  to = module.zia_url_categories.zia_url_categories.this["raw_category_name"]\n'
      + '  id = "CUSTOM_01"\n'
      + '}\n',
  );
  assert.deepEqual(JSON.parse(artifacts.lookup), {
    by_id: { CUSTOM_01: "Provider Normalized Name" },
    key_by_id: { CUSTOM_01: "raw_category_name" },
  });
  const tfvars = JSON.parse(artifacts.tfvars) as {
    items: Record<string, Record<string, unknown>>;
  };
  const item = tfvars.items.raw_category_name;
  assert.equal(item?.configured_name, "Provider Normalized Name");
  assert.deepEqual(item?.scope_entities, undefined);
  assert.deepEqual(item?.scopes, [{
    scope_entities: { id: [10] },
    type: "ORGANIZATION",
  }]);
  assert.deepEqual(item?.url_keyword_counts, {
    retain_parent_url_count: 0,
    total_url_count: 2,
  });
  for (const computedOnly of ["category_id", "id", "val"]) {
    assert.equal(Object.hasOwn(item ?? {}, computedOnly), false, computedOnly);
  }
});

test("provider-state artifacts remain byte-compatible with the Python migration oracle", () => {
  const observation = observed()[0];
  assert.notEqual(observation, undefined);
  const artifacts = compileZiaUrlCategoryArtifacts({
    observations: observed(),
    rawItems: RAW,
  });
  const python = spawnSync("python3", ["-c", PYTHON_ARTIFACT_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: JSON.stringify({
      raw_items: RAW,
      sensitive_values: observation?.sensitiveValues,
      values: observation?.values,
    }),
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.deepEqual(JSON.parse(python.stdout), artifacts);
});

test("empty adoption emits Python-compatible empty artifacts", () => {
  const artifacts = compileZiaUrlCategoryArtifacts({
    observations: [],
    rawItems: [],
  });
  assert.equal(artifacts.pull, "[]\n");
  assert.equal(artifacts.tfvars, '{\n  "items": {}\n}\n');
  assert.equal(artifacts.imports, "");
  assert.equal(artifacts.lookup, "{}\n");
});

test("projection fails closed on sensitive or missing required nested inputs", () => {
  const sensitive = observed().map((entry) => ({
    ...entry,
    sensitiveValues: { configured_name: true },
  }));
  assert.throws(
    () => compileZiaUrlCategoryArtifacts({ observations: sensitive, rawItems: RAW }),
    /sensitive provider input cannot be projected/,
  );

  const missingNested = observed().map((entry) => ({
    ...entry,
    values: {
      ...(entry.values as Record<string, unknown>),
      scopes: [{ scope_entities: [{}], type: "ORGANIZATION" }],
    },
  }));
  assert.throws(
    () => compileZiaUrlCategoryArtifacts({ observations: missingNested, rawItems: RAW }),
    /required provider state path is missing/,
  );

  const wrongRemoteIdentity = observed().map((entry) => ({
    ...entry,
    values: {
      ...(entry.values as Record<string, unknown>),
      category_id: "CUSTOM_OTHER",
    },
  }));
  assert.throws(
    () => compileZiaUrlCategoryArtifacts({
      observations: wrongRemoteIdentity,
      rawItems: RAW,
    }),
    /provider observation does not match its URL-category identity/,
  );
});

test("OneAPI collector calls the real ZIA URL shape and paginates", async () => {
  const calls: Array<{ readonly method: string; readonly url: string }> = [];
  let now = 1000;
  const fullPage = Array.from({ length: 1000 }, (_unused, index) => ({
    configuredName: `Category ${index}`,
    id: `CUSTOM_${index}`,
    val: index,
  }));
  const items = await collectZiaUrlCategories({
    ZSCALER_CLIENT_ID: "client",
    ZSCALER_CLIENT_SECRET: "secret",
    ZSCALER_CLOUD: "production",
    ZSCALER_VANITY_DOMAIN: "tenant",
  }, {
    now: () => now,
    request: async (request) => {
      calls.push({ method: request.method, url: request.url });
      if (request.method === "POST") {
        assert.equal(request.url, "https://tenant.zslogin.net/oauth2/v1/token");
        assert.match(request.body ?? "", /client_id=client/);
        assert.match(request.body ?? "", /client_secret=secret/);
        return {
          body: wire('{"access_token":"token","expires_in":3600,"token_type":"Bearer"}'),
          retryAfter: null,
          status: 200,
        };
      }
      const url = new URL(request.url);
      assert.equal(url.origin + url.pathname, "https://api.zsapi.net/zia/api/v1/urlCategories");
      assert.equal(url.searchParams.get("customOnly"), "true");
      assert.equal(url.searchParams.get("pageSize"), "1000");
      const page = url.searchParams.get("page");
      now += 1;
      return {
        body: wire(JSON.stringify(page === "1" ? fullPage : [{
          configuredName: "Last Category",
          id: "CUSTOM_LAST",
          val: 1000,
        }])),
        retryAfter: null,
        status: 200,
      };
    },
    sleep: async () => undefined,
  });
  assert.equal(items.length, 1001);
  assert.equal(calls.filter((call) => call.method === "POST").length, 1);
  assert.equal(calls.filter((call) => call.method === "GET").length, 2);
  const last = items.at(-1) as Record<string, unknown>;
  assert.ok(last.val instanceof LosslessNumber);
});

test("OneAPI collector retries rate limits and refreshes once after 401", async () => {
  let auth = 0;
  let data = 0;
  const delays: number[] = [];
  const items = await collectZiaUrlCategories({
    ZSCALER_CLIENT_ID: "client",
    ZSCALER_CLIENT_SECRET: "secret",
    ZSCALER_VANITY_DOMAIN: "tenant",
  }, {
    request: async (request) => {
      if (request.method === "POST") {
        auth += 1;
        return {
          body: wire(JSON.stringify({
            access_token: `token-${auth}`,
            expires_in: 3600,
          })),
          retryAfter: null,
          status: 200,
        };
      }
      data += 1;
      if (data === 1) return { body: wire(""), retryAfter: "0", status: 429 };
      if (data === 2) return { body: wire(""), retryAfter: null, status: 401 };
      assert.equal(request.headers.authorization, "Bearer token-2");
      return {
        body: wire('[{"configuredName":"One","id":"CUSTOM_1"}]'),
        retryAfter: null,
        status: 200,
      };
    },
    sleep: async (milliseconds) => {
      delays.push(milliseconds);
    },
  });
  assert.equal(auth, 2);
  assert.equal(data, 3);
  assert.deepEqual(delays, [0]);
  assert.equal(items.length, 1);
});

test("malformed wire UTF-8 is rejected before observation or artifact writes", async (t) => {
  const cases = [
    {
      code: "ZIA_ONEAPI_AUTH_FAILED",
      name: "token response",
      request: async (request: { readonly method: "GET" | "POST" }) => {
        assert.equal(request.method, "POST");
        return { body: Uint8Array.of(0xff), retryAfter: null, status: 200 };
      },
    },
    {
      code: "INVALID_ZIA_URL_CATEGORY_RESPONSE",
      name: "data response",
      request: async (request: { readonly method: "GET" | "POST" }) => {
        return request.method === "POST"
          ? {
              body: wire('{"access_token":"token","expires_in":3600}'),
              retryAfter: null,
              status: 200,
            }
          : { body: Uint8Array.of(0xff), retryAfter: null, status: 200 };
      },
    },
  ] as const;

  for (const candidate of cases) {
    await t.test(candidate.name, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-invalid-utf8-"));
      let observeCalled = false;
      try {
        await assert.rejects(
          runZiaUrlCategoryArtifactWorkflow({
            environment: { ...ONEAPI_ENVIRONMENT, PATH: "", PYTHON: "/unavailable" },
            tenant: "utf8-test",
            terraformExecutable: "/trusted/terraform",
            workspace,
          }, {
            collect: async (environment) => collectZiaUrlCategories(environment, {
              request: candidate.request,
            }),
            observe: async () => {
              observeCalled = true;
              return observed();
            },
          }),
          failureCode(candidate.code),
        );
        assert.equal(observeCalled, false);
        for (const directory of ["config", "imports", "pulls"]) {
          await assert.rejects(access(path.join(workspace, directory)), /ENOENT/);
        }
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("custom CA input is bounded, regular, and stable before networking", async (t) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-ca-input-"));
  const expectCaFailure = async (
    filePath: string,
    hooks?: { readonly afterOpen?: () => void | Promise<void> },
  ): Promise<void> => {
    await assert.rejects(
      collectZiaUrlCategories({
        ...ONEAPI_ENVIRONMENT,
        REQUESTS_CA_BUNDLE: filePath,
      }, hooks === undefined ? {} : { caReadHooks: hooks }),
      failureCode("ZIA_ONEAPI_CA_FAILED"),
    );
  };
  try {
    await t.test("directory", async () => {
      const directory = path.join(workspace, "directory.pem");
      await mkdir(directory);
      await expectCaFailure(directory);
    });

    await t.test("FIFO", async (subtest) => {
      if (process.platform === "win32") {
        subtest.skip("FIFOs are not available on Windows");
        return;
      }
      const fifo = path.join(workspace, "bundle.fifo");
      const created = spawnSync("mkfifo", [fifo], { encoding: "utf8" });
      if (created.status !== 0) {
        subtest.skip("mkfifo is unavailable");
        return;
      }
      await expectCaFailure(fifo);
    });

    await t.test("sparse oversized file", async () => {
      const oversized = path.join(workspace, "oversized.pem");
      await writeFile(oversized, "", { mode: 0o600 });
      await truncate(oversized, (4 * 1024 * 1024) + 1);
      await expectCaFailure(oversized);
    });

    await t.test("same-size mutation", async () => {
      const certificate = getCACertificates("default")[0];
      assert.notEqual(certificate, undefined);
      const bundle = path.join(workspace, "mutating.pem");
      const before = `${certificate}\n# before\n`;
      const after = `${certificate}\n# after!\n`;
      assert.equal(Buffer.byteLength(before), Buffer.byteLength(after));
      await writeFile(bundle, before, { encoding: "utf8", mode: 0o600 });
      await expectCaFailure(bundle, {
        afterOpen: async () => {
          await writeFile(bundle, after, { encoding: "utf8", mode: 0o600 });
        },
      });
    });
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("Oracle uses init, generated import plan, scratch apply, and provider state", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-oracle-test-"));
  const stages: string[] = [];
  try {
    const identity = deriveZiaUrlCategoryIdentities(RAW)[0];
    assert.notEqual(identity, undefined);
    const observations = await observeZiaUrlCategories({
      environment: {},
      rawItems: RAW,
      terraformExecutable: "/trusted/terraform",
      workspace,
    }, {
      command: async ({ argv, cwd, stage }) => {
        stages.push(stage);
        assert.equal(path.dirname(cwd), path.join(workspace, ".infrawright-oracle"));
        if (stage === "init") {
          assert.match(await readFile(path.join(cwd, "main.tf"), "utf8"), /required_version = "= 1\.15\.4"/);
        }
        if (stage === "plan") {
          assert.ok(argv.some((value) => value.startsWith("-generate-config-out=")));
        }
      },
      show: async ({ stage }) => {
        stages.push(stage);
        if (stage === "show-plan") {
          return exactPlan(identity!);
        }
        return exactState(identity!);
      },
    });
    assert.deepEqual(stages, ["init", "plan", "show-plan", "apply", "show-state"]);
    assert.equal(observations.length, 1);
    assert.deepEqual(observations[0]?.values, observed()[0]?.values);
    await assert.rejects(
      access(path.join(workspace, ".infrawright-oracle")),
      /ENOENT/,
    );
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("Oracle rejects every non-exact plan before scratch apply", async (t) => {
  const identity = deriveZiaUrlCategoryIdentities(RAW)[0];
  assert.notEqual(identity, undefined);
  const variants: readonly [string, (plan: Record<string, unknown>) => void][] = [
    ["future format", (plan) => { plan.format_version = "1.3"; }],
    ["old format", (plan) => { plan.format_version = "1.1"; }],
    ["wrong Terraform", (plan) => { plan.terraform_version = "1.15.5"; }],
    ["missing Terraform", (plan) => { delete plan.terraform_version; }],
    ["incomplete", (plan) => { plan.complete = false; }],
    ["errored", (plan) => { plan.errored = true; }],
    ["not applyable", (plan) => { plan.applyable = false; }],
    ["errors", (plan) => { plan.errors = [{ summary: "hidden" }]; }],
    ["diagnostics", (plan) => { plan.diagnostics = [{ summary: "hidden" }]; }],
    ["checks", (plan) => { plan.checks = [{ status: "fail" }]; }],
    ["deferred changes", (plan) => { plan.deferred_changes = [{}]; }],
    ["action invocations", (plan) => { plan.action_invocations = [{}]; }],
    ["deferred actions", (plan) => { plan.deferred_action_invocations = [{}]; }],
    ["drift", (plan) => { plan.resource_drift = [{}]; }],
    ["outputs", (plan) => { plan.output_changes = { hidden: {} }; }],
    ["wrong empty type", (plan) => { plan.checks = {}; }],
    ["missing resource", (plan) => { plan.resource_changes = []; }],
    ["extra resource", (plan) => {
      const changes = plan.resource_changes as readonly unknown[];
      plan.resource_changes = [...changes, structuredClone(changes[0])];
    }],
    ["wrong action", (plan) => {
      const resource = (plan.resource_changes as Record<string, unknown>[])[0]!;
      resource.change = { actions: ["update"], importing: { id: identity!.importId } };
    }],
    ["wrong provider", (plan) => {
      const resource = (plan.resource_changes as Record<string, unknown>[])[0]!;
      resource.provider_name = "registry.terraform.io/example/wrong";
    }],
    ["wrong import ID", (plan) => {
      const resource = (plan.resource_changes as Record<string, unknown>[])[0]!;
      resource.change = { actions: ["no-op"], importing: { id: "CUSTOM_OTHER" } };
    }],
  ];

  for (const [name, mutate] of variants) {
    await t.test(name, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-plan-reject-"));
      const stages: string[] = [];
      try {
        const plan = structuredClone(exactPlan(identity!));
        mutate(plan);
        await assert.rejects(
          observeZiaUrlCategories({
            environment: {},
            rawItems: RAW,
            terraformExecutable: "/trusted/terraform",
            workspace,
          }, {
            command: async ({ stage }) => { stages.push(stage); },
            show: async ({ stage }) => {
              stages.push(stage);
              return stage === "show-plan" ? plan : exactState(identity!);
            },
          }),
          failureCode("ZIA_URL_CATEGORY_ORACLE_PLAN_REJECTED"),
        );
        assert.equal(stages.includes("apply"), false);
        await assert.rejects(access(path.join(workspace, ".infrawright-oracle")), /ENOENT/);
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("Oracle accepts only exact root state bound to the raw category ID", async (t) => {
  const identity = deriveZiaUrlCategoryIdentities(RAW)[0];
  assert.notEqual(identity, undefined);
  const root = (state: Record<string, unknown>): Record<string, unknown> => {
    return ((state.values as Record<string, unknown>).root_module as Record<string, unknown>);
  };
  const resource = (state: Record<string, unknown>): Record<string, unknown> => {
    return (root(state).resources as Record<string, unknown>[])[0]!;
  };
  const variants: readonly [string, (state: Record<string, unknown>) => void][] = [
    ["future format", (state) => { state.format_version = "1.1"; }],
    ["wrong Terraform", (state) => { state.terraform_version = "1.15.5"; }],
    ["missing Terraform", (state) => { delete state.terraform_version; }],
    ["checks", (state) => { state.checks = [{}]; }],
    ["checks wrong type", (state) => { state.checks = {}; }],
    ["outputs", (state) => {
      (state.values as Record<string, unknown>).outputs = { hidden: {} };
    }],
    ["missing resource", (state) => { root(state).resources = []; }],
    ["extra resource", (state) => {
      const resources = root(state).resources as readonly unknown[];
      root(state).resources = [...resources, structuredClone(resources[0])];
    }],
    ["child module", (state) => { root(state).child_modules = [{ resources: [] }]; }],
    ["wrong provider", (state) => {
      resource(state).provider_name = "registry.terraform.io/example/wrong";
    }],
    ["wrong resource type", (state) => { resource(state).type = "zia_other"; }],
    ["data mode", (state) => { resource(state).mode = "data"; }],
    ["missing provider category ID", (state) => {
      delete (resource(state).values as Record<string, unknown>).category_id;
    }],
    ["ID-versus-configured-name collision", (state) => {
      (resource(state).values as Record<string, unknown>).category_id = "CUSTOM_OTHER";
    }],
    ["deposed", (state) => { resource(state).deposed_key = null; }],
    ["tainted", (state) => { resource(state).tainted = true; }],
    ["invalid taint", (state) => { resource(state).tainted = null; }],
    ["missing sensitivity evidence", (state) => {
      delete resource(state).sensitive_values;
    }],
    ["invalid sensitivity evidence", (state) => {
      resource(state).sensitive_values = false;
    }],
  ];

  for (const [name, mutate] of variants) {
    await t.test(name, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-state-reject-"));
      const stages: string[] = [];
      try {
        const state = structuredClone(exactState(identity!));
        mutate(state);
        await assert.rejects(
          observeZiaUrlCategories({
            environment: {},
            rawItems: RAW,
            terraformExecutable: "/trusted/terraform",
            workspace,
          }, {
            command: async ({ stage }) => { stages.push(stage); },
            show: async ({ stage }) => {
              stages.push(stage);
              return stage === "show-plan" ? exactPlan(identity!) : state;
            },
          }),
          failureCode("ZIA_URL_CATEGORY_ORACLE_STATE_REJECTED"),
        );
        assert.equal(stages.includes("apply"), true);
        await assert.rejects(access(path.join(workspace, ".infrawright-oracle")), /ENOENT/);
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("PR-1 workflow writes all artifacts with Python unavailable", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-artifacts-test-"));
  try {
    const result = await runZiaUrlCategoryArtifactWorkflow({
      environment: { PATH: "", PYTHON: "/unavailable/python" },
      tenant: "production-test",
      terraformExecutable: "/trusted/terraform",
      workspace,
    }, {
      collect: async () => RAW,
      observe: async () => observed(),
    });
    assert.equal(result.itemCount, 1);
    const [pull, tfvars, imports, lookup] = await Promise.all([
      readFile(result.paths.pull, "utf8"),
      readFile(result.paths.tfvars, "utf8"),
      readFile(result.paths.imports, "utf8"),
      readFile(result.paths.lookup, "utf8"),
    ]);
    assert.deepEqual(parseDataJsonLosslessly(pull), parseDataJsonLosslessly(
      JSON.stringify(RAW),
    ));
    assert.match(tfvars, /Provider Normalized Name/);
    assert.match(imports, /CUSTOM_01/);
    assert.match(lookup, /raw_category_name/);
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("ZIA runtime bundle excludes the public process protocol and Python runtime", () => {
  const build = buildSync({
    bundle: true,
    entryPoints: ["node-src/zia-url-categories-main.ts"],
    format: "esm",
    logLevel: "silent",
    metafile: true,
    platform: "node",
    target: "node24",
    write: false,
  });
  const inputs = Object.keys(build.metafile?.inputs ?? {});
  for (const forbidden of [
    "node-src/process/main.ts",
    "node-src/process/execute.ts",
    "node-src/contracts/validators.ts",
    "docs/schemas/process-request.schema.json",
    "docs/schemas/process-response.schema.json",
    "engine/adopt.py",
    "engine/import_oracle.py",
  ]) {
    assert.equal(inputs.some((input) => input.endsWith(forbidden)), false, forbidden);
  }
  const text = build.outputFiles?.[0]?.text ?? "";
  for (const forbidden of ["python3", "python -m", "engine.adopt", "engine.import_oracle"]) {
    assert.equal(text.includes(forbidden), false, forbidden);
  }
});
