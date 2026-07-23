package adopt

import (
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// FilteredGeneratedImports ports the FilteredGeneratedImports interface from
// node-src/domain/import-moves.ts.
type FilteredGeneratedImports struct {
	Text    string
	Kept    int
	Skipped int
}

func importFilterFailure(message string) error {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "INVALID_GENERATED_IMPORT_BLOCK",
		Category: procerr.CategoryDomain,
		Message:  message,
	})
}

func pythonWhitespaceRune(r rune) bool {
	return r >= '\x09' && r <= '\x0d' ||
		r >= '\x1c' && r <= '\x20' ||
		r == '\x85' || r == '\xa0' || r == '\u1680' ||
		r >= '\u2000' && r <= '\u200a' ||
		r >= '\u2028' && r <= '\u2029' ||
		r == '\u202f' || r == '\u205f' || r == '\u3000'
}

func skipPythonWhitespace(text string, start int) int {
	index := start
	for index < len(text) {
		r, size := utf8.DecodeRuneInString(text[index:])
		if !pythonWhitespaceRune(r) {
			break
		}
		index += size
	}
	return index
}

func trimPythonWhitespace(text string) string {
	start := skipPythonWhitespace(text, 0)
	end := len(text)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(text[start:end])
		if !pythonWhitespaceRune(r) {
			break
		}
		end -= size
	}
	return text[start:end]
}

func pythonWhitespaceOnly(text string) bool {
	return skipPythonWhitespace(text, 0) == len(text)
}

func nextGeneratedImportStart(text string, start int) (matchStart, openBrace int, found bool) {
	for lineStart := start; lineStart <= len(text); {
		if lineStart == 0 || text[lineStart-1] == '\n' {
			index := lineStart
			for index < len(text) && (text[index] == ' ' || text[index] == '\t') {
				index++
			}
			if strings.HasPrefix(text[index:], "import") {
				brace := skipPythonWhitespace(text, index+len("import"))
				if brace < len(text) && text[brace] == '{' {
					return lineStart, brace, true
				}
			}
		}
		next := strings.IndexByte(text[lineStart:], '\n')
		if next < 0 {
			break
		}
		lineStart += next + 1
		if lineStart < start {
			lineStart = start
		}
	}
	return 0, 0, false
}

func generatedImportBlockEnd(text string, openBrace int) (int, error) {
	depth := 0
	index := openBrace
	for index < len(text) {
		switch text[index] {
		case '"':
			parsed, err := tfrender.ParseHclQuotedString(text, index)
			if err != nil {
				return 0, importFilterFailure("malformed generated import block: " + err.Error())
			}
			index = parsed.End
		case '{':
			depth++
			index++
		case '}':
			depth--
			index++
			if depth == 0 {
				if strings.HasPrefix(text[index:], "\r\n") {
					return index + 2, nil
				}
				if index < len(text) && (text[index] == '\n' || text[index] == '\r') {
					return index + 1, nil
				}
				return index, nil
			}
		default:
			index++
		}
	}
	return 0, importFilterFailure("unterminated generated import block")
}

func generatedImportAddress(block string) (string, bool) {
	for lineStart := 0; lineStart <= len(block); {
		index := lineStart
		for index < len(block) && (block[index] == ' ' || block[index] == '\t') {
			index++
		}
		if strings.HasPrefix(block[index:], "to") {
			index = skipPythonWhitespace(block, index+len("to"))
			if index < len(block) && block[index] == '=' {
				index = skipPythonWhitespace(block, index+1)
				end := strings.IndexByte(block[index:], '\n')
				if end < 0 {
					end = len(block)
				} else {
					end += index
				}
				return trimPythonWhitespace(block[index:end]), true
			}
		}
		next := strings.IndexByte(block[lineStart:], '\n')
		if next < 0 {
			break
		}
		lineStart += next + 1
	}
	return "", false
}

// FilterGeneratedImports filters top-level generated import blocks down to
// exact unmanaged addresses. It ports filterGeneratedImports from
// node-src/domain/import-moves.ts.
func FilterGeneratedImports(importsText string, stateAddresses []string) (FilteredGeneratedImports, error) {
	managed := make(map[string]struct{}, len(stateAddresses))
	for _, address := range stateAddresses {
		managed[address] = struct{}{}
	}

	var output strings.Builder
	position := 0
	kept := 0
	skipped := 0
	for {
		matchStart, openBrace, found := nextGeneratedImportStart(importsText, position)
		if !found {
			break
		}
		end, err := generatedImportBlockEnd(importsText, openBrace)
		if err != nil {
			return FilteredGeneratedImports{}, err
		}
		output.WriteString(importsText[position:matchStart])
		block := importsText[matchStart:end]
		address, hasAddress := generatedImportAddress(block)
		_, alreadyManaged := managed[address]
		if hasAddress && alreadyManaged {
			skipped++
		} else {
			output.WriteString(block)
			kept++
		}
		position = end
	}
	output.WriteString(importsText[position:])
	text := output.String()
	if kept == 0 && pythonWhitespaceOnly(text) {
		text = ""
	}
	return FilteredGeneratedImports{Text: text, Kept: kept, Skipped: skipped}, nil
}
