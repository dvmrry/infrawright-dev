import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import {
  LosslessNumber,
  parse as parseLosslessJson,
  stringify as stringifyLosslessJson,
} from "lossless-json";

import { terraformJsonEqual } from "../node-src/json/python-equality.js";
import {
  transformPullItems,
} from "../node-src/domain/pull-transform.js";
import { pythonHtmlUnescape } from "../node-src/domain/python-html-unescape.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";

interface CorpusCase {
  readonly name: string;
  readonly resource_type: string;
  readonly raw_items: readonly unknown[];
  readonly expected: unknown;
}

interface Corpus {
  readonly schema_version: 1;
  readonly cases: readonly CorpusCase[];
}

const WORKSPACE = process.cwd();
const CORPUS_PATH = path.join(
  WORKSPACE,
  "node-tests/fixtures/zcc-transform-corpus.v1.json",
);

async function loadCorpus(): Promise<Corpus> {
  const text = await readFile(CORPUS_PATH, "utf8");
  return parseLosslessJson(text) as Corpus;
}

test("ZCC checkpoint corpus matches the frozen transform results", async () => {
  const catalog = loadZccTransformCatalog();
  const corpus = await loadCorpus();
  for (const fixture of corpus.cases) {
    const before = stringifyLosslessJson(fixture.raw_items);
    const result = transformPullItems({
      catalog,
      rawItems: fixture.raw_items,
      resourceType: fixture.resource_type,
    });
    assert.ok(
      terraformJsonEqual(result, fixture.expected),
      `${fixture.name} differed from its frozen result`,
    );
    assert.equal(stringifyLosslessJson(fixture.raw_items), before);
    assert.equal(Object.getPrototypeOf(result.items), null);
    assert.equal(Object.getPrototypeOf(result.originals), null);
    for (const item of Object.values(result.items)) {
      assert.equal(Object.getPrototypeOf(item), null);
    }
  }
});

test("high-risk ZCC ordering and coercion gates are explicit", async () => {
  const corpus = await loadCorpus();
  const catalog = loadZccTransformCatalog();
  const byName = new Map(corpus.cases.map((fixture) => [fixture.name, fixture]));

  const failopen = byName.get("failopen-inversion-and-strict-prompt");
  assert.ok(failopen !== undefined);
  const failopenItems = transformPullItems({
    catalog,
    rawItems: failopen.raw_items,
    resourceType: failopen.resource_type,
  }).items["9"];
  assert.ok(failopenItems !== undefined);
  assert.equal(failopenItems.active, true);
  assert.equal(failopenItems.enable_captive_portal_detection, false);
  assert.equal(failopenItems.enable_fail_open, false);
  assert.equal(failopenItems.enable_web_sec_on_proxy_unreachable, false);
  assert.equal(failopenItems.enable_web_sec_on_tunnel_failure, true);
  assert.equal(failopenItems.enable_strict_enforcement_prompt, true);

  const forwarding = byName.get(
    "forwarding-profile-nested-blocks-and-list-read-html",
  );
  assert.ok(forwarding !== undefined);
  const forwardingItems = transformPullItems({
    catalog,
    rawItems: forwarding.raw_items,
    resourceType: forwarding.resource_type,
  }).items;
  assert.ok(Object.prototype.hasOwnProperty.call(forwardingItems, "id_43"));
  const fallback = forwardingItems.id_43;
  assert.ok(fallback !== undefined);
  const zpaActions = fallback.forwarding_profile_zpa_actions as readonly {
    readonly partner_info?: unknown;
  }[];
  assert.equal(zpaActions[0]?.partner_info, undefined);
});

test("Python HTML compatibility covers named, numeric, invalid, and prefix references", () => {
  const tables = loadZccTransformCatalog().python_compatibility.html_unescape;
  assert.equal(
    pythonHtmlUnescape("&NotEqualTilde; &notit; &#x80; &#1; &#xD800;", tables),
    "≂̸ ¬it; €  �",
  );
  assert.equal(pythonHtmlUnescape("plain text", tables), "plain text");
});

