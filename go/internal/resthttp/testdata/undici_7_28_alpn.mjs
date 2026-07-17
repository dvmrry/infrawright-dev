import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import tls from "node:tls";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { Client } = require("undici");
const undiciPackage = require("undici/package.json");

assert.equal(process.version, "v24.15.0");
assert.equal(undiciPackage.version, "7.28.0");

const scratch = mkdtempSync(join(tmpdir(), "undici-7-28-alpn-"));
try {
  const keyPath = join(scratch, "server.key");
  const certificatePath = join(scratch, "server.pem");
  execFileSync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes",
    "-keyout", keyPath,
    "-out", certificatePath,
    "-days", "1",
    "-subj", "/CN=localhost",
    "-addext", "subjectAltName=DNS:localhost",
  ], { stdio: "ignore" });

  let negotiated;
  const server = tls.createServer({
    key: readFileSync(keyPath),
    cert: readFileSync(certificatePath),
    ALPNProtocols: ["h2", "http/1.1"],
  }, (socket) => {
    negotiated = socket.alpnProtocol;
    socket.once("data", () => {
      socket.end("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n");
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const client = new Client(`https://127.0.0.1:${server.address().port}`, {
    connect: { rejectUnauthorized: false },
  });
  const response = await client.request({ method: "GET", path: "/" });
  await response.body.dump();
  await client.close();
  await new Promise((resolve) => server.close(resolve));
  assert.equal(negotiated, "http/1.1");
} finally {
  rmSync(scratch, { recursive: true, force: true });
}

process.stdout.write("undici 7.28 ALPN regression: ok\n");
