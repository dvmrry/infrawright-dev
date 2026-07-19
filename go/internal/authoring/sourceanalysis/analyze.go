package sourceanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const (
	maxCallDepth       = 64
	maxCandidates      = 32
	maxParsedFiles     = 10000
	maxDeclarations    = 200000
	maxFunctions       = 100000
	maxCallExpressions = 500000
)

var errPackageVariableMissing = errors.New("missing package variable")

type analysisCaps struct {
	parsedFiles     int
	declarations    int
	functions       int
	callExpressions int
}

func defaultCaps() analysisCaps {
	return analysisCaps{
		parsedFiles: maxParsedFiles, declarations: maxDeclarations,
		functions: maxFunctions, callExpressions: maxCallExpressions,
	}
}

// QualifiedEvidence is a sealed canonical source-evidence report. It can only
// be created by Analyze from sourcebind-qualified input bytes.
type QualifiedEvidence struct {
	canonical []byte
	digest    string
}

// CanonicalBytes returns a detached copy of the canonical report bytes.
func (e QualifiedEvidence) CanonicalBytes() ([]byte, error) {
	if len(e.canonical) == 0 || e.digest == "" {
		return nil, fmt.Errorf("qualified evidence must come from Analyze")
	}
	return append([]byte(nil), e.canonical...), nil
}

// SHA256 returns the digest of CanonicalBytes.
func (e QualifiedEvidence) SHA256() (string, error) {
	if len(e.canonical) == 0 || e.digest == "" {
		return "", fmt.Errorf("qualified evidence must come from Analyze")
	}
	return e.digest, nil
}

// Snapshot returns a detached, strictly decoded report.
func (e QualifiedEvidence) Snapshot() (contracts.SourceEvidenceReport, error) {
	if len(e.canonical) == 0 || e.digest == "" {
		return contracts.SourceEvidenceReport{}, fmt.Errorf("qualified evidence must come from Analyze")
	}
	return contracts.DecodeSourceEvidenceReport(e.canonical)
}

// Analyze derives source evidence exclusively from one defensive qualified
// snapshot. It does not read paths, consult the network, or invoke tools.
func Analyze(ctx context.Context, inputs sourcebind.QualifiedInputs) (QualifiedEvidence, error) {
	if err := ctx.Err(); err != nil {
		return QualifiedEvidence{}, fmt.Errorf("source analysis cancelled: %w", err)
	}
	snapshot, err := inputs.Snapshot()
	if err != nil {
		return QualifiedEvidence{}, fmt.Errorf("snapshot qualified inputs: %w", err)
	}
	index, err := newIndex(ctx, snapshot)
	if err != nil {
		return QualifiedEvidence{}, err
	}
	report, err := index.report(ctx)
	if err != nil {
		return QualifiedEvidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return QualifiedEvidence{}, fmt.Errorf("source analysis cancelled: %w", err)
	}
	if err := contracts.ValidateSourceEvidenceReportAgainstInput(report, snapshot.InputProvenance); err != nil {
		return QualifiedEvidence{}, fmt.Errorf("validate derived source evidence: %w", err)
	}
	rendered, err := contracts.RenderSourceEvidenceReport(report)
	if err != nil {
		return QualifiedEvidence{}, fmt.Errorf("render derived source evidence: %w", err)
	}
	canonical := []byte(rendered)
	digest := sha256.Sum256(canonical)
	return QualifiedEvidence{canonical: canonical, digest: hex.EncodeToString(digest[:])}, nil
}

type sourceFile struct {
	origin      contracts.SourceLocationOrigin
	modulePath  string
	path        string
	packagePath string
	parsed      *ast.File
	fset        *token.FileSet
	imports     map[string]string
	constants   map[string]string
}

type function struct {
	file        *sourceFile
	decl        *ast.FuncDecl
	symbol      string
	packagePath string
	receiver    string
}

type typeReference struct {
	packagePath string
	name        string
}

type analysisIndex struct {
	snapshot          sourcebind.VerifiedSnapshot
	providerModule    string
	files             []*sourceFile
	functions         map[string]*function
	providerFunctions map[string]*function
	sdkFunctions      map[string]*function
	registrations     map[string]*registration
	filter            map[string]struct{}
	sdkModules        map[string]string
	missingSDKModules map[string]struct{}
	constants         map[string]map[string]string
	packageNames      map[string]string
	fields            map[string]typeReference
	interfaces        map[string]struct{}
	typeNames         map[string]struct{}
	nonTypeNames      map[string]struct{}
	selectedResources map[string]struct{}
	caps              analysisCaps
	parsedFiles       int
	declarations      int
	functionCount     int
	callCount         int
}

type registration struct {
	symbol         contracts.SourceSymbol
	constructorKey string
	callback       *function
}

func newIndex(ctx context.Context, snapshot sourcebind.VerifiedSnapshot) (*analysisIndex, error) {
	return newIndexWithCaps(ctx, snapshot, defaultCaps())
}

func newIndexWithCaps(ctx context.Context, snapshot sourcebind.VerifiedSnapshot, caps analysisCaps) (*analysisIndex, error) {
	i := &analysisIndex{
		snapshot:          snapshot,
		providerModule:    snapshot.Provider.ModulePath,
		functions:         make(map[string]*function),
		providerFunctions: make(map[string]*function),
		sdkFunctions:      make(map[string]*function),
		registrations:     make(map[string]*registration),
		filter:            make(map[string]struct{}),
		sdkModules:        make(map[string]string),
		missingSDKModules: make(map[string]struct{}),
		constants:         make(map[string]map[string]string),
		packageNames:      make(map[string]string),
		fields:            make(map[string]typeReference),
		interfaces:        make(map[string]struct{}),
		typeNames:         make(map[string]struct{}),
		nonTypeNames:      make(map[string]struct{}),
		selectedResources: make(map[string]struct{}),
		caps:              caps,
	}
	for _, filter := range snapshot.Manifest.Selection.Filters {
		if filter.Name == contracts.SelectionFilterReviewedNotApplicable {
			for _, value := range filter.Values {
				i.filter[value] = struct{}{}
			}
		}
	}
	for _, resource := range snapshot.Manifest.Selection.ResourceTypes {
		i.selectedResources[resource] = struct{}{}
	}
	if err := i.addTree(ctx, snapshot.Provider, contracts.SourceLocationProvider, ""); err != nil {
		return nil, err
	}
	modules := make([]string, 0, len(snapshot.SDKs))
	for module := range snapshot.SDKs {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		i.sdkModules[module] = sdkVersion(snapshot.Manifest, module)
		if err := i.addTree(ctx, snapshot.SDKs[module], contracts.SourceLocationSDK, module); err != nil {
			return nil, err
		}
	}
	for _, sdk := range snapshot.Manifest.UnavailableSDKs {
		i.missingSDKModules[sdk.ModulePath] = struct{}{}
	}
	if err := i.indexRegistrations(); err != nil {
		return nil, err
	}
	return i, nil
}

func sdkVersion(manifest contracts.SourceProvenance, module string) string {
	for _, sdk := range manifest.SDKs {
		if sdk.ModulePath == module {
			return sdk.ModuleVersion
		}
	}
	return ""
}

func (i *analysisIndex) addTree(ctx context.Context, tree sourcebind.CapturedTree, origin contracts.SourceLocationOrigin, module string) error {
	for _, captured := range tree.Files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("source analysis cancelled: %w", err)
		}
		if !strings.HasSuffix(captured.Path, ".go") {
			continue
		}
		i.parsedFiles++
		if i.parsedFiles > i.caps.parsedFiles {
			return fmt.Errorf("source analysis parsed-file limit exceeded")
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, captured.Path, captured.Bytes, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse captured %s: %w", captured.Path, err)
		}
		pkg := tree.ModulePath
		if dir := path.Dir(captured.Path); dir != "." {
			pkg += "/" + dir
		}
		if prior, exists := i.packageNames[pkg]; exists {
			if prior != parsed.Name.Name {
				return fmt.Errorf("conflicting package declarations in %s: %q and %q", pkg, prior, parsed.Name.Name)
			}
		} else {
			i.packageNames[pkg] = parsed.Name.Name
		}
		file := &sourceFile{origin: origin, modulePath: module, path: captured.Path, packagePath: pkg, parsed: parsed, fset: fset, imports: imports(parsed), constants: stringConstants(parsed)}
		i.declarations += len(parsed.Decls)
		if i.declarations > i.caps.declarations {
			return fmt.Errorf("source analysis declaration limit exceeded")
		}
		calls := countCalls(parsed)
		i.callCount += calls
		if i.callCount > i.caps.callExpressions {
			return fmt.Errorf("source analysis call-expression limit exceeded")
		}
		if i.constants[pkg] == nil {
			i.constants[pkg] = make(map[string]string)
		}
		constantNames := make([]string, 0, len(file.constants))
		for name := range file.constants {
			constantNames = append(constantNames, name)
		}
		sort.Strings(constantNames)
		for _, name := range constantNames {
			value := file.constants[name]
			if _, exists := i.constants[pkg][name]; exists {
				return fmt.Errorf("duplicate string constant %s in %s", name, pkg)
			}
			i.constants[pkg][name] = value
		}
		i.files = append(i.files, file)
		i.indexPackageNonTypeNames(file)
		if err := i.indexStructFields(file); err != nil {
			return err
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			receiver := receiverName(fn)
			symbol := fn.Name.Name
			if receiver != "" {
				symbol = "(*" + receiver + ")." + symbol
			}
			item := &function{file: file, decl: fn, symbol: symbol, packagePath: pkg, receiver: receiver}
			i.functionCount++
			if i.functionCount > i.caps.functions {
				return fmt.Errorf("source analysis function limit exceeded")
			}
			key := functionKey(pkg, symbol)
			if _, exists := i.functions[key]; exists {
				return fmt.Errorf("duplicate function declaration %s", key)
			}
			i.functions[key] = item
			if origin == contracts.SourceLocationProvider {
				i.providerFunctions[functionKey(pkg, symbol)] = item
			} else {
				i.sdkFunctions[key] = item
			}
		}
	}
	return nil
}

