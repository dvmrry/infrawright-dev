#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";

const SUPPORTED_PLATFORMS = [
  ["darwin", "arm64"],
  ["darwin", "x64"],
  ["linux", "arm64"],
  ["linux", "x64"],
];
const PUBLIC_REGISTRY_HOSTS = new Set(["registry.npmjs.org"]);
const INSTALL_SCRIPT_NETWORK_RISKS = new Map([
  [
    "esbuild",
    "may run nested npm and then download directly from registry.npmjs.org when its platform package is absent",
  ],
]);

function usage(message) {
  if (message !== undefined) process.stderr.write(`error: ${message}\n`);
  process.stderr.write(
    "usage: node scripts/build-environment-preflight.mjs "
    + "[--lock <package-lock.json>] [--npm <executable>] "
    + "[--platform <platform>] [--arch <arch>] [--manifest]\n",
  );
  process.exit(message === undefined ? 0 : 2);
}

function parseArguments(arguments_) {
  const options = {
    lock: "package-lock.json",
    npm: process.env.NPM?.trim() || "npm",
    platform: process.platform,
    arch: process.arch,
    manifest: false,
  };
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "--manifest") {
      options.manifest = true;
      index += 1;
    } else if (argument === "--help" || argument === "-h") {
      usage();
    } else if (
      argument === "--lock"
      || argument === "--npm"
      || argument === "--platform"
      || argument === "--arch"
    ) {
      const value = arguments_[index + 1];
      if (value === undefined || value.length === 0) usage(`${argument} requires a value`);
      options[argument.slice(2)] = value;
      index += 2;
    } else {
      usage(`unknown argument ${String(argument)}`);
    }
  }
  return options;
}

function packageName(lockPath) {
  const marker = "node_modules/";
  const index = lockPath.lastIndexOf(marker);
  return index < 0 ? undefined : lockPath.slice(index + marker.length);
}

function spec(item) {
  return `${item.name}@${item.version}`;
}

function platformMatches(item, platform, arch) {
  const osMatches = item.os === undefined || item.os.includes(platform);
  const cpuMatches = item.cpu === undefined || item.cpu.includes(arch);
  return osMatches && cpuMatches;
}

function inventory(lock) {
  if (lock.lockfileVersion !== 3 || typeof lock.packages !== "object" || lock.packages === null) {
    throw new Error("package-lock.json must use lockfileVersion 3 with package metadata");
  }
  const ordinary = [];
  const platform = [];
  const resolvedHosts = new Map();
  for (const [lockPath, item] of Object.entries(lock.packages)) {
    const name = packageName(lockPath);
    if (name === undefined || typeof item.version !== "string") continue;
    const record = {
      name,
      version: item.version,
      integrity: typeof item.integrity === "string" ? item.integrity : undefined,
      optional: item.optional === true,
      os: Array.isArray(item.os) ? item.os : undefined,
      cpu: Array.isArray(item.cpu) ? item.cpu : undefined,
      hasInstallScript: item.hasInstallScript === true,
    };
    if (typeof item.resolved === "string") {
      try {
        const host = new URL(item.resolved).hostname.toLowerCase();
        resolvedHosts.set(host, (resolvedHosts.get(host) ?? 0) + 1);
      } catch {
        throw new Error(`lock entry ${name} has an invalid resolved URL`);
      }
    }
    if (record.optional && (record.os !== undefined || record.cpu !== undefined)) {
      platform.push(record);
    } else {
      ordinary.push(record);
    }
  }
  const bySpec = (left, right) => spec(left).localeCompare(spec(right));
  ordinary.sort(bySpec);
  platform.sort(bySpec);
  return { ordinary, platform, resolvedHosts };
}

function platformPackages(items, platform, arch) {
  return items.filter((item) => platformMatches(item, platform, arch));
}

