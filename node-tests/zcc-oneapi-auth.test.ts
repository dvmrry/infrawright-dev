import assert from "node:assert/strict";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  parseZccOneApiTokenResponseJson,
  renderZccOneApiTokenForm,
  snapshotZccOneApiCredentials,
  zccOneApiTokenLease,
  ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES,
} from "../node-src/domain/zcc-oneapi-auth.js";

function captureFailure(run: () => unknown, code: string): ProcessFailure {
  let failure: unknown;
  try {
    run();
  } catch (error: unknown) {
    failure = error;
  }
  assert.ok(failure instanceof ProcessFailure);
  assert.equal(failure.code, code);
  return failure;
}

test("OneAPI credentials are detached and form encoding is exact by value", () => {
  const credentials = {
    clientId: "client ~ value",
    clientSecret: "secret&=value",
  };
  const snapshot = snapshotZccOneApiCredentials(credentials);
  credentials.clientId = "changed";
  assert.deepEqual(snapshot, {
    clientId: "client ~ value",
    clientSecret: "secret&=value",
  });
  assert.ok(Object.isFrozen(snapshot));

  const form = renderZccOneApiTokenForm(
    snapshot,
    "https://api.zscaler.com",
  );
  assert.equal(
    form,
    "grant_type=client_credentials&client_id=client+%7E+value&client_secret=secret%26%3Dvalue&audience=https%3A%2F%2Fapi.zscaler.com",
  );
  assert.deepEqual([...new URLSearchParams(form)], [
    ["grant_type", "client_credentials"],
    ["client_id", "client ~ value"],
    ["client_secret", "secret&=value"],
    ["audience", "https://api.zscaler.com"],
  ]);
});

test("credential boundaries reject proxies, accessors, extras, and unsafe strings statically", () => {
  const secret = "must-not-escape";
  const revoked = Proxy.revocable({ clientId: "id", clientSecret: secret }, {});
  revoked.revoke();
  const hostile: unknown[] = [
    revoked.proxy,
    new Proxy({ clientId: "id", clientSecret: secret }, {}),
    { clientId: "id", clientSecret: secret, extra: true },
    { clientId: "", clientSecret: secret },
    { clientId: "id\0", clientSecret: secret },
    { clientId: "id", clientSecret: "x".repeat(16 * 1024 + 1) },
  ];
  const accessor: Record<string, unknown> = { clientId: "id" };
  Object.defineProperty(accessor, "clientSecret", {
    enumerable: true,
    get() {
      throw new Error(secret);
    },
  });
  hostile.push(accessor);
  for (const candidate of hostile) {
    const failure = captureFailure(
      () => snapshotZccOneApiCredentials(candidate as never),
      "INVALID_ZCC_ONEAPI_CREDENTIALS",
    );
    assert.equal(JSON.stringify(failure).includes(secret), false);
    assert.equal(failure.message.includes(secret), false);
  }
});

test("token response accepts the official bounded shapes and computes a monotonic lease", () => {
  assert.deepEqual(
    parseZccOneApiTokenResponseJson(
      '{"access_token":"token","token_type":"Bearer","expires_in":3600}',
    ),
    { accessToken: "token", expiresInSeconds: 3600 },
  );
  assert.deepEqual(
    parseZccOneApiTokenResponseJson(
      '{"access_token":"token","token_type":"bearer","expires_in":"60","scope":"ignored"}',
    ),
    { accessToken: "token", expiresInSeconds: 60 },
  );
  assert.deepEqual(
    parseZccOneApiTokenResponseJson(
      '{"access_token":"token","expires_in":86400}',
    ),
    { accessToken: "token", expiresInSeconds: 86_400 },
  );
  assert.deepEqual(
    zccOneApiTokenLease(
      { accessToken: "token", expiresInSeconds: 60 },
      12.75,
    ),
    {
      accessToken: "token",
      expiresAtMs: 60_012.75,
      expiresInSeconds: 60,
      refreshAtMs: 30_012.75,
    },
  );
});

test("token response rejects malformed, ambiguous, unsafe, and oversized values", () => {
  const invalid = [
    "null",
    "[]",
    "{}",
    '{"access_token":"","expires_in":60}',
    '{"access_token":"line\\ntoken","expires_in":60}',
    '{"access_token":"token","token_type":"Basic","expires_in":60}',
    '{"access_token":"token","expires_in":59}',
    '{"access_token":"token","expires_in":86401}',
    '{"access_token":"token","expires_in":60.5}',
    '{"access_token":"token","expires_in":"060"}',
    '{"access_token":"one","access_token":"two","expires_in":60}',
    "{",
    "x".repeat(ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES + 1),
  ];
  for (const payload of invalid) {
    captureFailure(
      () => parseZccOneApiTokenResponseJson(payload),
      "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
    );
  }
});

test("token lease boundary rejects revoked proxies and accessors without evaluating them", () => {
  const secret = "lease-secret";
  const revoked = Proxy.revocable(
    { accessToken: secret, expiresInSeconds: 60 },
    {},
  );
  revoked.revoke();
  const accessor: Record<string, unknown> = { expiresInSeconds: 60 };
  Object.defineProperty(accessor, "accessToken", {
    enumerable: true,
    get() {
      throw new Error(secret);
    },
  });
  for (const candidate of [revoked.proxy, accessor]) {
    const failure = captureFailure(
      () => zccOneApiTokenLease(candidate as never, 1.5),
      "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
    );
    assert.equal(failure.message.includes(secret), false);
  }
});
