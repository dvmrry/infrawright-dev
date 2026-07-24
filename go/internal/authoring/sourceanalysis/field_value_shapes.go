package sourceanalysis

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

const maxFieldShapeDepth = 16

// FieldValueShapeKind is a bounded, provider-neutral description of values
// passed through Terraform Plugin SDK schema and ResourceData seams.
type FieldValueShapeKind string

const (
	FieldValueShapeUnknown FieldValueShapeKind = "unknown"
	FieldValueShapeBool    FieldValueShapeKind = "bool"
	FieldValueShapeInt     FieldValueShapeKind = "int"
	FieldValueShapeFloat   FieldValueShapeKind = "float"
	FieldValueShapeString  FieldValueShapeKind = "string"
	FieldValueShapeList    FieldValueShapeKind = "list"
	FieldValueShapeSet     FieldValueShapeKind = "set"
	FieldValueShapeMap     FieldValueShapeKind = "map"
	FieldValueShapeObject  FieldValueShapeKind = "object"
)

// FieldValueShape records only statically recovered container and literal-map
// structure. Closed means Fields is the complete recovered object key set.
// Length is present only for fixed-size collection literals.
type FieldValueShape struct {
	Kind     FieldValueShapeKind         `json:"kind"`
	Element  *FieldValueShape            `json:"element,omitempty"`
	Fields   map[string]*FieldValueShape `json:"fields,omitempty"`
	MaxItems *int                        `json:"max_items,omitempty"`
	Length   *int                        `json:"length,omitempty"`
	Closed   bool                        `json:"closed,omitempty"`
}

// ReadBackShapeStatus describes a static Plugin SDK assignment comparison.
// Partial means no contradiction was found in the recovered subset; it is not
// a claim of complete runtime compatibility.
type ReadBackShapeStatus string

const (
	ReadBackShapeConsistent  ReadBackShapeStatus = "consistent"
	ReadBackShapePartial     ReadBackShapeStatus = "partial"
	ReadBackShapeUnresolved  ReadBackShapeStatus = "unresolved"
	ReadBackShapeConflicting ReadBackShapeStatus = "conflicting"
)

// ReadBackShapeAssessment compares a recovered Read value with the selected
// field's provider schema using Terraform Plugin SDK container semantics.
type ReadBackShapeAssessment struct {
	Status   ReadBackShapeStatus `json:"status"`
	Expected *FieldValueShape    `json:"expected,omitempty"`
	Observed *FieldValueShape    `json:"observed,omitempty"`
	Details  []string            `json:"details"`
}

type readBackObservation struct {
	witness       ReadBackFieldWitness
	observedShape *FieldValueShape
	shapeIssue    string
}

type expressionBinding struct {
	owner      *function
	expression ast.Expr
}

func parentFieldPath(prefix string) string {
	return strings.TrimSuffix(prefix, "[]")
}

func cloneExpressionBindings(bindings map[string]expressionBinding) map[string]expressionBinding {
	if bindings == nil {
		return nil
	}
	cloned := make(map[string]expressionBinding, len(bindings))
	for name, expression := range bindings {
		cloned[name] = expression
	}
	return cloned
}

func (i *analysisIndex) resolveBoundExpression(
	owner *function,
	expression ast.Expr,
	bindings map[string]expressionBinding,
) (*function, ast.Expr) {
	for depth := 0; depth < maxFieldShapeDepth; depth++ {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
			continue
		case *ast.Ident:
			if bound, ok := bindings[value.Name]; ok && bound.expression != expression {
				owner = bound.owner
				expression = bound.expression
				bindings = nil
				continue
			}
		}
		return owner, expression
	}
	return owner, expression
}

func (i *analysisIndex) boundString(owner *function, expression ast.Expr, bindings map[string]expressionBinding) (string, bool) {
	owner, expression = i.resolveBoundExpression(owner, expression, bindings)
	if value, ok := stringLiteral(expression); ok {
		return value, true
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return "", false
	}
	if value, ok := owner.file.constants[identifier.Name]; ok {
		return value, true
	}
	value, ok := i.constants[owner.packagePath][identifier.Name]
	return value, ok
}

func (i *analysisIndex) boundBool(owner *function, expression ast.Expr, bindings map[string]expressionBinding) (bool, bool) {
	_, expression = i.resolveBoundExpression(owner, expression, bindings)
	return boolLiteral(expression, nil)
}

