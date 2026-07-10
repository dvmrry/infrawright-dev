import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import path from "node:path";
import test from "node:test";

const WORKSPACE = process.cwd();
const PROCESS_MAIN = path.join(
  WORKSPACE,
  ".node-test/node-src/process/main.js",
);

function invoke(input: string | Buffer) {
  return spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: WORKSPACE,
    input,
    encoding: Buffer.isBuffer(input) ? undefined : "utf8",
  });
}

test("process host emits one structured roots response", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "test-roots",
    operation: "roots",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      tenant: "prod",
      selectors: ["zpa/application_segment"],
    },
  };
  const result = invoke(JSON.stringify(request));
  assert.equal(result.status, 0, String(result.stderr));
  assert.equal(String(result.stderr), "");
  const response = JSON.parse(String(result.stdout));
  assert.equal(response.kind, "infrawright.process_response");
  assert.equal(response.request_id, "test-roots");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.equal(response.result.kind, "infrawright.root_topology");
  assert.ok(String(result.stdout).endsWith("\n"));
});

test("process host rejects malformed and schema-invalid requests structurally", () => {
  const malformed = invoke("{");
  assert.equal(malformed.status, 2);
  assert.equal(JSON.parse(String(malformed.stdout)).error.code, "INVALID_JSON");
  assert.equal(String(malformed.stderr), "");

  const invalid = invoke(JSON.stringify({
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "invalid",
    operation: "roots",
    context: {},
    input: {},
    surprise: true,
  }));
  assert.equal(invalid.status, 2);
  const response = JSON.parse(String(invalid.stdout));
  assert.equal(response.request_id, "invalid");
  assert.equal(response.operation, "roots");
  assert.equal(response.error.code, "INVALID_REQUEST");
  assert.ok(response.error.details.length > 0);
  assert.equal(String(invalid.stderr), "");
});

test("process host rejects invalid UTF-8 without replacement", () => {
  const result = invoke(Buffer.from([0xff]));
  assert.equal(result.status, 2);
  assert.equal(
    JSON.parse(String(result.stdout)).error.code,
    "INVALID_UTF8",
  );
});
