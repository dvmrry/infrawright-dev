package tfrender

// import_moves.go is a LOCAL port of the subset of
// node-src/domain/import-moves.ts that node-src/domain/transform-artifacts.ts
// calls: RenderHclQuotedString, ParseHclQuotedString (used transitively by
// ParseGeneratedImports), RenderGeneratedImports, ParseGeneratedImports,
// DeriveImportMoves, and RenderMovedBlocks, plus their supporting types. See
// doc.go's "Local dependency ports" section for why this file exists here
// instead of in the future internal/adopt package that will own the whole
// of import-moves.ts.
//
// filterGeneratedImports, its FilteredGeneratedImports return type, and
// their private helpers (generatedImportBlockEnd, pythonWhitespaceOnly,
// trimPythonWhitespace) are NOT ported: transform-artifacts.ts never calls
// filterGeneratedImports, so porting it here would be unreviewable
// unreachable code against this slice's actual byte contract.
//
// Indexing: the Node source indexes this grammar by UTF-16 code unit (JS
// strings are UTF-16 sequences). This port instead indexes by Go string
// byte offset, which is safe and produces identical results here because
// every structural character this grammar's scanner tests for ("\"", "\\",
// the escape letters n/r/t, "$", "{", "%", and NUL) is pure ASCII: a
// multi-byte UTF-8 sequence (or, symmetrically, a UTF-16 surrogate pair)
// can never contain a byte/unit that collides with one of these ASCII
// values, so scanning by UTF-8 byte and scanning by UTF-16 unit visit
// exactly the same structural boundaries and copy exactly the same
// non-ASCII content through unchanged either way.
//
// Vectors: node-tests/import-moves-differential.test.ts exercises this
// source against a live Python oracle (PYTHON_ORACLE), which is out of
// scope for this Go-only wave (Python is mid-archival; see
// docs/go-runtime-plan.md). import_moves_test.go instead pins the exact
// same CASES table from that file by probing the compiled TypeScript
// directly: `npx esbuild node-src/domain/import-moves.ts --bundle
// --platform=node --format=esm --outfile=.../import-moves.mjs`, then a
// small Node driver script re-running CASES through
// renderGeneratedImports/parseGeneratedImports/deriveImportMoves/
// renderMovedBlocks and dumping JSON -- see that file's doc comment for the
// full probe transcript reference.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	maxGeneratedImportPairs = 50_000
	maxImportMoveCandidates = 50_000
)

var importResourceTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// GeneratedImportPair is the Go analogue of the GeneratedImportPair
// interface in node-src/domain/import-moves.ts.
type GeneratedImportPair struct {
	Key      string
	ImportID string
}

// ImportMove is the Go analogue of the ImportMove interface in
// node-src/domain/import-moves.ts.
type ImportMove struct {
	OldKey string
	NewKey string
}

// ImportMoveSuppressionReason is the Go analogue of the
// ImportMoveSuppressionReason string-literal union in
// node-src/domain/import-moves.ts.
type ImportMoveSuppressionReason string

// The four ImportMoveSuppressionReason literals from
// node-src/domain/import-moves.ts.
const (
	SuppressionAmbiguous           ImportMoveSuppressionReason = "ambiguous"
	SuppressionDuplicateFrom       ImportMoveSuppressionReason = "duplicate_from"
	SuppressionKeySwap             ImportMoveSuppressionReason = "key_swap"
	SuppressionDestinationOccupied ImportMoveSuppressionReason = "destination_occupied"
)

// ImportMoveSuppression is the Go analogue of the ImportMoveSuppression
// interface in node-src/domain/import-moves.ts.
type ImportMoveSuppression struct {
	OldKey   string
	NewKey   string
	ImportID string
	Reason   ImportMoveSuppressionReason
}

// ImportMoveDerivation is the Go analogue of the ImportMoveDerivation
// interface in node-src/domain/import-moves.ts.
type ImportMoveDerivation struct {
	Moves      []ImportMove
	Suppressed []ImportMoveSuppression
}

// ParsedHclQuotedString is the Go analogue of the ParsedHclQuotedString
// interface in node-src/domain/import-moves.ts. End is a byte offset (see
// this file's indexing note above).
type ParsedHclQuotedString struct {
	Value string
	End   int
}

func importMovesFail(code, message string) error {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  message,
	})
}

func requireImportResourceType(resourceType string) error {
	if !importResourceTypePattern.MatchString(resourceType) {
		return importMovesFail(
			"INVALID_IMPORT_RESOURCE_TYPE",
			"import resource type must be a canonical Terraform identifier",
		)
	}
	return nil
}

