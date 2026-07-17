import assert from "node:assert/strict";
import net from "node:net";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { ProxyAgent, request } = require("undici");
const undiciPackage = require("undici/package.json");

assert.equal(process.version, "v24.15.0");
assert.equal(undiciPackage.version, "7.28.0");
assert.equal(require("node:http").maxHeaderSize, 16_384);

async function exchange(raw, { holdOpen = false } = {}) {
  let activeSocket;
  const server = net.createServer((socket) => {
    activeSocket = socket;
    socket.once("data", () => {
      if (holdOpen) socket.write(Buffer.from(raw, "latin1"));
      else socket.end(Buffer.from(raw, "latin1"));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  let response;
  let failure;
  try {
    response = await request(`http://127.0.0.1:${server.address().port}/`, {
      headersTimeout: 1_000,
      bodyTimeout: 1_000,
    });
  } catch (error) {
    failure = error;
  }
  return {
    response,
    failure,
    async close() {
      response?.body.destroy();
      activeSocket?.destroy();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}

async function proxyExchange(valueLength) {
  let innerRequest = false;
  const server = net.createServer((socket) => {
    let phase = 0;
    let pending = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      pending = Buffer.concat([pending, chunk]);
      const end = pending.indexOf(Buffer.from("\r\n\r\n"));
      if (end === -1) return;
      pending = pending.subarray(end + 4);
      if (phase === 0) {
        phase = 1;
        socket.write(
          `HTTP/1.1 200 OK\r\nx:${"a".repeat(valueLength)}\r\n\r\n`,
          "latin1",
        );
      } else {
        innerRequest = true;
        socket.end("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n");
      }
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const dispatcher = new ProxyAgent(`http://127.0.0.1:${server.address().port}`);
  let failure;
  try {
    const response = await request("http://127.0.0.1:4444/", { dispatcher });
    await response.body.dump();
  } catch (error) {
    failure = error;
  }
  await dispatcher.close();
  await new Promise((resolve) => server.close(resolve));
  return { failure, innerRequest };
}

for (const [name, raw, errorName, errorCode] of [
  [
    "100",
    "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
    "SocketError",
    "UND_ERR_SOCKET",
  ],
  [
    "101",
    "HTTP/1.1 101 Switching Protocols\r\nConnection: upgrade\r\nUpgrade: x\r\n\r\n",
    "SocketError",
    "UND_ERR_SOCKET",
  ],
  [
    "duplicate Content-Length",
    "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\nok",
    "ResponseContentLengthMismatchError",
    "UND_ERR_RES_CONTENT_LENGTH_MISMATCH",
  ],
  [
    "Transfer-Encoding plus Content-Length",
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 2\r\n\r\n2\r\nok\r\n0\r\n\r\n",
    "HTTPParserError",
    undefined,
  ],
  [
    "obs-fold",
    "HTTP/1.1 200 OK\r\nX: one\r\n two\r\nContent-Length: 2\r\n\r\nok",
    "HTTPParserError",
    undefined,
  ],
  [
    "signed Content-Length",
    "HTTP/1.1 200 OK\r\nContent-Length: +2\r\n\r\nok",
    "HTTPParserError",
    undefined,
  ],
]) {
  const result = await exchange(raw);
  assert.equal(result.response, undefined, `${name} unexpectedly produced a response`);
  assert.equal(result.failure?.name, errorName, name);
  assert.equal(result.failure?.code, errorCode, name);
  await result.close();
}

for (const status of ["000", "020", "099"]) {
  const result = await exchange(
    `HTTP/1.1 ${status} Invalid\r\n\r\n`
      + "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
  );
  assert.equal(result.response, undefined, status);
  assert.equal(result.failure?.name, "AssertionError", status);
  assert.equal(result.failure?.code, "ERR_ASSERTION", status);
  await result.close();
}

for (const statusLine of ["102 Processing", "103 Early Hints", "199 Informational"]) {
  const result = await exchange(
    `HTTP/1.1 ${statusLine}\r\nLink: </preload>\r\n\r\n`
      + "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
  );
  assert.equal(result.failure, undefined);
  assert.deepEqual(result.response.headers, { "content-length": "2" });
  assert.equal(await result.response.body.text(), "ok");
  await result.close();
}

{
  const result = await exchange(
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: identity\r\n\r\nok",
  );
  assert.equal(result.failure, undefined);
  assert.equal(result.response.headers["transfer-encoding"], "identity");
  assert.equal(await result.response.body.text(), "ok");
  await result.close();
}

{
  const result = await exchange(
    "HTTP/1.1 200 OK\r\n"
      + "Connection: close\r\n"
      + "Transfer-Encoding: chunked\r\n"
      + "Trailer: x-check\r\n"
      + "Pragma: no-cache\r\n\r\n"
      + "2\r\nok\r\n0\r\nX-Check: done\r\n\r\n",
  );
  assert.equal(result.failure, undefined);
  assert.deepEqual(result.response.headers, {
    connection: "close",
    "transfer-encoding": "chunked",
    trailer: "x-check",
    pragma: "no-cache",
  });
  assert.equal(await result.response.body.text(), "ok");
  await result.close();
}

const literalChunkStream = "2\r\nok\r\n0\r\n\r\n";
for (const [name, value, expectedBody] of [
  ["UTF-8-looking NBSP suffix", "chunked\xc2\xa0", literalChunkStream],
  ["UTF-8-looking EM SPACE suffix", "chunked\xe2\x80\x83", literalChunkStream],
  ["trailing HTAB", "chunked\t", literalChunkStream],
  ["space inside opaque token", "foo bar", literalChunkStream],
  ["semicolon inside opaque token", "foo;bar", literalChunkStream],
  ["parameter-like opaque token", "gzip; level=1", literalChunkStream],
  ["chunked with parameter", "chunked;foo=bar", literalChunkStream],
  ["chunked before final coding", "chunked, gzip", literalChunkStream],
  ["space before final empty candidate", "chunked ,", literalChunkStream],
  ["two final empty candidates", "chunked,,", literalChunkStream],
  ["nonfinal chunked without OWS", "chunked,gzip", literalChunkStream],
  ["chunked prefix without comma", "chunked;foo", literalChunkStream],
  ["trailing SP then HTAB", "chunked \t", literalChunkStream],
  ["leading empty candidate", ",chunked", "ok"],
  ["opaque parameter then final chunked", "gzip ; level=1, chunked", "ok"],
  ["two empty candidates before chunked", "gzip,,chunked", "ok"],
  ["repeated chunked candidates", "chunked,chunked", "ok"],
  ["ASCII case folding", "ChUnKeD", "ok"],
  ["trailing SP", "chunked  ", "ok"],
  ["leading OWS after comma", "gzip, \tchunked", "ok"],
  ["comma in opaque quote-like bytes", "gzip;foo=\",chunked", "ok"],
]) {
  const result = await exchange(
    `HTTP/1.1 200 OK\r\nTransfer-Encoding: ${value}\r\n\r\n`
      + literalChunkStream,
  );
  assert.equal(result.failure, undefined, name);
  assert.equal(await result.response.body.text(), expectedBody, name);
  await result.close();
}

for (const [name, headerLines, expectedBody] of [
  ["later nonempty field replaces chunked", ["chunked", "identity"], literalChunkStream],
  ["later chunked field replaces identity", ["identity", "chunked"], "ok"],
  ["later empty field preserves chunked", ["chunked", ""], "ok"],
  ["later OWS-only field preserves chunked", ["chunked", " \t "], "ok"],
]) {
  const headers = headerLines.map((value) => `Transfer-Encoding: ${value}\r\n`).join("");
  const result = await exchange(
    `HTTP/1.1 200 OK\r\n${headers}\r\n${literalChunkStream}`,
  );
  assert.equal(result.failure, undefined, name);
  assert.equal(await result.response.body.text(), expectedBody, name);
  await result.close();
}

for (const [name, headers, accepted] of [
  ["empty transfer encoding before content length", "Transfer-Encoding:\r\nContent-Length: 2\r\n", true],
  ["OWS-only transfer encoding before content length", "Transfer-Encoding: \t \r\nContent-Length: 2\r\n", true],
  ["repeated empty transfer encoding before content length", "Transfer-Encoding:\r\nTransfer-Encoding: \t\r\nContent-Length: 2\r\n", true],
  ["content length before empty transfer encoding", "Content-Length: 2\r\nTransfer-Encoding:\r\n", false],
  ["content length before OWS-only transfer encoding", "Content-Length: 2\r\nTransfer-Encoding: \t \r\n", false],
  ["nonempty transfer encoding before content length", "Transfer-Encoding: identity\r\nContent-Length: 2\r\n", false],
  ["nonempty then empty transfer encoding before content length", "Transfer-Encoding: chunked\r\nTransfer-Encoding:\r\nContent-Length: 2\r\n", false],
  ["content length before nonempty transfer encoding", "Content-Length: 2\r\nTransfer-Encoding: identity\r\n", false],
]) {
  const result = await exchange(`HTTP/1.1 200 OK\r\n${headers}\r\nok`);
  if (accepted) {
    assert.equal(result.failure, undefined, name);
    assert.equal(await result.response.body.text(), "ok", name);
  } else {
    assert.equal(result.response, undefined, name);
    assert.notEqual(result.failure, undefined, name);
  }
  await result.close();
}

for (const [name, value, accepted] of [
  ["leading mixed OWS", " \t 2", true],
  ["one trailing SP", "2 ", true],
  ["two trailing SP", "2  ", true],
  ["trailing HTAB", "2\t", false],
  ["SP then trailing HTAB", "2 \t", false],
  ["HTAB then trailing SP", "2\t ", false],
  ["internal SP", "2 3", false],
  ["internal HTAB", "2\t3", false],
]) {
  const result = await exchange(
    `HTTP/1.1 200 OK\r\nContent-Length:${value}\r\n\r\nok`,
  );
  if (accepted) {
    assert.equal(result.failure, undefined, name);
    assert.equal(await result.response.body.text(), "ok", name);
  } else {
    assert.equal(result.response, undefined, name);
    assert.notEqual(result.failure, undefined, name);
  }
  await result.close();
}

for (const [name, extension] of [
  ["bare semicolon", ";"],
  ["semicolon then space", "; "],
  ["semicolon then space and name", "; a"],
  ["quote where name starts", ";\""],
  ["trailing empty name", ";;"],
  ["second equals in unquoted value", ";foo==bar"],
  ["unterminated quoted value", ";foo=\"bar"],
  ["HTAB before name", ";\tfoo"],
  ["space in unquoted value", ";foo=bar baz"],
  ["bytes after quoted value", ";foo=\"bar\"x"],
  ["SP after quoted value", ";foo=\"bar\" "],
  ["HTAB after quoted value", ";foo=\"bar\"\t"],
  ["unterminated quoted pair", ";foo=\"bar\\"],
  ["escaped DEL", ";foo=\"\\\x7f\""],
]) {
  const result = await exchange(
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      + `1${extension}\r\nx\r\n0\r\n\r\n`,
  );
  assert.equal(result.failure, undefined, name);
  await assert.rejects(
    result.response.body.text(),
    (error) => error?.name === "HTTPParserError" && error?.code === undefined,
    name,
  );
  await result.close();
}

for (const [name, extension] of [
  ["name without value", ";foo"],
  ["empty unquoted value", ";foo="],
  ["empty value before next extension", ";foo=;bar"],
  ["empty name and value", ";="],
  ["unquoted value", ";foo=bar"],
  ["quoted value", ";foo=\"bar\""],
  ["empty name with value", ";=x"],
  ["empty name between semicolons", ";;foo"],
  ["multiple extensions", ";foo;bar=baz"],
  ["quoted whitespace", ";foo=\"bar baz\tqux\""],
  ["quoted pair", ";foo=\"bar\\\"baz\""],
  ["escaped obs-text", ";foo=\"\\\x80\""],
  ["quote after unquoted prefix", ";foo=bar\"baz\""],
  ["obs-text inside quote", ";foo=\"\x80\""],
  ["DEL inside quote", ";foo=\"\x7f\""],
]) {
  const result = await exchange(
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      + `1${extension}\r\nx\r\n0\r\n\r\n`,
  );
  assert.equal(result.failure, undefined, name);
  assert.equal(await result.response.body.text(), "x", name);
  await result.close();
}

for (const [name, chunkSize, accepted] of [
  ["17 digits with leading zeros", `${"0".repeat(16)}1`, true],
  ["101 digits with leading zeros", `${"0".repeat(100)}1`, true],
  ["uint64 overflow", "10000000000000000", false],
  ["uint64 overflow after leading zero", "010000000000000000", false],
]) {
  const result = await exchange(
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      + `${chunkSize}\r\nx\r\n0\r\n\r\n`,
  );
  assert.equal(result.failure, undefined, name);
  if (accepted) {
    assert.equal(await result.response.body.text(), "x", name);
  } else {
    await assert.rejects(
      result.response.body.text(),
      (error) => error?.name === "HTTPParserError" && error?.code === undefined,
      name,
    );
  }
  await result.close();
}

for (const [valueLength, accepted] of [[16_382, true], [16_383, false]]) {
  const result = await exchange(
    `HTTP/1.1 200 OK\r\nx:${"a".repeat(valueLength)}\r\n\r\n`,
  );
  if (accepted) {
    assert.equal(result.failure, undefined);
    assert.equal(result.response.headers.x.length, valueLength);
    await result.response.body.dump();
  } else {
    assert.equal(result.response, undefined);
    assert.equal(result.failure?.name, "HeadersOverflowError");
    assert.equal(result.failure?.code, "UND_ERR_HEADERS_OVERFLOW");
  }
  await result.close();
}

for (const [valueLength, accepted] of [[16_382, true], [16_383, false]]) {
  const result = await proxyExchange(valueLength);
  assert.equal(result.innerRequest, accepted);
  if (accepted) {
    assert.equal(result.failure, undefined);
  } else {
    assert.equal(result.failure?.name, "HeadersOverflowError");
    assert.equal(result.failure?.code, "UND_ERR_HEADERS_OVERFLOW");
  }
}

// Unlike initial and CONNECT response heads, Undici 7.28 does not apply
// maxHeaderSize to chunk trailers. The Go port deliberately keeps the shared
// bounded parser there as a fail-closed safety boundary.
for (const valueLength of [16_383, 65_534]) {
  const result = await exchange(
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      + `0\r\nx:${"a".repeat(valueLength)}\r\n\r\n`,
  );
  assert.equal(result.failure, undefined);
  await result.response.body.dump();
  await result.close();
}

// Chunk extensions are likewise not covered by maxHeaderSize in Undici 7.28.
// The Go port caps the raw chunk-size line separately as a safety boundary.
{
  const result = await exchange(
    "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      + `1;${"a".repeat(65_534)}\r\nx\r\n0\r\n\r\n`,
  );
  assert.equal(result.failure, undefined);
  assert.equal(await result.response.body.text(), "x");
  await result.close();
}

for (const contentLength of ["9223372036854775808", "18446744073709551615"]) {
  const result = await exchange(
    `HTTP/1.1 200 OK\r\nContent-Length: ${contentLength}\r\n\r\n`,
    { holdOpen: true },
  );
  assert.equal(result.failure, undefined);
  assert.equal(result.response.headers["content-length"], contentLength);
  await result.close();
}

process.stdout.write("undici 7.28 response-head regression: ok\n");
