import { ProcessFailure } from "./domain/errors.js";
import { runZiaUrlCategoryArtifactWorkflow } from "./io/zia-url-categories-artifacts.js";

interface Arguments {
  readonly tenant: string;
  readonly terraformExecutable: string;
  readonly workspace: string;
}

function usage(): never {
  throw new ProcessFailure({
    code: "INVALID_ZIA_ARTIFACT_ARGUMENTS",
    category: "request",
    message: "usage: infrawright-zia-url-categories --workspace <absolute-dir> --tenant <label> --terraform <absolute-executable>",
  });
}

function parseArguments(argv: readonly string[]): Arguments {
  const values = new Map<string, string>();
  for (let index = 0; index < argv.length; index += 2) {
    const name = argv[index];
    const value = argv[index + 1];
    if (
      value === undefined
      || (name !== "--workspace" && name !== "--tenant" && name !== "--terraform")
      || values.has(name)
    ) {
      return usage();
    }
    values.set(name, value);
  }
  const workspace = values.get("--workspace");
  const tenant = values.get("--tenant");
  const terraformExecutable = values.get("--terraform");
  if (workspace === undefined || tenant === undefined || terraformExecutable === undefined) {
    return usage();
  }
  return Object.freeze({ tenant, terraformExecutable, workspace });
}

async function main(): Promise<void> {
  try {
    const input = parseArguments(process.argv.slice(2));
    const result = await runZiaUrlCategoryArtifactWorkflow({
      environment: process.env,
      tenant: input.tenant,
      terraformExecutable: input.terraformExecutable,
      workspace: input.workspace,
    });
    for (const path of [
      result.paths.pull,
      result.paths.tfvars,
      result.paths.imports,
      result.paths.lookup,
    ]) {
      process.stderr.write(`wrote ${path}\n`);
    }
    process.stderr.write(`adopted ${result.itemCount} ZIA URL categor${
      result.itemCount === 1 ? "y" : "ies"
    }\n`);
  } catch (error: unknown) {
    const failure = error instanceof ProcessFailure
      ? error
      : new ProcessFailure({
          code: "ZIA_URL_CATEGORY_WORKFLOW_FAILED",
          category: "internal",
          message: "ZIA URL-category artifact workflow failed",
        });
    process.stderr.write(`error [${failure.code}]: ${failure.message}\n`);
    process.exitCode = failure.category === "request" || failure.category === "domain" ? 2 : 1;
  }
}

await main();
