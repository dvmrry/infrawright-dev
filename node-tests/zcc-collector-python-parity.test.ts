import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import {
  collectZccOneApiResource,
  type ZccCollectorTransportResponse,
} from "../node-src/domain/zcc-collector.js";

const RESOURCE_TYPES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

function response(text: string): ZccCollectorTransportResponse {
  return { body: Buffer.from(text, "utf8"), status: 200 };
}

test("all five ZCC fixture bytes match the retained Python JSON oracle", async () => {
  for (const resourceType of RESOURCE_TYPES) {
    const fixturePath = path.join(
      process.cwd(),
      "tests/fixtures/demo",
      `${resourceType}.json`,
    );
    const fixture = readFileSync(fixturePath, "utf8");
    const body = resourceType === "zcc_trusted_network"
      ? `{"totalCount":2,"trustedNetworkContracts":${fixture}}`
      : fixture;
    const result = await collectZccOneApiResource({
      cloud: "",
      resourceType,
      sleep: () => undefined,
      transport: () => response(body),
    });
    const python = spawnSync(PYTHON_ORACLE, ["-c", [
      "import json,sys",
      "with open(sys.argv[1], encoding='utf-8') as f: value=json.load(f)",
      "sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
    ].join("\n"), fixturePath], { encoding: "utf8" });
    assert.equal(python.status, 0, python.stderr);
    assert.equal(result.canonical_json, python.stdout, resourceType);
  }
});

test("large integer and Unicode bytes match the retained Python JSON oracle", async () => {
  const source = '[{"html":"&amp; <b>&#x1F600;</b>","id":900719925474099312345678901234567890,"name":"München 😀"}]';
  const result = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_failopen_policy",
    sleep: () => undefined,
    transport: () => response(source),
  });
  const python = spawnSync(PYTHON_ORACLE, ["-c", [
    "import json,sys",
    "value=json.loads(sys.stdin.read())",
    "sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
  ].join(";")], { encoding: "utf8", input: source });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(result.canonical_json, python.stdout);
});
