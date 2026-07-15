import assert from "node:assert/strict";
import test from "node:test";

import { LosslessNumber } from "lossless-json";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";

import {
  HclExpression,
  applyExpressionBindings,
  expressionModuleTargets,
  expressionRemoteStateReferences,
  expressionVariables,
  mergeExpressionBindingLayers,
  parseExpressionBindings,
  renderExpressionBindingsHcl,
  renderExpressionHclValue,
  toTerraformJsonValue,
  validateExpressionBindingSchemaPaths,
} from "../node-src/domain/expression-bindings.js";

function binding(expression: string, options?: { readonly sensitive?: boolean }): unknown {
  return {
    resources: {
      "sample_resource.example": {
        "nested.target": {
          expression,
          ...(options?.sensitive === undefined ? {} : { sensitive: options.sensitive }),
        },
      },
    },
  };
}

function bindingAt(path: string, expression = "var.value"): unknown {
  return {
    resources: {
      "sample_resource.example": {
        [path]: { expression },
      },
    },
  };
}

test("binding parsing, nested application, HCL, and Terraform JSON preserve expression semantics", () => {
  const parsed = parseExpressionBindings(binding("var.client_secret", { sensitive: true }), "sample_resource");
  assert.deepEqual(parsed, [{
    address: "sample_resource.example",
    expression: "var.client_secret",
    key: "example",
    path: "nested.target",
    pathParts: ["nested", "target"],
    reason: null,
    sensitive: true,
  }]);
  assert.deepEqual(expressionVariables(parsed), { client_secret: true });
  const applied = applyExpressionBindings({
    example: { nested: { literal: "unchanged", target: "old" } },
  }, parsed);
  const nested = (applied.example as { nested: Record<string, unknown> }).nested;
  assert.equal(nested.literal, "unchanged");
  assert.ok(nested.target instanceof HclExpression);
  assert.equal(renderExpressionHclValue(nested), "{\n  literal = \"unchanged\"\n  target = var.client_secret\n}");
  assert.deepEqual(toTerraformJsonValue(nested), {
    literal: "unchanged",
    target: "${var.client_secret}",
  });
  const rendered = renderExpressionBindingsHcl(parsed);
  assert.match(rendered, /variable "client_secret"/);
  assert.match(rendered, /sensitive = true/);
  assert.match(rendered, /target = var\.client_secret/);
  assert.match(rendered, /try\(var\.items\["example"\]\.nested, null\)/);
});

test("the v1 expression allowlist accepts selectors and generated lists", () => {
  const allowed = [
    "var.secret",
    "local.shared",
    "data.external.example.result[\"id\"]",
    "module.groups.items[\"one\"].id",
    "module.groups.items[0].id",
    '[module.groups.items["one"].id, "literal"]',
    "[]",
  ];
  for (const expression of allowed) {
    assert.doesNotThrow(() => parseExpressionBindings(binding(expression), "sample_resource"), expression);
  }
  assert.deepEqual(
    expressionModuleTargets('[module.groups.items["module.ignored"].id, "module.also_ignored"]'),
    ["groups"],
  );
});

test("Infrawright remote-state discovery accepts only the exact canonical selector", () => {
  const canonical = 'data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]';
  assert.deepEqual(expressionRemoteStateReferences(canonical), [{
    key: "segment_one",
    resourceType: "zpa_segment_group",
    root: "zpa_segment_group",
  }]);
  assert.deepEqual(
    expressionRemoteStateReferences(`[${canonical}, "literal"]`),
    [{ key: "segment_one", resourceType: "zpa_segment_group", root: "zpa_segment_group" }],
  );
  for (const expression of [
    "data.terraform_remote_state.zpa_segment_group.outputs.other",
    `${canonical}.bogus`,
  ]) {
    assert.throws(
      () => expressionRemoteStateReferences(expression),
      /canonical|must end/u,
      expression,
    );
  }
});

