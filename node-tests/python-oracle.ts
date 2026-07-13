import { spawnSync } from "node:child_process";

export interface PythonOracle {
  readonly executable: string;
  readonly pythonVersion: "3.12" | "3.13";
  readonly unicodeVersion: "15.0.0" | "15.1.0";
}

interface ResolvePythonOracleOptions {
  readonly environment?: NodeJS.ProcessEnv;
}

const PROBE = [
  "import json",
  "import sys",
  "import unicodedata",
  "json.dump({",
  '    "python": "%d.%d" % sys.version_info[:2],',
  '    "unicode": unicodedata.unidata_version,',
  "}, sys.stdout, sort_keys=True, separators=(\",\", \":\"))",
].join("\n");

const SUPPORTED: ReadonlyMap<
  string,
  Omit<PythonOracle, "executable">
> = new Map([
  ["3.12/15.0.0", { pythonVersion: "3.12", unicodeVersion: "15.0.0" }],
  ["3.13/15.1.0", { pythonVersion: "3.13", unicodeVersion: "15.1.0" }],
] as const);

function unsupported(
  executable: string,
  detail: string,
): Error {
  return new Error(
    `unsupported Python migration oracle ${JSON.stringify(executable)}: ${detail}. `
    + "Set PYTHON to Python 3.12/UCD 15.0.0 or Python 3.13/UCD 15.1.0",
  );
}

export function resolvePythonOracle(
  options: ResolvePythonOracleOptions = {},
): PythonOracle {
  const environment = options.environment ?? process.env;
  const configured = environment.PYTHON?.trim();
  const explicit = configured !== undefined && configured.length > 0;
  const candidates = explicit ? [configured] : ["python3", "python"];

  for (const executable of candidates) {
    const probe = spawnSync(executable, ["-I", "-c", PROBE], {
      encoding: "utf8",
      env: environment,
      maxBuffer: 64 * 1024,
      timeout: 10_000,
    });
    if (probe.error !== undefined) {
      const code = (probe.error as NodeJS.ErrnoException).code;
      if (!explicit && code === "ENOENT") continue;
      throw unsupported(executable, code === undefined ? probe.error.message : code);
    }
    if (probe.status !== 0) {
      throw unsupported(executable, `version probe exited ${String(probe.status)}`);
    }

    let document: unknown;
    try {
      document = JSON.parse(probe.stdout);
    } catch {
      throw unsupported(executable, "version probe returned invalid JSON");
    }
    if (
      typeof document !== "object"
      || document === null
      || typeof (document as { python?: unknown }).python !== "string"
      || typeof (document as { unicode?: unknown }).unicode !== "string"
    ) {
      throw unsupported(executable, "version probe returned an invalid result");
    }
    const python = (document as { python: string }).python;
    const unicode = (document as { unicode: string }).unicode;
    const supported = SUPPORTED.get(`${python}/${unicode}`);
    if (supported === undefined) {
      throw unsupported(executable, `found Python ${python} / UCD ${unicode}`);
    }
    return { executable, ...supported };
  }

  throw unsupported(
    "python3/python",
    "neither fallback executable was found",
  );
}

export const PYTHON_ORACLE = resolvePythonOracle().executable;