func (i *analysisIndex) boundInt(owner *function, expression ast.Expr, bindings map[string]expressionBinding) (int, bool) {
	_, expression = i.resolveBoundExpression(owner, expression, bindings)
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return 0, false
	}
	value, err := strconv.Atoi(literal.Value)
	return value, err == nil
}

func (i *analysisIndex) bindCallExpressions(
	caller *function,
	callee *function,
	arguments []ast.Expr,
	callerBindings map[string]expressionBinding,
) (map[string]expressionBinding, string) {
	if callee.decl.Type.Params == nil {
		if len(arguments) == 0 {
			return map[string]expressionBinding{}, ""
		}
		return nil, "schema helper argument count does not match its declaration"
	}
	bindings := make(map[string]expressionBinding)
	argument := 0
	for _, parameter := range callee.decl.Type.Params.List {
		if len(parameter.Names) == 0 {
			argument++
			continue
		}
		for _, name := range parameter.Names {
			if argument >= len(arguments) {
				return nil, "schema helper argument count does not match its declaration"
			}
			argumentOwner, argumentExpression := i.resolveBoundExpression(caller, arguments[argument], callerBindings)
			bindings[name.Name] = expressionBinding{owner: argumentOwner, expression: argumentExpression}
			argument++
		}
	}
	if argument != len(arguments) {
		return nil, "schema helper argument count does not match its declaration"
	}
	return bindings, ""
}

func localFunctionExpression(function *function, name string, before token.Pos) (ast.Expr, bool) {
	var matches []ast.Expr
	for _, statement := range function.decl.Body.List {
		if statement.Pos() >= before {
			continue
		}
		switch value := statement.(type) {
		case *ast.AssignStmt:
			if len(value.Lhs) != len(value.Rhs) {
				continue
			}
			for index, left := range value.Lhs {
				identifier, ok := left.(*ast.Ident)
				if ok && identifier.Name == name {
					matches = append(matches, value.Rhs[index])
				}
			}
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
			if !ok || (declaration.Tok != token.VAR && declaration.Tok != token.CONST) {
				continue
			}
			for _, specification := range declaration.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok || len(values.Names) != len(values.Values) {
					continue
				}
				for index, candidate := range values.Names {
					if candidate.Name == name {
						matches = append(matches, values.Values[index])
					}
				}
			}
		}
	}
	if len(matches) != 1 {
		return nil, false
	}
	return matches[0], true
}

func (i *analysisIndex) providerValueShape(resolved resolvedComposite, depth int) (*FieldValueShape, string) {
	if depth >= maxFieldShapeDepth {
		return nil, "provider schema shape depth limit exceeded"
	}
	kind := schemaShapeKind(providerSchemaType(resolved.literal))
	shape := &FieldValueShape{Kind: kind}
	if maxExpression, ok := compositeField(resolved.literal, "MaxItems"); ok {
		if maximum, known := i.boundInt(resolved.owner, maxExpression, resolved.bindings); known {
			shape.MaxItems = intPointer(maximum)
		}
	}
	switch kind {
	case FieldValueShapeList, FieldValueShapeSet, FieldValueShapeMap:
		elementExpression, present := compositeField(resolved.literal, "Elem")
		if !present {
			shape.Element = &FieldValueShape{Kind: FieldValueShapeUnknown}
			return shape, "schema container element is absent"
		}
		element, issue := i.providerElementShape(resolved, elementExpression, depth+1)
		shape.Element = element
		return shape, issue
	case FieldValueShapeUnknown:
		return shape, "provider schema type is not statically recognized"
	default:
		return shape, ""
	}
}

func (i *analysisIndex) providerElementShape(
	parent resolvedComposite,
	expression ast.Expr,
	depth int,
) (*FieldValueShape, string) {
	boundOwner, boundExpression := i.resolveBoundExpression(parent.owner, expression, parent.bindings)
	if boundOwner != parent.owner {
		parent.owner = boundOwner
		parent.bindings = nil
	}
	expression = boundExpression
	if literal := resourceLiteral(expression); literal != nil && isHashicorpSchemaComposite(parent.owner.file, literal, "Resource") {
		return i.providerResourceShape(resolvedComposite{owner: parent.owner, literal: literal, bindings: parent.bindings}, depth)
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		if alias, aliasOK := selector.X.(*ast.Ident); aliasOK && hashicorpSchemaPackage(parent.owner.file.imports[alias.Name]) {
			return &FieldValueShape{Kind: schemaShapeKind(goExpression(expression))}, ""
		}
	}
	resolved, issue := i.resolveSchemaLiteralBound(parent.owner, expression, parent.bindings, map[string]bool{}, depth)
	if issue != "" {
		return &FieldValueShape{Kind: FieldValueShapeUnknown}, issue
	}
	return i.providerValueShape(resolved, depth+1)
}

