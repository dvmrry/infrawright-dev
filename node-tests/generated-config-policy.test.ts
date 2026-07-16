import assert from "node:assert/strict";
import { join, resolve } from "node:path";
import test from "node:test";

import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import {
  applyGeneratedConfigPolicies,
  applyGeneratedConfigPolicy,
  GeneratedConfigPolicyError,
} from "../node-src/domain/generated-config-policy.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const ROOT = process.cwd();
const PACKS_ROOT = resolve(
  process.env.INFRAWRIGHT_PACKS?.trim() || join(ROOT, "packs"),
);
const PACK_PROFILE = resolve(
  process.env.PACK_PROFILE?.trim() || join(ROOT, "packsets", "full.json"),
);

const SCHEMA: JsonObject = {
  block: {
    attributes: {
      description: { optional: true, type: "string" },
      filled: { optional: true, type: ["list", "string"] },
      name: { required: true, type: "string" },
      size_quota: { optional: true, type: "number" },
    },
    block_types: {
      rules: {
        nesting_mode: "list",
        block: {
          attributes: {
            label: { required: true, type: "string" },
            order: { optional: true, type: "number" },
          },
        },
      },
    },
  },
};

function root(override: JsonObject | null = null): LoadedPackRoot {
  return {
    loadResourceSchema: async () => SCHEMA,
    resources: new Map([["sample_resource", { override }]]),
  } as unknown as LoadedPackRoot;
}

function policy(resource: JsonObject): DriftPolicy {
  return new DriftPolicy({
    version: 1,
    resource_types: { sample_resource: resource },
  });
}

const ADDRESS = "sample_resource.iw_deadbeef";
const SIBLING_ADDRESS = "sibling_resource.iw_cafebabe";
const GENERATED = `resource "sample_resource" "iw_deadbeef" {
  description = "DROP"
  name        = "Example"

  rules {
    label = "one"
    order = 0
  }
  rules {
    label = "two"
    order = 2
  }
}
`;

test("generated config removes optional scalar and wildcard block leaves, then fills raw evidence", async () => {
  const selected = policy({
    projection_fill: [{
      path: "filled",
      source: "rawFilled",
      reason: "provider omitted it",
      approved_by: "unit",
    }],
    projection_omit: [{
      path: "description",
      reason: "provider default",
      approved_by: "unit",
    }],
    projection_omit_if: [{
      path: "rules[*].order",
      values: [0],
      reason: "sentinel",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED,
    policy: selected,
    rawItems: new Map([["example", { rawFilled: ["one", "two"] }]]),
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.edits, 3);
  assert.equal(result.text.includes("description"), false);
  assert.equal(result.text.includes("order = 0"), false);
  assert.equal(result.text.includes("order = 2"), true);
  assert.equal(result.text.includes('filled = [\n    "one",\n    "two",\n  ]'), true);
  assert.deepEqual(selected.staleEntries(), []);
});

test("pack drop_if_default removes provider-emitted sentinel without a drift policy", async () => {
  const generated = GENERATED.replace(
    '  description = "DROP"\n',
    "  size_quota = 0\n",
  );
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: generated,
    policy: null,
    resourceType: "sample_resource",
    root: root({ drop_if_default: { size_quota: 0 } }),
  });
  assert.equal(result.edits, 1);
  assert.equal(result.text.includes("size_quota"), false);
  assert.equal(result.text.includes('name        = "Example"'), true);
});

test("pack drop_if_default preserves nonmatching provider values", async () => {
  const generated = GENERATED.replace(
    '  description = "DROP"\n',
    "  size_quota = 10\n",
  );
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: generated,
    policy: null,
    resourceType: "sample_resource",
    root: root({ drop_if_default: { size_quota: 0 } }),
  });
  assert.deepEqual(result, { edits: 0, text: generated });
});

