import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
} from "../node-src/contracts/validators.js";

const WORKSPACE = process.cwd();
const PROCESS_MAIN = path.join(
  WORKSPACE,
  ".node-test/node-src/process/main.js",
);

function invoke(input: string | Buffer, cwd = WORKSPACE) {
  return spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd,
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

test("process host emits one structured scope_paths response", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "test-scope-paths",
    operation: "scope_paths",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      paths: ["config/prod/zpa_application_segment.auto.tfvars.json"],
    },
  };
  const result = invoke(JSON.stringify(request));
  assert.equal(result.status, 0, String(result.stderr));
  assert.equal(String(result.stderr), "");
  const response = JSON.parse(String(result.stdout));
  assert.equal(response.request_id, "test-scope-paths");
  assert.equal(response.operation, "scope_paths");
  assert.equal(response.status, "ok");
  assert.deepEqual(response.diagnostics, []);
  assert.equal(response.result.kind, "infrawright.changed_path_scope");
});

test("process host emits one structured plan_roots response", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "test-plan-roots",
    operation: "plan_roots",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      tenant: "not-materialized",
      selectors: ["zpa/application_segment"],
    },
  };
  const result = invoke(JSON.stringify(request));
  assert.equal(result.status, 0, String(result.stderr));
  assert.equal(String(result.stderr), "");
  const response = JSON.parse(String(result.stdout));
  assert.equal(response.request_id, "test-plan-roots");
  assert.equal(response.operation, "plan_roots");
  assert.equal(response.result.kind, "infrawright.plan_roots");
  assert.deepEqual(response.result.roots, []);
});

test("plan_roots rejects unknown selectors before reading deployment", () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "infrawright-process-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    writeFileSync(deployment, "{");
    const result = invoke(JSON.stringify({
      kind: "infrawright.process_request",
      schema_version: 1,
      request_id: "plan-roots-order",
      operation: "plan_roots",
      context: {
        workspace: WORKSPACE,
        deployment,
        root_catalog: "catalogs/zscaler-root-catalog.v1.json",
      },
      input: {
        tenant: null,
        selectors: ["not_a_resource"],
      },
    }));
    assert.equal(result.status, 2);
    assert.equal(
      JSON.parse(String(result.stdout)).error.code,
      "UNKNOWN_RESOURCE_SELECTOR",
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("scope_paths resolves relative contract paths from context.workspace", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "workspace-scoping",
    operation: "scope_paths",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      paths: ["config/prod/zpa_application_segment.auto.tfvars.json"],
    },
  };
  const result = invoke(JSON.stringify(request), path.dirname(WORKSPACE));
  assert.equal(result.status, 0, String(result.stderr));
  const response = JSON.parse(String(result.stdout));
  assert.deepEqual(response.result.affected_resources, ["zpa_application_segment"]);
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

  const duplicate = invoke(
    '{"kind":"infrawright.process_request","schema_version":1,'
    + '"request_id":"duplicate","request_id":"duplicate",'
    + '"operation":"roots","context":{"workspace":"/tmp",'
    + '"deployment":"deployment.json","root_catalog":"catalog.json"},'
    + '"input":{"tenant":null,"selectors":[]}}',
  );
  assert.equal(duplicate.status, 2);
  assert.equal(
    JSON.parse(String(duplicate.stdout)).error.code,
    "INVALID_JSON",
  );
});

test("process host rejects invalid UTF-8 without replacement", () => {
  const result = invoke(Buffer.from([0xff]));
  assert.equal(result.status, 2);
  assert.equal(
    JSON.parse(String(result.stdout)).error.code,
    "INVALID_UTF8",
  );
});

test("response schema forbids success diagnostics on errors", () => {
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "mixed",
    operation: "roots",
    status: "error",
    diagnostics: [
      {
        level: "note",
        code: "WHOLE_ROOT_SELECTION",
        message: "not valid on an error",
        selected_members: ["one"],
        root: "group",
        additional_members: ["two"],
      },
    ],
    result: null,
    error: {
      code: "INVALID_REQUEST",
      category: "request",
      message: "bad request",
      retryable: false,
      details: [],
    },
  }), false);
});

test("request schema binds each operation to its input shape", () => {
  const context = {
    workspace: WORKSPACE,
    deployment: "deployment.json",
    root_catalog: "catalog.json",
  };
  const base = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "shape",
    context,
  };
  assert.equal(validateProcessRequest({
    ...base,
    operation: "roots",
    input: { paths: [] },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "plan_roots",
    input: { paths: [] },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "scope_paths",
    input: { tenant: null, selectors: [] },
  }), false);
});

test("response schema binds scope_paths success to an empty diagnostic set", () => {
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "scope-diagnostic",
    operation: "scope_paths",
    status: "ok",
    diagnostics: [
      {
        level: "note",
        code: "WHOLE_ROOT_SELECTION",
        message: "not valid for scope_paths",
        selected_members: ["one"],
        root: "group",
        additional_members: ["two"],
      },
    ],
    result: {
      kind: "infrawright.changed_path_scope",
      schema_version: 1,
      paths: [],
      path_matches: [],
      unmatched_paths: [],
      affected_resources: [],
      affected_roots: [],
    },
    error: null,
  }), false);
});

test("response schema binds plan_roots to its result contract", () => {
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "cross-operation",
    operation: "roots",
    status: "ok",
    diagnostics: [],
    result: {
      kind: "infrawright.plan_roots",
      schema_version: 1,
      request: { tenant: null, selectors: [] },
      roots: [],
    },
    error: null,
  }), false);
});
