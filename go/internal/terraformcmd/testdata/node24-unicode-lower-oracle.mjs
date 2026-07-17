import { createHash } from "node:crypto";

if (process.versions.node !== "24.15.0" || process.versions.unicode !== "16.0") {
  throw new Error(
    `expected Node 24.15.0 / Unicode 16.0, got ${process.versions.node} / ${process.versions.unicode}`,
  );
}

const hash = createHash("sha256");
const length = Buffer.allocUnsafe(4);
for (let codePoint = 0; codePoint <= 0x10ffff; codePoint += 1) {
  if (codePoint >= 0xd800 && codePoint <= 0xdfff) continue;
  const character = String.fromCodePoint(codePoint);
  const lowered = Buffer.from(character.toLowerCase(), "utf8");
  length.writeUInt32BE(lowered.length);
  hash.update(length);
  hash.update(lowered);

  // Derive the three-way class that controls Final_Sigma. Case_Ignorable
  // takes precedence over Cased when a character has both properties.
  const directlyFinal = `${character}Σ`.toLowerCase().endsWith("ς");
  const anchoredFinal = `A${character}Σ`.toLowerCase().endsWith("ς");
  const finalSigmaClass = directlyFinal ? 1 : anchoredFinal ? 0 : 2;
  hash.update(Buffer.from([finalSigmaClass]));
}

console.log(JSON.stringify({
  node: process.versions.node,
  v8: process.versions.v8,
  unicode: process.versions.unicode,
  corpus: "all Unicode scalar lowercase mappings plus Node-derived Final_Sigma class",
  sha256: hash.digest("hex"),
}));
