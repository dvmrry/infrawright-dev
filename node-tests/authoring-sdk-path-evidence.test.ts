import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  discoverSdkGoFiles,
  extractBalancedGoBody,
  extractSdkPaths,
  goCodeWithoutComments,
  matchOpenApiBySdkPath,
  matchSdkEvidenceToOpenApi,
  normalizeSdkPathSegments,
  splitGoCallArguments,
} from "../node-src/authoring/sdk-path-evidence.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

async function fixture(files: Readonly<Record<string, string | Uint8Array>>): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "sdk-evidence-node-"));
  for (const [relative, contents] of Object.entries(files)) {
    const filename = path.join(root, relative);
    await mkdir(path.dirname(filename), { recursive: true });
    await writeFile(filename, contents);
  }
  return root;
}

function pythonExtract(root: string): unknown {
  const script = [
    "import json,sys",
    "from engine import sdk_path_evidence as s",
    "e,u=s.extract_sdk_paths(sys.argv[1])",
    "json.dump({'evidence':e,'unresolved':u},sys.stdout,sort_keys=True,separators=(',',':'))",
  ].join(";");
  const result = spawnSync("python3", ["-c", script, root], {
    cwd: process.cwd(), encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

const SOURCE = String.raw`package sdk

import ("context"; "fmt"; "net/http")

const widgetsBasePath = "v2/widgets"
const (
  groupedBasePath = "v2/grouped"
)

type WidgetsServiceOp struct { client *Client }
type GroupedClient struct { client *Client }
type RawAPI struct { client *Client }

func (s *WidgetsServiceOp) Get(ctx context.Context, widgetID int) error {
  // path := "ignored"
  quoted := "brace } retained by body scanner"
  _ = quoted
  path := fmt.Sprintf("%s/%d", widgetsBasePath, widgetID)
  path = fmt.Sprintf("%s/%s/%v", path, "literal", complexID())
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}

func (s WidgetsServiceOp) List(ctx context.Context) error {
  path := widgetsBasePath
  _, err := s.client.NewRequest(ctx, "GET", path, nil)
  return err
}

func (s *GroupedClient) Read(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", groupedBasePath, id)
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}

func (s *RawAPI) Create(ctx context.Context) error {
  path := widgetsBasePath
  raw := ` + "`quoted { raw }`" + String.raw`
  _ = raw
  _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil)
  return err
}

func (s *WidgetsService) MissingMethod(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", widgetsBasePath, id)
  return nil
}

func (s *WidgetsService) MissingPath(ctx context.Context) error {
  _, err := s.client.NewRequest(ctx, http.MethodGet, unknown, nil)
  return err
}
`;

const TESTS_ONLY = String.raw`package sdk
const testsBasePath = "v2/tests"
type TestsServiceOp struct { client *Client }
func (s *TestsServiceOp) Get(ctx context.Context) error {
  path := testsBasePath
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}
`;

test("scanner report is exact Python-compatible across supported path shapes", async (context) => {
  const root = await fixture({
    ".git/ignored.go": SOURCE,
    "a/widgets.go": SOURCE,
    "a/widgets_test.go": SOURCE,
    "test/ignored.go": SOURCE,
    "testdata/ignored.go": SOURCE,
    "tests/only.go": TESTS_ONLY,
    "z/invalid.go": new Uint8Array([0xff, 0xfe, 0xfd]),
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const report = await extractSdkPaths(root);
  assert.deepEqual(report, pythonExtract(root));
  assert.equal(report.evidence["Widgets.Get"]?.path_template, "v2/widgets/{widgetID}/literal/{param}");
  assert.equal(report.evidence["Widgets.List"]?.source_role, "list");
  assert.equal(report.evidence["Grouped.Read"]?.path_template, "v2/grouped/{id}");
  assert.equal(report.evidence["Raw.Create"]?.method, "POST");
  assert.equal(report.evidence["Tests.Get"]?.path_template, "v2/tests");
  assert.equal(report.unresolved["Widgets.MissingMethod"]?.reason, "method_not_detected");
  assert.equal(report.unresolved["Widgets.MissingPath"]?.reason, "path_template_not_found");
});

test("discovery excludes ignored directories/tests and uses portable deterministic ordering", async (context) => {
  const root = await fixture({
    "a/ä.go": "package a",
    "a/z.go": "package a",
    "b/a.go": "package b",
    "b/a_test.go": "package b",
    "tests/included.go": "package tests",
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const files = await discoverSdkGoFiles(root);
  assert.deepEqual(files.map((filename) => path.relative(root, filename).split(path.sep).join("/")), [
    "a/z.go", "a/ä.go", "b/a.go", "tests/included.go",
  ]);
});

test("balanced bodies ignore quoted braces and reject malformed bodies", () => {
  const code = String.raw`{"}", '}', ` + "`}`" + String.raw`, { nested := true }}`;
  assert.equal(extractBalancedGoBody(code, 0)[0], String.raw`"}", '}', ` + "`}`" + String.raw`, { nested := true }`);
  assert.equal(extractBalancedGoBody("{ malformed", 0)[0], undefined);
  assert.deepEqual(splitGoCallArguments(String.raw`"%s/%s", base, fn(a, "x,y")`), [
    String.raw`"%s/%s"`, " base", String.raw` fn(a, "x,y")`,
  ]);
  assert.equal(goCodeWithoutComments("a/* x\ny */b// z\nc"), "a\nb\nc");
});

test("OpenAPI path matching ignores parameter names but preserves method and ambiguity", () => {
  const operations: JsonObject[] = [
    { method: "GET", operation_id: "one", path: "/v2/widgets/{widget_id}" },
    { method: "POST", operation_id: "action", path: "/v2/widgets/{widget_id}" },
  ];
  assert.equal(matchOpenApiBySdkPath(operations, "v2/widgets/{id}").operation?.operation_id, "one");
  assert.equal(matchOpenApiBySdkPath(operations, "/v2/widgets/{id}", "POST").operation?.operation_id, "action");
  assert.deepEqual(normalizeSdkPathSegments("v2/Widgets/{name}"), ["v2", "widgets", "{param}"]);
  const ambiguous = matchOpenApiBySdkPath([
    ...operations,
    { method: "GET", operation_id: "two", path: "/v2/widgets/{other}" },
  ], "v2/widgets/{id}");
  assert.equal(ambiguous.operation, undefined);
  assert.deepEqual(ambiguous.ambiguous.map((item) => item.operation_id), ["one", "two"]);
});

test("combined matching separates unique, ambiguous, missing, and action evidence", () => {
  const extracted = {
    evidence: {
      "Widgets.Get": { client_symbol: "Widgets.Get", method: "GET", path_template: "v2/widgets/{id}", sdk_file: "widgets.go", source_role: "read" as const },
      "Widgets.List": { client_symbol: "Widgets.List", method: "GET", path_template: "v2/widgets", sdk_file: "widgets.go", source_role: "list" as const },
      "Widgets.Post": { client_symbol: "Widgets.Post", method: "POST", path_template: "v2/widgets/{id}/actions", sdk_file: "widgets.go", source_role: null },
    },
    unresolved: {},
  };
  const report = matchSdkEvidenceToOpenApi(extracted, [
    { method: "GET", operation_id: "list", path: "/v2/widgets" },
    { method: "GET", operation_id: "detail-a", path: "/v2/widgets/{a}" },
    { method: "GET", operation_id: "detail-b", path: "/v2/widgets/{b}" },
    { method: "POST", operation_id: "action", path: "/v2/widgets/{id}/actions" },
  ]);
  assert.deepEqual((report.matched as JsonObject[]).map((item) => item.client_symbol), ["Widgets.List", "Widgets.Post"]);
  const unresolved = report.unresolved as JsonObject[];
  assert.equal(unresolved[0]?.reason, "ambiguous_openapi_path");
  assert.equal((unresolved[0]?.candidates as JsonObject[]).length, 2);
});
