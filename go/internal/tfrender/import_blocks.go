package tfrender

// import_blocks.go ports node-src/json/canonical-import-blocks.ts: a
// closed, canonical `import {}` block grammar parser, deliberately not a
// general HCL parser (it never evaluates expressions, traversals,
// interpolation, functions, or variables -- see ParseCanonicalImportBlocks's
// doc comment, carried over from the Node source's own).
//
// No node-tests/*.test.ts file imports this source (confirmed by grepping
// node-tests/ for "canonical-import-blocks", "CanonicalImportBlock", and
// "parseCanonicalImportBlocks": zero matches) -- the module appears to be
// unwired/not-yet-integrated in the Node tree as of this port. Absent
// ported vectors, import_blocks_test.go instead pins this file's behavior
// by probing the compiled TypeScript directly (per this task's
// instructions): `npx esbuild node-src/json/canonical-import-blocks.ts
// --bundle --platform=node --format=esm --outfile=.../canonical-import-blocks.mjs`,
// then a small Node driver script exercising empty/single/multi-unsorted/
// escaped/invalid-resource-type/malformed/wrong-embedded-type inputs and
// dumping JSON results -- see that file's doc comment for the full
// transcript. The most important probe finding, load-bearing for this
// port: unlike node-src/domain/import-moves.ts's renderGeneratedImports
// (which always sorts pairs by key before rendering, and so only accepts
// pre-sorted canonical text), this parser does NOT require sorted block
// order -- it accepts blocks in whatever order the input text presents
// them, as long as re-rendering that exact same order reproduces the input
// byte-for-byte. See this file's ParseCanonicalImportBlocks doc comment.

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxImportBytes  = 32 * 1024 * 1024
	maxImportBlocks = 50_000
)

// CanonicalImportBlock is the Go analogue of the CanonicalImportBlock
// interface in node-src/json/canonical-import-blocks.ts.
type CanonicalImportBlock struct {
	Key string
	ID  string
}

var canonicalImportResourceType = regexp.MustCompile(`^zcc_[a-z0-9_]+$`)

func canonicalImportSyntaxFailure() error {
	return &canonicalImportSyntaxError{}
}

// canonicalImportSyntaxError is the Go analogue of the
// `throw new SyntaxError(...)` every failure path in
// node-src/json/canonical-import-blocks.ts raises, always with the same
// fixed message (syntaxFailure() in the TS source takes no arguments and
// is called from every failure site, so no positional/reason detail is
// ever included -- deliberately, since this parser's whole point is a
// closed grammar with no partial-recovery diagnostics).
type canonicalImportSyntaxError struct{}

func (e *canonicalImportSyntaxError) Error() string {
	return "imports must use the canonical bootstrap import grammar"
}

// hclStringLiteral ports the local hclStringLiteral function in
// node-src/json/canonical-import-blocks.ts. This is a deliberate,
// self-contained duplicate of RenderHclQuotedString's escaping logic in
// import_moves.go -- the TS source itself does not import
// renderHclQuotedString from import-moves.ts here either, it has its own
// copy (see this file's canonicalImportRenderBlock, which mirrors the TS
// module's private renderBlock). Preserved as a duplicate rather than
// unified, to track the Node source's own structure exactly.
func hclStringLiteral(value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", canonicalImportSyntaxFailure()
	}
	var body strings.Builder
	for _, r := range value {
		switch r {
		case '\\':
			body.WriteString(`\\`)
		case '"':
			body.WriteString(`\"`)
		case '\n':
			body.WriteString(`\n`)
		case '\r':
			body.WriteString(`\r`)
		case '\t':
			body.WriteString(`\t`)
		default:
			body.WriteRune(r)
		}
	}
	escaped := body.String()
	escaped = strings.ReplaceAll(escaped, "${", "$${")
	escaped = strings.ReplaceAll(escaped, "%{", "%%{")
	return `"` + escaped + `"`, nil
}

func canonicalImportRenderBlock(resourceType string, block CanonicalImportBlock) (string, error) {
	key, err := hclStringLiteral(block.Key)
	if err != nil {
		return "", err
	}
	id, err := hclStringLiteral(block.ID)
	if err != nil {
		return "", err
	}
	return "import {\n" +
		"  to = module." + resourceType + "." + resourceType + ".this[" + key + "]\n" +
		"  id = " + id + "\n" +
		"}\n", nil
}

// canonicalImportParser ports the CanonicalImportParser class in
// node-src/json/canonical-import-blocks.ts.
type canonicalImportParser struct {
	text         string
	resourceType string
	index        int
}

