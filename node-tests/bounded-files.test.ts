import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  chmod,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  sha256StableFile,
  snapshotStableFile,
} from "../node-src/io/bounded-files.js";

async function temporaryDirectory(): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), "infrawright-bounded-"));
  await chmod(directory, 0o700);
  return directory;
}

function limits(options: {
  files?: number;
  directories?: number;
  entries?: number;
  depth?: number;
  total?: bigint;
  file?: bigint;
} = {}) {
  return {
    maxFiles: options.files ?? 10,
    maxDirectories: options.directories ?? 10,
    maxDirectoryEntries: options.entries ?? 100,
    maxDepth: options.depth ?? 8,
    maxTotalBytes: options.total ?? 1024n,
    maxFileBytes: options.file ?? 1024n,
  };
}

async function failureCode(operation: Promise<unknown>): Promise<string> {
  try {
    await operation;
  } catch (error: unknown) {
    assert.ok(error instanceof ProcessFailure);
    return error.code;
  }
  assert.fail("operation unexpectedly succeeded");
}

test("stable hashing and bounded UTF-8 reads share exact budgets", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const first = path.join(directory, "first");
  const second = path.join(directory, "second");
  await writeFile(first, "alpha", { mode: 0o600 });
  await writeFile(second, "beta", { mode: 0o600 });
  const budget = new ReadBudget(limits());
  const digest = await sha256StableFile(first, budget);
  assert.equal(
    digest.sha256,
    createHash("sha256").update("alpha").digest("hex"),
  );
  assert.equal(digest.size, 5n);
  const decoded = await readBoundedUtf8File(second, budget);
  assert.equal(decoded.text, "beta");
  assert.equal(budget.files, 2);
  assert.equal(budget.bytes, 9n);
});

test("bounded UTF-8 reads preserve the BOM like Python encoding=utf-8", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, "bom");
  await writeFile(source, Buffer.from([0xef, 0xbb, 0xbf, 0x78]), { mode: 0o600 });
  const result = await readBoundedUtf8File(source, new ReadBudget(limits()));
  assert.equal(result.text, "\ufeffx");
});

test("file, aggregate byte, and count limits fail before unbounded reads", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const first = path.join(directory, "first");
  const second = path.join(directory, "second");
  await writeFile(first, "12345", { mode: 0o600 });
  await writeFile(second, "67890", { mode: 0o600 });
  assert.equal(
    await failureCode(sha256StableFile(first, new ReadBudget(limits({ file: 4n })))),
    "FILE_LIMIT_EXCEEDED",
  );
  const totalBudget = new ReadBudget(limits({ total: 9n }));
  await sha256StableFile(first, totalBudget);
  assert.equal(
    await failureCode(sha256StableFile(second, totalBudget)),
    "BYTE_BUDGET_EXCEEDED",
  );
  const countBudget = new ReadBudget(limits({ files: 1 }));
  await sha256StableFile(first, countBudget);
  assert.equal(
    await failureCode(sha256StableFile(second, countBudget)),
    "FILE_COUNT_EXCEEDED",
  );
});

test("directory count, entry, and depth budgets are explicit and bounded", () => {
  const count = new ReadBudget(limits({ directories: 1 }));
  count.enterDirectory(0);
  assert.throws(() => count.enterDirectory(0), (error: unknown) => {
    return error instanceof ProcessFailure && error.code === "DIRECTORY_COUNT_EXCEEDED";
  });

  const entries = new ReadBudget(limits({ entries: 1 }));
  entries.reserveDirectoryEntry();
  assert.throws(() => entries.reserveDirectoryEntry(), (error: unknown) => {
    return error instanceof ProcessFailure
      && error.code === "DIRECTORY_ENTRY_LIMIT_EXCEEDED";
  });

  const depth = new ReadBudget(limits({ depth: 1 }));
  depth.enterDirectory(1);
  assert.throws(() => depth.enterDirectory(2), (error: unknown) => {
    return error instanceof ProcessFailure && error.code === "DIRECTORY_DEPTH_EXCEEDED";
  });
  assert.equal(count.directories, 1);
  assert.equal(entries.directoryEntries, 1);
});