test("committed ZIA pack defaults remove only exact empty enums from generated config", async () => {
  const committed = await loadPackRoot({
    packsRoot: PACKS_ROOT,
    profilePath: PACK_PROFILE,
    catalogPath: PACK_PROFILE,
  });
  const cases = [{
    resourceType: "zia_dlp_dictionaries",
    edits: 2,
    generated: `resource "zia_dlp_dictionaries" "iw_deadbeef" {
  confidence_level_for_predefined_dict = ""
  confidence_threshold                 = ""
}
`,
  }, {
    resourceType: "zia_http_header_profile",
    edits: 2,
    generated: `resource "zia_http_header_profile" "iw_deadbeef" {
  name = "Header"

  http_header_profile_criteria {
    header     = "USERAGENT"
    operator   = ""
    user_agent = ""
  }
}
`,
  }, {
    resourceType: "zia_location_management",
    edits: 3,
    generated: `resource "zia_location_management" "iw_deadbeef" {
  display_time_unit           = ""
  name                        = "Location"
  sub_loc_scope               = ""
  surrogate_refresh_time_unit = ""
}
`,
  }, {
    resourceType: "zia_ssl_inspection_rules",
    edits: 1,
    generated: `resource "zia_ssl_inspection_rules" "iw_deadbeef" {
  name  = "SSL"
  order = 1

  action {
    type = "DO_NOT_DECRYPT"

    do_not_decrypt_sub_actions {
      bypass_other_policies = true
      min_tls_version       = ""
    }
  }
}
`,
  }] as const;

  for (const fixture of cases) {
    const address = `${fixture.resourceType}.iw_deadbeef`;
    const empty = await applyGeneratedConfigPolicy({
      addressToKey: new Map([[address, "example"]]),
      generatedConfig: fixture.generated,
      policy: null,
      resourceType: fixture.resourceType,
      root: committed,
    });
    assert.equal(empty.edits, fixture.edits, `${fixture.resourceType} empty edits`);
    const nonempty = await applyGeneratedConfigPolicy({
      addressToKey: new Map([[address, "example"]]),
      generatedConfig: fixture.generated.replaceAll('= ""', '= "SET"'),
      policy: null,
      resourceType: fixture.resourceType,
      root: committed,
    });
    assert.equal(nonempty.edits, 0, `${fixture.resourceType} nonempty edits`);
    const nullable = await applyGeneratedConfigPolicy({
      addressToKey: new Map([[address, "example"]]),
      generatedConfig: fixture.generated.replaceAll('= ""', "= null"),
      policy: null,
      resourceType: fixture.resourceType,
      root: committed,
    });
    assert.equal(nullable.edits, 0, `${fixture.resourceType} null edits`);
  }
});

test("pack defaults precede overlapping conditional drift omissions", async () => {
  const generated = GENERATED.replace(
    '  description = "DROP"\n',
    "  size_quota = 0\n",
  );
  const selected = policy({
    projection_omit_if: [{
      path: "size_quota",
      values: [0],
      reason: "overlap",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: generated,
    policy: selected,
    resourceType: "sample_resource",
    root: root({ drop_if_default: { size_quota: 0 } }),
  });
  assert.equal(result.edits, 1);
  assert.equal(result.text.includes("size_quota"), false);
  assert.equal(selected.staleEntries().length, 1);
});

test("batch generated-config policy edits known sibling resource blocks independently", async () => {
  const siblingGenerated = GENERATED.replaceAll("sample_resource", "sibling_resource")
    .replaceAll("iw_deadbeef", "iw_cafebabe")
    .replaceAll('description = "DROP"', 'description = "SIBLING"');
  const firstPolicy = policy({
    projection_omit: [{ path: "description", reason: "first", approved_by: "unit" }],
  });
  const siblingPolicy = new DriftPolicy({
    version: 1,
    resource_types: {
      sibling_resource: {
        projection_omit_if: [{
          path: "description",
          values: ["SIBLING"],
          reason: "second",
          approved_by: "unit",
        }],
      },
    },
  });
  const result = await applyGeneratedConfigPolicies({
    generatedConfig: `${GENERATED}\n${siblingGenerated}`,
    resources: [{
      addressToKey: new Map([[ADDRESS, "first"]]),
      policy: firstPolicy,
      resourceType: "sample_resource",
    }, {
      addressToKey: new Map([[SIBLING_ADDRESS, "second"]]),
      policy: siblingPolicy,
      resourceType: "sibling_resource",
    }],
    root: root(),
  });
  assert.equal(result.edits, 2);
  assert.equal(result.text.includes('description = "DROP"'), false);
  assert.equal(result.text.includes('description = "SIBLING"'), false);
  assert.deepEqual(firstPolicy.staleEntries(), []);
  assert.deepEqual(siblingPolicy.staleEntries(), []);
});

test("batch generated-config policy rejects unknown sibling blocks", async () => {
  const siblingGenerated = GENERATED.replaceAll("sample_resource", "sibling_resource")
    .replaceAll("iw_deadbeef", "iw_cafebabe");
  await assert.rejects(
    () => applyGeneratedConfigPolicies({
      generatedConfig: `${GENERATED}\n${siblingGenerated}\nresource "unknown_resource" "extra" {\n  name = "extra"\n}\n`,
      resources: [{
        addressToKey: new Map([[ADDRESS, "first"]]),
        policy: null,
        resourceType: "sample_resource",
      }, {
        addressToKey: new Map([[SIBLING_ADDRESS, "second"]]),
        policy: null,
        resourceType: "sibling_resource",
      }],
      root: root(),
    }),
    /unexpected resource block unknown_resource\.extra/u,
  );
});

test("no-policy single-resource wrapper preserves the schema-free fast path", async () => {
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED,
    policy: null,
    resourceType: "sample_resource",
    root: {
      loadResourceSchema: async () => {
        throw new Error("schema must not be loaded without policy entries");
      },
    } as unknown as LoadedPackRoot,
  });
  assert.deepEqual(result, { edits: 0, text: GENERATED });
});