// RenderHclQuotedString matches engine.transform.hcl_string_literal for
// generated import addresses. Ports renderHclQuotedString from
// node-src/domain/import-moves.ts.
//
// The TS source's `!value.isWellFormed()` guard (rejecting lone UTF-16
// surrogates) has no Go analogue to check for: a Go string is always valid
// UTF-8 by construction, so there is no lone-surrogate state to reject.
//
// Order matters here exactly as it does in the Node source's chained
// `.replaceAll` calls: backslash/quote/control-char escaping must happen
// before the "${"/"%{" interpolation-guard pass, so that pass only ever
// sees "$"/"{"/"%" characters that were already literal in the input (none
// of the escape replacements above ever introduce a "$", "{", or "%"
// character, so the two passes cannot interact).
func RenderHclQuotedString(value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", importMovesFail(
			"INVALID_HCL_QUOTED_STRING",
			"generated HCL string values contain an unsupported character",
		)
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

// ParseHclQuotedString decodes the deliberately small HCL quoted-string
// grammar RenderHclQuotedString emits, starting at byte offset start (see
// this file's indexing note above). Ports parseHclQuotedString from
// node-src/domain/import-moves.ts.
func ParseHclQuotedString(text string, start int) (ParsedHclQuotedString, error) {
	if start < 0 || start >= len(text) || text[start] != '"' {
		return ParsedHclQuotedString{}, importMovesFail(
			"INVALID_HCL_QUOTED_STRING",
			"expected a generated HCL quoted string literal",
		)
	}
	var output strings.Builder
	index := start + 1
	for index < len(text) {
		switch {
		case text[index] == '"':
			return ParsedHclQuotedString{Value: output.String(), End: index + 1}, nil
		case text[index] == '\\':
			index++
			if index >= len(text) {
				return ParsedHclQuotedString{}, importMovesFail(
					"INVALID_HCL_QUOTED_STRING",
					"generated HCL string has an unterminated escape sequence",
				)
			}
			switch text[index] {
			case 'n':
				output.WriteByte('\n')
			case 'r':
				output.WriteByte('\r')
			case 't':
				output.WriteByte('\t')
			case '"', '\\':
				output.WriteByte(text[index])
			default:
				return ParsedHclQuotedString{}, importMovesFail(
					"INVALID_HCL_QUOTED_STRING",
					"generated HCL string contains an unsupported escape sequence",
				)
			}
			index++
		case strings.HasPrefix(text[index:], "$${"):
			output.WriteString("${")
			index += 3
		case strings.HasPrefix(text[index:], "%%{"):
			output.WriteString("%{")
			index += 3
		case text[index] == 0:
			return ParsedHclQuotedString{}, importMovesFail(
				"INVALID_HCL_QUOTED_STRING",
				"generated HCL string values cannot contain NUL bytes",
			)
		default:
			r, size := decodeRuneAt(text, index)
			output.WriteRune(r)
			index += size
		}
	}
	return ParsedHclQuotedString{}, importMovesFail(
		"INVALID_HCL_QUOTED_STRING",
		"generated HCL string literal is unterminated",
	)
}

// RenderGeneratedImports renders the byte-canonical import blocks emitted
// by engine.transform. Ports renderGeneratedImports from
// node-src/domain/import-moves.ts.
func RenderGeneratedImports(resourceType string, pairs []GeneratedImportPair) (string, error) {
	if err := requireImportResourceType(resourceType); err != nil {
		return "", err
	}
	if len(pairs) > maxGeneratedImportPairs {
		return "", importMovesFail(
			"IMPORT_MOVE_LIMIT_EXCEEDED",
			"generated imports exceed the bounded pair contract",
		)
	}
	seen := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		if seen[pair.Key] {
			return "", importMovesFail(
				"INVALID_GENERATED_IMPORTS",
				"generated imports contain a duplicate Terraform address",
			)
		}
		seen[pair.Key] = true
	}
	sorted := append([]GeneratedImportPair(nil), pairs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return canonjson.ComparePythonStrings(sorted[i].Key, sorted[j].Key) < 0
	})
	blocks := make([]string, len(sorted))
	for i, pair := range sorted {
		key, err := RenderHclQuotedString(pair.Key)
		if err != nil {
			return "", err
		}
		id, err := RenderHclQuotedString(pair.ImportID)
		if err != nil {
			return "", err
		}
		blocks[i] = "import {\n" +
			"  to = module." + resourceType + "." + resourceType + ".this[" + key + "]\n" +
			"  id = " + id + "\n" +
			"}\n"
	}
	return strings.Join(blocks, "\n"), nil
}