func (i *analysisIndex) providerResourceShape(resource resolvedComposite, depth int) (*FieldValueShape, string) {
	if depth >= maxFieldShapeDepth {
		return nil, "provider resource shape depth limit exceeded"
	}
	shape := &FieldValueShape{Kind: FieldValueShapeObject, Fields: map[string]*FieldValueShape{}, Closed: true}
	schemaExpression, present := compositeField(resource.literal, "Schema")
	if !present {
		shape.Closed = false
		return shape, "nested provider resource has no direct Schema field"
	}
	schemaOwner, boundSchemaExpression := i.resolveBoundExpression(resource.owner, schemaExpression, resource.bindings)
	if schemaOwner != resource.owner {
		resource.owner = schemaOwner
		resource.bindings = nil
	}
	schemaMap := resourceLiteral(boundSchemaExpression)
	if schemaMap == nil {
		shape.Closed = false
		return shape, "nested provider resource schema map is not a direct literal"
	}
	var issues []string
	for _, element := range schemaMap.Elts {
		entry, ok := element.(*ast.KeyValueExpr)
		if !ok {
			shape.Closed = false
			continue
		}
		name, known := i.boundString(resource.owner, entry.Key, resource.bindings)
		if !known || name == "" {
			shape.Closed = false
			issues = append(issues, "nested provider resource contains an unresolved field name")
			continue
		}
		resolved, issue := i.resolveSchemaLiteralBound(resource.owner, entry.Value, resource.bindings, map[string]bool{}, depth+1)
		if issue != "" {
			shape.Fields[name] = &FieldValueShape{Kind: FieldValueShapeUnknown}
			issues = append(issues, name+": "+issue)
			continue
		}
		fieldShape, issue := i.providerValueShape(resolved, depth+1)
		if fieldShape == nil {
			fieldShape = &FieldValueShape{Kind: FieldValueShapeUnknown}
		}
		shape.Fields[name] = fieldShape
		if issue != "" {
			issues = append(issues, name+": "+issue)
		}
	}
	return shape, strings.Join(sortedUniqueStrings(issues), "; ")
}

func schemaShapeKind(typeName string) FieldValueShapeKind {
	switch {
	case strings.HasSuffix(typeName, ".TypeBool"):
		return FieldValueShapeBool
	case strings.HasSuffix(typeName, ".TypeInt"):
		return FieldValueShapeInt
	case strings.HasSuffix(typeName, ".TypeFloat"):
		return FieldValueShapeFloat
	case strings.HasSuffix(typeName, ".TypeString"):
		return FieldValueShapeString
	case strings.HasSuffix(typeName, ".TypeList"):
		return FieldValueShapeList
	case strings.HasSuffix(typeName, ".TypeSet"):
		return FieldValueShapeSet
	case strings.HasSuffix(typeName, ".TypeMap"):
		return FieldValueShapeMap
	default:
		return FieldValueShapeUnknown
	}
}

func intPointer(value int) *int {
	copy := value
	return &copy
}

func (i *analysisIndex) inferReadBackShape(callback *function, expression ast.Expr) (*FieldValueShape, string) {
	shape, attempted, issue := i.inferShapeExpression(callback, expression, map[string]*FieldValueShape{}, map[string]bool{}, 0)
	if !attempted {
		if _, isCall := expression.(*ast.CallExpr); isCall {
			return nil, "read-back call shape is not statically recognized"
		}
		return nil, ""
	}
	return shape, issue
}

func (i *analysisIndex) inferFunctionReturnShape(
	function *function,
	seen map[string]bool,
	depth int,
) (*FieldValueShape, string) {
	if depth >= maxFieldShapeDepth {
		return nil, "read-back helper shape depth limit exceeded"
	}
	key := functionKey(function.packagePath, function.symbol)
	if seen[key] {
		return nil, "recursive read-back helper shape"
	}
	nextSeen := cloneBoolMap(seen)
	nextSeen[key] = true
	environment := make(map[string]*FieldValueShape)
	if function.decl.Type.Params != nil {
		for _, parameter := range function.decl.Type.Params.List {
			shape := shapeFromGoType(parameter.Type)
			for _, name := range parameter.Names {
				environment[name.Name] = cloneFieldValueShape(shape)
			}
		}
	}
	var issues []string
	returns := i.analyzeShapeBlock(function, function.decl.Body, environment, nextSeen, depth+1, &issues)
	var merged *FieldValueShape
	for _, returned := range returns {
		merged = mergeFieldValueShapes(merged, returned)
	}
	if merged == nil || merged.Kind == FieldValueShapeUnknown {
		return nil, "read-back helper return shape is not statically recoverable"
	}
	return merged, strings.Join(sortedUniqueStrings(issues), "; ")
}