func (i *analysisIndex) indexPackageNonTypeNames(file *sourceFile) {
	for _, declaration := range file.parsed.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || (group.Tok != token.VAR && group.Tok != token.CONST) {
			continue
		}
		for _, specification := range group.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				i.nonTypeNames[typeKey(file.packagePath, name.Name)] = struct{}{}
			}
		}
	}
}

func countCalls(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(current ast.Node) bool {
		if _, ok := current.(*ast.CallExpr); ok {
			count++
		}
		return true
	})
	return count
}

func (i *analysisIndex) indexStructFields(file *sourceFile) error {
	for _, declaration := range file.parsed.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}
		for _, specification := range group.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			i.typeNames[typeKey(file.packagePath, typeSpec.Name.Name)] = struct{}{}
			if _, ok := typeSpec.Type.(*ast.InterfaceType); ok {
				i.interfaces[typeKey(file.packagePath, typeSpec.Name.Name)] = struct{}{}
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structure.Fields.List {
				if len(field.Names) != 1 {
					continue
				}
				reference, ok := typeReferenceForExpr(file, field.Type)
				if !ok {
					continue
				}
				key := fieldKey(file.packagePath, typeSpec.Name.Name, field.Names[0].Name)
				if _, exists := i.fields[key]; exists {
					return fmt.Errorf("duplicate captured struct field %s", key)
				}
				i.fields[key] = reference
			}
		}
	}
	return nil
}

func typeReferenceForExpr(file *sourceFile, expr ast.Expr) (typeReference, bool) {
	for {
		switch value := expr.(type) {
		case *ast.StarExpr:
			expr = value.X
		case *ast.ParenExpr:
			expr = value.X
		default:
			goto resolved
		}
	}
resolved:
	switch value := expr.(type) {
	case *ast.Ident:
		return typeReference{packagePath: file.packagePath, name: value.Name}, true
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok {
			return typeReference{}, false
		}
		packagePath, ok := file.imports[alias.Name]
		return typeReference{packagePath: packagePath, name: value.Sel.Name}, ok
	default:
		return typeReference{}, false
	}
}

func fieldKey(packagePath, receiver, field string) string {
	return packagePath + "\x00" + receiver + "\x00" + field
}

func typeKey(packagePath, name string) string { return packagePath + "\x00" + name }

func imports(file *ast.File) map[string]string {
	out := make(map[string]string)
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(value)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		out[name] = value
	}
	return out
}

func stringConstants(file *ast.File) map[string]string {
	constants := make(map[string]string)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, specification := range group.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			if literal, ok := stringLiteral(value.Values[0]); ok {
				constants[value.Names[0].Name] = literal
			}
		}
	}
	return constants
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	return typeName(fn.Recv.List[0].Type)
}

func typeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.StarExpr:
		return typeName(value.X)
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		if pkg, ok := value.X.(*ast.Ident); ok {
			return pkg.Name + "." + value.Sel.Name
		}
	}
	return ""
}

func functionKey(pkg, symbol string) string { return pkg + "\x00" + symbol }

func (i *analysisIndex) indexRegistrations() error {
	serveFactories, err := i.pluginServeFactories()
	if err != nil {
		return err
	}
	factories := serveFactories
	if len(factories) == 0 {
		factories = i.providerFactories()
	}
	if len(factories) != 0 {
		for _, factory := range factories {
			if err := i.indexFactoryResources(factory); err != nil {
				return err
			}
		}
	}
	resources := make([]string, 0, len(i.registrations))
	if len(i.selectedResources) == 0 {
		for resource := range i.registrations {
			resources = append(resources, resource)
		}
	} else {
		for resource := range i.selectedResources {
			if _, registered := i.registrations[resource]; !registered {
				continue
			}
			resources = append(resources, resource)
		}
	}
	sort.Strings(resources)
	for _, resource := range resources {
		registration := i.registrations[resource]
		constructor := i.providerFunctions[registration.constructorKey]
		if constructor == nil {
			continue
		}
		callback, err := i.callbackForConstructor(constructor)
		if err != nil {
			return err
		}
		registration.callback = callback
	}
	return nil
}

func (i *analysisIndex) providerFactories() []*function {
	var factories []*function
	seen := make(map[string]struct{})
	for _, file := range i.files {
		if file.origin != contracts.SourceLocationProvider || file.packagePath != i.providerModule {
			continue
		}
		if factory := i.providerFunctions[functionKey(file.packagePath, "Provider")]; factory != nil && factory.receiver == "" {
			if _, exists := seen[functionKey(factory.packagePath, factory.symbol)]; exists {
				continue
			}
			seen[functionKey(factory.packagePath, factory.symbol)] = struct{}{}
			factories = append(factories, factory)
		}
	}
	return factories
}

func (i *analysisIndex) pluginServeFactories() ([]*function, error) {
	var factories []*function
	var scanErr error
	for _, file := range i.files {
		if file.origin != contracts.SourceLocationProvider || file.packagePath != i.providerModule || file.path != "main.go" || file.parsed.Name.Name != "main" {
			continue
		}
		for _, declaration := range file.parsed.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "main" || fn.Body == nil {
				continue
			}
			for _, call := range callsInBlock(fn.Body, map[string]bool{}) {
				if scanErr != nil {
					break
				}
				factory, recognized, err := i.pluginServeFactory(file, call)
				if err != nil {
					scanErr = err
					break
				}
				if recognized {
					factories = append(factories, factory)
				}
			}
		}
	}
	if scanErr != nil {
		return nil, scanErr
	}
	if len(factories) > 1 {
		return nil, fmt.Errorf("multiple plugin Serve provider authorities")
	}
	return factories, nil
}

func (i *analysisIndex) pluginServeFactory(file *sourceFile, call *ast.CallExpr) (*function, bool, error) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Serve" {
		return nil, false, nil
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok || !hashicorpPluginPackage(file.imports[alias.Name]) {
		return nil, false, nil
	}
	if len(call.Args) != 1 {
		return nil, true, fmt.Errorf("plugin Serve must have one direct ServeOpts argument")
	}
	options := resourceLiteral(call.Args[0])
	if options == nil || !isNamedImportedType(file, options.Type, alias.Name, "ServeOpts") {
		return nil, true, fmt.Errorf("plugin Serve requires direct %s.ServeOpts", alias.Name)
	}
	for _, element := range options.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "ProviderFunc" {
			continue
		}
		switch value := field.Value.(type) {
		case *ast.Ident:
			factory := i.providerFunctions[functionKey(file.packagePath, value.Name)]
			if factory == nil || factory.receiver != "" {
				return nil, true, fmt.Errorf("plugin Serve ProviderFunc must name a captured provider function")
			}
			return factory, true, nil
		case *ast.SelectorExpr:
			packageAlias, ok := value.X.(*ast.Ident)
			if !ok {
				break
			}
			packagePath, ok := file.imports[packageAlias.Name]
			if !ok || !i.isProviderPackage(packagePath) {
				break
			}
			factory := i.providerFunctions[functionKey(packagePath, value.Sel.Name)]
			if factory != nil && factory.receiver == "" {
				return factory, true, nil
			}
		}
		return nil, true, fmt.Errorf("plugin Serve ProviderFunc must name a captured provider function")
	}
	return nil, true, fmt.Errorf("plugin Serve requires ProviderFunc")
}

