package ferrouswheel

import (
	"fmt"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// ResolveAt resolves the type of target using lexical bindings visible at its position.
func (e *TypeEnv) ResolveAt(root, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (Type, error) {
	if target == nil {
		return nil, fmt.Errorf("nil node")
	}
	scoped := e.Clone()
	if scoped == nil {
		scoped = NewTypeEnv()
	}
	if root == nil {
		return scoped.Resolve(resolveTargetNode(target, lang), lang, src)
	}
	if found, typ, err := resolveAtNode(scoped, root, target, lang, src); found {
		return typ, err
	}
	return scoped.Resolve(resolveTargetNode(target, lang), lang, src)
}

func resolveAtNode(env *TypeEnv, node, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (bool, Type, error) {
	if node == nil {
		return false, nil, nil
	}
	if node == resolveTargetNode(target, lang) {
		typ, err := env.Resolve(node, lang, src)
		return true, typ, err
	}

	switch node.Type(lang) {
	case "source_file", "statement_list":
		return resolveSequence(env, node, target, lang, src)
	case "block":
		env.PushScope()
		defer env.PopScope()
		return resolveSequence(env, node, target, lang, src)
	case "function_declaration", "method_declaration", "func_literal", "lambda_expression":
		env.PushScope()
		defer env.PopScope()
		registerParameterList(env, node.ChildByFieldName("receiver", lang), lang, src)
		registerParameterList(env, node.ChildByFieldName("parameters", lang), lang, src)
		registerParameterList(env, namedResultList(node.ChildByFieldName("result", lang), lang), lang, src)
		for i := 0; i < int(node.NamedChildCount()); i++ {
			found, typ, err := resolveAtNode(env, node.NamedChild(i), target, lang, src)
			if found || err != nil {
				return found, typ, err
			}
		}
		return false, nil, nil
	case "if_let_statement":
		return resolveIfLetAt(env, node, target, lang, src)
	case "for_in_statement":
		return resolveForInAt(env, node, target, lang, src)
	case "for_in_index_statement":
		return resolveForInIndexAt(env, node, target, lang, src)
	case "list_comprehension":
		return resolveListComprehensionAt(env, node, target, lang, src)
	default:
		for i := 0; i < int(node.NamedChildCount()); i++ {
			found, typ, err := resolveAtNode(env, node.NamedChild(i), target, lang, src)
			if found || err != nil {
				return found, typ, err
			}
		}
		return false, nil, nil
	}
}

func resolveSequence(env *TypeEnv, node, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (bool, Type, error) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		found, typ, err := resolveAtNode(env, child, target, lang, src)
		if found || err != nil {
			return found, typ, err
		}
		registerSequentialBindings(env, child, lang, src)
	}
	return false, nil, nil
}

func resolveIfLetAt(env *TypeEnv, node, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (bool, Type, error) {
	pattern := node.ChildByFieldName("pattern", lang)
	value := node.ChildByFieldName("value", lang)
	var blocks []*gotreesitter.Node
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child != nil && child.Type(lang) == "block" {
			blocks = append(blocks, child)
		}
	}

	if pattern != nil && pattern == resolveTargetNode(target, lang) {
		registerIfLetBinding(env, pattern, value, lang, src)
		typ, err := env.Resolve(pattern, lang, src)
		return true, typ, err
	}
	if containsNode(value, target) {
		return resolveAtNode(env, value, target, lang, src)
	}
	if len(blocks) > 0 && containsNode(blocks[0], target) {
		env.PushScope()
		defer env.PopScope()
		registerIfLetBinding(env, pattern, value, lang, src)
		return resolveAtNode(env, blocks[0], target, lang, src)
	}
	if len(blocks) > 1 && containsNode(blocks[1], target) {
		return resolveAtNode(env, blocks[1], target, lang, src)
	}
	return false, nil, nil
}

func resolveForInAt(env *TypeEnv, node, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (bool, Type, error) {
	varNode := node.ChildByFieldName("var", lang)
	iterable := node.ChildByFieldName("iterable", lang)
	body := findNodeType(node, lang, "block")
	if containsNode(iterable, target) {
		return resolveAtNode(env, iterable, target, lang, src)
	}
	if body != nil && (containsNode(body, target) || varNode == resolveTargetNode(target, lang)) {
		env.PushScope()
		defer env.PopScope()
		registerRangeBindings(env, "", varNode, iterable, lang, src)
		if varNode == resolveTargetNode(target, lang) {
			typ, err := env.Resolve(varNode, lang, src)
			return true, typ, err
		}
		return resolveAtNode(env, body, target, lang, src)
	}
	return false, nil, nil
}

func resolveForInIndexAt(env *TypeEnv, node, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (bool, Type, error) {
	indexNode := node.ChildByFieldName("index", lang)
	varNode := node.ChildByFieldName("var", lang)
	iterable := node.ChildByFieldName("iterable", lang)
	body := findNodeType(node, lang, "block")
	if containsNode(iterable, target) {
		return resolveAtNode(env, iterable, target, lang, src)
	}
	if body != nil && (containsNode(body, target) || indexNode == resolveTargetNode(target, lang) || varNode == resolveTargetNode(target, lang)) {
		env.PushScope()
		defer env.PopScope()
		registerRangeBindings(env, nodeText(src, indexNode), varNode, iterable, lang, src)
		if indexNode == resolveTargetNode(target, lang) || varNode == resolveTargetNode(target, lang) {
			typ, err := env.Resolve(resolveTargetNode(target, lang), lang, src)
			return true, typ, err
		}
		return resolveAtNode(env, body, target, lang, src)
	}
	return false, nil, nil
}

func resolveListComprehensionAt(env *TypeEnv, node, target *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (bool, Type, error) {
	iterable := node.ChildByFieldName("iterable", lang)
	varNode := node.ChildByFieldName("var", lang)
	expr := node.ChildByFieldName("expr", lang)
	if expr == nil {
		expr = node.ChildByFieldName("expression", lang)
	}
	filter := node.ChildByFieldName("filter", lang)
	if containsNode(iterable, target) {
		return resolveAtNode(env, iterable, target, lang, src)
	}
	env.PushScope()
	defer env.PopScope()
	registerRangeBindings(env, "", varNode, iterable, lang, src)
	if varNode == resolveTargetNode(target, lang) {
		typ, err := env.Resolve(varNode, lang, src)
		return true, typ, err
	}
	if containsNode(expr, target) {
		return resolveAtNode(env, expr, target, lang, src)
	}
	if containsNode(filter, target) {
		return resolveAtNode(env, filter, target, lang, src)
	}
	return false, nil, nil
}

func registerSequentialBindings(env *TypeEnv, node *gotreesitter.Node, lang *gotreesitter.Language, src []byte) {
	if env == nil || node == nil {
		return
	}
	switch node.Type(lang) {
	case "let_declaration":
		nameNode := node.ChildByFieldName("name", lang)
		if nameNode == nil {
			return
		}
		name := nodeText(src, nameNode)
		isMut := node.ChildByFieldName("mutable", lang) != nil
		typ := letBindingType(env, node, lang, src)
		registerBinding(env, name, typ)
		if name != "" {
			env.scope.mutable[name] = isMut
		}
	case "let_multi_declaration":
		isMut := node.ChildByFieldName("mutable", lang) != nil
		specs := letMultiNamesAndTypes(node, lang, src)
		registerExpressionBindings(env, specs, node.ChildByFieldName("value", lang), lang, src)
		for _, spec := range specs {
			if spec.Name != "" {
				env.scope.mutable[spec.Name] = isMut
			}
		}
	case "short_var_declaration":
		left := node.ChildByFieldName("left", lang)
		right := node.ChildByFieldName("right", lang)
		var names []bindingSpec
		if left != nil {
			for i := 0; i < int(left.NamedChildCount()); i++ {
				child := left.NamedChild(i)
				if child != nil && child.Type(lang) == "identifier" {
					names = append(names, bindingSpec{Name: nodeText(src, child)})
				}
			}
		}
		registerExpressionBindings(env, names, right, lang, src)
	case "var_declaration":
		registerVarDeclBindings(env, node, SymVar, lang, src)
	case "const_declaration":
		registerVarDeclBindings(env, node, SymConst, lang, src)
	}
}

type bindingSpec struct {
	Name string
	Type Type
}

func letMultiNamesAndTypes(node *gotreesitter.Node, lang *gotreesitter.Language, src []byte) []bindingSpec {
	var specs []bindingSpec
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "identifier":
			specs = append(specs, bindingSpec{Name: nodeText(src, child)})
		case "let_typed_binding":
			nameNode := child.ChildByFieldName("name", lang)
			typeNode := child.ChildByFieldName("type", lang)
			if nameNode == nil {
				continue
			}
			specs = append(specs, bindingSpec{
				Name: nodeText(src, nameNode),
				Type: resolveTypeExprNormalized(typeNode, nil, lang, src),
			})
		}
	}
	return specs
}

func registerExpressionBindings(env *TypeEnv, specs []bindingSpec, exprList *gotreesitter.Node, lang *gotreesitter.Language, src []byte) {
	types := resolveExpressionListTypes(env, exprList, lang, src)
	for i, spec := range specs {
		typ := spec.Type
		if typ == nil && i < len(types) {
			typ = types[i]
		}
		registerBinding(env, spec.Name, typ)
	}
}

func registerVarDeclBindings(env *TypeEnv, node *gotreesitter.Node, _ SymbolKind, lang *gotreesitter.Language, src []byte) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		spec := node.NamedChild(i)
		if spec == nil {
			continue
		}
		switch spec.Type(lang) {
		case "var_spec", "const_spec":
			explicit := resolveTypeExprNormalized(spec.ChildByFieldName("type", lang), env, lang, src)
			value := spec.ChildByFieldName("value", lang)
			inferred := normalizeType(resolveValueType(env, value, lang, src))
			for j := 0; j < int(spec.ChildCount()); j++ {
				if spec.FieldNameForChild(j, lang) != "name" {
					continue
				}
				nameNode := spec.Child(j)
				if nameNode == nil {
					continue
				}
				typ := explicit
				if typ == nil {
					typ = inferred
				}
				registerBinding(env, nodeText(src, nameNode), typ)
			}
		}
	}
}

func registerIfLetBinding(env *TypeEnv, pattern, value *gotreesitter.Node, lang *gotreesitter.Language, src []byte) {
	if pattern == nil {
		return
	}
	registerBinding(env, nodeText(src, pattern), normalizeType(resolveValueType(env, value, lang, src)))
}

func registerRangeBindings(env *TypeEnv, indexName string, varNode, iterable *gotreesitter.Node, lang *gotreesitter.Language, src []byte) {
	if indexName != "" {
		registerBinding(env, indexName, Primitive("int"))
	}
	if varNode == nil {
		return
	}
	registerBinding(env, nodeText(src, varNode), rangeValueType(env, iterable, lang, src))
}

func registerParameterList(env *TypeEnv, paramList *gotreesitter.Node, lang *gotreesitter.Language, src []byte) {
	if env == nil || paramList == nil || paramList.Type(lang) != "parameter_list" {
		return
	}
	for i := 0; i < int(paramList.NamedChildCount()); i++ {
		child := paramList.NamedChild(i)
		if child == nil || child.Type(lang) != "parameter_declaration" {
			continue
		}
		typeNode := child.ChildByFieldName("type", lang)
		typ := resolveTypeExprNormalized(typeNode, env, lang, src)
		for j := 0; j < int(child.ChildCount()); j++ {
			if child.FieldNameForChild(j, lang) != "name" {
				continue
			}
			nameNode := child.Child(j)
			if nameNode == nil {
				continue
			}
			registerBinding(env, nodeText(src, nameNode), typ)
		}
	}
}

func namedResultList(result *gotreesitter.Node, lang *gotreesitter.Language) *gotreesitter.Node {
	if result == nil || result.Type(lang) != "parameter_list" {
		return nil
	}
	return result
}

func registerBinding(env *TypeEnv, name string, typ Type) {
	if env == nil || name == "" || typ == nil {
		return
	}
	env.RegisterVar(name, normalizeType(typ))
}

func letBindingType(env *TypeEnv, node *gotreesitter.Node, lang *gotreesitter.Language, src []byte) Type {
	if node == nil {
		return nil
	}
	if ann := node.ChildByFieldName("type_annotation", lang); ann != nil {
		return resolveTypeExprNormalized(ann, env, lang, src)
	}
	return normalizeType(resolveValueType(env, node.ChildByFieldName("value", lang), lang, src))
}

func resolveValueType(env *TypeEnv, node *gotreesitter.Node, lang *gotreesitter.Language, src []byte) Type {
	if env == nil || node == nil {
		return nil
	}
	typ, err := env.Resolve(node, lang, src)
	if err != nil {
		return nil
	}
	return normalizeType(typ)
}

func resolveTypeExprNormalized(node *gotreesitter.Node, env *TypeEnv, lang *gotreesitter.Language, src []byte) Type {
	if node == nil {
		return nil
	}
	if env != nil {
		typ, err := env.resolveTypeExpr(node, lang, src)
		if err == nil {
			return normalizeType(typ)
		}
	}
	return normalizeType(parseTypeString(nodeText(src, node)))
}

func resolveExpressionListTypes(env *TypeEnv, exprList *gotreesitter.Node, lang *gotreesitter.Language, src []byte) []Type {
	if env == nil || exprList == nil {
		return nil
	}
	if exprList.NamedChildCount() == 1 {
		typ := resolveValueType(env, exprList.NamedChild(0), lang, src)
		if tuple, ok := typ.(*TupleType); ok {
			return tuple.Elems
		}
		if typ != nil {
			return []Type{typ}
		}
		return nil
	}
	var types []Type
	for i := 0; i < int(exprList.NamedChildCount()); i++ {
		types = append(types, resolveValueType(env, exprList.NamedChild(i), lang, src))
	}
	return types
}

func rangeValueType(env *TypeEnv, iterable *gotreesitter.Node, lang *gotreesitter.Language, src []byte) Type {
	if env == nil || iterable == nil {
		return nil
	}
	if iterable.Type(lang) == "range_expression" {
		return Primitive("int")
	}
	iterType := resolveValueType(env, iterable, lang, src)
	switch it := iterType.(type) {
	case *SliceType:
		return it.Elem
	case *MapType:
		return it.Value
	case *ChanType:
		return it.Elem
	case Primitive:
		if it == Primitive("string") {
			return Primitive("rune")
		}
	}
	return nil
}

func normalizeType(typ Type) Type {
	if u, ok := typ.(*UntypedConstType); ok {
		return u.Default()
	}
	return typ
}

func resolveTargetNode(target *gotreesitter.Node, lang *gotreesitter.Language) *gotreesitter.Node {
	if target == nil {
		return nil
	}
	parent := target.Parent()
	if parent == nil {
		return target
	}
	switch parent.Type(lang) {
	case "selector_expression":
		if parent.ChildByFieldName("field", lang) == target {
			return parent
		}
	case "safe_navigation":
		if parent.ChildByFieldName("field", lang) == target {
			return parent
		}
	}
	return target
}

func containsNode(container, target *gotreesitter.Node) bool {
	if container == nil || target == nil {
		return false
	}
	return container.StartByte() <= target.StartByte() && target.EndByte() <= container.EndByte()
}

func findNodeType(node *gotreesitter.Node, lang *gotreesitter.Language, typ string) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child != nil && child.Type(lang) == typ {
			return child
		}
	}
	return nil
}

func nodeText(src []byte, node *gotreesitter.Node) string {
	if node == nil {
		return ""
	}
	return string(src[node.StartByte():node.EndByte()])
}
