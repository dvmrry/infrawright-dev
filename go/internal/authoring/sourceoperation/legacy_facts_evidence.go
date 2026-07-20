package sourceoperation

import (
	"path/filepath"
	"strings"
)

func legacyFactsEvidenceFull(root string, files []string, facts map[string]any) (map[string]bool, []map[string]any, []map[string]any, []map[string]any, bool) {
	selected := legacySelected(files)
	identifiers := map[string]bool{}
	for _, group := range []string{"functions", "identifier_references"} {
		for _, item := range legacyArray(facts[group]) {
			record := legacyObject(item)
			if selected[filepath.ToSlash(legacyFactPath(root, legacyString(record["file"])))] {
				if token := legacyCanonical(legacyString(record["name"])); token != "" {
					identifiers[token] = true
				}
			}
		}
	}
	for _, item := range legacyArray(facts["selector_calls"]) {
		record := legacyObject(item)
		if !selected[filepath.ToSlash(legacyFactPath(root, legacyString(record["file"])))] {
			continue
		}
		if token := legacyCanonical(legacyString(record["symbol"])); token != "" {
			identifiers[token] = true
		}
		for _, part := range legacyArray(record["parts"]) {
			if token := legacyCanonical(legacyString(part)); token != "" {
				identifiers[token] = true
			}
		}
	}
	packages := map[string]map[string]any{}
	for _, item := range legacyArray(facts["package_calls"]) {
		record := legacyObject(item)
		if !selected[filepath.ToSlash(legacyFactPath(root, legacyString(record["file"])))] {
			continue
		}
		pkg, importPath, method := legacyString(record["package"]), legacyString(record["import_path"]), legacyString(record["method"])
		for _, value := range []string{method, legacyString(record["symbol"])} {
			if token := legacyCanonical(value); token != "" {
				identifiers[token] = true
			}
		}
		if pkg == "" || importPath == "" || method == "" || !legacyExternalImport(importPath) || legacyLocalImportDirectory(root, importPath) != "" {
			continue
		}
		if role := legacyPackageRole(method); role != "" {
			symbol := legacyString(record["symbol"])
			if symbol == "" {
				symbol = pkg + "." + method
			}
			packages[symbol] = map[string]any{"client_symbol": symbol, "method": method, "package": pkg, "package_path": importPath, "source_role": role}
		}
	}
	raw := map[string]map[string]any{}
	for _, item := range legacyArray(facts["raw_rest_calls"]) {
		record := legacyObject(item)
		if !selected[filepath.ToSlash(legacyFactPath(root, legacyString(record["file"])))] {
			continue
		}
		method, path := strings.ToUpper(legacyString(record["method"])), legacyString(record["path"])
		if method == "" || path == "" {
			continue
		}
		rest := NormalizeRawRESTPath(path)
		symbol := legacyString(record["symbol"])
		if symbol == "" {
			symbol = "NewRequest"
		}
		raw[symbol+"\x00"+method+"\x00"+rest] = map[string]any{"client_symbol": symbol + " " + method + " " + rest, "method": method, "path": rest, "source_role": "read"}
	}
	graphql := false
	for _, item := range legacyArray(facts["files"]) {
		record := legacyObject(item)
		if !selected[filepath.ToSlash(legacyFactPath(root, legacyString(record["path"])))] {
			continue
		}
		for _, imported := range legacyArray(record["imports"]) {
			if strings.Contains(legacyString(legacyObject(imported)["path"]), "githubv4") {
				graphql = true
			}
		}
	}
	return identifiers, legacySDKCallsFromFacts(root, files, facts, true), legacyCallMap(packages), legacyCallMap(raw), graphql
}