func hashicorpPluginPackage(packagePath string) bool {
	return packagePath == "github.com/hashicorp/terraform-plugin-sdk/plugin" || packagePath == "github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
}

func isNamedImportedType(file *sourceFile, expr ast.Expr, alias, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	prefix, ok := selector.X.(*ast.Ident)
	return ok && prefix.Name == alias
}

func (i *analysisIndex) indexFactoryResources(factory *function) error {
	for _, statement := range factory.decl.Body.List {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			continue
		}
		provider := resourceLiteral(returned.Results[0])
		if provider == nil {
			if identifier, ok := returned.Results[0].(*ast.Ident); ok {
				provider = factoryLocalProviderLiteral(factory, identifier.Name, returned.Pos())
			}
		}
		if provider == nil {
			continue
		}
		for _, element := range provider.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := field.Key.(*ast.Ident)
			if !ok || key.Name != "ResourcesMap" {
				continue
			}
			if err := i.indexResourceMap(factory.file, field.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// factoryLocalProviderLiteral accepts the sole local binding shape used by
// captured provider factories: an unconditional top-level p := &Provider{…}
// (or var p = …) immediately available to a later return p. It intentionally
// does not generalize local-value resolution for resource constructors.
func factoryLocalProviderLiteral(factory *function, name string, returned token.Pos) *ast.CompositeLit {
	bindings := 0
	var bound ast.Expr
	var boundAt token.Pos
	for _, statement := range factory.decl.Body.List {
		expression, boundName, ok := topLevelFactoryBinding(statement)
		if !ok || boundName != name {
			continue
		}
		bindings++
		bound, boundAt = expression, statement.Pos()
	}
	if bindings != 1 || boundAt >= returned || factoryBindingWrites(factory.decl.Body, name) != 1 {
		return nil
	}
	return resourceLiteral(bound)
}

func topLevelFactoryBinding(statement ast.Stmt) (ast.Expr, string, bool) {
	switch value := statement.(type) {
	case *ast.AssignStmt:
		if value.Tok != token.DEFINE || len(value.Lhs) != 1 || len(value.Rhs) != 1 {
			return nil, "", false
		}
		name, ok := value.Lhs[0].(*ast.Ident)
		if !ok {
			return nil, "", false
		}
		return value.Rhs[0], name.Name, true
	case *ast.DeclStmt:
		group, ok := value.Decl.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR || len(group.Specs) != 1 {
			return nil, "", false
		}
		specification, ok := group.Specs[0].(*ast.ValueSpec)
		if !ok || len(specification.Names) != 1 || len(specification.Values) != 1 {
			return nil, "", false
		}
		return specification.Values[0], specification.Names[0].Name, true
	default:
		return nil, "", false
	}
}

func factoryBindingWrites(body *ast.BlockStmt, name string) int {
	writes := 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for _, left := range value.Lhs {
				if identifier, ok := left.(*ast.Ident); ok && identifier.Name == name {
					writes++
				}
			}
		case *ast.ValueSpec:
			for _, candidate := range value.Names {
				if candidate.Name == name {
					writes++
				}
			}
		case *ast.RangeStmt:
			for _, candidate := range []ast.Expr{value.Key, value.Value} {
				if identifier, ok := candidate.(*ast.Ident); ok && identifier.Name == name {
					writes++
				}
			}
		case *ast.IncDecStmt:
			if identifier, ok := value.X.(*ast.Ident); ok && identifier.Name == name {
				writes++
			}
		}
		return true
	})
	return writes
}

func (i *analysisIndex) indexResourceMap(file *sourceFile, expr ast.Expr) error {
	if name, ok := expr.(*ast.Ident); ok {
		var err error
		expr, err = i.packageVariable(file.packagePath, name.Name)
		if err != nil {
			return err
		}
	}
	composite, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	for _, element := range composite.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := stringLiteral(kv.Key)
		if !ok {
			continue
		}
		if _, exists := i.registrations[key]; exists {
			return fmt.Errorf("duplicate selected resource registration %q", key)
		}
		call, ok := kv.Value.(*ast.CallExpr)
		if !ok {
			i.registrations[key] = &registration{symbol: i.symbol(file, "", "ResourcesMap["+key+"]", kv.Value.Pos())}
			continue
		}
		constructorKey, spelling, ok := i.registrationConstructor(file, call)
		if !ok {
			i.registrations[key] = &registration{symbol: i.symbol(file, "", "ResourcesMap["+key+"]", call.Pos())}
			continue
		}
		i.registrations[key] = &registration{symbol: i.symbol(file, "", spelling, call.Pos()), constructorKey: constructorKey}
	}
	return nil
}

func (i *analysisIndex) registrationConstructor(file *sourceFile, call *ast.CallExpr) (string, string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return functionKey(file.packagePath, fun.Name), fun.Name, true
	case *ast.SelectorExpr:
		alias, ok := fun.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		importPath, ok := file.imports[alias.Name]
		if !ok || !i.isProviderPackage(importPath) {
			return "", "", false
		}
		return functionKey(importPath, fun.Sel.Name), alias.Name + "." + fun.Sel.Name, true
	default:
		return "", "", false
	}
}

func (i *analysisIndex) packageVariable(packagePath, name string) (ast.Expr, error) {
	var values []ast.Expr
	for _, file := range i.files {
		if file.origin != contracts.SourceLocationProvider || file.packagePath != packagePath {
			continue
		}
		for _, declaration := range file.parsed.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.VAR {
				continue
			}
			for _, specification := range group.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != name || len(value.Values) != 1 {
					continue
				}
				values = append(values, value.Values[0])
			}
		}
	}
	if len(values) > 1 {
		return nil, fmt.Errorf("ambiguous package variable %s in %s", name, packagePath)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%w %s in %s", errPackageVariableMissing, name, packagePath)
	}
	return values[0], nil
}

func (i *analysisIndex) callbackForConstructor(constructor *function) (*function, error) {
	var literals []*ast.CompositeLit
	for _, statement := range constructor.decl.Body.List {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			continue
		}
		if literal := resourceLiteral(returned.Results[0]); literal != nil {
			literals = append(literals, literal)
			continue
		}
		if name, ok := returned.Results[0].(*ast.Ident); ok {
			expr, err := i.packageVariable(constructor.packagePath, name.Name)
			if err != nil {
				if errors.Is(err, errPackageVariableMissing) {
					return nil, nil
				}
				return nil, err
			}
			if literal := resourceLiteral(expr); literal != nil {
				literals = append(literals, literal)
			}
		}
	}
	if len(literals) > 1 {
		return nil, fmt.Errorf("ambiguous returned resource literal in %s", constructor.symbol)
	}
	if len(literals) == 0 {
		return nil, nil
	}
	callback, err := readCallback(literals[0])
	if err != nil || callback == "" {
		return nil, err
	}
	return i.providerFunctions[functionKey(constructor.packagePath, callback)], nil
}

func readCallback(literal *ast.CompositeLit) (string, error) {
	var callbacks []string
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || (key.Name != "Read" && key.Name != "ReadContext" && key.Name != "ReadWithoutTimeout") {
			continue
		}
		callback, ok := field.Value.(*ast.Ident)
		if !ok {
			return "", nil
		}
		callbacks = append(callbacks, callback.Name)
	}
	if len(callbacks) > 1 {
		return "", fmt.Errorf("ambiguous resource read callback")
	}
	if len(callbacks) == 0 {
		return "", nil
	}
	return callbacks[0], nil
}

func resourceLiteral(expr ast.Expr) *ast.CompositeLit {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		expr = unary.X
	}
	literal, _ := expr.(*ast.CompositeLit)
	return literal
}

func (i *analysisIndex) report(ctx context.Context) (contracts.SourceEvidenceReport, error) {
	resources := make(map[string]contracts.SourceEvidenceRow, len(i.snapshot.Manifest.Selection.ResourceTypes))
	for _, resource := range i.snapshot.Manifest.Selection.ResourceTypes {
		if err := ctx.Err(); err != nil {
			return contracts.SourceEvidenceReport{}, fmt.Errorf("source analysis cancelled: %w", err)
		}
		resources[resource] = i.resourceRow(ctx, resource)
		if err := ctx.Err(); err != nil {
			return contracts.SourceEvidenceReport{}, fmt.Errorf("source analysis cancelled: %w", err)
		}
	}
	manifestSHA := i.snapshot.ManifestSHA256
	report := contracts.SourceEvidenceReport{Kind: "infrawright.source_evidence_report", SchemaVersion: 1, SourceTrust: contracts.SourceTrustVerified, SourceManifestSHA256: &manifestSHA, InputProvenanceSHA256: i.snapshot.InputProvenanceSHA256, Resources: resources}
	report.Summary = summary(resources)
	return report, nil
}

