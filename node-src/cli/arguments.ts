import {
  parseArgs,
  type ParseArgsConfig,
  type ParseArgsOptionsConfig,
} from "node:util";

export class CliArgumentParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CliArgumentParseError";
  }
}

export interface CliValueOption {
  readonly allowEmpty?: boolean;
  readonly multiple?: boolean;
}

export interface ParseCommandArgumentsOptions {
  readonly allowPositionals?: boolean;
  readonly flags?: readonly string[];
  readonly help?: boolean;
  readonly values?: Readonly<Record<string, CliValueOption>>;
}

export interface ParsedCommandArguments {
  readonly flags: ReadonlySet<string>;
  readonly occurrences: readonly ParsedCommandArgumentOccurrence[];
  readonly options: Readonly<Record<string, readonly string[]>>;
  readonly positionals: readonly string[];
}

export type ParsedCommandArgumentOccurrence =
  | { readonly kind: "option"; readonly name: string; readonly value?: string }
  | { readonly kind: "positional"; readonly value: string };

function optionKey(option: string): string {
  if (!/^--[a-z][a-z0-9-]*$/u.test(option)) {
    throw new TypeError(`invalid CLI option declaration ${JSON.stringify(option)}`);
  }
  return option.slice(2);
}

function parseFailure(error: unknown): CliArgumentParseError {
  if (!(error instanceof Error)) {
    return new CliArgumentParseError("invalid command arguments");
  }
  const unknown = /Unknown option ['"](?<option>-{1,2}[^'"]+)['"]/u.exec(error.message);
  if (unknown?.groups?.option !== undefined) {
    return new CliArgumentParseError(`unknown argument ${unknown.groups.option}`);
  }
  const missing = /Option ['"](?<option>--[a-z][a-z0-9-]*)(?: <value>)?['"] argument missing/u.exec(
    error.message,
  );
  if (missing?.groups?.option !== undefined) {
    return new CliArgumentParseError(`${missing.groups.option} requires a value`);
  }
  const positional = /Unexpected argument ['"](?<argument>[^'"]+)['"]\. This command does not take positional arguments/u.exec(
    error.message,
  );
  if (positional?.groups?.argument !== undefined) {
    return new CliArgumentParseError(`unknown argument ${positional.groups.argument}`);
  }
  return new CliArgumentParseError(error.message);
}

function argumentsThroughHelp(
  arguments_: readonly string[],
  valueOptions: ReadonlySet<string>,
): readonly string[] {
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (argument !== undefined && valueOptions.has(argument)) {
      index += 1;
      continue;
    }
    if (argument === "--help" || argument === "-h") {
      return arguments_.slice(0, index + 1);
    }
    if (argument === "--") {
      throw new CliArgumentParseError("unknown argument --");
    }
  }
  return arguments_;
}

function bindStringOptionValues(
  arguments_: readonly string[],
  valueOptions: ReadonlySet<string>,
): string[] {
  const bound: string[] = [];
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (argument === undefined) continue;
    if (valueOptions.has(argument) && arguments_[index + 1] !== undefined) {
      bound.push(`${argument}=${arguments_[index + 1]}`);
      index += 1;
    } else {
      bound.push(argument);
    }
  }
  return bound;
}

/**
 * Parse one command's arguments with Node's strict `util.parseArgs` parser.
 *
 * String options are configured as repeatable internally so this adapter can
 * reject accidental duplicates instead of accepting parseArgs' last value.
 * The returned option keys retain their leading `--` to keep command code and
 * diagnostics aligned with the public CLI spelling.
 */
export function parseCommandArguments(
  arguments_: readonly string[],
  configuration: ParseCommandArgumentsOptions = {},
): ParsedCommandArguments {
  const declarations: Record<string, ParseArgsOptionsConfig[string]> = Object.create(null) as Record<
    string,
    ParseArgsOptionsConfig[string]
  >;
  const valueOptions = configuration.values ?? {};
  for (const option of Object.keys(valueOptions)) {
    declarations[optionKey(option)] = { multiple: true, type: "string" };
  }
  for (const flag of configuration.flags ?? []) {
    declarations[optionKey(flag)] = { type: "boolean" };
  }
  if ((configuration.help ?? true) && declarations.help === undefined) {
    declarations.help = { short: "h", type: "boolean" };
  }

  let parsed: ReturnType<typeof parseArgs>;
  try {
    const valueOptionNames = new Set(Object.keys(valueOptions));
    parsed = parseArgs({
      allowPositionals: configuration.allowPositionals ?? false,
      // Existing Infrawright commands bind the next token verbatim, including
      // values beginning with `-`. parseArgs treats those as ambiguous unless
      // they use `--option=value`, so normalize only declared string options.
      args: bindStringOptionValues(
        argumentsThroughHelp(arguments_, valueOptionNames),
        valueOptionNames,
      ),
      options: declarations as ParseArgsConfig["options"],
      strict: true,
      tokens: true,
    });
  } catch (error: unknown) {
    throw parseFailure(error);
  }

  const options: Record<string, readonly string[]> = Object.create(null) as Record<
    string,
    readonly string[]
  >;
  for (const [option, declaration] of Object.entries(valueOptions)) {
    const parsedValue = parsed.values[optionKey(option)];
    if (parsedValue === undefined) continue;
    const values = Array.isArray(parsedValue) ? parsedValue : [parsedValue];
    if (values.some((value) => typeof value !== "string")) {
      throw new TypeError(`${option} did not parse as a string option`);
    }
    const strings = values as string[];
    if (declaration.multiple === false && strings.length > 1) {
      throw new CliArgumentParseError(`${option} may be specified only once`);
    }
    if (!declaration.allowEmpty && strings.some((value) => value.length === 0)) {
      throw new CliArgumentParseError(`${option} requires a value`);
    }
    options[option] = strings;
  }

  const flags = new Set<string>();
  for (const flag of configuration.flags ?? []) {
    if (parsed.values[optionKey(flag)] === true) flags.add(flag);
  }
  if ((configuration.help ?? true) && parsed.values.help === true) flags.add("--help");
  const occurrences: ParsedCommandArgumentOccurrence[] = [];
  const tokens = parsed.tokens;
  if (tokens === undefined) throw new TypeError("parseArgs did not return tokens");
  for (const token of tokens) {
    if (token.kind === "option-terminator") continue;
    if (token.kind === "positional") {
      occurrences.push({ kind: "positional", value: token.value });
    } else {
      occurrences.push({
        kind: "option",
        name: `--${token.name}`,
        ...(token.value === undefined ? {} : { value: token.value }),
      });
    }
  }
  return { flags, occurrences, options, positionals: parsed.positionals };
}

export function lastOption(
  parsed: ParsedCommandArguments,
  name: string,
): string | undefined {
  return parsed.options[name]?.at(-1);
}