func (i *analysisIndex) analyzeShapeBlock(
	function *function,
	block *ast.BlockStmt,
	environment map[string]*FieldValueShape,
	seen map[string]bool,
	depth int,
	issues *[]string,
) []*FieldValueShape {
	if depth >= maxFieldShapeDepth {
		*issues = append(*issues, "read-back helper statement nesting depth limit exceeded")
		return nil
	}
	var returns []*FieldValueShape
	for _, statement := range block.List {
		switch value := statement.(type) {
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range declaration.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range values.Names {
					shape := shapeFromGoType(values.Type)
					if index < len(values.Values) {
						shape, _, _ = i.inferShapeExpression(function, values.Values[index], environment, seen, depth)
					}
					environment[name.Name] = cloneFieldValueShape(shape)
				}
			}
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				if index >= len(value.Rhs) {
					continue
				}
				shape, _, _ := i.inferShapeExpression(function, value.Rhs[index], environment, seen, depth)
				switch target := left.(type) {
				case *ast.Ident:
					environment[target.Name] = cloneFieldValueShape(shape)
				case *ast.IndexExpr:
					identifier, ok := target.X.(*ast.Ident)
					if !ok {
						continue
					}
					container := environment[identifier.Name]
					if container == nil {
						container = &FieldValueShape{Kind: FieldValueShapeList}
						environment[identifier.Name] = container
					}
					if container.Kind == FieldValueShapeObject || container.Kind == FieldValueShapeMap {
						if fieldName, known := i.boundString(function, target.Index, nil); known {
							if container.Fields == nil {
								container.Fields = make(map[string]*FieldValueShape)
							}
							container.Fields[fieldName] = mergeFieldValueShapes(container.Fields[fieldName], shape)
							continue
						}
						container.Closed = false
					}
					container.Element = mergeFieldValueShapes(container.Element, shape)
				}
			}
		case *ast.ReturnStmt:
			if len(value.Results) == 1 {
				shape, _, _ := i.inferShapeExpression(function, value.Results[0], environment, seen, depth)
				returns = append(returns, shape)
			}
		case *ast.IfStmt:
			if value.Init != nil {
				returns = append(returns, i.analyzeShapeStatement(function, value.Init, environment, seen, depth+1, issues)...)
			}
			returns = append(returns, i.analyzeShapeBlock(function, value.Body, environment, seen, depth+1, issues)...)
			switch alternative := value.Else.(type) {
			case *ast.BlockStmt:
				returns = append(returns, i.analyzeShapeBlock(function, alternative, environment, seen, depth+1, issues)...)
			case *ast.IfStmt:
				returns = append(returns, i.analyzeShapeStatement(function, alternative, environment, seen, depth+1, issues)...)
			}
		case *ast.RangeStmt:
			if identifier, ok := value.Key.(*ast.Ident); ok {
				environment[identifier.Name] = &FieldValueShape{Kind: FieldValueShapeInt}
			}
			if identifier, ok := value.Value.(*ast.Ident); ok {
				collection, _, _ := i.inferShapeExpression(function, value.X, environment, seen, depth)
				if collection != nil {
					environment[identifier.Name] = cloneFieldValueShape(collection.Element)
				}
			}
			returns = append(returns, i.analyzeShapeBlock(function, value.Body, environment, seen, depth+1, issues)...)
		case *ast.ForStmt:
			if value.Init != nil {
				returns = append(returns, i.analyzeShapeStatement(function, value.Init, environment, seen, depth+1, issues)...)
			}
			returns = append(returns, i.analyzeShapeBlock(function, value.Body, environment, seen, depth+1, issues)...)
		case *ast.BlockStmt:
			returns = append(returns, i.analyzeShapeBlock(function, value, environment, seen, depth+1, issues)...)
		case *ast.EmptyStmt, *ast.IncDecStmt:
			// These statements do not introduce a new value shape.
		default:
			*issues = append(*issues, fmt.Sprintf("read-back helper statement %T is not shape-analyzed", statement))
		}
	}
	return returns
}