test("transform maps are prototype-safe and drops use Python string ordering", () => {
  const item: Record<string, unknown> = {
    active: new LosslessNumber("1"),
    constructor: "ordinary",
    id: "prototype-test",
    zNoise: true,
    äNoise: true,
  };
  Object.defineProperty(item, "__proto__", {
    enumerable: true,
    value: { polluted: true },
  });
  const result = transformPullItems({
    catalog: loadZccTransformCatalog(),
    rawItems: [item],
    resourceType: "zcc_device_cleanup",
  });
  assert.equal(Object.getPrototypeOf(result.items), null);
  assert.equal(Object.getPrototypeOf(result.originals), null);
  assert.deepEqual(
    result.drops,
    ["__proto__", "constructor", "z_noise", "ä_noise"],
  );
  assert.equal((Object.prototype as Record<string, unknown>).polluted, undefined);
});

test("null stubs suppress only closed schema-known or acknowledged shapes", () => {
  const catalog = loadZccTransformCatalog();
  const transform = (unifiedTunnel: readonly unknown[]) => {
    return transformPullItems({
      catalog,
      rawItems: [{
        id: "stub-id",
        name: "Stub Gate",
        unifiedTunnel,
      }],
      resourceType: "zcc_forwarding_profile",
    });
  };

  const legitimate = transform([{ id: "0" }]);
  assert.deepEqual(legitimate.items.stub_gate?.unified_tunnel, []);
  assert.deepEqual(legitimate.drops, []);

  for (const [key, expectedPath] of [
    ["futureSecret", "unified_tunnel[].future_secret"],
    ["futureId", "unified_tunnel[].future_id"],
  ] as const) {
    const extended = transform([{ id: "0", [key]: null }]);
    assert.equal(
      stringifyLosslessJson(extended.items.stub_gate?.unified_tunnel),
      "[{}]",
    );
    assert.deepEqual(extended.drops, [expectedPath]);
  }
});

test("single-block merging reports unknown nullable members exactly once", () => {
  const catalog = loadZccTransformCatalog();
  const transform = (systemProxyData: readonly unknown[]) => {
    return transformPullItems({
      catalog,
      rawItems: [{
        id: "merge-id",
        name: "Merge Gate",
        unifiedTunnel: [{ systemProxyData }],
      }],
      resourceType: "zcc_forwarding_profile",
    });
  };

  const extended = transform([
    { proxyServerAddress: null },
    { futureSecret: null },
  ]);
  assert.equal(
    stringifyLosslessJson(
      extended.items.merge_gate?.unified_tunnel,
    ),
    '[{"system_proxy_data":{}}]',
  );
  assert.deepEqual(
    extended.drops,
    ["unified_tunnel[].system_proxy_data.future_secret"],
  );

  const legitimate = transform([
    { proxyServerAddress: null },
    { pacUrl: null },
  ]);
  assert.equal(
    stringifyLosslessJson(
      legitimate.items.merge_gate?.unified_tunnel,
    ),
    '[{"system_proxy_data":{}}]',
  );
  assert.deepEqual(legitimate.drops, []);
});

