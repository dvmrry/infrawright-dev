import assert from "node:assert/strict";
import net from "node:net";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { request } = require("undici");
const undiciPackage = require("undici/package.json");

assert.equal(process.version, "v24.15.0");
assert.equal(undiciPackage.version, "7.28.0");

let connections = 0;
let wire = Buffer.alloc(0);
const server = net.createServer((socket) => {
  connections += 1;
  socket.on("data", (chunk) => {
    wire = Buffer.concat([wire, chunk]);
  });
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));

let failure;
try {
  await request(`http://127.0.0.1:${server.address().port}/`, {
    method: "POST",
    body: Buffer.from("abc"),
    // This is deliberately a raw string. JavaScript source `+3` without
    // quotes is the number 3 and exercises a different coercion path.
    headers: { "Content-Length": "+3" },
  });
} catch (error) {
  failure = error;
}
await new Promise((resolve) => server.close(resolve));

assert.equal(failure?.name, "InvalidArgumentError");
assert.equal(failure?.code, "UND_ERR_INVALID_ARG");
assert.equal(failure?.message, "invalid content-length header");
assert.equal(connections, 0);
assert.equal(wire.length, 0);

process.stdout.write("undici 7.28 raw-string Content-Length regression: ok\n");