func (i *analysisIndex) analyzeShapeStatement(
	function *function,
	statement ast.Stmt,
	environment map[string]*FieldValueShape,
	seen map[string]bool,
	depth int,
	issues *[]string,
) []*FieldValueShape {
	return i.analyzeShapeBlock(function, &ast.BlockStmt{List: []ast.Stmt{statement}}, environment, seen, depth, issues)
}

func (i *analysisIndex) inferShapeExpression(
	function *function,
	expression ast.Expr,
	environment map[string]*FieldValueShape,
	seen map[string]bool,
	depth int,
) (*FieldValueShape, bool, string) {
	if depth >= maxFieldShapeDepth {
		return nil, true, "read-back expression shape depth limit exceeded"
	}
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return i.inferShapeExpression(function, value.X, environment, seen, depth+1)
	case *ast.UnaryExpr:
		return i.inferShapeExpression(function, value.X, environment, seen, depth+1)
	case *ast.BasicLit:
		switch value.Kind {
		case token.STRING:
			return &FieldValueShape{Kind: FieldValueShapeString}, true, ""
		case token.INT:
			return &FieldValueShape{Kind: FieldValueShapeInt}, true, ""
		case token.FLOAT:
			return &FieldValueShape{Kind: FieldValueShapeFloat}, true, ""
		default:
			return &FieldValueShape{Kind: FieldValueShapeUnknown}, true, ""
		}
	case *ast.Ident:
		if shape := environment[value.Name]; shape != nil {
			return cloneFieldValueShape(shape), true, ""
		}
		if value.Name == "true" || value.Name == "false" {
			return &FieldValueShape{Kind: FieldValueShapeBool}, true, ""
		}
		return &FieldValueShape{Kind: FieldValueShapeUnknown}, false, ""
	case *ast.CompositeLit:
		return i.inferCompositeShape(function, value, environment, seen, depth+1), true, ""
	case *ast.CallExpr:
		if identifier, ok := value.Fun.(*ast.Ident); ok {
			switch identifier.Name {
			case "make":
				if len(value.Args) == 0 {
					return nil, true, "make call has no type argument"
				}
				return shapeFromGoType(value.Args[0]), true, ""
			case "append":
				if len(value.Args) < 2 {
					return nil, true, "append call has fewer than two arguments"
				}
				collection, _, _ := i.inferShapeExpression(function, value.Args[0], environment, seen, depth+1)
				if collection == nil {
					collection = &FieldValueShape{Kind: FieldValueShapeList}
				}
				for _, argument := range value.Args[1:] {
					element, _, _ := i.inferShapeExpression(function, argument, environment, seen, depth+1)
					if value.Ellipsis.IsValid() && element != nil && isCollectionShape(element.Kind) {
						element = element.Element
					}
					collection.Element = mergeFieldValueShapes(collection.Element, element)
				}
				return collection, true, ""
			case "bool":
				return &FieldValueShape{Kind: FieldValueShapeBool}, true, ""
			case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
				return &FieldValueShape{Kind: FieldValueShapeInt}, true, ""
			case "float32", "float64":
				return &FieldValueShape{Kind: FieldValueShapeFloat}, true, ""
			case "string":
				return &FieldValueShape{Kind: FieldValueShapeString}, true, ""
			}
		}
		helper := i.providerHelperForCall(function.file, value)
		if helper == nil {
			if _, local := value.Fun.(*ast.Ident); local {
				return nil, true, "read-back helper declaration is not captured"
			}
			return nil, false, ""
		}
		shape, issue := i.inferFunctionReturnShape(helper, seen, depth+1)
		return shape, true, issue
	default:
		return &FieldValueShape{Kind: FieldValueShapeUnknown}, false, ""
	}
}

func (i *analysisIndex) inferCompositeShape(
	function *function,
	literal *ast.CompositeLit,
	environment map[string]*FieldValueShape,
	seen map[string]bool,
	depth int,
) *FieldValueShape {
	switch literal.Type.(type) {
	case *ast.MapType:
		shape := &FieldValueShape{Kind: FieldValueShapeObject, Fields: map[string]*FieldValueShape{}, Closed: true}
		for _, element := range literal.Elts {
			entry, ok := element.(*ast.KeyValueExpr)
			if !ok {
				shape.Closed = false
				continue
			}
			name, ok := i.boundString(function, entry.Key, nil)
			if !ok {
				shape.Closed = false
				continue
			}
			fieldShape, _, _ := i.inferShapeExpression(function, entry.Value, environment, seen, depth+1)
			if fieldShape == nil {
				fieldShape = &FieldValueShape{Kind: FieldValueShapeUnknown}
			}
			shape.Fields[name] = fieldShape
		}
		return shape
	case *ast.ArrayType:
		shape := &FieldValueShape{Kind: FieldValueShapeList, Length: intPointer(len(literal.Elts))}
		for _, element := range literal.Elts {
			elementShape, _, _ := i.inferShapeExpression(function, element, environment, seen, depth+1)
			shape.Element = mergeFieldValueShapes(shape.Element, elementShape)
		}
		return shape
	default:
		return &FieldValueShape{Kind: FieldValueShapeUnknown}
	}
}

