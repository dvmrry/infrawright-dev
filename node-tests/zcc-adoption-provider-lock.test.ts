import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import {
  ZCC_ADOPTION_PROVIDER_LOCK_SHA256,
  ZCC_ADOPTION_TERRAFORM_VERSION,
  requireZccAdoptionProviderLock,
  zccAdoptionProviderLock,
} from "../node-src/domain/zcc-adoption-provider-lock.js";
import { ProcessFailure } from "../node-src/domain/errors.js";

const LOCK_PATH = path.join(
  process.cwd(),
  "catalogs/zcc-adoption-provider-lock/.terraform.lock.hcl",
);
const PROVENANCE_PATH = path.join(
  process.cwd(),
  "catalogs/zcc-adoption-provider-lock/provenance.json",
);

test("embedded ZCC provider lock is byte-identical to retained official-tooling output", async () => {
  const retained = await readFile(LOCK_PATH, "utf8");
  const provenance = JSON.parse(await readFile(PROVENANCE_PATH, "utf8")) as {
    terraform_version: string;
    lock_sha256: string;
    registry_signing_key_id: string;
    platforms: string[];
    platform_archive_sha256: Record<string, string>;
  };
  const sha256 = createHash("sha256").update(retained, "utf8").digest("hex");

  assert.equal(retained, zccAdoptionProviderLock());
  assert.equal(sha256, ZCC_ADOPTION_PROVIDER_LOCK_SHA256);
  assert.equal(provenance.lock_sha256, sha256);
  assert.equal(provenance.terraform_version, ZCC_ADOPTION_TERRAFORM_VERSION);
  assert.equal(provenance.registry_signing_key_id, "289EF1F15F4B3846");
  assert.deepEqual(provenance.platforms, [
    "darwin_amd64",
    "darwin_arm64",
    "linux_amd64",
    "linux_arm64",
  ]);

  for (const archiveSha of Object.values(provenance.platform_archive_sha256)) {
    assert.match(archiveSha, /^[0-9a-f]{64}$/);
    assert.match(retained, new RegExp(`"zh:${archiveSha}"`));
  }
  assert.match(retained, /provider "registry\.terraform\.io\/zscaler\/zcc"/);
  assert.match(retained, /version\s+= "0\.1\.0-beta\.1"/);
  assert.equal((retained.match(/"h1:/g) ?? []).length, 4);
});

test("provider version and platform archive hash drift fail closed", () => {
  const retained = zccAdoptionProviderLock();
  for (const candidate of [
    retained.replace("0.1.0-beta.1", "0.1.0-beta.2"),
    retained.replace(
      "43eb063c2685b2978895fd507ee0463b4f80299502625ed408c50d31a10fb705",
      "0".repeat(64),
    ),
  ]) {
    assert.throws(
      () => requireZccAdoptionProviderLock(candidate),
      (error: unknown) => {
        assert.ok(error instanceof ProcessFailure);
        assert.equal(error.code, "INVALID_ZCC_ADOPTION_PROVIDER_LOCK");
        assert.equal(JSON.stringify(error).includes(candidate), false);
        return true;
      },
    );
  }
});
