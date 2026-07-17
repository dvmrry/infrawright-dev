import assert from "node:assert/strict";
import fs from "node:fs";
import { createRequire } from "node:module";
import net from "node:net";
import { pathToFileURL } from "node:url";

import { Cookie, CookieJar } from "tough-cookie";
import { request } from "undici";

const require = createRequire(import.meta.url);
const toughCookiePackage = JSON.parse(
  fs.readFileSync(
    new URL("../package.json", pathToFileURL(require.resolve("tough-cookie"))),
    "utf8",
  ),
);
const undiciPackage = JSON.parse(
  fs.readFileSync(
    new URL("./package.json", pathToFileURL(require.resolve("undici"))),
    "utf8",
  ),
);
const corpus = JSON.parse(
  fs.readFileSync(new URL("./tough_cookie_6_0_2_values.json", import.meta.url), "utf8"),
);

assert.equal(`node ${process.version}`, corpus.oracle.runtime);
assert.equal(toughCookiePackage.version, corpus.oracle.package_version);
assert.equal(undiciPackage.version, corpus.oracle.wire_package_version);

for (const vector of corpus.value_vectors) {
  const parsed = Cookie.parse(vector.set_cookie);
  assert.equal(parsed?.value, vector.parsed_value, vector.name);

  const jar = new CookieJar();
  jar.setCookieSync(vector.set_cookie, "https://example.test/start", {
    ignoreError: true,
  });
  assert.equal(
    jar.getCookieStringSync("https://example.test/final"),
    vector.want_cookie,
    vector.name,
  );
}

for (const vector of corpus.name_vectors) {
  const parsed = Cookie.parse(vector.set_cookie);
  assert.equal(parsed?.key, vector.parsed_key, vector.name);

  const jar = new CookieJar();
  jar.setCookieSync(vector.set_cookie, "https://example.test/start", {
    ignoreError: true,
  });
  assert.equal(
    jar.getCookieStringSync("https://example.test/final"),
    vector.want_cookie,
    vector.name,
  );
}

for (const vector of corpus.attribute_vectors) {
  const parsed = Cookie.parse(vector.set_cookie);
  assert.deepEqual(
    {
      domain: parsed?.domain ?? null,
      path: parsed?.path ?? null,
      secure: parsed?.secure ?? false,
      http_only: parsed?.httpOnly ?? false,
      same_site: parsed?.sameSite ?? null,
      max_age: parsed?.maxAge ?? null,
      expires:
        parsed?.expires instanceof Date
          ? parsed.expires.toISOString()
          : parsed?.expires,
    },
    vector.parsed,
    vector.name,
  );

  const jar = new CookieJar();
  jar.setCookieSync(vector.set_cookie, vector.set_url, { ignoreError: true });
  assert.equal(
    jar.getCookieStringSync(vector.get_url),
    vector.want_cookie,
    vector.name,
  );
}

for (const vector of corpus.domain_scope_vectors) {
  const jar = new CookieJar();
  const stored = jar.setCookieSync(vector.set_cookie, vector.set_url, {
    ignoreError: true,
  });
  assert.deepEqual(
    {
      domain: stored?.domain ?? null,
      host_only: stored?.hostOnly ?? null,
    },
    {
      domain: vector.stored_domain,
      host_only: vector.host_only,
    },
    vector.name,
  );
  assert.equal(
    jar.getCookieStringSync(vector.get_url),
    vector.want_cookie,
    vector.name,
  );
}

const rejectedWireVectors = [
  ...corpus.value_vectors,
  ...corpus.name_vectors,
].filter((vector) => !vector.production_wire);
assert.equal(rejectedWireVectors.length, 2);
let connections = 0;
const server = net.createServer(() => {
  connections += 1;
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
for (const vector of rejectedWireVectors) {
  let failure;
  try {
    await request(`http://127.0.0.1:${server.address().port}/`, {
      headers: { cookie: vector.want_cookie },
    });
  } catch (error) {
    failure = error;
  }
  assert.equal(failure?.name, "InvalidArgumentError", vector.name);
  assert.equal(failure?.code, "UND_ERR_INVALID_ARG", vector.name);
  assert.equal(failure?.message, "invalid cookie header", vector.name);
}
await new Promise((resolve) => server.close(resolve));
assert.equal(connections, 0);

process.stdout.write("tough-cookie 6.0.2 request-pair corpus: ok\n");
