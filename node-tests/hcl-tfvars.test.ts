import assert from "node:assert/strict";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  HCL_TFVARS_HEADER,
  hclTfvarsCommentKey,
  renderTfvarsHcl,
} from "../node-src/domain/hcl-tfvars.js";

test("empty and namespaced tfvars match the Python bytes", () => {
  assert.equal(renderTfvarsHcl({}), `${HCL_TFVARS_HEADER}\nitems = {}\n`);
  assert.equal(
    renderTfvarsHcl({ rule: { name: "Rule" } }, {}, "sample_resource_items"),
    `${HCL_TFVARS_HEADER}\n`
      + "sample_resource_items = {\n"
      + "  \"rule\" = {\n"
      + "    name = \"Rule\"\n"
      + "  }\n"
      + "}\n",
  );
});

test("mixed scalars, quoted keys, interpolation, and Python numbers are exact", () => {
  assert.equal(
    renderTfvarsHcl({
      "block risky": {
        "123abc": "leading",
        enabled: true,
        expression: "prefix ${var.name} %{if ok}",
        huge: new LosslessNumber("900719925474099312345"),
        name: "Rule",
        nothing: null,
        ratio: new LosslessNumber("0.5"),
        signed_zero: new LosslessNumber("-0.0"),
        tiny: new LosslessNumber("1e-6"),
      },
    }),
    `${HCL_TFVARS_HEADER}\n`
      + "items = {\n"
      + "  \"block risky\" = {\n"
      + "    \"123abc\"    = \"leading\"\n"
      + "    enabled     = true\n"
      + "    expression  = \"prefix $${var.name} %%{if ok}\"\n"
      + "    huge        = 900719925474099312345\n"
      + "    name        = \"Rule\"\n"
      + "    nothing     = null\n"
      + "    ratio       = 0.5\n"
      + "    signed_zero = -0.0\n"
      + "    tiny        = 1e-06\n"
      + "  }\n"
      + "}\n",
  );
});

test("multiline containers split alignment runs and retain trailing commas", () => {
  assert.equal(
    renderTfvarsHcl({
      item: {
        aa: 1,
        bb: 22,
        empty_list: [],
        empty_obj: {},
        mm: ["x"],
        nested: [["u", "v"], ["w"]],
        objs: [{ a: 1 }, { b: 2 }],
        yy: 3,
        zzzz: 44,
      },
    }),
    `${HCL_TFVARS_HEADER}\n`
      + "items = {\n"
      + "  \"item\" = {\n"
      + "    aa         = 1\n"
      + "    bb         = 22\n"
      + "    empty_list = []\n"
      + "    empty_obj  = {}\n"
      + "    mm = [\n"
      + "      \"x\",\n"
      + "    ]\n"
      + "    nested = [\n"
      + "      [\n"
      + "        \"u\",\n"
      + "        \"v\",\n"
      + "      ],\n"
      + "      [\n"
      + "        \"w\",\n"
      + "      ],\n"
      + "    ]\n"
      + "    objs = [\n"
      + "      {\n"
      + "        a = 1\n"
      + "      },\n"
      + "      {\n"
      + "        b = 2\n"
      + "      },\n"
      + "    ]\n"
      + "    yy   = 3\n"
      + "    zzzz = 44\n"
      + "  }\n"
      + "}\n",
  );
});

test("scalar and list comments align exactly, including object closers", () => {
  const comments = {
    [hclTfvarsCommentKey("item", "a")]: "short",
    [hclTfvarsCommentKey("item", "much_longer")]: "long",
    [hclTfvarsCommentKey("item", "z_categories", 0)]: "Finance",
    [hclTfvarsCommentKey("item", "z_categories", 1)]: "HR",
    [hclTfvarsCommentKey("item", "zz_objects", 0)]: "annotated",
  };
  assert.equal(
    renderTfvarsHcl({
      item: {
        a: 1,
        much_longer: 22,
        z_categories: ["A", "CUSTOM_02"],
        zz_objects: [{ a: 1 }],
      },
    }, comments),
    `${HCL_TFVARS_HEADER}\n`
      + "items = {\n"
      + "  \"item\" = {\n"
      + "    a           = 1  # short\n"
      + "    much_longer = 22 # long\n"
      + "    z_categories = [\n"
      + "      \"A\",         # Finance\n"
      + "      \"CUSTOM_02\", # HR\n"
      + "    ]\n"
      + "    zz_objects = [\n"
      + "      {\n"
      + "        a = 1\n"
      + "      }, # annotated\n"
      + "    ]\n"
      + "  }\n"
      + "}\n",
  );
});

test("code-point lengths, not UTF-16 lengths, control comment alignment", () => {
  assert.equal(
    renderTfvarsHcl(
      { item: { a: "😀", longer: "x" } },
      {
        [hclTfvarsCommentKey("item", "a")]: "emoji",
        [hclTfvarsCommentKey("item", "longer")]: "ascii",
      },
    ),
    `${HCL_TFVARS_HEADER}\n`
      + "items = {\n"
      + "  \"item\" = {\n"
      + "    a      = \"😀\" # emoji\n"
      + "    longer = \"x\" # ascii\n"
      + "  }\n"
      + "}\n",
  );
});

test("invalid variable names, comments, values, and native numbers fail loudly", () => {
  assert.match(renderTfvarsHcl({ item: { value: -0 } }), /value = -0\.0/);
  assert.throws(() => renderTfvarsHcl({}, {}, "bad-name"), /bare HCL identifier/);
  assert.throws(
    () => renderTfvarsHcl(
      { item: { value: "x" } },
      { [hclTfvarsCommentKey("item", "value")]: "line\nbreak" },
    ),
    /single-line/,
  );
  for (const value of [Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY]) {
    assert.throws(() => renderTfvarsHcl({ item: { value } }), /non-finite/);
  }
  assert.throws(
    () => renderTfvarsHcl({ item: { value: Number.MAX_SAFE_INTEGER + 1 } }),
    /unsafe native integer/,
  );
  assert.throws(() => renderTfvarsHcl({ item: { value: undefined } }), /unsupported/);
});