func (i *analysisIndex) resourceRow(ctx context.Context, resource string) contracts.SourceEvidenceRow {
	if _, ok := i.filter[resource]; ok {
		return contracts.SourceEvidenceRow{Classification: contracts.SourceNotApplicable, Chains: []contracts.SourceEvidenceChain{}, ReasonCode: reason(contracts.ReasonReviewedNotApplicable)}
	}
	registration := i.registrations[resource]
	if registration == nil {
		return contracts.SourceEvidenceRow{Classification: contracts.SourceNoSource, Chains: []contracts.SourceEvidenceChain{}, ReasonCode: reason(contracts.ReasonProviderSourceMissing)}
	}
	registrationSymbol := registration.symbol
	callback := registration.callback
	if callback == nil {
		return contracts.SourceEvidenceRow{Classification: contracts.SourceUnresolved, ProviderRegistration: &registrationSymbol, Chains: []contracts.SourceEvidenceChain{}, ReasonCode: reason(contracts.ReasonReadCallbackUnresolved)}
	}
	callbackSymbol := i.functionSymbol(callback)
	state := traceState{ctx: ctx, index: i, current: callback, bools: map[string]bool{}, steps: nil, depth: 0}
	chains := state.trace()
	if len(chains) == 0 {
		chains = []contracts.SourceEvidenceChain{{Steps: []contracts.SourceCallStep{unresolvedStep(callback, callback.decl.Name.Pos(), callback.decl.Name.Name)}, ReasonCode: reason(contracts.ReasonCallChainUnresolved)}}
	}
	row := contracts.SourceEvidenceRow{ProviderRegistration: &registrationSymbol, ReadCallback: &callbackSymbol, Chains: chains}
	if allMissingSDKChains(chains) {
		row.Classification = contracts.SourceNoSource
		row.ReasonCode = reason(contracts.ReasonSDKSourceMissing)
		if err := sortChains(row.Chains); err != nil {
			return contracts.SourceEvidenceRow{Classification: contracts.SourceUnresolved, ProviderRegistration: &registrationSymbol, ReadCallback: &callbackSymbol, Chains: []contracts.SourceEvidenceChain{{Steps: []contracts.SourceCallStep{unresolvedStep(callback, callback.decl.Name.Pos(), callback.decl.Name.Name)}, ReasonCode: reason(contracts.ReasonCallChainUnresolved)}}, ReasonCode: reason(contracts.ReasonCallChainUnresolved)}
		}
		return row
	}
	if len(chains) > 1 {
		row.Classification = contracts.SourceAmbiguous
		row.ReasonCode = reason(contracts.ReasonMultipleCandidates)
		if err := sortChains(row.Chains); err != nil {
			return contracts.SourceEvidenceRow{Classification: contracts.SourceUnresolved, ProviderRegistration: &registrationSymbol, ReadCallback: &callbackSymbol, Chains: []contracts.SourceEvidenceChain{{Steps: []contracts.SourceCallStep{unresolvedStep(callback, callback.decl.Name.Pos(), callback.decl.Name.Name)}, ReasonCode: reason(contracts.ReasonCallChainUnresolved)}}, ReasonCode: reason(contracts.ReasonCallChainUnresolved)}
		}
		return row
	}
	chain := chains[0]
	switch {
	case chain.Endpoint != nil:
		row.Classification = contracts.SourceObservedHTTP
	case chain.ReasonCode != nil && (*chain.ReasonCode == contracts.ReasonDynamicPath || *chain.ReasonCode == contracts.ReasonDynamicMethod || *chain.ReasonCode == contracts.ReasonDynamicDispatch):
		row.Classification = contracts.SourceDynamic
	case chain.ReasonCode != nil && *chain.ReasonCode == contracts.ReasonSDKSourceMissing:
		row.Classification = contracts.SourceNoSource
		row.ReasonCode = reason(contracts.ReasonSDKSourceMissing)
	case chain.SDKCall != nil:
		row.Classification = contracts.SourceObservedSDKCall
	default:
		row.Classification = contracts.SourceUnresolved
		row.ReasonCode = reason(contracts.ReasonCallChainUnresolved)
	}
	row.LegacyMapped = row.Classification == contracts.SourceObservedHTTP
	return row
}

func allMissingSDKChains(chains []contracts.SourceEvidenceChain) bool {
	if len(chains) == 0 {
		return false
	}
	for _, chain := range chains {
		if !terminalMissingSDKChain(chain) {
			return false
		}
	}
	return true
}

func terminalMissingSDKChain(chain contracts.SourceEvidenceChain) bool {
	return chain.ReasonCode != nil && *chain.ReasonCode == contracts.ReasonSDKSourceMissing &&
		chain.Endpoint == nil && chain.SDKCall == nil && len(chain.Steps) != 0 &&
		chain.Steps[len(chain.Steps)-1].Kind == contracts.CallSDKSourceMissing &&
		!chainHasKind(chain, contracts.CallUnresolvedDispatch)
}

func chainHasKind(chain contracts.SourceEvidenceChain, kind contracts.SourceCallKind) bool {
	for _, step := range chain.Steps {
		if step.Kind == kind {
			return true
		}
	}
	return false
}

func reason(value contracts.SourceReasonCode) *contracts.SourceReasonCode { return &value }

func summary(resources map[string]contracts.SourceEvidenceRow) contracts.SourceSummary {
	var counts contracts.SourceClassificationCounts
	for _, row := range resources {
		switch row.Classification {
		case contracts.SourceObservedHTTP:
			counts.ObservedHTTP++
		case contracts.SourceObservedSDKCall:
			counts.ObservedSDKCall++
		case contracts.SourceAmbiguous:
			counts.Ambiguous++
		case contracts.SourceDynamic:
			counts.Dynamic++
		case contracts.SourceUnresolved:
			counts.Unresolved++
		case contracts.SourceNoSource:
			counts.NoSource++
		case contracts.SourceNotApplicable:
			counts.NotApplicable++
		}
	}
	selected := len(resources)
	applicable := selected - counts.NotApplicable
	state := contracts.CoverageRatio
	if applicable == 0 {
		state = contracts.CoverageNotApplicable
	}
	return contracts.SourceSummary{SelectedTotal: selected, ApplicableTotal: applicable, SourceCallObservedTotal: counts.ObservedHTTP + counts.ObservedSDKCall + counts.Dynamic, EndpointObservedTotal: counts.ObservedHTTP, ClassificationCounts: counts, EndpointCoverage: contracts.ExactCoverage{State: state, Numerator: counts.ObservedHTTP, Denominator: applicable}}
}

func (i *analysisIndex) loc(file *sourceFile, function string, pos token.Pos) contracts.SourceLocation {
	p := file.fset.Position(pos)
	var fn *string
	if function != "" {
		copied := function
		fn = &copied
	}
	location := contracts.SourceLocation{Origin: file.origin, Path: file.path, Function: fn, Line: p.Line, Column: p.Column}
	if file.origin == contracts.SourceLocationSDK {
		module := file.modulePath
		location.SDKModulePath = &module
	}
	return location
}

func (i *analysisIndex) symbol(file *sourceFile, function, symbol string, pos token.Pos) contracts.SourceSymbol {
	return contracts.SourceSymbol{PackagePath: file.packagePath, Symbol: symbol, Location: i.loc(file, function, pos)}
}
func (i *analysisIndex) functionSymbol(fn *function) contracts.SourceSymbol {
	return i.symbol(fn.file, fn.symbol, fn.symbol, fn.decl.Name.Pos())
}

type traceState struct {
	ctx     context.Context
	index   *analysisIndex
	current *function
	bools   map[string]bool
	steps   []contracts.SourceCallStep
	depth   int
}