func expectLiteral(text string, start int, literal string) (int, error) {
	if start > len(text) || !strings.HasPrefix(text[start:], literal) {
		return 0, importMovesFail(
			"INVALID_GENERATED_IMPORTS",
			"imports artifact is not a complete canonical generated import file",
		)
	}
	return start + len(literal), nil
}

// ParseGeneratedImports parses only complete, byte-canonical
// Infrawright-generated import files. Ports parseGeneratedImports from
// node-src/domain/import-moves.ts.
//
// This intentionally rejects HCL that is semantically equivalent but was
// not generated by Infrawright: comments, partial blocks, repeated
// addresses, and alternate formatting must not be interpreted
// heuristically, because the prior artifact becomes state-move evidence.
func ParseGeneratedImports(resourceType, text string) ([]GeneratedImportPair, error) {
	if err := requireImportResourceType(resourceType); err != nil {
		return nil, err
	}
	if len(text) == 0 {
		return []GeneratedImportPair{}, nil
	}

	pairs := []GeneratedImportPair{}
	seen := make(map[string]bool)
	cursor := 0
	prefix := fmt.Sprintf("import {\n  to = module.%s.%s.this[", resourceType, resourceType)
	var err error
	for cursor < len(text) {
		cursor, err = expectLiteral(text, cursor, prefix)
		if err != nil {
			return nil, err
		}
		parsedKey, err := ParseHclQuotedString(text, cursor)
		if err != nil {
			return nil, err
		}
		cursor, err = expectLiteral(text, parsedKey.End, "]\n  id = ")
		if err != nil {
			return nil, err
		}
		parsedID, err := ParseHclQuotedString(text, cursor)
		if err != nil {
			return nil, err
		}
		cursor, err = expectLiteral(text, parsedID.End, "\n}\n")
		if err != nil {
			return nil, err
		}

		if seen[parsedKey.Value] {
			return nil, importMovesFail(
				"INVALID_GENERATED_IMPORTS",
				"generated imports contain a duplicate Terraform address",
			)
		}
		if len(pairs) >= maxGeneratedImportPairs {
			return nil, importMovesFail(
				"IMPORT_MOVE_LIMIT_EXCEEDED",
				"generated imports exceed the bounded pair contract",
			)
		}
		seen[parsedKey.Value] = true
		pairs = append(pairs, GeneratedImportPair{Key: parsedKey.Value, ImportID: parsedID.Value})

		if cursor < len(text) {
			cursor, err = expectLiteral(text, cursor, "\n")
			if err != nil {
				return nil, err
			}
		}
	}

	rendered, err := RenderGeneratedImports(resourceType, pairs)
	if err != nil {
		return nil, err
	}
	if rendered != text {
		return nil, importMovesFail(
			"INVALID_GENERATED_IMPORTS",
			"imports artifact is not byte-canonical generated output",
		)
	}
	return pairs, nil
}

func pairsByKey(pairs []GeneratedImportPair) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		out[pair.Key] = pair.ImportID
	}
	return out
}

func keysByImportID(pairs []GeneratedImportPair) map[string][]string {
	grouped := make(map[string][]string)
	for _, pair := range pairs {
		grouped[pair.ImportID] = append(grouped[pair.ImportID], pair.Key)
	}
	for id := range grouped {
		keys := grouped[id]
		sort.SliceStable(keys, func(i, j int) bool {
			return canonjson.ComparePythonStrings(keys[i], keys[j]) < 0
		})
		grouped[id] = keys
	}
	return grouped
}

func isKeySwap(oldKey, newKey string, oldPairs, newPairs map[string]string) bool {
	oldOldID, oldOldOK := oldPairs[oldKey]
	oldNewID, oldNewOK := oldPairs[newKey]
	newOldID, newOldOK := newPairs[oldKey]
	newNewID, newNewOK := newPairs[newKey]
	return oldOldOK && oldNewOK && newOldOK && newNewOK &&
		oldOldID == newNewID && oldNewID == newOldID
}

type importMoveCandidate struct {
	oldKey   string
	newKey   string
	importID string
}