func shapeFromGoType(expression ast.Expr) *FieldValueShape {
	if expression == nil {
		return &FieldValueShape{Kind: FieldValueShapeUnknown}
	}
	switch value := expression.(type) {
	case *ast.ArrayType:
		return &FieldValueShape{Kind: FieldValueShapeList, Element: shapeFromGoType(value.Elt)}
	case *ast.MapType:
		return &FieldValueShape{Kind: FieldValueShapeMap, Element: shapeFromGoType(value.Value)}
	case *ast.InterfaceType:
		return &FieldValueShape{Kind: FieldValueShapeUnknown}
	case *ast.Ident:
		switch value.Name {
		case "bool":
			return &FieldValueShape{Kind: FieldValueShapeBool}
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			return &FieldValueShape{Kind: FieldValueShapeInt}
		case "float32", "float64":
			return &FieldValueShape{Kind: FieldValueShapeFloat}
		case "string":
			return &FieldValueShape{Kind: FieldValueShapeString}
		default:
			return &FieldValueShape{Kind: FieldValueShapeUnknown}
		}
	default:
		return &FieldValueShape{Kind: FieldValueShapeUnknown}
	}
}

func mergeFieldValueShapes(left, right *FieldValueShape) *FieldValueShape {
	if left == nil || left.Kind == FieldValueShapeUnknown {
		return cloneFieldValueShape(right)
	}
	if right == nil || right.Kind == FieldValueShapeUnknown {
		return cloneFieldValueShape(left)
	}
	if left.Kind != right.Kind {
		if isCollectionShape(left.Kind) && isCollectionShape(right.Kind) {
			merged := cloneFieldValueShape(left)
			merged.Element = mergeFieldValueShapes(left.Element, right.Element)
			merged.Length = nil
			return merged
		}
		return &FieldValueShape{Kind: FieldValueShapeUnknown}
	}
	merged := cloneFieldValueShape(left)
	switch left.Kind {
	case FieldValueShapeList, FieldValueShapeSet, FieldValueShapeMap:
		merged.Element = mergeFieldValueShapes(left.Element, right.Element)
		if left.Length == nil || right.Length == nil || *left.Length != *right.Length {
			merged.Length = nil
		}
		if left.Kind == FieldValueShapeMap {
			if merged.Fields == nil {
				merged.Fields = map[string]*FieldValueShape{}
			}
			for name, shape := range right.Fields {
				merged.Fields[name] = mergeFieldValueShapes(merged.Fields[name], shape)
			}
			merged.Closed = left.Closed && right.Closed && sameStringKeys(left.Fields, right.Fields)
		}
	case FieldValueShapeObject:
		if merged.Fields == nil {
			merged.Fields = map[string]*FieldValueShape{}
		}
		for name, shape := range right.Fields {
			merged.Fields[name] = mergeFieldValueShapes(merged.Fields[name], shape)
		}
		merged.Closed = left.Closed && right.Closed && sameStringKeys(left.Fields, right.Fields)
	}
	return merged
}

func cloneFieldValueShape(shape *FieldValueShape) *FieldValueShape {
	if shape == nil {
		return nil
	}
	cloned := *shape
	cloned.Element = cloneFieldValueShape(shape.Element)
	cloned.MaxItems = cloneIntPointer(shape.MaxItems)
	cloned.Length = cloneIntPointer(shape.Length)
	if shape.Fields != nil {
		cloned.Fields = make(map[string]*FieldValueShape, len(shape.Fields))
		for name, field := range shape.Fields {
			cloned.Fields[name] = cloneFieldValueShape(field)
		}
	}
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	return intPointer(*value)
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(values)+1)
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func sameStringKeys(left, right map[string]*FieldValueShape) bool {
	if len(left) != len(right) {
		return false
	}
	for name := range left {
		if _, ok := right[name]; !ok {
			return false
		}
	}
	return true
}

func isCollectionShape(kind FieldValueShapeKind) bool {
	return kind == FieldValueShapeList || kind == FieldValueShapeSet
}