test("transform requires lossless raw numbers and canonicalizes finite floats", () => {
  const catalog = loadZccTransformCatalog();
  const run = (autoPurgeDays: unknown): void => {
    transformPullItems({
      catalog,
      rawItems: [{ id: "numeric", active: true, autoPurgeDays }],
      resourceType: "zcc_device_cleanup",
    });
  };
  assert.throws(() => run(1), /must be LosslessNumber/);
  assert.throws(() => run(1.5), /must be LosslessNumber/);
  assert.throws(() => run(-0), /must be LosslessNumber/);
  assert.throws(() => run(Number.MAX_SAFE_INTEGER + 1), /must be LosslessNumber/);
  assert.throws(
    () => run(new LosslessNumber("1e400")),
    /finite losslessly parsed JSON numbers/,
  );

  for (const [input, expected] of [
    ["1e0", "1.0"],
    ["-0.0", "-0.0"],
    ["1e-6", "1e-06"],
    ["1e15", "1000000000000000.0"],
    ["1e16", "1e+16"],
  ] as const) {
    const result = transformPullItems({
      catalog,
      rawItems: [{ id: `float-${input}`, autoPurgeDays: new LosslessNumber(input) }],
      resourceType: "zcc_device_cleanup",
    });
    assert.equal(
      (result.items[`float_${input.replace(/[^a-z0-9]+/gi, "_").replace(/^_+|_+$/g, "").toLowerCase()}`]
        ?.auto_purge_days as LosslessNumber | undefined)?.toString(),
      expected,
    );
  }

  for (const [input, expected] of [
    ["+1.", "1.0"],
    [".5", "0.5"],
    ["1e-6", "1e-06"],
  ] as const) {
    const result = transformPullItems({
      catalog,
      rawItems: [{ id: `string-${input}`, autoPurgeDays: input }],
      resourceType: "zcc_device_cleanup",
    });
    const item = Object.values(result.items)[0];
    assert.equal(
      (item?.auto_purge_days as LosslessNumber | undefined)?.toString(),
      expected,
    );
  }
  assert.throws(() => run("Infinity"), /finite numbers only/);
});

test("lossless JSON negative zero canonicalizes to Python integer zero", () => {
  const catalog = loadZccTransformCatalog();
  const result = transformPullItems({
    catalog,
    rawItems: [{
      id: "negative-zero",
      autoPurgeDays: new LosslessNumber("-0"),
    }],
    resourceType: "zcc_device_cleanup",
  });
  assert.equal(
    stringifyLosslessJson(result.items),
    '{"negative_zero":{"auto_purge_days":0}}',
  );
  assert.equal(
    stringifyLosslessJson(result.originals),
    '{"negative_zero":{"id":"negative-zero","auto_purge_days":0}}',
  );
});

test("transform results do not alias caller-owned LosslessNumber objects", () => {
  const rawNumber = new LosslessNumber("7");
  const result = transformPullItems({
    catalog: loadZccTransformCatalog(),
    rawItems: [{ id: "snapshot", autoPurgeDays: rawNumber }],
    resourceType: "zcc_device_cleanup",
  });
  (rawNumber as unknown as { value: string }).value = "99";
  assert.equal(
    stringifyLosslessJson(result.items),
    '{"snapshot":{"auto_purge_days":7}}',
  );
  assert.equal(
    stringifyLosslessJson(result.originals),
    '{"snapshot":{"id":"snapshot","auto_purge_days":7}}',
  );
});

test("duplicate derived keys fail closed", () => {
  assert.throws(
    () => transformPullItems({
      catalog: loadZccTransformCatalog(),
      rawItems: [
        { id: "1", active: true },
        { id: new LosslessNumber("1"), active: false },
      ],
      resourceType: "zcc_device_cleanup",
    }),
    /duplicate derived key "1"/,
  );
});

test("snake-case collisions fail closed recursively", () => {
  const catalog = loadZccTransformCatalog();
  assert.throws(
    () => transformPullItems({
      catalog,
      rawItems: [{ id: "top", fooBar: true, foo_bar: false }],
      resourceType: "zcc_device_cleanup",
    }),
    /snake_case key collision at \$raw/,
  );
  assert.throws(
    () => transformPullItems({
      catalog,
      rawItems: [{
        id: "nested",
        name: "Nested",
        forwardingProfileActions: [{
          actionType: "1",
          systemProxyData: {
            proxyServerPort: "8080",
            proxy_server_port: "9090",
          },
        }],
      }],
      resourceType: "zcc_forwarding_profile",
    }),
    /snake_case key collision.*systemProxyData/,
  );
});

test("trusted-network rename destinations cannot overwrite source data", () => {
  assert.throws(
    () => transformPullItems({
      catalog: loadZccTransformCatalog(),
      rawItems: [{
        id: "trusted",
        name: "Destination",
        networkName: "Source",
      }],
      resourceType: "zcc_trusted_network",
    }),
    /rename destination collision.*network_name.*name/,
  );
});

