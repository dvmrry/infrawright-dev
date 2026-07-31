package tfrender

import "unicode/utf8"

// decodeRuneAt decodes the UTF-8 rune starting at byte offset index in s,
// returning the decoded rune and its width in bytes. Used by
// ParseHclQuotedString (import_moves.go), which otherwise scans byte-by-byte
// looking for ASCII structural characters (see import_moves.go's indexing
// note) and falls back to this for any other, possibly multi-byte, content
// character.
func decodeRuneAt(s string, index int) (rune, int) {
	return utf8.DecodeRuneInString(s[index:])
}

// mapKeys returns m's keys as a plain, unsorted slice. Shared by every
// function in this package that needs sortedStrings(Object.keys(...))'s
// Go equivalent -- callers pass the result through canonjson.SortedStrings
// themselves; this helper only performs the Object.keys(...) half.
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