func (s traceState) trace() []contracts.SourceEvidenceChain {
	if s.depth >= maxCallDepth || s.ctx.Err() != nil {
		return []contracts.SourceEvidenceChain{{Steps: append(s.steps, unresolvedStep(s.current, s.current.decl.Name.Pos(), s.current.decl.Name.Name)), ReasonCode: reason(contracts.ReasonCallChainUnresolved)}}
	}
	nestedArguments := make(map[*ast.CallExpr]bool)
	calls := callsInBlock(s.current.decl.Body, s.bools, nestedArguments)
	var out []contracts.SourceEvidenceChain
	for _, call := range calls {
		if nestedArguments[call] && !s.nestedArgumentOperation(call) {
			continue
		}
		if len(out) >= maxCandidates {
			return []contracts.SourceEvidenceChain{{Steps: append(append([]contracts.SourceCallStep(nil), s.steps...), unresolvedStep(s.current, s.current.decl.Name.Pos(), s.current.decl.Name.Name)), ReasonCode: reason(contracts.ReasonCallChainUnresolved)}}
		}
		produced := s.follow(call)
		if nestedArguments[call] && len(produced) == 0 && s.unclassifiedImportedReceiver(call) {
			step := s.step(contracts.CallUnresolvedDispatch, nil, nil, call, exprText(call.Fun))
			produced = []contracts.SourceEvidenceChain{{
				Steps:      append(append([]contracts.SourceCallStep(nil), s.steps...), step),
				ReasonCode: reason(contracts.ReasonCallChainUnresolved),
			}}
		}
		if len(out)+len(produced) > maxCandidates {
			return []contracts.SourceEvidenceChain{{Steps: append(append([]contracts.SourceCallStep(nil), s.steps...), unresolvedStep(s.current, s.current.decl.Name.Pos(), s.current.decl.Name.Name)), ReasonCode: reason(contracts.ReasonCallChainUnresolved)}}
		}
		out = append(out, produced...)
		if s.ctx.Err() != nil {
			return nil
		}
	}
	return out
}

// nestedArgumentOperation keeps an unresolved nested argument call only when
// it crosses an imported operation boundary. Resolved constructors and local
// helpers are evaluation details of the enclosing operation; counting them
// independently would create false candidates.
func (s traceState) nestedArgumentOperation(call *ast.CallExpr) bool {
	callee, kind, _, _, _, _ := s.resolveCall(call)
	if kind == contracts.CallSDKSourceMissing {
		return true
	}
	if callee != nil {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if packageName, ok := selector.X.(*ast.Ident); ok {
		if imported, ok := s.current.file.imports[packageName.Name]; ok {
			return !nonOperationImport(imported)
		}
	}
	reference, _, _, proven := s.receiverReference(selector.X)
	if proven {
		return !nonOperationImport(reference.packagePath)
	}
	// The receiver may itself be an imported factory or operation call. Keep
	// the outer unresolved operation as one candidate, but do not emit or walk
	// the receiver call independently.
	return s.importedOperationReceiver(selector.X)
}

// unclassifiedImportedReceiver identifies the only nested calls that
// resolveCall cannot itself turn into an unresolved dispatch: a selector whose
// receiver is an imported factory/operation call. The outer call, not its
// receiver, is emitted once as unresolved by trace.
func (s traceState) unclassifiedImportedReceiver(call *ast.CallExpr) bool {
	callee, kind, _, _, _, unresolved := s.resolveCall(call)
	if callee != nil || kind != "" || unresolved {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && s.importedOperationReceiver(selector.X)
}

// importedOperationReceiver recognizes a receiver expression rooted in a call
// to an imported operation package. It intentionally answers only whether the
// outer call should remain fail-closed; callsInBlock never enumerates the
// receiver expression as a separate candidate.
func (s traceState) importedOperationReceiver(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return s.importedOperationReceiver(value.X)
	case *ast.SelectorExpr:
		return s.importedOperationReceiver(value.X)
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if packageName, ok := selector.X.(*ast.Ident); ok {
			imported, ok := s.current.file.imports[packageName.Name]
			return ok && !nonOperationImport(imported)
		}
		return s.importedOperationReceiver(selector.X)
	default:
		return false
	}
}

func (s traceState) follow(call *ast.CallExpr) []contracts.SourceEvidenceChain {
	if s.ctx.Err() != nil {
		return nil
	}
	if raw, method, pathTemplate, dynamic := s.raw(call); raw {
		step := s.step(contracts.CallRawHTTP, nil, nil, call, s.rawSymbol(call))
		steps := append(append([]contracts.SourceCallStep(nil), s.steps...), step)
		if dynamic == contracts.ReasonDynamicMethod || dynamic == contracts.ReasonDynamicPath {
			return []contracts.SourceEvidenceChain{{Steps: steps, ReasonCode: reason(dynamic)}}
		}
		endpoint := contracts.HTTPEndpointEvidence{Origin: contracts.EndpointOriginProvider, Method: method, PathTemplate: pathTemplate, Location: step.Location}
		if s.current.file.origin == contracts.SourceLocationSDK {
			endpoint.Origin = contracts.EndpointOriginSDK
		}
		return []contracts.SourceEvidenceChain{{Steps: steps, Endpoint: &endpoint}}
	}
	if request, importPath, spelling, ok := s.uncertifiedSDKRequestBuilder(call); ok {
		callee := s.index.functionSymbol(request)
		step := s.step(contracts.CallSDKReceiverMethod, &importPath, &callee, call, spelling)
		return []contracts.SourceEvidenceChain{{
			Steps:   append(append([]contracts.SourceCallStep(nil), s.steps...), step),
			SDKCall: sdkEvidence(request, s.index.sdkModules), ReasonCode: reason(contracts.ReasonEndpointNotRecovered),
		}}
	}
	callee, kind, importPath, spelling, nextBools, unresolved := s.resolveCall(call)
	if unresolved {
		step := s.step(contracts.CallUnresolvedDispatch, nil, nil, call, spelling)
		why := contracts.ReasonCallChainUnresolved
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "NewRequest" || selector.Sel.Name == "NewRequestDo") {
			if _, _, _, _, interfaceDispatch := s.receiverCall(selector.X); interfaceDispatch {
				why = contracts.ReasonDynamicDispatch
			}
		}
		return []contracts.SourceEvidenceChain{{Steps: append(append([]contracts.SourceCallStep(nil), s.steps...), step), ReasonCode: reason(why)}}
	}
	if kind == contracts.CallSDKSourceMissing {
		step := s.step(kind, &importPath, nil, call, spelling)
		return []contracts.SourceEvidenceChain{{Steps: append(append([]contracts.SourceCallStep(nil), s.steps...), step), ReasonCode: reason(contracts.ReasonSDKSourceMissing)}}
	}
	if callee == nil {
		return nil
	}
	calleeSymbol := s.index.functionSymbol(callee)
	var importPtr *string
	if importPath != "" {
		copied := importPath
		importPtr = &copied
	}
	step := s.step(kind, importPtr, &calleeSymbol, call, spelling)
	next := traceState{ctx: s.ctx, index: s.index, current: callee, bools: nextBools, steps: append(append([]contracts.SourceCallStep(nil), s.steps...), step), depth: s.depth + 1}
	chains := next.trace()
	if len(chains) == 0 && s.current.file.origin == contracts.SourceLocationProvider && callee.file.origin == contracts.SourceLocationSDK && (kind == contracts.CallSDKPackageFunction || kind == contracts.CallSDKReceiverMethod) {
		return []contracts.SourceEvidenceChain{{Steps: next.steps, SDKCall: sdkEvidence(callee, s.index.sdkModules), ReasonCode: reason(contracts.ReasonEndpointNotRecovered)}}
	}
	if callee.file.origin == contracts.SourceLocationSDK && s.current.file.origin == contracts.SourceLocationProvider {
		for n := range chains {
			chains[n].SDKCall = sdkEvidence(callee, s.index.sdkModules)
		}
	}
	return chains
}

func (s traceState) uncertifiedSDKRequestBuilder(call *ast.CallExpr) (*function, string, string, bool) {
	request, importPath, receiver, proven, interfaceDispatch := s.requestBuilder(call)
	if !proven || interfaceDispatch || request == nil || request.file.origin != contracts.SourceLocationSDK || directNetHTTPSink(request) {
		return nil, "", "", false
	}
	return request, importPath, "(*" + path.Base(importPath) + "." + receiver + ")." + requestMethod(call), true
}

func (s traceState) step(kind contracts.SourceCallKind, importPath *string, callee *contracts.SourceSymbol, call *ast.CallExpr, spelling string) contracts.SourceCallStep {
	position := call.Fun.Pos()
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		position = selector.Sel.Pos()
	}
	return contracts.SourceCallStep{Kind: kind, Symbol: spelling, ImportPath: importPath, Caller: s.index.functionSymbol(s.current), Callee: callee, Location: s.index.loc(s.current.file, s.current.symbol, position)}
}

func sdkEvidence(fn *function, versions map[string]string) *contracts.SDKCallEvidence {
	return &contracts.SDKCallEvidence{ModulePath: fn.file.modulePath, ModuleVersion: versions[fn.file.modulePath], PackagePath: fn.packagePath, Symbol: fn.symbol, Location: contracts.SourceLocation{Origin: fn.file.origin, SDKModulePath: stringPtr(fn.file.modulePath), Path: fn.file.path, Function: stringPtr(fn.symbol), Line: fn.file.fset.Position(fn.decl.Name.Pos()).Line, Column: fn.file.fset.Position(fn.decl.Name.Pos()).Column}}
}
func stringPtr(value string) *string { copied := value; return &copied }