// DeriveImportMoves derives only unambiguous, unoccupied state-address
// moves. Ports deriveImportMoves from node-src/domain/import-moves.ts.
func DeriveImportMoves(resourceType, oldImportsText, newImportsText string) (ImportMoveDerivation, error) {
	oldEntries, err := ParseGeneratedImports(resourceType, oldImportsText)
	if err != nil {
		return ImportMoveDerivation{}, err
	}
	newEntries, err := ParseGeneratedImports(resourceType, newImportsText)
	if err != nil {
		return ImportMoveDerivation{}, err
	}
	oldPairs := pairsByKey(oldEntries)
	newPairs := pairsByKey(newEntries)
	oldByID := keysByImportID(oldEntries)
	newByID := keysByImportID(newEntries)

	importIDs := make([]string, 0, len(newByID))
	for id := range newByID {
		importIDs = append(importIDs, id)
	}
	sort.SliceStable(importIDs, func(i, j int) bool {
		return canonjson.ComparePythonStrings(importIDs[i], importIDs[j]) < 0
	})

	var candidates []importMoveCandidate
	for _, importID := range importIDs {
		for _, oldKey := range oldByID[importID] {
			for _, newKey := range newByID[importID] {
				if oldKey != newKey {
					if len(candidates) >= maxImportMoveCandidates {
						return ImportMoveDerivation{}, importMovesFail(
							"IMPORT_MOVE_LIMIT_EXCEEDED",
							"import rename candidates exceed the bounded derivation contract",
						)
					}
					candidates = append(candidates, importMoveCandidate{oldKey, newKey, importID})
				}
			}
		}
	}

	fromCounts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		fromCounts[candidate.oldKey]++
	}

	moves := []ImportMove{}
	suppressed := []ImportMoveSuppression{}
	for _, candidate := range candidates {
		var reason ImportMoveSuppressionReason
		switch {
		case len(oldByID[candidate.importID]) > 1:
			reason = SuppressionAmbiguous
		case fromCounts[candidate.oldKey] > 1:
			reason = SuppressionDuplicateFrom
		case isKeySwap(candidate.oldKey, candidate.newKey, oldPairs, newPairs):
			reason = SuppressionKeySwap
		default:
			_, oldHasNewKey := oldPairs[candidate.newKey]
			_, newHasOldKey := newPairs[candidate.oldKey]
			if oldHasNewKey || newHasOldKey {
				reason = SuppressionDestinationOccupied
			}
		}

		if reason == "" {
			moves = append(moves, ImportMove{OldKey: candidate.oldKey, NewKey: candidate.newKey})
		} else {
			suppressed = append(suppressed, ImportMoveSuppression{
				OldKey:   candidate.oldKey,
				NewKey:   candidate.newKey,
				ImportID: candidate.importID,
				Reason:   reason,
			})
		}
	}

	sort.SliceStable(moves, func(i, j int) bool { return compareMoves(moves[i], moves[j]) < 0 })
	sort.SliceStable(suppressed, func(i, j int) bool { return compareSuppressions(suppressed[i], suppressed[j]) < 0 })
	return ImportMoveDerivation{Moves: moves, Suppressed: suppressed}, nil
}

func compareMoves(left, right ImportMove) int {
	if c := canonjson.ComparePythonStrings(left.OldKey, right.OldKey); c != 0 {
		return c
	}
	return canonjson.ComparePythonStrings(left.NewKey, right.NewKey)
}

func compareSuppressions(left, right ImportMoveSuppression) int {
	if c := compareMoves(ImportMove{OldKey: left.OldKey, NewKey: left.NewKey}, ImportMove{OldKey: right.OldKey, NewKey: right.NewKey}); c != 0 {
		return c
	}
	if c := canonjson.ComparePythonStrings(left.ImportID, right.ImportID); c != 0 {
		return c
	}
	return canonjson.ComparePythonStrings(string(left.Reason), string(right.Reason))
}

// RenderMovedBlocks matches engine.transform.render_moves byte-for-byte for
// derived moves. Ports renderMovedBlocks from
// node-src/domain/import-moves.ts.
func RenderMovedBlocks(resourceType string, moves []ImportMove) (string, error) {
	if err := requireImportResourceType(resourceType); err != nil {
		return "", err
	}
	if len(moves) > maxImportMoveCandidates {
		return "", importMovesFail(
			"IMPORT_MOVE_LIMIT_EXCEEDED",
			"import moves exceed the bounded render contract",
		)
	}
	blocks := make([]string, len(moves))
	for i, move := range moves {
		from, err := RenderHclQuotedString(move.OldKey)
		if err != nil {
			return "", err
		}
		to, err := RenderHclQuotedString(move.NewKey)
		if err != nil {
			return "", err
		}
		blocks[i] = "moved {\n" +
			"  from = module." + resourceType + "." + resourceType + ".this[" + from + "]\n" +
			"  to   = module." + resourceType + "." + resourceType + ".this[" + to + "]\n" +
			"}\n"
	}
	return strings.Join(blocks, "\n"), nil
}
