package main

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Report struct {
	SourceRoot            string                 `json:"source_root"`
	GoMod                 *GoMod                 `json:"go_mod,omitempty"`
	Files                 []FileFact             `json:"files"`
	Functions             []FunctionFact         `json:"functions"`
	ResourceRegistrations []ResourceRegistration `json:"resource_registrations"`
	ResourceReferences    []ResourceReference    `json:"resource_references"`
	IdentifierReferences  []IdentifierReference  `json:"identifier_references"`
	ReadCallbacks         []ReadCallback         `json:"read_callbacks"`
	SelectorCalls         []SelectorCall         `json:"selector_calls"`
	PackageCalls          []PackageCall          `json:"package_calls"`
	RawRESTCalls          []RawRESTCall          `json:"raw_rest_calls"`
}

type GoMod struct {
	Module   string      `json:"module,omitempty"`
	Requires []GoRequire `json:"requires,omitempty"`
}

type GoRequire struct {
	Path     string `json:"path"`
	Version  string `json:"version"`
	Indirect bool   `json:"indirect,omitempty"`
}

type FileFact struct {
	Path    string       `json:"path"`
	Package string       `json:"package"`
	Imports []ImportFact `json:"imports,omitempty"`
}

type ImportFact struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FunctionFact struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Receiver string `json:"receiver,omitempty"`
}

type ResourceRegistration struct {
	Resource    string `json:"resource"`
	File        string `json:"file"`
	Constructor string `json:"constructor"`
	Package     string `json:"package,omitempty"`
}

type ResourceReference struct {
	Resource string `json:"resource"`
	File     string `json:"file"`
}

type IdentifierReference struct {
	Name string `json:"name"`
	File string `json:"file"`
}

type ReadCallback struct {
	Field    string `json:"field"`
	File     string `json:"file"`
	Function string `json:"function"`
	Package  string `json:"package,omitempty"`
}

type SelectorCall struct {
	File     string   `json:"file"`
	Function string   `json:"function,omitempty"`
	Symbol   string   `json:"symbol"`
	Parts    []string `json:"parts"`
}

type PackageCall struct {
	File       string `json:"file"`
	Function   string `json:"function,omitempty"`
	Symbol     string `json:"symbol"`
	Package    string `json:"package"`
	ImportPath string `json:"import_path"`
	Method     string `json:"method"`
}

type RawRESTCall struct {
	File     string `json:"file"`
	Function string `json:"function,omitempty"`
	Symbol   string `json:"symbol"`
	Method   string `json:"method"`
	Path     string `json:"path,omitempty"`
}

