const MAX_IMPORT_BYTES = 32 * 1024 * 1024;
const MAX_IMPORT_BLOCKS = 50_000;

export interface CanonicalImportBlock {
  readonly key: string;
  readonly id: string;
}

function syntaxFailure(): never {
  throw new SyntaxError("imports must use the canonical bootstrap import grammar");
}

function hclStringLiteral(value: string): string {
  if (value.includes("\0")) {
    return syntaxFailure();
  }
  const escaped = value
    .replaceAll("\\", "\\\\")
    .replaceAll('"', '\\"')
    .replaceAll("\n", "\\n")
    .replaceAll("\r", "\\r")
    .replaceAll("\t", "\\t")
    .replaceAll("${", () => "$${")
    .replaceAll("%{", "%%{");
  return `"${escaped}"`;
}

function renderBlock(resourceType: string, block: CanonicalImportBlock): string {
  return `import {\n`
    + `  to = module.${resourceType}.${resourceType}.this[${hclStringLiteral(block.key)}]\n`
    + `  id = ${hclStringLiteral(block.id)}\n`
    + `}\n`;
}

class CanonicalImportParser {
  private index = 0;

  constructor(
    private readonly text: string,
    private readonly resourceType: string,
  ) {}

  parse(): readonly CanonicalImportBlock[] {
    if (Buffer.byteLength(this.text, "utf8") > MAX_IMPORT_BYTES) {
      return syntaxFailure();
    }
    if (this.text.length === 0) {
      return [];
    }

    const blocks: CanonicalImportBlock[] = [];
    while (this.index < this.text.length) {
      if (blocks.length >= MAX_IMPORT_BLOCKS) {
        return syntaxFailure();
      }
      this.expect(
        `import {\n  to = module.${this.resourceType}.${this.resourceType}.this[`,
      );
      const key = this.stringLiteral();
      this.expect("]\n  id = ");
      const id = this.stringLiteral();
      this.expect("\n}\n");
      blocks.push({ key, id });
      if (this.index < this.text.length) {
        this.expect("\n");
      }
    }

    const canonical = blocks
      .map((block) => renderBlock(this.resourceType, block))
      .join("\n");
    if (canonical !== this.text) {
      return syntaxFailure();
    }
    return blocks;
  }

  private stringLiteral(): string {
    this.expect('"');
    let output = "";
    while (this.index < this.text.length) {
      const character = this.text[this.index] ?? "";
      if (character === '"') {
        this.index += 1;
        return output;
      }
      if (character === "\\") {
        const escaped = this.text[this.index + 1];
        if (escaped === "\\" || escaped === '"') {
          output += escaped;
        } else if (escaped === "n") {
          output += "\n";
        } else if (escaped === "r") {
          output += "\r";
        } else if (escaped === "t") {
          output += "\t";
        } else {
          return syntaxFailure();
        }
        this.index += 2;
        continue;
      }
      if (this.text.startsWith("$${", this.index)) {
        output += "${";
        this.index += 3;
        continue;
      }
      if (this.text.startsWith("%%{", this.index)) {
        output += "%{";
        this.index += 3;
        continue;
      }
      if (
        character === "\0"
        || character === "\n"
        || character === "\r"
        || character === "\t"
        || (character === "$" && this.text[this.index + 1] === "{")
        || (character === "%" && this.text[this.index + 1] === "{")
      ) {
        return syntaxFailure();
      }
      output += character;
      this.index += 1;
    }
    return syntaxFailure();
  }

  private expect(expected: string): void {
    if (!this.text.startsWith(expected, this.index)) {
      return syntaxFailure();
    }
    this.index += expected.length;
  }
}

/**
 * Parse only the compiler's closed, canonical import-block grammar. This is
 * deliberately not a general HCL parser: it never evaluates expressions,
 * traversals, interpolation, functions, or variables.
 */
export function parseCanonicalImportBlocks(
  text: string,
  resourceType: string,
): readonly CanonicalImportBlock[] {
  if (!/^zcc_[a-z0-9_]+$/.test(resourceType)) {
    return syntaxFailure();
  }
  return new CanonicalImportParser(text, resourceType).parse();
}