func unresolvedStep(fn *function, pos token.Pos, symbol string) contracts.SourceCallStep {
	return contracts.SourceCallStep{Kind: contracts.CallUnresolvedDispatch, Symbol: symbol, Caller: fnSymbol(fn), Location: locFor(fn, pos)}
}
func fnSymbol(fn *function) contracts.SourceSymbol {
	return contracts.SourceSymbol{PackagePath: fn.packagePath, Symbol: fn.symbol, Location: locFor(fn, fn.decl.Name.Pos())}
}
func locFor(fn *function, pos token.Pos) contracts.SourceLocation {
	p := fn.file.fset.Position(pos)
	l := contracts.SourceLocation{Origin: fn.file.origin, Path: fn.file.path, Function: stringPtr(fn.symbol), Line: p.Line, Column: p.Column}
	if fn.file.origin == contracts.SourceLocationSDK {
		l.SDKModulePath = stringPtr(fn.file.modulePath)
	}
	return l
}

func (s traceState) resolveCall(call *ast.CallExpr) (*function, contracts.SourceCallKind, string, string, map[string]bool, bool) {
	nextBools := make(map[string]bool)
	for key, value := range s.bools {
		nextBools[key] = value
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		callee := s.index.providerFunctions[functionKey(s.current.packagePath, fun.Name)]
		if s.current.file.origin == contracts.SourceLocationSDK {
			callee = s.index.functions[functionKey(s.current.packagePath, fun.Name)]
		}
		if callee == nil {
			if s.ignoredIdentifierCall(fun) {
				return nil, "", "", fun.Name, nextBools, false
			}
			return nil, "", "", fun.Name, nextBools, true
		}
		bindBoolParameters(callee, call.Args, nextBools)
		kind := contracts.CallProviderHelper
		if s.current.file.origin == contracts.SourceLocationSDK {
			kind = contracts.CallSDKPackageFunction
			if callee.receiver != "" {
				kind = contracts.CallSDKReceiverMethod
			}
		}
		importPath := ""
		if kind == contracts.CallSDKPackageFunction || kind == contracts.CallSDKReceiverMethod {
			importPath = s.current.packagePath
		}
		return callee, kind, importPath, fun.Name, nextBools, false
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok {
			if imported, ok := s.current.file.imports[pkg.Name]; ok {
				if _, found := s.index.ownerModule(imported); found {
					callee := s.index.functions[functionKey(imported, fun.Sel.Name)]
					if callee == nil {
						return nil, "", "", pkg.Name + "." + fun.Sel.Name, nextBools, true
					}
					bindBoolParameters(callee, call.Args, nextBools)
					return callee, contracts.CallSDKPackageFunction, imported, pkg.Name + "." + fun.Sel.Name, nextBools, false
				}
				if _, missing := s.index.missingSDKOwner(imported); missing && s.current.file.origin == contracts.SourceLocationProvider {
					return nil, contracts.CallSDKSourceMissing, imported, pkg.Name + "." + fun.Sel.Name, nextBools, false
				}
				if nonOperationImport(imported) {
					return nil, "", "", pkg.Name + "." + fun.Sel.Name, nextBools, false
				}
				return nil, "", "", pkg.Name + "." + fun.Sel.Name, nextBools, true
			}
		}
		if receiver, importPath, receiverType, proven, interfaceDispatch := s.receiverCall(fun.X); interfaceDispatch {
			return nil, "", "", receiverType + "." + fun.Sel.Name, nextBools, true
		} else if proven {
			if s.current.file.origin == contracts.SourceLocationProvider && !s.index.isSDKPackage(importPath) && !s.index.isProviderPackage(importPath) {
				if nonOperationImport(importPath) {
					return nil, "", "", exprText(fun.X) + "." + fun.Sel.Name, nextBools, false
				}
				return nil, "", "", exprText(fun.X) + "." + fun.Sel.Name, nextBools, true
			}
			callee := s.index.functions[functionKey(importPath, "(*"+receiverType+")."+fun.Sel.Name)]
			if callee == nil {
				return nil, "", "", exprText(fun.X) + "." + fun.Sel.Name, nextBools, true
			}
			bindBoolParameters(callee, call.Args, nextBools)
			kind := contracts.CallSDKReceiverMethod
			if s.current.file.origin == contracts.SourceLocationProvider && !s.index.isSDKPackage(importPath) {
				kind = contracts.CallProviderHelper
				importPath = ""
			}
			spelling := "(*" + path.Base(importPath) + "." + receiverType + ")." + fun.Sel.Name
			if importPath == "" {
				spelling = "(*" + receiver + ")." + fun.Sel.Name
			}
			return callee, kind, importPath, spelling, nextBools, false
		}
	}
	return nil, "", "", exprText(call.Fun), nextBools, false
}

// nonOperationImport is deliberately closed: only Go's standard library and
// Terraform framework/tooling packages are known not to describe a provider
// service operation. Every other unbound imported call stays visible as an
// unresolved dispatch rather than being silently discarded.
func nonOperationImport(importPath string) bool {
	if standardLibraryImport(importPath) {
		return true
	}
	for _, prefix := range []string{
		"github.com/hashicorp/terraform-plugin-sdk/",
		"github.com/hashicorp/terraform-plugin-sdk/v2/",
		"github.com/hashicorp/terraform-plugin-framework/",
		"github.com/hashicorp/terraform-plugin-log/",
	} {
		if strings.HasPrefix(importPath, prefix) {
			return true
		}
	}
	return false
}

func standardLibraryImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

// ignoredIdentifierCall recognizes only calls that cannot be source-operation
// edges: closed predeclared builtins/types and captured TypeSpec conversions.
// A shadowed builtin is deliberately not ignored: parser object binding proves
// it is a local variable/function rather than the predeclared universe name.
func (s traceState) ignoredIdentifierCall(identifier *ast.Ident) bool {
	if identifier.Obj != nil {
		return identifier.Obj.Kind == ast.Typ
	}
	if _, shadowed := s.index.nonTypeNames[typeKey(s.current.packagePath, identifier.Name)]; shadowed {
		return false
	}
	if predeclaredBuiltin(identifier.Name) || predeclaredType(identifier.Name) {
		return true
	}
	_, captured := s.index.typeNames[typeKey(s.current.packagePath, identifier.Name)]
	return captured
}

func predeclaredBuiltin(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete", "imag", "len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover":
		return true
	default:
		return false
	}
}

func predeclaredType(name string) bool {
	switch name {
	case "any", "bool", "byte", "comparable", "complex64", "complex128", "error", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	default:
		return false
	}
}

func (i *analysisIndex) ownerModule(pkg string) (string, bool) {
	var owner string
	for module := range i.sdkModules {
		if pkg == module || strings.HasPrefix(pkg, module+"/") {
			if len(module) > len(owner) {
				owner = module
			}
		}
	}
	return owner, owner != ""
}

func (i *analysisIndex) missingSDKOwner(pkg string) (string, bool) {
	owner := ""
	for module := range i.missingSDKModules {
		if (pkg == module || strings.HasPrefix(pkg, module+"/")) && len(module) > len(owner) {
			owner = module
		}
	}
	return owner, owner != ""
}
func (i *analysisIndex) isSDKPackage(pkg string) bool { _, ok := i.ownerModule(pkg); return ok }

func (i *analysisIndex) isProviderPackage(pkg string) bool {
	for _, file := range i.files {
		if file.origin == contracts.SourceLocationProvider && file.packagePath == pkg {
			return true
		}
	}
	return false
}

func bindBoolParameters(fn *function, args []ast.Expr, out map[string]bool) {
	if fn.decl.Type.Params == nil {
		return
	}
	n := 0
	for _, field := range fn.decl.Type.Params.List {
		for _, name := range field.Names {
			if n >= len(args) {
				return
			}
			if value, ok := boolLiteral(args[n], out); ok {
				out[name.Name] = value
			}
			n++
		}
	}
}
func boolLiteral(expr ast.Expr, known map[string]bool) (bool, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		if x.Name == "true" {
			return true, true
		}
		if x.Name == "false" {
			return false, true
		}
		value, ok := known[x.Name]
		return value, ok
	case *ast.ParenExpr:
		return boolLiteral(x.X, known)
	}
	return false, false
}

func (s traceState) receiverCall(expr ast.Expr) (string, string, string, bool, bool) {
	reference, display, interfaceDispatch, ok := s.receiverReference(expr)
	if !ok {
		return "", "", "", false, false
	}
	if interfaceDispatch {
		return "", "", display, false, true
	}
	return path.Base(reference.packagePath), reference.packagePath, reference.name, true, false
}