function renderManifest(items) {
  const lines = [
    "Infrawright source-build mirror manifest",
    "",
    `Ordinary packages (${items.ordinary.length}):`,
    ...items.ordinary.map((item) => `  ${spec(item)}`),
    "",
    "Platform packages:",
  ];
  for (const [platform, arch] of SUPPORTED_PLATFORMS) {
    lines.push(`  ${platform}/${arch}:`);
    lines.push(...platformPackages(items.platform, platform, arch).map((item) => {
      return `    ${spec(item)}`;
    }));
  }
  const installScripts = items.ordinary.filter((item) => item.hasInstallScript);
  lines.push("", "Install-script packages:");
  for (const item of installScripts) {
    const risk = INSTALL_SCRIPT_NETWORK_RISKS.get(item.name)
      ?? "contains an unreviewed install script";
    lines.push(`  ${spec(item)} — ${risk}`);
  }
  lines.push(
    "",
    "Supported repository build command: npm ci --ignore-scripts, then npm run build.",
  );
  return `${lines.join("\n")}\n`;
}

function runNpm(executable, arguments_) {
  return spawnSync(executable, arguments_, {
    encoding: "utf8",
    env: process.env,
    maxBuffer: 1024 * 1024,
    timeout: 20_000,
  });
}

function configValue(npm, key) {
  const result = runNpm(npm, ["config", "get", key]);
  if (result.error !== undefined || result.status !== 0) {
    throw new Error(`npm could not read the sanitized ${key} setting`);
  }
  const value = result.stdout.trim();
  return value === "undefined" || value === "null" ? "" : value;
}

function registryHost(value) {
  try {
    return new URL(value).hostname.toLowerCase();
  } catch {
    throw new Error("the configured npm registry URL is invalid");
  }
}

function packageScope(name) {
  return name.startsWith("@") ? name.slice(0, name.indexOf("/")) : undefined;
}

function viewPackage(npm, item) {
  const result = runNpm(npm, [
    "view",
    spec(item),
    "version",
    "dist.integrity",
    "--json",
  ]);
  if (result.error !== undefined) return { state: "unresolved" };
  if (result.status !== 0) {
    return {
      state: /(?:E404|404 Not Found|not found)/iu.test(result.stderr)
        ? "missing"
        : "unresolved",
    };
  }
  try {
    const document = JSON.parse(result.stdout);
    const version = document.version;
    const integrity = document["dist.integrity"] ?? document.dist?.integrity;
    if (version !== item.version || integrity !== item.integrity) {
      return { state: "mismatch" };
    }
  } catch {
    return { state: "unresolved" };
  }
  return { state: "available" };
}

function sourceBuildUnavailable(lines) {
  process.stderr.write("Source build unavailable in the configured registry.\n");
  for (const line of lines) process.stderr.write(`${line}\n`);
  process.stderr.write(
    "The operational CLI can still be used from the prebuilt, checksum-verified "
    + "dist/infrawright-cli.mjs artifact. Mirror the listed build packages before "
    + "requiring local source builds.\n",
  );
  process.exit(1);
}