test("malformed expressions, paths, addresses, and secret-bearing metadata fail closed", () => {
  for (const expression of [
    "aws_secret.value",
    "${var.secret}",
    'module.groups.items["${unsafe}"].id',
    "module.groups.items[-1].id",
    "module.groups.items[1.2].id",
    "module.groups.items[01x].id",
    "var.secret\n",
    "[\uFEFF]",
    "",
  ]) {
    assert.throws(
      () => parseExpressionBindings(binding(expression), "sample_resource"),
      /expression/,
      expression,
    );
  }
  assert.throws(
    () => parseExpressionBindings({ resources: null }, "sample_resource"),
    /resources must be an object/,
  );
  assert.throws(
    () => parseExpressionBindings({
      resources: {
        "sample_resource.example": {
          value: { expression: "var.x", sensitive: null },
        },
      },
    }, "sample_resource"),
    /sensitive must be a boolean/,
  );
  assert.throws(
    () => parseExpressionBindings({ resources: { "other.example": { value: { expression: "var.x" } } } }, "sample_resource"),
    /address must be sample_resource\.<key>/,
  );
  for (const path of ["items[]", "items[*]", "items[-1]", "items[01]", 'items["0"]', "items[9007199254740992]", "items[0]id"]) {
    assert.throws(
      () => parseExpressionBindings(bindingAt(path), "sample_resource"),
      /selector|segment|safe integer/,
      path,
    );
  }
  for (const extra of ["value", "secret", "secret_value", "credential"]) {
    assert.throws(
      () => parseExpressionBindings({
        resources: { "sample_resource.example": { value: { expression: "var.x", [extra]: "leak" } } },
      }, "sample_resource"),
      /unknown key/,
    );
  }
});

test("Terraform JSON conversion preserves arbitrary-size numeric scalars", () => {
  const converted = toTerraformJsonValue({
    decimal: new LosslessNumber("1.2500"),
    integer: new LosslessNumber("900719925474099312345"),
    nested: [new LosslessNumber("2"), new HclExpression("local.value")],
  });
  assert.equal(
    renderPythonLosslessArtifactJson(converted),
    [
      "{",
      '  "decimal": 1.25,',
      '  "integer": 900719925474099312345,',
      '  "nested": [',
      "    2,",
      '    "${local.value}"',
      "  ]",
      "}",
      "",
    ].join("\n"),
  );
});

test("binding path validation rejects unknown items, missing parents/leaves, and conflicts", () => {
  const parsed = parseExpressionBindings(binding("var.secret"), "sample_resource");
  assert.throws(() => applyExpressionBindings({}, parsed), /unknown resource address/);
  assert.throws(() => applyExpressionBindings({ example: {} }, parsed), /missing parent path/);
  assert.throws(
    () => applyExpressionBindings({ example: { nested: {} } }, parsed),
    /missing target leaf/,
  );
  const conflicts = parseExpressionBindings({
    resources: {
      "sample_resource.example": {
        nested: { expression: "var.parent" },
        "nested.target": { expression: "var.child" },
      },
    },
  }, "sample_resource");
  assert.throws(() => renderExpressionBindingsHcl(conflicts), /conflicting expression binding/);
});