func assessReadBackShape(
	fieldPath string,
	providerSchemas []ProviderSchemaFieldWitness,
	observation readBackObservation,
) (*ReadBackShapeAssessment, []string) {
	if observation.shapeIssue != "" {
		return &ReadBackShapeAssessment{
			Status:   ReadBackShapeUnresolved,
			Observed: cloneFieldValueShape(observation.observedShape),
			Details:  []string{observation.shapeIssue},
		}, nil
	}
	if observation.observedShape == nil {
		return nil, nil
	}
	var expected *FieldValueShape
	for _, provider := range providerSchemas {
		if provider.ShapeIssue != "" {
			return &ReadBackShapeAssessment{
				Status:   ReadBackShapeUnresolved,
				Observed: cloneFieldValueShape(observation.observedShape),
				Details:  []string{provider.ShapeIssue},
			}, nil
		}
		if provider.ValueShape == nil {
			continue
		}
		if expected != nil {
			return &ReadBackShapeAssessment{
				Status:   ReadBackShapeUnresolved,
				Observed: cloneFieldValueShape(observation.observedShape),
				Details:  []string{"multiple provider schema shapes were recovered"},
			}, nil
		}
		expected = provider.ValueShape
	}
	if expected == nil {
		return &ReadBackShapeAssessment{
			Status:   ReadBackShapeUnresolved,
			Observed: cloneFieldValueShape(observation.observedShape),
			Details:  []string{"provider schema shape is unavailable"},
		}, nil
	}
	conflicts, partial := compareFieldValueShapes(fieldPath, expected, observation.observedShape)
	status := ReadBackShapeConsistent
	if len(conflicts) != 0 {
		status = ReadBackShapeConflicting
	} else if partial {
		status = ReadBackShapePartial
	}
	return &ReadBackShapeAssessment{
		Status:   status,
		Expected: cloneFieldValueShape(expected),
		Observed: cloneFieldValueShape(observation.observedShape),
		Details:  append([]string(nil), conflicts...),
	}, conflicts
}

func compareFieldValueShapes(fieldPath string, expected, observed *FieldValueShape) ([]string, bool) {
	if expected == nil || observed == nil || expected.Kind == FieldValueShapeUnknown || observed.Kind == FieldValueShapeUnknown {
		return nil, true
	}
	if isCollectionShape(expected.Kind) && isCollectionShape(observed.Kind) {
		elementConflicts, partial := compareFieldValueShapes(fieldPath+"[]", expected.Element, observed.Element)
		return elementConflicts, partial
	}
	if expected.Kind == FieldValueShapeMap && observed.Kind == FieldValueShapeObject {
		partial := !observed.Closed
		var conflicts []string
		for _, name := range sortedShapeFieldNames(observed.Fields) {
			fieldConflicts, fieldPartial := compareFieldValueShapes(fieldPath+"{}", expected.Element, observed.Fields[name])
			conflicts = append(conflicts, fieldConflicts...)
			partial = partial || fieldPartial
		}
		return conflicts, partial
	}
	if expected.Kind == FieldValueShapeObject && observed.Kind == FieldValueShapeMap {
		conflicts, _ := compareObjectFields(fieldPath, expected, observed)
		return conflicts, true
	}
	if expected.Kind != observed.Kind {
		if isScalarShape(expected.Kind) && isScalarShape(observed.Kind) {
			// Plugin SDK primitive writers use mapstructure coercion. Static Go
			// primitive differences are therefore not a definite Set failure.
			return nil, true
		}
		return []string{fmt.Sprintf("%s read-back shape is %s but provider schema expects %s", fieldPath, observed.Kind, expected.Kind)}, false
	}
	switch expected.Kind {
	case FieldValueShapeObject:
		conflicts, partial := compareObjectFields(fieldPath, expected, observed)
		return conflicts, partial
	case FieldValueShapeMap:
		return compareFieldValueShapes(fieldPath+"{}", expected.Element, observed.Element)
	default:
		return nil, false
	}
}