func (p *canonicalImportParser) parse() ([]CanonicalImportBlock, error) {
	if len(p.text) > maxImportBytes {
		return nil, canonicalImportSyntaxFailure()
	}
	if len(p.text) == 0 {
		return []CanonicalImportBlock{}, nil
	}

	blocks := []CanonicalImportBlock{}
	for p.index < len(p.text) {
		if len(blocks) >= maxImportBlocks {
			return nil, canonicalImportSyntaxFailure()
		}
		if err := p.expect(fmt.Sprintf(
			"import {\n  to = module.%s.%s.this[", p.resourceType, p.resourceType,
		)); err != nil {
			return nil, err
		}
		key, err := p.stringLiteral()
		if err != nil {
			return nil, err
		}
		if err := p.expect("]\n  id = "); err != nil {
			return nil, err
		}
		id, err := p.stringLiteral()
		if err != nil {
			return nil, err
		}
		if err := p.expect("\n}\n"); err != nil {
			return nil, err
		}
		blocks = append(blocks, CanonicalImportBlock{Key: key, ID: id})
		if p.index < len(p.text) {
			if err := p.expect("\n"); err != nil {
				return nil, err
			}
		}
	}

	var canonical strings.Builder
	for i, block := range blocks {
		if i > 0 {
			canonical.WriteByte('\n')
		}
		rendered, err := canonicalImportRenderBlock(p.resourceType, block)
		if err != nil {
			return nil, err
		}
		canonical.WriteString(rendered)
	}
	if canonical.String() != p.text {
		return nil, canonicalImportSyntaxFailure()
	}
	return blocks, nil
}

// stringLiteral ports CanonicalImportParser.stringLiteral. Indexing is by
// Go string byte offset rather than UTF-16 code unit; see import_moves.go's
// package-level indexing note for why this produces identical results for
// this ASCII-delimited grammar.
func (p *canonicalImportParser) stringLiteral() (string, error) {
	if err := p.expect(`"`); err != nil {
		return "", err
	}
	var output strings.Builder
	for p.index < len(p.text) {
		character := p.text[p.index]
		if character == '"' {
			p.index++
			return output.String(), nil
		}
		if character == '\\' {
			if p.index+1 >= len(p.text) {
				return "", canonicalImportSyntaxFailure()
			}
			escaped := p.text[p.index+1]
			switch escaped {
			case '\\', '"':
				output.WriteByte(escaped)
			case 'n':
				output.WriteByte('\n')
			case 'r':
				output.WriteByte('\r')
			case 't':
				output.WriteByte('\t')
			default:
				return "", canonicalImportSyntaxFailure()
			}
			p.index += 2
			continue
		}
		if strings.HasPrefix(p.text[p.index:], "$${") {
			output.WriteString("${")
			p.index += 3
			continue
		}
		if strings.HasPrefix(p.text[p.index:], "%%{") {
			output.WriteString("%{")
			p.index += 3
			continue
		}
		nextIsInterpolation := character == '$' && p.index+1 < len(p.text) && p.text[p.index+1] == '{'
		nextIsDirective := character == '%' && p.index+1 < len(p.text) && p.text[p.index+1] == '{'
		if character == 0 || character == '\n' || character == '\r' || character == '\t' ||
			nextIsInterpolation || nextIsDirective {
			return "", canonicalImportSyntaxFailure()
		}
		r, size := decodeRuneAt(p.text, p.index)
		output.WriteRune(r)
		p.index += size
	}
	return "", canonicalImportSyntaxFailure()
}

func (p *canonicalImportParser) expect(expected string) error {
	if !strings.HasPrefix(p.text[p.index:], expected) {
		return canonicalImportSyntaxFailure()
	}
	p.index += len(expected)
	return nil
}

// ParseCanonicalImportBlocks parses only the compiler's closed, canonical
// import-block grammar. This is deliberately not a general HCL parser: it
// never evaluates expressions, traversals, interpolation, functions, or
// variables. Ports parseCanonicalImportBlocks from
// node-src/json/canonical-import-blocks.ts.
//
// Unlike RenderGeneratedImports/ParseGeneratedImports in import_moves.go
// (which always sort blocks by key), this parser accepts blocks in
// whatever order the input text presents them: it only requires that
// re-rendering the parsed blocks IN THAT SAME ORDER reproduces the input
// text byte-for-byte. See this file's package-level doc comment for the
// probe that confirmed this.
func ParseCanonicalImportBlocks(text, resourceType string) ([]CanonicalImportBlock, error) {
	if !canonicalImportResourceType.MatchString(resourceType) {
		return nil, canonicalImportSyntaxFailure()
	}
	parser := &canonicalImportParser{text: text, resourceType: resourceType}
	return parser.parse()
}