const options = parseArguments(process.argv.slice(2));
const lockFile = path.resolve(options.lock);
let lock;
try {
  lock = JSON.parse(await readFile(lockFile, "utf8"));
} catch (error) {
  process.stderr.write(`error: unable to read package lock: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exit(2);
}

let items;
try {
  items = inventory(lock);
} catch (error) {
  process.stderr.write(`error: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exit(2);
}
if (options.manifest) {
  process.stdout.write(renderManifest(items));
  process.exit(0);
}

if (!SUPPORTED_PLATFORMS.some(([platform, arch]) => {
  return platform === options.platform && arch === options.arch;
})) {
  sourceBuildUnavailable([
    `Unsupported source-build platform: ${options.platform}/${options.arch}`,
  ]);
} else {
  let registry;
  let replaceRegistryHost;
  let omit;
  let optional;
  try {
    registry = registryHost(configValue(options.npm, "registry"));
    replaceRegistryHost = configValue(options.npm, "replace-registry-host") || "npmjs";
    omit = configValue(options.npm, "omit");
    optional = configValue(options.npm, "optional");
  } catch (error) {
    sourceBuildUnavailable([
      error instanceof Error ? error.message : "npm configuration could not be read",
    ]);
  }

  const currentPlatform = platformPackages(items.platform, options.platform, options.arch);
  const required = [...items.ordinary, ...currentPlatform];
  const lockPublicCount = [...items.resolvedHosts.entries()]
    .filter(([host]) => PUBLIC_REGISTRY_HOSTS.has(host))
    .reduce((total, [, count]) => total + count, 0);
  const policyErrors = [];
  if (omit.split(/[\s,]+/u).includes("optional") || optional === "false") {
    policyErrors.push("npm is configured to omit required optional platform packages");
  }
  if (
    !PUBLIC_REGISTRY_HOSTS.has(registry)
    && lockPublicCount > 0
    && replaceRegistryHost === "never"
  ) {
    policyErrors.push(
      "replace-registry-host=never would leave public lockfile URLs authoritative",
    );
  }
  const scopes = [...new Set(required.map((item) => packageScope(item.name)).filter(Boolean))];
  for (const scope of scopes) {
    let configured;
    try {
      configured = configValue(options.npm, `${scope}:registry`);
    } catch (error) {
      policyErrors.push(error instanceof Error ? error.message : `unable to inspect ${scope}`);
      continue;
    }
    if (configured.length === 0) continue;
    let host;
    try {
      host = registryHost(configured);
    } catch {
      policyErrors.push(`${scope} has an invalid registry URL`);
      continue;
    }
    if (!PUBLIC_REGISTRY_HOSTS.has(registry) && PUBLIC_REGISTRY_HOSTS.has(host)) {
      policyErrors.push(`${scope} is configured to bypass the internal registry via ${host}`);
    }
  }

  process.stdout.write(`Configured registry host: ${registry}\n`);
  process.stdout.write(`Lockfile public-registry URLs: ${lockPublicCount}\n`);
  process.stdout.write(`Registry-host replacement: ${replaceRegistryHost}\n`);
  process.stdout.write(`Current source-build platform: ${options.platform}/${options.arch}\n`);
  process.stdout.write("Required platform packages:\n");
  for (const item of currentPlatform) process.stdout.write(`  ${spec(item)}\n`);
  const installScripts = items.ordinary.filter((item) => item.hasInstallScript);
  process.stdout.write(
    `Install scripts in lock: ${installScripts.map((item) => spec(item)).join(", ") || "none"}\n`,
  );
  for (const item of installScripts) {
    const risk = INSTALL_SCRIPT_NETWORK_RISKS.get(item.name)
      ?? "contains an unreviewed install script";
    process.stdout.write(`  ${spec(item)}: ${risk}\n`);
  }
  process.stdout.write("Supported install policy: scripts disabled\n");

  if (policyErrors.length > 0) {
    sourceBuildUnavailable(policyErrors);
  } else {
    const failures = required.map((item) => {
      return { item, result: viewPackage(options.npm, item) };
    }).filter(({ result }) => result.state !== "available");
    if (failures.length === 0) {
      process.stdout.write("Source build environment is suitable for the pinned dependency set.\n");
    } else {
      const missing = failures.filter(({ result }) => result.state === "missing");
      const mismatched = failures.filter(({ result }) => result.state === "mismatch");
      const unresolved = failures.filter(({ result }) => result.state === "unresolved");
      sourceBuildUnavailable([
        ...missing.map(({ item }) => `Missing mirrored build dependency: ${spec(item)}`),
        ...mismatched.map(({ item }) => `Registry metadata does not match the lock: ${spec(item)}`),
        ...unresolved.map(({ item }) => `Registry resolution failed without a verified 404: ${spec(item)}`),
      ]);
    }
  }
}
