#!/usr/bin/env node

// Captures the exact normalized Node 24.15 filesystem errors used by
// transform_artifacts_fserr_test.go. Run from the Go module root:
//
//   node internal/tfrender/testdata/capture-node24-transform-filesystem-errors.mjs
//
// The probe deliberately requires the original unprivileged Darwin/arm64
// oracle environment. Exit 77 means the host cannot reproduce that oracle.

import {
  chmod,
  lstat,
  mkdir,
  mkdtemp,
  rename,
  rm,
  unlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import process from "node:process";

const authority = {
  nodeVersion: "v24.15.0",
  nodeCommit: "848430679556aed0bd073f2bc263331ad84fa119",
  platform: "darwin",
  arch: "arm64",
  release: "24.6.0",
  uid: 501,
  euid: 501,
};

const actual = {
  nodeVersion: process.version,
  platform: process.platform,
  arch: process.arch,
  release: os.release(),
  uid: process.getuid?.(),
  euid: process.geteuid?.(),
};
for (const key of ["nodeVersion", "platform", "arch", "release", "uid", "euid"]) {
  if (actual[key] !== authority[key]) {
    process.stderr.write(
      `UNSUPPORTED_CAPTURE_PLATFORM: ${key}=${JSON.stringify(actual[key])}, expected ${JSON.stringify(authority[key])}\n`,
    );
    process.exit(77);
  }
}

const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-node24-transform-fserr-"));
const lockedParent = path.join(root, "locked");
const unreadableTree = path.join(root, "unreadable");
const vectors = [];

function normalize(value) {
  if (typeof value !== "string") {
    return value;
  }
  return value
    .split(root)
    .join("$ROOT")
    .replace(
      /(\$ROOT\/missing-parent\/\.batch-)[A-Za-z0-9]{6}/g,
      (_, prefix) => `${prefix}$SUFFIX`,
    );
}

async function capture(id, mapped, deferral, operation) {
  let failure;
  try {
    await operation();
  } catch (error) {
    failure = error;
  }
  if (failure === undefined) {
    throw new Error(`${id}: operation unexpectedly succeeded`);
  }
  for (const field of ["code", "errno", "syscall", "path", "message"]) {
    if (failure[field] === undefined) {
      throw new Error(`${id}: captured error has no ${field}: ${failure}`);
    }
  }

  const vector = {
    id,
    mapped,
    code: failure.code,
    errno: failure.errno,
    syscall: failure.syscall,
    path: normalize(failure.path),
  };
  if (failure.dest !== undefined) {
    vector.dest = normalize(failure.dest);
  }
  vector.message = normalize(failure.message);
  if (deferral !== "") {
    vector.deferral = deferral;
  }
  vectors.push(vector);
}

async function ignoreFailure(operation) {
  try {
    await operation();
  } catch {
    // Best-effort restoration for oracle cleanup only.
  }
}

try {
  const blocker = path.join(root, "blocker");
  await writeFile(blocker, "not a directory", { mode: 0o600 });
  await capture("mkdir_through_file", true, "", () =>
    mkdir(path.join(blocker, "parent"), { recursive: true }),
  );

  const directory = path.join(root, "directory");
  await mkdir(directory, { mode: 0o700 });
  await capture("write_file_directory", true, "", () =>
    writeFile(directory, "contents"),
  );

  await capture("lstat_through_file", true, "", () =>
    lstat(path.join(blocker, "child")),
  );
  await capture("rename_through_file", true, "", () =>
    rename(path.join(blocker, "child"), path.join(root, "dest")),
  );

  const sourceDestFile = path.join(root, "source-dest-file");
  await writeFile(sourceDestFile, "source", { mode: 0o600 });
  await capture("rename_dest_through_file", true, "", () =>
    rename(sourceDestFile, path.join(blocker, "backup")),
  );

  const sourceMissingParent = path.join(root, "source-missing-parent");
  await writeFile(sourceMissingParent, "source", { mode: 0o600 });
  await capture("rename_dest_missing_parent", true, "", () =>
    rename(sourceMissingParent, path.join(root, "missing", "target")),
  );

  await capture(
    "unlink_missing",
    false,
    "nodefserr has no unlink operation; raw ENOENT remains a control-flow success and other unlink errors remain untranslated",
    () => unlink(path.join(root, "missing-unlink")),
  );
  await capture(
    "mkdtemp_missing_parent",
    false,
    "nodefserr has no mkdtemp operation and the observable error path includes the generated suffix",
    () => mkdtemp(path.join(root, "missing-parent", ".batch-")),
  );
  await capture(
    "chmod_missing",
    false,
    "nodefserr has no chmod operation",
    () => chmod(path.join(root, "missing-chmod"), 0o600),
  );

  const lockedVictim = path.join(lockedParent, "victim");
  await mkdir(lockedVictim, { recursive: true, mode: 0o700 });
  await chmod(lockedParent, 0o555);
  await capture(
    "rm_locked_parent",
    false,
    "recursive rm can surface internal traversal operations; nodefserr has no rm/rmdir operation",
    () => rm(lockedVictim, { recursive: true, force: true }),
  );
  await chmod(lockedParent, 0o700);

  await mkdir(unreadableTree, { mode: 0o700 });
  await writeFile(path.join(unreadableTree, "child"), "contents", { mode: 0o600 });
  await chmod(unreadableTree, 0);
  await capture(
    "rm_unreadable_tree",
    false,
    "the same recursive rm API can instead surface scandir, so cleanup errors remain untranslated pending an operation-aware contract",
    () => rm(unreadableTree, { recursive: true, force: true }),
  );
  await chmod(unreadableTree, 0o700);
} finally {
  await ignoreFailure(() => chmod(lockedParent, 0o700));
  await ignoreFailure(() => chmod(unreadableTree, 0o700));
  await ignoreFailure(() => rm(root, { recursive: true, force: true }));
}

const fixture = {
  schema_version: 1,
  captured_at: "2026-07-17",
  node_version: authority.nodeVersion,
  node_commit: authority.nodeCommit,
  platform: "darwin/arm64 (Darwin 24.6.0, uid/euid 501)",
  api: "node:fs/promises",
  normalization: "fresh temporary root -> $ROOT; mkdtemp's six generated characters -> $SUFFIX",
  capture_command: "node internal/tfrender/testdata/capture-node24-transform-filesystem-errors.mjs",
  vectors,
};
process.stdout.write(`${JSON.stringify(fixture, null, 2)}\n`);