func compareObjectFields(fieldPath string, expected, observed *FieldValueShape) ([]string, bool) {
	var conflicts []string
	partial := !expected.Closed || !observed.Closed
	if expected.Closed {
		// Plugin SDK MapFieldWriter.setObject writes every emitted map key
		// through the nested schema address. A recovered extra key can therefore
		// fail as an invalid address when that static return path is exercised.
		for _, name := range sortedShapeFieldNames(observed.Fields) {
			if _, ok := expected.Fields[name]; !ok {
				conflicts = append(conflicts, fmt.Sprintf("%s read-back shape can emit undeclared object field %q", fieldPath, name))
			}
		}
	}
	for _, name := range sortedShapeFieldNames(observed.Fields) {
		expectedField, ok := expected.Fields[name]
		if !ok {
			continue
		}
		fieldConflicts, fieldPartial := compareFieldValueShapes(joinFieldPath(fieldPath, name), expectedField, observed.Fields[name])
		conflicts = append(conflicts, fieldConflicts...)
		partial = partial || fieldPartial
	}
	return conflicts, partial
}

func sortedShapeFieldNames(fields map[string]*FieldValueShape) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isScalarShape(kind FieldValueShapeKind) bool {
	switch kind {
	case FieldValueShapeBool, FieldValueShapeInt, FieldValueShapeFloat, FieldValueShapeString:
		return true
	default:
		return false
	}
}

func assessFieldWitness(witness FieldWitness) FieldWitnessAssessment {
	assessment := FieldWitnessAssessment{
		Declaration: FieldDeclarationAbsent,
		Read:        FieldReadAbsent,
		Write:       FieldWriteAbsent,
		Acceptance:  FieldAcceptanceSilent,
	}
	hasTerraform := witness.TerraformSchema != nil
	hasProvider := len(witness.ProviderSchemas) != 0
	switch {
	case hasTerraform && hasProvider && fieldHasSchemaConflict(witness):
		assessment.Declaration = FieldDeclarationConflicting
	case hasTerraform && hasProvider:
		assessment.Declaration = FieldDeclarationConsistent
	case hasTerraform || hasProvider:
		assessment.Declaration = FieldDeclarationObserved
	}
	if len(witness.ReadBacks) != 0 {
		assessment.Read = FieldReadObserved
		for _, readBack := range witness.ReadBacks {
			if readBack.ShapeAssessment == nil {
				continue
			}
			switch readBack.ShapeAssessment.Status {
			case ReadBackShapeConflicting:
				assessment.Read = FieldReadShapeConflicting
			case ReadBackShapeUnresolved:
				if assessment.Read != FieldReadShapeConflicting {
					assessment.Read = FieldReadShapeUnresolved
				}
			case ReadBackShapePartial:
				if assessment.Read != FieldReadShapeConflicting && assessment.Read != FieldReadShapeUnresolved {
					assessment.Read = FieldReadShapePartial
				}
			case ReadBackShapeConsistent:
				if assessment.Read == FieldReadObserved {
					assessment.Read = FieldReadShapeConsistent
				}
			}
		}
	}
	if len(witness.WriteInputs) != 0 {
		assessment.Write = FieldWriteObserved
	}
	hasConfig := len(witness.AcceptanceConfigs) != 0
	hasCheck := len(witness.AcceptanceChecks) != 0
	switch {
	case hasConfig && hasCheck:
		assessment.Acceptance = FieldAcceptanceConfiguredAndAsserted
	case hasConfig:
		assessment.Acceptance = FieldAcceptanceConfigured
	case hasCheck:
		assessment.Acceptance = FieldAcceptanceAsserted
	}
	return assessment
}

func fieldEvidenceFamilyCount(witness FieldWitness) int {
	count := 0
	if witness.Assessment.Declaration != FieldDeclarationAbsent {
		count++
	}
	switch witness.Assessment.Read {
	case FieldReadObserved, FieldReadShapeConsistent, FieldReadShapePartial, FieldReadShapeUnresolved:
		// A literal-key d.Set is still an independent Read witness when its
		// value shape cannot be resolved. The unresolved assessment and its
		// diagnostic remain visible; only a proven shape conflict defeats the
		// aggregate disposition.
		count++
	}
	if witness.Assessment.Write == FieldWriteObserved {
		count++
	}
	if witness.Assessment.Acceptance != FieldAcceptanceSilent {
		count++
	}
	return count
}

func fieldHasSchemaConflict(witness FieldWitness) bool {
	if witness.TerraformSchema == nil {
		return false
	}
	for _, provider := range witness.ProviderSchemas {
		if fieldFlagDisagrees(witness.TerraformSchema.Required, provider.Required) ||
			fieldFlagDisagrees(witness.TerraformSchema.Optional, provider.Optional) ||
			fieldFlagDisagrees(witness.TerraformSchema.Computed, provider.Computed) ||
			fieldDeclarationTypeDisagrees(*witness.TerraformSchema, provider) {
			return true
		}
	}
	return false
}