func (s traceState) receiverReference(expr ast.Expr) (typeReference, string, bool, bool) {
	switch value := expr.(type) {
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok {
			return typeReference{}, "", false, false
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return typeReference{}, "", false, false
		}
		imported, ok := s.current.file.imports[pkg.Name]
		if !ok {
			return typeReference{}, "", false, false
		}
		if _, ok := s.index.ownerModule(imported); !ok {
			return typeReference{}, "", false, false
		}
		factory := s.index.functions[functionKey(imported, selector.Sel.Name)]
		if factory == nil {
			return typeReference{}, "", false, false
		}
		reference, ok := pointerResultReference(factory)
		return reference, "", false, ok
	case *ast.Ident:
		return s.namedReceiverReference(value.Name)
	case *ast.SelectorExpr:
		base, display, interfaceDispatch, ok := s.receiverReference(value.X)
		if !ok || interfaceDispatch {
			return typeReference{}, display, interfaceDispatch, ok
		}
		next, ok := s.index.fields[fieldKey(base.packagePath, base.name, value.Sel.Name)]
		if !ok || !s.index.isCapturedPackage(next.packagePath) {
			return typeReference{}, "", false, false
		}
		// A field-typed interface has no statically proven implementation. It is
		// not a direct receiver interface dispatch (which remains dynamic); omit
		// it so opaque SDK internals stay endpoint-not-recovered.
		if s.index.isInterface(next) {
			return typeReference{}, "", false, false
		}
		return next, "", false, true
	default:
		return typeReference{}, "", false, false
	}
}

func (s traceState) namedReceiverReference(name string) (typeReference, string, bool, bool) {
	if reference, interfaceDispatch, ok := s.receiverParameterReference(name); ok {
		return reference, reference.name, interfaceDispatch, true
	}
	var result typeReference
	found := false
	interfaceDispatch := false
	ast.Inspect(s.current.decl.Body, func(node ast.Node) bool {
		specification, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, candidate := range specification.Names {
			if candidate.Name != name {
				continue
			}
			reference, ok := typeReferenceForExpr(s.current.file, specification.Type)
			if !ok {
				return false
			}
			result, found = reference, true
			interfaceDispatch = s.index.isInterface(reference)
			return false
		}
		return true
	})
	if found {
		return result, result.name, interfaceDispatch, true
	}
	return typeReference{}, "", false, false
}

func (s traceState) receiverParameterReference(name string) (typeReference, bool, bool) {
	if s.current.decl.Recv != nil {
		for _, field := range s.current.decl.Recv.List {
			for _, candidate := range field.Names {
				if candidate.Name == name {
					reference, ok := typeReferenceForExpr(s.current.file, field.Type)
					return reference, ok && s.index.isInterface(reference), ok
				}
			}
		}
	}
	if s.current.decl.Type.Params == nil {
		return typeReference{}, false, false
	}
	for _, field := range s.current.decl.Type.Params.List {
		for _, candidate := range field.Names {
			if candidate.Name == name {
				reference, ok := typeReferenceForExpr(s.current.file, field.Type)
				return reference, ok && s.index.isInterface(reference), ok
			}
		}
	}
	return typeReference{}, false, false
}

func pointerResultReference(fn *function) (typeReference, bool) {
	if fn.decl.Type.Results == nil || len(fn.decl.Type.Results.List) == 0 {
		return typeReference{}, false
	}
	if _, ok := fn.decl.Type.Results.List[0].Type.(*ast.StarExpr); !ok {
		return typeReference{}, false
	}
	return typeReferenceForExpr(fn.file, fn.decl.Type.Results.List[0].Type)
}

func (i *analysisIndex) isInterface(reference typeReference) bool {
	_, ok := i.interfaces[typeKey(reference.packagePath, reference.name)]
	return ok
}

func (i *analysisIndex) isCapturedPackage(packagePath string) bool {
	for _, file := range i.files {
		if file.packagePath == packagePath {
			return true
		}
	}
	return false
}

func (s traceState) raw(call *ast.CallExpr) (bool, string, string, contracts.SourceReasonCode) {
	request, _, _, proven, interfaceDispatch := s.requestBuilder(call)
	if !proven || interfaceDispatch || request == nil || !directNetHTTPSink(request) {
		return false, "", "", ""
	}
	if len(call.Args) < 2 {
		return true, "", "", contracts.ReasonDynamicPath
	}
	methodIndex, pathIndex := requestArgumentPositions(request)
	if methodIndex < 0 || pathIndex >= len(call.Args) {
		return true, "", "", contracts.ReasonDynamicPath
	}
	method, methodOK := s.methodExpression(call.Args[methodIndex])
	pathTemplate, pathOK := s.expression(call.Args[pathIndex], s.localPathBindings(call.Pos()))
	if !methodOK {
		return true, "", "", contracts.ReasonDynamicMethod
	}
	if !pathOK {
		return true, "", "", contracts.ReasonDynamicPath
	}
	if !hasLiteralPathComponent(pathTemplate) {
		return true, "", "", contracts.ReasonDynamicPath
	}
	return true, method, pathTemplate, ""
}

func (s traceState) requestBuilder(call *ast.CallExpr) (*function, string, string, bool, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "NewRequest" && selector.Sel.Name != "NewRequestDo") {
		return nil, "", "", false, false
	}
	_, importPath, receiverType, proven, interfaceDispatch := s.receiverCall(selector.X)
	request := s.index.functions[functionKey(importPath, "(*"+receiverType+")."+selector.Sel.Name)]
	if !proven || interfaceDispatch || receiverType != "Client" || !validRequestSignature(request) {
		return nil, "", "", false, interfaceDispatch
	}
	return request, importPath, receiverType, true, false
}

func requestMethod(call *ast.CallExpr) string {
	selector, _ := call.Fun.(*ast.SelectorExpr)
	return selector.Sel.Name
}

func directNetHTTPSink(request *function) bool {
	methodIndex, pathIndex := requestArgumentPositions(request)
	if methodIndex < 0 || pathIndex < 0 {
		return false
	}
	methodName := parameterName(request.decl, methodIndex)
	pathName := parameterName(request.decl, pathIndex)
	if methodName == "" || pathName == "" {
		return false
	}
	for _, call := range callsInBlock(request.decl.Body, map[string]bool{}) {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "NewRequest" && selector.Sel.Name != "NewRequestWithContext") {
			continue
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok || request.file.imports[alias.Name] != "net/http" {
			continue
		}
		methodArg, pathArg := 0, 1
		if selector.Sel.Name == "NewRequestWithContext" {
			methodArg, pathArg = 1, 2
		}
		if pathArg >= len(call.Args) || !sameIdentifier(call.Args[methodArg], methodName) || !sameIdentifier(call.Args[pathArg], pathName) {
			continue
		}
		return true
	}
	return false
}

func parameterName(fn *ast.FuncDecl, index int) string {
	position := 0
	if fn.Type.Params == nil {
		return ""
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if position == index {
				return name.Name
			}
			position++
		}
	}
	return ""
}

func sameIdentifier(expr ast.Expr, want string) bool {
	identifier, ok := expr.(*ast.Ident)
	return ok && identifier.Name == want
}

func hasLiteralPathComponent(template string) bool {
	for remainder := template; remainder != ""; {
		open := strings.IndexByte(remainder, '{')
		if open < 0 {
			return strings.Trim(remainder, "/") != ""
		}
		if strings.Trim(remainder[:open], "/") != "" {
			return true
		}
		close := strings.IndexByte(remainder[open:], '}')
		if close < 0 {
			return true
		}
		remainder = remainder[open+close+1:]
	}
	return false
}

func validRequestSignature(fn *function) bool {
	if fn == nil || fn.decl.Type.Params == nil {
		return false
	}
	var types []string
	for _, field := range fn.decl.Type.Params.List {
		for range field.Names {
			types = append(types, typeName(field.Type))
		}
	}
	pairs := 0
	for index := 0; index+1 < len(types); index++ {
		if types[index] == "string" && types[index+1] == "string" && (index == 0 || index == 1) {
			pairs++
		}
	}
	return pairs == 1
}

func requestArgumentPositions(fn *function) (int, int) {
	var types []string
	for _, field := range fn.decl.Type.Params.List {
		for range field.Names {
			types = append(types, typeName(field.Type))
		}
	}
	for index := 0; index+1 < len(types); index++ {
		if types[index] == "string" && types[index+1] == "string" && (index == 0 || index == 1) {
			return index, index + 1
		}
	}
	return -1, -1
}

