package sourceanalysis

import (
	"go/ast"
	"sort"
)

const maxFieldWriteCallDepth = 32

func (i *analysisIndex) writeInputWitnesses(
	resource resolvedComposite,
	diagnostics *[]FieldWitnessDiagnostic,
) map[string][]WriteInputFieldWitness {
	witnesses := make(map[string][]WriteInputFieldWitness)
	callbacks := i.resourceWriteCallbacks(resource, diagnostics)
	excluded := resourceCallbackKeys(resource, map[string]struct{}{
		"Read": {}, "ReadContext": {}, "ReadWithoutTimeout": {},
	})
	for _, callback := range callbacks {
		i.scanWriteInputs(callback, callback, nil, resourceDataParameters(callback), excluded, map[string]bool{}, 0, witnesses, diagnostics)
	}
	return witnesses
}

func (i *analysisIndex) resourceWriteCallbacks(
	resource resolvedComposite,
	diagnostics *[]FieldWitnessDiagnostic,
) []*function {
	callbackNames := map[string]struct{}{
		"Create":               {},
		"CreateContext":        {},
		"CreateWithoutTimeout": {},
		"Update":               {},
		"UpdateContext":        {},
		"UpdateWithoutTimeout": {},
	}
	callbacksByKey := make(map[string]*function)
	for _, element := range resource.literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if _, selected := callbackNames[name.Name]; !selected {
			continue
		}
		callbackName, ok := field.Value.(*ast.Ident)
		if !ok {
			location := i.loc(resource.owner.file, resource.owner.symbol, field.Value.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{
				Code:     "write_callback_unresolved",
				Message:  name.Name + " callback is not a direct function identifier",
				Location: &location,
			})
			continue
		}
		callback := i.providerFunctions[functionKey(resource.owner.packagePath, callbackName.Name)]
		if callback == nil {
			location := i.loc(resource.owner.file, resource.owner.symbol, field.Value.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{
				Code:     "write_callback_unresolved",
				Message:  name.Name + " callback declaration is not captured",
				Location: &location,
			})
			continue
		}
		callbacksByKey[functionKey(callback.packagePath, callback.symbol)] = callback
	}
	callbacks := make([]*function, 0, len(callbacksByKey))
	for _, callback := range callbacksByKey {
		callbacks = append(callbacks, callback)
	}
	sort.Slice(callbacks, func(left, right int) bool {
		return functionKey(callbacks[left].packagePath, callbacks[left].symbol) <
			functionKey(callbacks[right].packagePath, callbacks[right].symbol)
	})
	return callbacks
}

func (i *analysisIndex) scanWriteInputs(
	root *function,
	current *function,
	bindings map[string]expressionBinding,
	receivers map[string]bool,
	excluded map[string]bool,
	seen map[string]bool,
	depth int,
	witnesses map[string][]WriteInputFieldWitness,
	diagnostics *[]FieldWitnessDiagnostic,
) {
	if depth >= maxFieldWriteCallDepth {
		location := i.loc(current.file, current.symbol, current.decl.Name.Pos())
		*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{
			Code:     "write_call_depth_exceeded",
			Message:  "Create or Update call graph exceeded the field-write analysis depth limit",
			Location: &location,
		})
		return
	}
	key := functionKey(current.packagePath, current.symbol)
	if seen[key] {
		return
	}
	nextSeen := cloneBoolMap(seen)
	nextSeen[key] = true
	ast.Inspect(current.decl.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, selectorOK := call.Fun.(*ast.SelectorExpr); selectorOK {
			if receiver, receiverOK := selector.X.(*ast.Ident); receiverOK && receivers[receiver.Name] && isResourceDataAccessor(selector.Sel.Name) {
				if len(call.Args) == 0 {
					return true
				}
				fieldPath, static := i.boundString(current, call.Args[0], bindings)
				location := i.loc(current.file, current.symbol, call.Pos())
				if !static || fieldPath == "" {
					*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{
						Code:     "write_input_key_dynamic",
						Message:  "ResourceData field accessor key is not statically bound to a non-empty string",
						Location: &location,
					})
				} else {
					witnesses[fieldPath] = append(witnesses[fieldPath], WriteInputFieldWitness{
						Accessor: selector.Sel.Name,
						Callback: root.symbol,
						Location: location,
					})
				}
			}
		}
		helper := i.providerHelperForCall(current.file, call)
		if helper == nil {
			if callUsesResourceDataArgument(call, receivers) {
				i.recordUnresolvedWriteHelper(current, call, bindings, diagnostics)
			}
			return true
		}
		if excluded[functionKey(helper.packagePath, helper.symbol)] {
			return true
		}
		helperReceivers := resourceDataReceiversPassedToHelper(helper, call, receivers)
		if len(helperReceivers) == 0 {
			if callUsesResourceDataArgument(call, receivers) {
				i.recordUnresolvedWriteHelper(current, call, bindings, diagnostics)
			}
			return true
		}
		helperBindings, ok := i.bindWriteCallExpressions(current, helper, call.Args, bindings)
		if !ok {
			return true
		}
		i.scanWriteInputs(root, helper, helperBindings, helperReceivers, excluded, nextSeen, depth+1, witnesses, diagnostics)
		return true
	})
}

