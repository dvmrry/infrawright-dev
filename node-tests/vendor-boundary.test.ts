import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  auditVendorBoundary,
  loadVendorBoundaryAllowlist,
  runVendorBoundaryAudit,
  scanVendorBoundary,
  VendorBoundaryAuditError,
} from "../node-src/authoring/vendor-boundary.js";

async function fixture(files: Readonly<Record<string, string>>): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "vendor-boundary-node-"));
  for (const [relative, contents] of Object.entries(files)) {
    const filename = path.join(root, relative);
    await mkdir(path.dirname(filename), { recursive: true });
    await writeFile(filename, contents);
  }
  return root;
}

function allow(pathname: string, pattern: string, token = "aws"): string {
  return JSON.stringify({
    allow: [{
      path: pathname,
      token,
      pattern,
      reason: "test allowlist entry",
    }],
  });
}

test("scanner preserves Python file ordering, token boundaries, and one match per token per line", async (context) => {
  const root = await fixture({
    "engine/.hidden/ignored.py": "aws\n",
    "engine/__pycache__/ignored.py": "aws\n",
    "engine/a.py": "awesome zia-zia ZCC\r\n",
    "engine/audit_vendor_boundary.py": "aws\n",
    "engine/aaa/b.py": "google netbox aws_default_tags\n",
    "engine/z.py": "cloudflare\n",
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  assert.deepEqual(await scanVendorBoundary(root), [
    { path: "engine/a.py", line: 1, token: "zia", excerpt: "awesome zia-zia ZCC" },
    { path: "engine/a.py", line: 1, token: "zcc", excerpt: "awesome zia-zia ZCC" },
    { path: "engine/z.py", line: 1, token: "cloudflare", excerpt: "cloudflare" },
    { path: "engine/aaa/b.py", line: 1, token: "google", excerpt: "google netbox aws_default_tags" },
    { path: "engine/aaa/b.py", line: 1, token: "netbox", excerpt: "google netbox aws_default_tags" },
    { path: "engine/aaa/b.py", line: 1, token: "aws", excerpt: "google netbox aws_default_tags" },
  ]);
});

test("allowlisted matches pass and an unmatched occurrence fails closed", async (context) => {
  const root = await fixture({
    "engine/edge.py": "VALUE = 'aws_default_tags'\naws_future_backdoor = True\n",
    "allow.json": allow("engine/edge.py", "aws_default_tags"),
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const result = await auditVendorBoundary({ root, allowlist: path.join(root, "allow.json") });
  assert.equal(result.allowed.length, 1);
  assert.equal(result.violations.length, 1);
  assert.equal(result.violations[0]?.excerpt, "aws_future_backdoor = True");
  const command = await runVendorBoundaryAudit({ root, allowlist: path.join(root, "allow.json") });
  assert.equal(command.exitCode, 1);
  assert.equal(command.stderr, "");
  assert.equal(command.stdout, [
    "vendor boundary audit",
    "tokens: zscaler, zia, zpa, zcc, cloudflare, netbox, aws, google",
    "allowed matches: 1",
    "violations: 1",
    "",
    "violations:",
    "engine/edge.py:2: aws: aws_future_backdoor = True",
    "",
  ].join("\n"));
});

test("empty allowlist produces the Python-compatible successful command result", async (context) => {
  const root = await fixture({
    "engine/plain.py": "VALUE = 'awesome'\n",
    "allow.json": "{\"allow\":[]}",
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  assert.deepEqual(
    await runVendorBoundaryAudit({ root, allowlist: path.join(root, "allow.json") }),
    {
      exitCode: 0,
      stdout: [
        "vendor boundary audit",
        "tokens: zscaler, zia, zpa, zcc, cloudflare, netbox, aws, google",
        "allowed matches: 0",
        "violations: 0",
        "",
      ].join("\n"),
      stderr: "",
    },
  );
});

test("allowlist validation rejects malformed documents and unknown keys", async (context) => {
  const root = await fixture({
    "missing.json": JSON.stringify({ allow: [{ path: "engine/a.py" }] }),
    "unknown.json": JSON.stringify({ allow: [{
      path: "engine/a.py", token: "aws", pattern: "aws", reason: "test", extra: true,
    }] }),
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  await assert.rejects(
    loadVendorBoundaryAllowlist(path.join(root, "missing.json")),
    (error: unknown) => error instanceof VendorBoundaryAuditError
      && /\.token must be a non-empty string$/u.test(error.message),
  );
  await assert.rejects(
    loadVendorBoundaryAllowlist(path.join(root, "unknown.json")),
    (error: unknown) => error instanceof VendorBoundaryAuditError
      && / unknown keys: extra$/u.test(error.message),
  );
});

test("allowlist read and parse errors become exit 2 diagnostics", async (context) => {
  const root = await fixture({
    "engine/a.py": "aws\n",
    "bad.json": "{",
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const result = await runVendorBoundaryAudit({
    root,
    allowlist: path.join(root, "bad.json"),
  });
  assert.equal(result.exitCode, 2);
  assert.equal(result.stdout, "");
  assert.match(result.stderr, /^error: failed to read allowlist .*bad\.json:/u);
});

test("current repository passes against the committed transitional allowlist", async () => {
  const result = await auditVendorBoundary({ root: process.cwd() });
  assert.ok(result.allowed.length > 0);
  assert.equal(result.violations.length, 0);
});