func (s traceState) methodExpression(expr ast.Expr) (string, bool) {
	if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		value, err := strconv.Unquote(literal.Value)
		return value, err == nil
	}
	if parenthesized, ok := expr.(*ast.ParenExpr); ok {
		return s.methodExpression(parenthesized.X)
	}
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if alias, ok := selector.X.(*ast.Ident); ok && s.current.file.imports[alias.Name] == "net/http" && exactHTTPMethod(selector.Sel.Name) {
			return strings.TrimPrefix(strings.ToUpper(selector.Sel.Name), "METHOD"), true
		}
	}
	return "", false
}

func exactHTTPMethod(value string) bool {
	_, ok := map[string]struct{}{"MethodGet": {}, "MethodHead": {}, "MethodPost": {}, "MethodPut": {}, "MethodPatch": {}, "MethodDelete": {}, "MethodConnect": {}, "MethodOptions": {}, "MethodTrace": {}}[value]
	return ok
}

func (s traceState) expression(expr ast.Expr, locals map[string]string) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			result, err := strconv.Unquote(value.Value)
			return result, err == nil
		}
	case *ast.ParenExpr:
		return s.expression(value.X, locals)
	case *ast.Ident:
		if locals != nil {
			if replacement, ok := locals[value.Name]; ok {
				return replacement, true
			}
		}
		if constant, ok := s.index.constants[s.current.packagePath][value.Name]; ok {
			return constant, true
		}
		if isFunctionParameter(s.current.decl, value.Name) {
			return "{" + value.Name + "}", true
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftOK := s.expression(value.X, locals)
			right, rightOK := s.expression(value.Y, locals)
			return left + right, leftOK && rightOK
		}
	case *ast.CallExpr:
		if sel, ok := value.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && s.current.file.imports[pkg.Name] == "fmt" && sel.Sel.Name == "Sprintf" && len(value.Args) >= 1 {
				format, ok := s.expression(value.Args[0], locals)
				if !ok {
					return "", false
				}
				verbs, ok := formatVerbs(format)
				if !ok || len(verbs) != len(value.Args)-1 {
					return "", false
				}
				var rendered strings.Builder
				argument := 0
				for index := 0; index < len(format); {
					if format[index] != '%' {
						rendered.WriteByte(format[index])
						index++
						continue
					}
					value, ok := s.expression(value.Args[argument+1], locals)
					if !ok {
						return "", false
					}
					rendered.WriteString(value)
					argument++
					index += 2
				}
				return rendered.String(), true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && s.current.file.imports[pkg.Name] == "path" && sel.Sel.Name == "Join" {
				parts := make([]string, 0, len(value.Args))
				for _, arg := range value.Args {
					part, ok := s.expression(arg, locals)
					if !ok {
						return "", false
					}
					parts = append(parts, part)
				}
				return path.Join(parts...), true
			}
		}
	}
	return "", false
}

func formatVerbs(format string) ([]byte, bool) {
	verbs := make([]byte, 0, strings.Count(format, "%"))
	for index := 0; index < len(format); {
		if format[index] != '%' {
			index++
			continue
		}
		if index+1 >= len(format) || (format[index+1] != 's' && format[index+1] != 'v') {
			return nil, false
		}
		verbs = append(verbs, format[index+1])
		index += 2
	}
	return verbs, true
}

// localPathBindings permits one unconditional, function-local assignment
// before the request call. Any duplicate assignment is deliberately dynamic.
func (s traceState) localPathBindings(before token.Pos) map[string]string {
	values := make(map[string]string)
	counts := make(map[string]int)
	for _, node := range s.current.decl.Body.List {
		if node.Pos() >= before {
			break
		}
		var names []*ast.Ident
		var expressions []ast.Expr
		switch value := node.(type) {
		case *ast.AssignStmt:
			if len(value.Lhs) != 1 || len(value.Rhs) != 1 {
				continue
			}
			identifier, ok := value.Lhs[0].(*ast.Ident)
			if !ok {
				continue
			}
			names, expressions = []*ast.Ident{identifier}, []ast.Expr{value.Rhs[0]}
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
			if !ok || declaration.Tok != token.VAR {
				continue
			}
			for _, specification := range declaration.Specs {
				spec, ok := specification.(*ast.ValueSpec)
				if ok && len(spec.Names) == 1 && len(spec.Values) == 1 {
					names = append(names, spec.Names[0])
					expressions = append(expressions, spec.Values[0])
				}
			}
		}
		for index, name := range names {
			counts[name.Name]++
			if counts[name.Name] == 1 {
				if rendered, ok := s.expression(expressions[index], values); ok {
					values[name.Name] = rendered
				}
			}
		}
	}
	for _, node := range s.current.decl.Body.List {
		conditional, ok := node.(*ast.IfStmt)
		if !ok || conditional.Pos() >= before {
			continue
		}
		ast.Inspect(conditional, func(candidate ast.Node) bool {
			assignment, ok := candidate.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, left := range assignment.Lhs {
				if identifier, ok := left.(*ast.Ident); ok {
					if _, tracked := counts[identifier.Name]; tracked {
						counts[identifier.Name]++
					}
				}
			}
			return true
		})
	}
	for name, count := range counts {
		if count != 1 {
			delete(values, name)
		}
	}
	return values
}

func (s traceState) rawSymbol(call *ast.CallExpr) string {
	selector, _ := call.Fun.(*ast.SelectorExpr)
	_, importPath, receiver, _, _ := s.receiverCall(selector.X)
	request := s.index.functions[functionKey(importPath, "(*"+receiver+")."+selector.Sel.Name)]
	packageName := path.Base(importPath)
	if request != nil {
		packageName = path.Base(request.packagePath)
		receiver = request.receiver
	}
	method := "NewRequest"
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		method = selector.Sel.Name
	}
	return "(*" + packageName + "." + receiver + ")." + method
}

func callsInBlock(block *ast.BlockStmt, bools map[string]bool, nestedArguments ...map[*ast.CallExpr]bool) []*ast.CallExpr {
	var calls []*ast.CallExpr
	var nested map[*ast.CallExpr]bool
	if len(nestedArguments) > 0 {
		nested = nestedArguments[0]
	}
	var walkBlock func(*ast.BlockStmt) bool
	var collect func(ast.Node)
	var collectCall func(*ast.CallExpr)
	var collectArgument func(ast.Expr)
	collectCall = func(call *ast.CallExpr) {
		calls = append(calls, call)
		// Arguments are independently evaluated operations; preserve any calls
		// nested there. Deliberately do not walk call.Fun: receiver/factory
		// expressions such as sdk.NewClient().Get() are not independent edges.
		for _, argument := range call.Args {
			collectArgument(argument)
		}
	}
	collectArgument = func(argument ast.Expr) {
		ast.Inspect(argument, func(node ast.Node) bool {
			if node == nil {
				return false
			}
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if nested != nil {
					nested[call] = true
				}
				collectCall(call)
				return false
			}
			return true
		})
	}
	collect = func(node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if call, ok := n.(*ast.CallExpr); ok {
				collectCall(call)
				return false
			}
			return true
		})
	}
	walkBlock = func(current *ast.BlockStmt) bool {
		for _, statement := range current.List {
			switch value := statement.(type) {
			case *ast.ReturnStmt:
				collect(value)
				return true
			case *ast.IfStmt:
				if value.Init != nil {
					collect(value.Init)
				}
				if condition, known := boolLiteral(value.Cond, bools); known {
					if condition {
						if walkBlock(value.Body) {
							return true
						}
					} else if alternative, ok := value.Else.(*ast.BlockStmt); ok && walkBlock(alternative) {
						return true
					}
					continue
				}
				collect(value.Cond)
				walkBlock(value.Body)
				if alternative, ok := value.Else.(*ast.BlockStmt); ok {
					walkBlock(alternative)
				}
			default:
				collect(value)
			}
		}
		return false
	}
	walkBlock(block)
	return calls
}

func stringLiteral(expr ast.Expr) (string, bool) {
	basic, ok := expr.(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(basic.Value)
	return value, err == nil
}
func exprText(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return exprText(value.X) + "." + value.Sel.Name
	}
	return "dynamic"
}

func sortChains(chains []contracts.SourceEvidenceChain) error {
	for _, chain := range chains {
		if _, err := chainKey(chain); err != nil {
			return err
		}
	}
	sort.SliceStable(chains, func(a, b int) bool {
		left, _ := chainKey(chains[a])
		right, _ := chainKey(chains[b])
		return left < right
	})
	return nil
}
func chainKey(chain contracts.SourceEvidenceChain) (string, error) {
	encoded, err := json.Marshal(chain)
	if err != nil {
		return "", err
	}
	value, err := canonjson.Decode(encoded)
	if err != nil {
		return "", err
	}
	return canonjson.Render(value)
}

func isFunctionParameter(fn *ast.FuncDecl, name string) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		for _, candidate := range field.Names {
			if candidate.Name == name {
				return true
			}
		}
	}
	return false
}