test("directories, symlinks, and FIFOs cannot masquerade as plan files", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, "source");
  const alias = path.join(directory, "alias");
  await writeFile(source, "plan", { mode: 0o600 });
  await symlink(source, alias);
  assert.equal(
    await failureCode(sha256StableFile(directory, new ReadBudget(limits()))),
    "NOT_REGULAR_FILE",
  );
  assert.equal(
    await failureCode(sha256StableFile(alias, new ReadBudget(limits()))),
    "SYMLINK_NOT_ALLOWED",
  );
  assert.equal(
    (await sha256StableFile(alias, new ReadBudget(limits()), {
      followSymlinks: true,
    })).size,
    4n,
  );
  if (process.platform !== "win32") {
    const fifo = path.join(directory, "fifo");
    const created = spawnSync("mkfifo", [fifo], { encoding: "utf8" });
    assert.equal(created.status, 0, created.stderr);
    assert.equal(
      await failureCode(sha256StableFile(fifo, new ReadBudget(limits()))),
      "NOT_REGULAR_FILE",
    );
  }
});

test("same-size mutation is detected through the opened descriptor", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, "source");
  await writeFile(source, "before", { mode: 0o600 });
  const result = sha256StableFile(source, new ReadBudget(limits()), {
    hooks: {
      afterOpen: async () => writeFile(source, "after!", { mode: 0o600 }),
    },
  });
  assert.equal(await failureCode(result), "FILE_CHANGED");
});

test("path replacement is detected even when the opened bytes stay stable", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, "source");
  const replacement = path.join(directory, "replacement");
  await writeFile(source, "stable", { mode: 0o600 });
  await writeFile(replacement, "stable", { mode: 0o600 });
  const result = sha256StableFile(source, new ReadBudget(limits()), {
    hooks: {
      beforeFinalStat: async () => rename(replacement, source),
    },
  });
  assert.equal(await failureCode(result), "FILE_CHANGED");
});

test("snapshot binds bytes, digest, size, and private mode", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const snapshots = path.join(directory, "snapshots");
  await mkdir(snapshots, { mode: 0o700 });
  await chmod(snapshots, 0o700);
  const source = path.join(directory, "tfplan");
  const bytes = Buffer.from([0, 1, 2, 3, 255]);
  await writeFile(source, bytes, { mode: 0o600 });
  const snapshot = await snapshotStableFile({
    sourcePath: source,
    privateDirectory: snapshots,
    budget: new ReadBudget(limits()),
  });
  assert.deepEqual(await readFile(snapshot.path), bytes);
  assert.equal(snapshot.size, 5n);
  assert.equal(
    snapshot.sha256,
    createHash("sha256").update(bytes).digest("hex"),
  );
  const metadata = await lstat(snapshot.path);
  assert.equal(metadata.mode & 0o777, 0o600);
});

test("unsafe snapshot directories and invalid UTF-8 fail without leaking content", async (context) => {
  const directory = await temporaryDirectory();
  context.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, "source");
  await writeFile(source, Buffer.from([0xff, 0x53, 0x45, 0x43, 0x52, 0x45, 0x54]));
  await chmod(directory, 0o755);
  assert.equal(
    await failureCode(snapshotStableFile({
      sourcePath: source,
      privateDirectory: directory,
      budget: new ReadBudget(limits()),
    })),
    "UNSAFE_SNAPSHOT_DIRECTORY",
  );
  await chmod(directory, 0o700);
  try {
    await readBoundedUtf8File(source, new ReadBudget(limits()));
    assert.fail("invalid UTF-8 unexpectedly decoded");
  } catch (error: unknown) {
    assert.ok(error instanceof ProcessFailure);
    assert.equal(error.code, "INVALID_UTF8");
    assert.equal(error.message.includes("SECRET"), false);
  }
});