var resourceNamePattern = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9_]+$`)

func Collect(sourceRoot string) (*Report, error) {
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, err
	}

	report := &Report{
		SourceRoot:            root,
		Files:                 []FileFact{},
		Functions:             []FunctionFact{},
		ResourceRegistrations: []ResourceRegistration{},
		ResourceReferences:    []ResourceReference{},
		IdentifierReferences:  []IdentifierReference{},
		ReadCallbacks:         []ReadCallback{},
		SelectorCalls:         []SelectorCall{},
		PackageCalls:          []PackageCall{},
		RawRESTCalls:          []RawRESTCall{},
	}
	if gomod, err := parseGoMod(filepath.Join(root, "go.mod")); err == nil && gomod != nil {
		report.GoMod = gomod
	}

	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if shouldSkipDir(entry.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSourceFile(entry.Name()) {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		return collectFile(root, path, parsed, report)
	})
	if err != nil {
		return nil, err
	}

	sortReport(report)
	return report, nil
}

func collectFile(root, path string, file *ast.File, report *Report) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	imports := collectImports(file)
	importAliases := make(map[string]string, len(imports))
	for _, item := range imports {
		importAliases[item.Name] = item.Path
	}

	report.Files = append(report.Files, FileFact{
		Path:    rel,
		Package: file.Name.Name,
		Imports: imports,
	})

	resourceReferences := map[string]struct{}{}
	identifierReferences := map[string]struct{}{}
	ast.Inspect(file, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name != "_" {
			identifierReferences[ident.Name] = struct{}{}
		}
		if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if resource, ok := stringLiteral(lit); ok && resourceNamePattern.MatchString(resource) {
				resourceReferences[resource] = struct{}{}
			}
		}
		kv, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if cb, ok := readCallback(rel, kv); ok {
			report.ReadCallbacks = append(report.ReadCallbacks, cb)
		}
		resource, ok := stringLiteral(kv.Key)
		if !ok || !resourceNamePattern.MatchString(resource) {
			return true
		}
		if reg, ok := resourceRegistration(rel, resource, kv.Value); ok {
			report.ResourceRegistrations = append(report.ResourceRegistrations, reg)
		}
		return true
	})
	for resource := range resourceReferences {
		report.ResourceReferences = append(report.ResourceReferences, ResourceReference{
			Resource: resource,
			File:     rel,
		})
	}
	for name := range identifierReferences {
		report.IdentifierReferences = append(report.IdentifierReferences, IdentifierReference{
			Name: name,
			File: rel,
		})
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		functionName := fn.Name.Name
		receiverName := receiverName(fn)
		report.Functions = append(report.Functions, FunctionFact{
			Name:     functionName,
			File:     rel,
			Receiver: receiverName,
		})
		if fn.Body == nil {
			continue
		}
		collectCalls(rel, functionName, fn.Body, importAliases, report)
	}
	return nil
}

func collectImports(file *ast.File) []ImportFact {
	imports := []ImportFact{}
	for _, spec := range file.Imports {
		path, ok := stringLiteral(spec.Path)
		if !ok {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports = append(imports, ImportFact{Name: name, Path: path})
	}
	sort.Slice(imports, func(i, j int) bool {
		if imports[i].Name == imports[j].Name {
			return imports[i].Path < imports[j].Path
		}
		return imports[i].Name < imports[j].Name
	})
	return imports
}

func collectCalls(
	file string,
	function string,
	body *ast.BlockStmt,
	importAliases map[string]string,
	report *Report,
) {
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		parts, ok := selectorParts(call.Fun)
		if !ok || len(parts) < 2 {
			return true
		}
		symbol := strings.Join(parts, ".")
		report.SelectorCalls = append(report.SelectorCalls, SelectorCall{
			File:     file,
			Function: function,
			Symbol:   symbol,
			Parts:    append([]string(nil), parts...),
		})
		if importPath := importAliases[parts[0]]; importPath != "" {
			report.PackageCalls = append(report.PackageCalls, PackageCall{
				File:       file,
				Function:   function,
				Symbol:     symbol,
				Package:    parts[0],
				ImportPath: importPath,
				Method:     parts[len(parts)-1],
			})
		}
		if rest, ok := rawRESTCall(file, function, symbol, parts, call); ok {
			report.RawRESTCalls = append(report.RawRESTCalls, rest)
		}
		return true
	})
}

func resourceRegistration(file string, resource string, expr ast.Expr) (ResourceRegistration, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ResourceRegistration{}, false
	}
	parts, ok := selectorParts(call.Fun)
	if !ok || len(parts) == 0 {
		return ResourceRegistration{}, false
	}
	reg := ResourceRegistration{
		Resource:    resource,
		File:        file,
		Constructor: parts[len(parts)-1],
	}
	if len(parts) > 1 {
		reg.Package = strings.Join(parts[:len(parts)-1], ".")
	}
	return reg, true
}

func readCallback(file string, kv *ast.KeyValueExpr) (ReadCallback, bool) {
	key, ok := identName(kv.Key)
	if !ok {
		return ReadCallback{}, false
	}
	if key != "Read" && key != "ReadContext" && key != "ReadWithoutTimeout" {
		return ReadCallback{}, false
	}
	parts, ok := selectorParts(kv.Value)
	if !ok || len(parts) == 0 {
		return ReadCallback{}, false
	}
	cb := ReadCallback{
		Field:    key,
		File:     file,
		Function: parts[len(parts)-1],
	}
	if len(parts) > 1 {
		cb.Package = strings.Join(parts[:len(parts)-1], ".")
	}
	return cb, true
}

func rawRESTCall(file, function, symbol string, parts []string, call *ast.CallExpr) (RawRESTCall, bool) {
	if parts[len(parts)-1] != "NewRequest" || len(call.Args) < 2 {
		return RawRESTCall{}, false
	}
	method, ok := httpMethod(call.Args[0])
	if !ok {
		return RawRESTCall{}, false
	}
	path := rawPath(call.Args[1])
	return RawRESTCall{
		File:     file,
		Function: function,
		Symbol:   symbol,
		Method:   method,
		Path:     path,
	}, true
}

func httpMethod(expr ast.Expr) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return strings.ToUpper(value), true
	}
	parts, ok := selectorParts(expr)
	if !ok || len(parts) != 2 || parts[0] != "http" {
		return "", false
	}
	if strings.HasPrefix(parts[1], "Method") {
		return strings.ToUpper(strings.TrimPrefix(parts[1], "Method")), true
	}
	return "", false
}

func rawPath(expr ast.Expr) string {
	if value, ok := stringLiteral(expr); ok {
		return value
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	parts, ok := selectorParts(call.Fun)
	if !ok || strings.Join(parts, ".") != "fmt.Sprintf" {
		return ""
	}
	value, _ := stringLiteral(call.Args[0])
	return value
}

func selectorParts(expr ast.Expr) ([]string, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		return []string{value.Name}, true
	case *ast.SelectorExpr:
		parts, ok := selectorParts(value.X)
		if !ok {
			return nil, false
		}
		return append(parts, value.Sel.Name), true
	default:
		return nil, false
	}
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func identName(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return exprName(fn.Recv.List[0].Type)
}

func exprName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return exprName(value.X)
	case *ast.SelectorExpr:
		parts, ok := selectorParts(value)
		if !ok {
			return ""
		}
		return strings.Join(parts, ".")
	default:
		return ""
	}
}

func parseGoMod(path string) (*GoMod, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gomod := GoMod{Requires: []GoRequire{}}
	inRequireBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "module ") {
			gomod.Module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			continue
		}
		if line == "require (" {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}
		if strings.HasPrefix(line, "require ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
			if req, ok := parseRequire(line); ok {
				gomod.Requires = append(gomod.Requires, req)
			}
			continue
		}
		if inRequireBlock {
			if req, ok := parseRequire(line); ok {
				gomod.Requires = append(gomod.Requires, req)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Slice(gomod.Requires, func(i, j int) bool {
		return gomod.Requires[i].Path < gomod.Requires[j].Path
	})
	return &gomod, nil
}

func parseRequire(line string) (GoRequire, bool) {
	indirect := strings.Contains(line, "// indirect")
	line = strings.TrimSpace(strings.Split(line, "//")[0])
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return GoRequire{}, false
	}
	return GoRequire{
		Path:     fields[0],
		Version:  fields[1],
		Indirect: indirect,
	}, true
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".terraform", "acceptance", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func isSourceFile(name string) bool {
	return strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go") &&
		name != "sweep.go"
}

func sortReport(report *Report) {
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })
	sort.Slice(report.Functions, func(i, j int) bool {
		return report.Functions[i].File+report.Functions[i].Name < report.Functions[j].File+report.Functions[j].Name
	})
	sort.Slice(report.ResourceRegistrations, func(i, j int) bool {
		left := report.ResourceRegistrations[i]
		right := report.ResourceRegistrations[j]
		return left.Resource+left.File+left.Constructor < right.Resource+right.File+right.Constructor
	})
	sort.Slice(report.ResourceReferences, func(i, j int) bool {
		left := report.ResourceReferences[i]
		right := report.ResourceReferences[j]
		return left.Resource+left.File < right.Resource+right.File
	})
	sort.Slice(report.IdentifierReferences, func(i, j int) bool {
		left := report.IdentifierReferences[i]
		right := report.IdentifierReferences[j]
		return left.File+left.Name < right.File+right.Name
	})
	sort.Slice(report.ReadCallbacks, func(i, j int) bool {
		left := report.ReadCallbacks[i]
		right := report.ReadCallbacks[j]
		return left.File+left.Field+left.Function < right.File+right.Field+right.Function
	})
	sort.Slice(report.SelectorCalls, func(i, j int) bool {
		left := report.SelectorCalls[i]
		right := report.SelectorCalls[j]
		return left.File+left.Function+left.Symbol < right.File+right.Function+right.Symbol
	})
	sort.Slice(report.PackageCalls, func(i, j int) bool {
		left := report.PackageCalls[i]
		right := report.PackageCalls[j]
		return left.File+left.Function+left.Symbol < right.File+right.Function+right.Symbol
	})
	sort.Slice(report.RawRESTCalls, func(i, j int) bool {
		left := report.RawRESTCalls[i]
		right := report.RawRESTCalls[j]
		return left.File+left.Function+left.Symbol < right.File+right.Function+right.Symbol
	})
}