test("exact numeric list selectors preserve siblings and render fail-closed list edits", () => {
  const parsed = parseExpressionBindings(bindingAt("nested[1].target"), "sample_resource");
  assert.deepEqual(parsed[0]?.pathParts, ["nested", 1, "target"]);
  assert.equal(parsed[0]?.path, "nested[1].target");
  const applied = applyExpressionBindings({
    example: {
      nested: [
        { target: "first", untouched: 1 },
        { target: "second", untouched: 2 },
        { target: "third", untouched: 3 },
      ],
    },
  }, parsed) as { readonly example: { readonly nested: readonly Record<string, unknown>[] } };
  assert.equal(applied.example.nested[0]?.target, "first");
  assert.equal(applied.example.nested[2]?.target, "third");
  assert.ok(applied.example.nested[1]?.target instanceof HclExpression);
  assert.equal(applied.example.nested[1]?.untouched, 2);
  const rendered = renderExpressionBindingsHcl(parsed);
  assert.match(rendered, /concat\(slice\(/u);
  assert.match(rendered, /nested\[1\]/u);
  assert.match(rendered, /target = var\.value/u);
  assert.throws(
    () => applyExpressionBindings({ example: { nested: [{ target: "first" }] } }, parsed),
    /out-of-range list index \[1\]/u,
  );
  const withoutIndex = parseExpressionBindings(bindingAt("nested.target"), "sample_resource");
  assert.throws(
    () => applyExpressionBindings({ example: { nested: [{ target: "first" }] } }, withoutIndex),
    /traverses a list.*exact numeric list selector/u,
  );
  const indexOnObject = parseExpressionBindings(bindingAt("nested[0].target"), "sample_resource");
  assert.throws(
    () => applyExpressionBindings({ example: { nested: { target: "first" } } }, indexOnObject),
    /indexes a non-list/u,
  );
});

test("provider schema validation distinguishes ordered lists from unordered sets", () => {
  const schema = {
    block: {
      attributes: {},
      block_types: {
        list_block: {
          nesting_mode: "list",
          block: { attributes: { id: { optional: true, type: "string" } } },
        },
        set_block: {
          nesting_mode: "set",
          block: { attributes: { id: { optional: true, type: "string" } } },
        },
        singleton_set: {
          nesting_mode: "set",
          max_items: 1,
          block: { attributes: { id: { optional: true, type: ["set", "number"] } } },
        },
      },
    },
  };
  assert.doesNotThrow(() => validateExpressionBindingSchemaPaths(
    schema,
    "sample_resource",
    parseExpressionBindings(bindingAt("list_block[0].id"), "sample_resource"),
  ));
  assert.doesNotThrow(() => validateExpressionBindingSchemaPaths(
    schema,
    "sample_resource",
    parseExpressionBindings(bindingAt("singleton_set.id"), "sample_resource"),
  ));
  assert.throws(
    () => validateExpressionBindingSchemaPaths(
      schema,
      "sample_resource",
      parseExpressionBindings(bindingAt("list_block.id"), "sample_resource"),
    ),
    /list block.*without an exact numeric selector/u,
  );
  assert.throws(
    () => validateExpressionBindingSchemaPaths(
      schema,
      "sample_resource",
      parseExpressionBindings(bindingAt("set_block[0].id"), "sample_resource"),
    ),
    /unordered set block/u,
  );
});

test("layer merge is generated-first/operator-last and variable sensitivity uses logical OR", () => {
  const generated = parseExpressionBindings(binding("module.other.items[\"generated\"].id"), "sample_resource");
  const operator = parseExpressionBindings(binding("var.operator", { sensitive: true }), "sample_resource");
  const merged = mergeExpressionBindingLayers([generated, operator]);
  assert.equal(merged.length, 1);
  assert.equal(merged[0]?.expression, "var.operator");
  const duplicateVariable = parseExpressionBindings({
    resources: {
      "sample_resource.one": { value: { expression: "var.shared", sensitive: false } },
      "sample_resource.two": { value: { expression: "var.shared", sensitive: true } },
    },
  }, "sample_resource");
  assert.deepEqual(expressionVariables(duplicateVariable), { shared: true });
});

test("native HCL rendering retains lossless numeric and non-identifier key behavior", () => {
  assert.equal(renderExpressionHclValue(new LosslessNumber("900719925474099312345")), "900719925474099312345");
  assert.equal(
    renderExpressionHclValue({ "not-an-ident": [new HclExpression("local.value"), "literal"] }),
    '{\n  "not-an-ident" = [local.value, "literal"]\n}',
  );
});