test("exact-index generated-config omits are intentionally deferred to state projection", async () => {
  const selected = policy({
    projection_omit: [{
      path: "rules[0].order",
      reason: "exact plan index",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED,
    policy: selected,
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.edits, 0);
  assert.equal(result.text, GENERATED);
  assert.equal(selected.staleEntries().length, 1);
});

test("a value added by projection_fill is subsequently eligible for projection_omit_if", async () => {
  const selected = policy({
    projection_fill: [{
      path: "description",
      source: "rawDescription",
      reason: "provider omitted it",
      approved_by: "unit",
    }],
    projection_omit_if: [{
      path: "description",
      values: ["DROP"],
      reason: "sentinel",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED.replace('  description = "DROP"\n', ""),
    policy: selected,
    rawItems: new Map([["example", { rawDescription: "DROP" }]]),
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.edits, 2);
  assert.equal(result.text.includes("description"), false);
  assert.deepEqual(selected.staleEntries(), []);
});

test("required-path, missing-config, missing-raw, missing-address, duplicate, and unexpected blocks fail closed", async () => {
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: GENERATED,
      policy: policy({ projection_omit: [{ path: "name", reason: "bad", approved_by: "unit" }] }),
      resourceType: "sample_resource",
      root: root(),
    }),
    /cannot projection_omit required path name/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: "",
      policy: policy({ projection_omit: [{ path: "description", reason: "test", approved_by: "unit" }] }),
      resourceType: "sample_resource",
      root: root(),
    }),
    /generated import config is missing/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: GENERATED,
      policy: policy({ projection_fill: [{ path: "filled", source: "rawFilled", reason: "test", approved_by: "unit" }] }),
      resourceType: "sample_resource",
      root: root(),
    }),
    /projection_fill requires raw_items/,
  );

  const active = policy({ projection_omit: [{ path: "description", reason: "test", approved_by: "unit" }] });
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"], ["sample_resource.iw_missing", "missing"]]),
      generatedConfig: GENERATED,
      policy: active,
      resourceType: "sample_resource",
      root: root(),
    }),
    /missing resource block/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: `${GENERATED}${GENERATED}`,
      policy: active,
      resourceType: "sample_resource",
      root: root(),
    }),
    /duplicate resource block/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: GENERATED.replaceAll("iw_deadbeef", "unexpected"),
      policy: active,
      resourceType: "sample_resource",
      root: root(),
    }),
    /unexpected resource block/,
  );
});

test("unknown compound values are preserved rather than treated as omit-if scalar evidence", async () => {
  const generated = GENERATED.replace(
    'description = "DROP"',
    'description = upper("DROP")',
  );
  const selected = policy({
    projection_omit_if: [{
      path: "description",
      values: ["DROP"],
      reason: "only literal evidence is safe",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: generated,
    policy: selected,
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.text, generated);
  assert.equal(result.edits, 0);
  assert.equal(selected.staleEntries().length, 1);
});

test("policy error type remains stable for callers", () => {
  const error = new GeneratedConfigPolicyError("example");
  assert.equal(error.name, "GeneratedConfigPolicyError");
});