func resourceDataReceiversPassedToHelper(
	callee *function,
	call *ast.CallExpr,
	callerReceivers map[string]bool,
) map[string]bool {
	receivers := make(map[string]bool)
	if callee.decl.Type.Params == nil {
		return receivers
	}
	argument := 0
	for _, parameter := range callee.decl.Type.Params.List {
		parameterIsResourceData := isResourceDataType(callee, parameter.Type)
		parameterCount := len(parameter.Names)
		if parameterCount == 0 {
			parameterCount = 1
		}
		for parameterIndex := 0; parameterIndex < parameterCount; parameterIndex++ {
			if argument >= len(call.Args) {
				return receivers
			}
			if parameterIsResourceData && isResourceDataReceiverExpression(call.Args[argument], callerReceivers) {
				if parameterIndex < len(parameter.Names) {
					receivers[parameter.Names[parameterIndex].Name] = true
				}
			}
			argument++
		}
	}
	return receivers
}

func isResourceDataReceiverExpression(expression ast.Expr, receivers map[string]bool) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && receivers[identifier.Name]
}

func callUsesResourceDataArgument(call *ast.CallExpr, receivers map[string]bool) bool {
	for _, argument := range call.Args {
		if isResourceDataReceiverExpression(argument, receivers) {
			return true
		}
	}
	return false
}

func (i *analysisIndex) recordUnresolvedWriteHelper(
	current *function,
	call *ast.CallExpr,
	bindings map[string]expressionBinding,
	diagnostics *[]FieldWitnessDiagnostic,
) {
	location := i.loc(current.file, current.symbol, call.Pos())
	fieldPaths := make([]string, 0, len(call.Args))
	for _, argument := range call.Args {
		if fieldPath, ok := i.boundString(current, argument, bindings); ok && fieldPath != "" {
			fieldPaths = append(fieldPaths, fieldPath)
		}
	}
	fieldPaths = sortedUniqueStrings(fieldPaths)
	if len(fieldPaths) == 0 {
		*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{
			Code:     "write_helper_unresolved",
			Message:  "Create or Update calls an uncaptured helper with ResourceData",
			Location: &location,
		})
		return
	}
	for _, fieldPath := range fieldPaths {
		*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{
			Code:      "write_helper_unresolved",
			FieldPath: fieldPath,
			Message:   fieldPath + ": Create or Update calls an uncaptured helper with ResourceData",
			Location:  &location,
		})
	}
}

func resourceCallbackKeys(resource resolvedComposite, selected map[string]struct{}) map[string]bool {
	keys := make(map[string]bool)
	for _, element := range resource.literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if _, ok := selected[name.Name]; !ok {
			continue
		}
		callback, ok := field.Value.(*ast.Ident)
		if ok {
			keys[functionKey(resource.owner.packagePath, callback.Name)] = true
		}
	}
	return keys
}

func resourceDataParameters(function *function) map[string]bool {
	parameters := make(map[string]bool)
	if function.decl.Type.Params == nil {
		return parameters
	}
	for _, parameter := range function.decl.Type.Params.List {
		if !isResourceDataType(function, parameter.Type) {
			continue
		}
		for _, name := range parameter.Names {
			parameters[name.Name] = true
		}
	}
	return parameters
}

func isResourceDataType(function *function, expression ast.Expr) bool {
	pointer, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ResourceData" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && hashicorpSchemaPackage(function.file.imports[alias.Name])
}

func isResourceDataAccessor(name string) bool {
	switch name {
	case "Get", "GetOk", "GetOkExists":
		return true
	default:
		return false
	}
}

func (i *analysisIndex) bindWriteCallExpressions(
	caller *function,
	callee *function,
	arguments []ast.Expr,
	callerBindings map[string]expressionBinding,
) (map[string]expressionBinding, bool) {
	if callee.decl.Type.Params == nil {
		return map[string]expressionBinding{}, len(arguments) == 0
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
				return nil, false
			}
			argumentOwner, argumentExpression := i.resolveBoundExpression(caller, arguments[argument], callerBindings)
			bindings[name.Name] = expressionBinding{owner: argumentOwner, expression: argumentExpression}
			argument++
		}
	}
	return bindings, argument == len(arguments)
}