test("identity components have a strict, deterministic domain", () => {
  const catalog = loadZccTransformCatalog();
  const transformDeviceId = (id: unknown): void => {
    transformPullItems({
      catalog,
      rawItems: [{ id }],
      resourceType: "zcc_device_cleanup",
    });
  };
  for (const invalid of [null, true, "", "   ", [], {}]) {
    assert.throws(
      () => transformDeviceId(invalid),
      /key field "id"/,
    );
  }
  for (const invalid of [new Date(0), new Map<string, string>()]) {
    assert.throws(
      () => transformDeviceId(invalid),
      /transform input must contain JSON values only/,
    );
  }
  assert.throws(() => transformDeviceId("网络"), /fallback key field "id"/);

  const stringFallback = transformPullItems({
    catalog,
    rawItems: [{ id: "fallback-1", name: "网络" }],
    resourceType: "zcc_forwarding_profile",
  });
  assert.ok(Object.prototype.hasOwnProperty.call(stringFallback.items, "id_fallback_1"));
  const numberFallback = transformPullItems({
    catalog,
    rawItems: [{ id: new LosslessNumber("42"), name: "网络" }],
    resourceType: "zcc_forwarding_profile",
  });
  assert.ok(Object.prototype.hasOwnProperty.call(numberFallback.items, "id_42"));

  for (const invalidId of [null, false, " ", "网络", [], {}]) {
    assert.throws(
      () => transformPullItems({
        catalog,
        rawItems: [{ id: invalidId, name: "网络" }],
        resourceType: "zcc_forwarding_profile",
      }),
      /key field "id"|fallback key field "id"/,
    );
  }
  assert.throws(
    () => transformPullItems({
      catalog,
      rawItems: [{ id: "valid-id", name: "   " }],
      resourceType: "zcc_forwarding_profile",
    }),
    /key field "name" must not be blank/,
  );
});

test("block arrays reject every non-object entry", () => {
  const catalog = loadZccTransformCatalog();
  const forwarding = (forwardingProfileActions: unknown): void => {
    transformPullItems({
      catalog,
      rawItems: [{
        id: "profile",
        name: "Profile",
        forwardingProfileActions,
      }],
      resourceType: "zcc_forwarding_profile",
    });
  };
  assert.throws(
    () => forwarding([null]),
    /block forwarding_profile_actions\[0\] must be a JSON object/,
  );
  assert.throws(
    () => forwarding([{ actionType: "1" }, "noise"]),
    /block forwarding_profile_actions\[1\] must be a JSON object/,
  );
  assert.throws(
    () => forwarding([{
      actionType: "1",
      systemProxyData: [{ enableProxyServer: true }, null],
    }]),
    /block forwarding_profile_actions\[\]\.system_proxy_data\[1\]/,
  );
});

test("missing key fields fail closed", () => {
  assert.throws(
    () => transformPullItems({
      catalog: loadZccTransformCatalog(),
      rawItems: [{ active: true }],
      resourceType: "zcc_device_cleanup",
    }),
    /key field "id" missing/,
  );
});

test("transform rejects forged catalogs at its own boundary", () => {
  const forged = JSON.parse(
    JSON.stringify(loadZccTransformCatalog()),
  ) as {
    resources: { projection: { attributes: Record<string, string> } }[];
  };
  const active = forged.resources[0]?.projection.attributes;
  assert.ok(active !== undefined);
  active.active = "string";
  assert.throws(
    () => transformPullItems({
      catalog: forged as never,
      rawItems: [{ id: "1", active: true }],
      resourceType: "zcc_device_cleanup",
    }),
    /supported embedded ZCC catalog/,
  );
});

test("transform rejects non-JSON object instances", () => {
  assert.throws(
    () => transformPullItems({
      catalog: loadZccTransformCatalog(),
      rawItems: [new Date(0)],
      resourceType: "zcc_device_cleanup",
    }),
    /JSON/,
  );
});
