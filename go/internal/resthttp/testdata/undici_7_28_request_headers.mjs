import assert from "node:assert/strict";
import net from "node:net";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { ProxyAgent, request } = require("undici");
const undiciPackage = require("undici/package.json");

assert.equal(process.version, "v24.15.0");
assert.equal(undiciPackage.version, "7.28.0");

async function capture(headers) {
  let connections = 0;
  let wire = Buffer.alloc(0);
  const server = net.createServer((socket) => {
    connections += 1;
    socket.on("data", (chunk) => {
      wire = Buffer.concat([wire, chunk]);
      if (wire.includes(Buffer.from("\r\n\r\n"))) {
        socket.end("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n");
      }
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  let failure;
  try {
    const response = await request(`http://127.0.0.1:${server.address().port}/`, { headers });
    await response.body.dump();
  } catch (error) {
    failure = error;
  }
  await new Promise((resolve) => server.close(resolve));
  return { connections, wire, failure };
}

async function captureProxy(host) {
  let connectWire;
  let innerWire;
  const server = net.createServer((socket) => {
    let pending = Buffer.alloc(0);
    let phase = 0;
    socket.on("data", (chunk) => {
      pending = Buffer.concat([pending, chunk]);
      const end = pending.indexOf(Buffer.from("\r\n\r\n"));
      if (end === -1) return;
      const head = pending.subarray(0, end + 4);
      pending = pending.subarray(end + 4);
      if (phase === 0) {
        connectWire = head;
        phase = 1;
        socket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
      } else {
        innerWire = head;
        socket.end("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n");
      }
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const dispatcher = new ProxyAgent(`http://127.0.0.1:${server.address().port}`);
  const response = await request("http://127.0.0.1:4444/", {
    dispatcher,
    headers: { Host: host },
  });
  await response.body.dump();
  await dispatcher.close();
  await new Promise((resolve) => server.close(resolve));
  return { connectWire, innerWire };
}

function rawHeaderValue(wire, name) {
  const prefix = Buffer.from(`\r\n${name}: `, "latin1");
  const start = wire.indexOf(prefix);
  assert.notEqual(start, -1, `${name} was not emitted`);
  const valueStart = start + prefix.length;
  const end = wire.indexOf(Buffer.from("\r\n"), valueStart);
  assert.notEqual(end, -1);
  return wire.subarray(valueStart, end);
}

for (const value of ["", " ", "\t", " a\"`{|}/?#@\\<>^\x80\xff \t"]) {
  const result = await capture({ Host: value });
  assert.equal(result.failure, undefined);
  assert.equal(result.connections, 1);
  assert.deepEqual(rawHeaderValue(result.wire, "host"), Buffer.from(value, "latin1"));
}

for (const value of ["", " a\"`{|}/?#@\\<>^\x80\xff \t"]) {
  const result = await captureProxy(value);
  assert.deepEqual(rawHeaderValue(result.connectWire, "host"), Buffer.from("127.0.0.1:4444"));
  assert.deepEqual(rawHeaderValue(result.innerWire, "host"), Buffer.from(value, "latin1"));
}

for (const value of ["\0", "\n", "\r", "\x1f", "\x7f", "\u0100", "[unterminated"]) {
  const result = await capture({ Host: value });
  assert.equal(result.connections, 0, JSON.stringify(value));
  if (value === "[unterminated") {
    assert.equal(result.failure?.name, "AssertionError");
    assert.equal(result.failure?.code, "ERR_ASSERTION");
  } else {
    assert.equal(result.failure?.name, "InvalidArgumentError");
    assert.equal(result.failure?.code, "UND_ERR_INVALID_ARG");
  }
}

for (const [value, expected] of [["close", "close"], ["foo", "keep-alive"]]) {
  const result = await capture({ Connection: value });
  assert.equal(result.failure, undefined);
  assert.deepEqual(rawHeaderValue(result.wire, "connection"), Buffer.from(expected));
  assert.equal(result.wire.toString("latin1").match(/\r\nconnection:/gi)?.length, 1);
}

for (const headers of [
  { Connection: "a b" },
  { "Keep-Alive": "timeout=5" },
  { Upgrade: "websocket" },
]) {
  const result = await capture(headers);
  assert.equal(result.connections, 0);
  assert.equal(result.failure?.name, "InvalidArgumentError");
  assert.equal(result.failure?.code, "UND_ERR_INVALID_ARG");
}

{
  const result = await capture({ "User-Agent": "" });
  assert.equal(result.failure, undefined);
  assert.deepEqual(rawHeaderValue(result.wire, "User-Agent"), Buffer.alloc(0));
}

{
  const result = await capture({});
  assert.equal(result.failure, undefined);
  assert.equal(result.wire.toString("latin1").includes("\r\nUser-Agent:"), false);
  assert.equal(result.wire.toString("latin1").includes("\r\nuser-agent:"), false);
}

process.stdout.write("undici 7.28 request-header regression: ok\n");
