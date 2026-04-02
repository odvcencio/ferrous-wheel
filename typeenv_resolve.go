package ferrouswheel

import (
	"fmt"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// Unify returns the common type of two types, or an error if they are incompatible.
func Unify(a, b Type) (Type, error) {
	// Rule 1: structurally identical
	if TypeEquals(a, b) {
		return a, nil
	}

	// Rule 2: UntypedConst + concrete -> concrete
	if ua, ok := a.(*UntypedConstType); ok {
		if _, ok := b.(*UntypedConstType); !ok {
			return unifyUntypedWithConcrete(ua, b)
		}
	}
	if ub, ok := b.(*UntypedConstType); ok {
		if _, ok := a.(*UntypedConstType); !ok {
			return unifyUntypedWithConcrete(ub, a)
		}
	}

	// Rule 3: two untyped -> promote
	if ua, ok := a.(*UntypedConstType); ok {
		if ub, ok := b.(*UntypedConstType); ok {
			return promoteUntyped(ua, ub), nil
		}
	}

	// Rule 5: nil + nillable -> nillable
	if isUntypedNil(a) && isNillable(b) {
		return b, nil
	}
	if isUntypedNil(b) && isNillable(a) {
		return a, nil
	}

	// Rule 4 & 6: Named vs underlying does NOT unify; otherwise error
	return nil, fmt.Errorf("incompatible types: %s and %s", a, b)
}

func unifyUntypedWithConcrete(u *UntypedConstType, concrete Type) (Type, error) {
	return concrete, nil
}

func promoteUntyped(a, b *UntypedConstType) Type {
	if a.Kind == UntypedFloat || b.Kind == UntypedFloat {
		return (&UntypedConstType{Kind: UntypedFloat}).Default()
	}
	if a.Kind == UntypedRune || b.Kind == UntypedRune {
		return (&UntypedConstType{Kind: UntypedRune}).Default()
	}
	return a.Default()
}

func isUntypedNil(t Type) bool {
	u, ok := t.(*UntypedConstType)
	return ok && u.Kind == UntypedNil
}

func isNillable(t Type) bool {
	switch t.(type) {
	case *PointerType, *SliceType, *MapType, *ChanType, *InterfaceType, *FuncType:
		return true
	}
	return false
}

// Resolve returns the type of the expression at node n.
func (e *TypeEnv) Resolve(n *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (Type, error) {
	if n == nil {
		return nil, fmt.Errorf("nil node")
	}

	text := func(node *gotreesitter.Node) string {
		return string(src[node.StartByte():node.EndByte()])
	}
	field := func(name string) *gotreesitter.Node {
		return n.ChildByFieldName(name, lang)
	}

	switch n.Type(lang) {
	// --- Literals ---
	case "int_literal":
		return &UntypedConstType{Kind: UntypedInt}, nil
	case "float_literal":
		return &UntypedConstType{Kind: UntypedFloat}, nil
	case "interpreted_string_literal", "raw_string_literal":
		return &UntypedConstType{Kind: UntypedString}, nil
	case "true", "false":
		return &UntypedConstType{Kind: UntypedBool}, nil
	case "rune_literal":
		return &UntypedConstType{Kind: UntypedRune}, nil
	case "nil":
		return &UntypedConstType{Kind: UntypedNil}, nil

	// --- Identifiers ---
	case "identifier":
		name := text(n)
		if typ, err := e.LookupVar(name); err == nil {
			return typ, nil
		}
		if fn, err := e.LookupFunc(name); err == nil {
			return fn, nil
		}
		return nil, fmt.Errorf("undefined identifier: %s", name)

	// --- Let bindings ---
	case "let_declaration":
		if ann := field("type_annotation"); ann != nil {
			return e.resolveTypeExpr(ann, lang, src)
		}
		if val := field("value"); val != nil {
			typ, err := e.Resolve(val, lang, src)
			if err != nil {
				return nil, err
			}
			if u, ok := typ.(*UntypedConstType); ok {
				return u.Default(), nil
			}
			return typ, nil
		}
		return nil, fmt.Errorf("let declaration has no value")

	// --- Function calls ---
	case "call_expression":
		fn := field("function")
		if fn == nil {
			return nil, fmt.Errorf("call has no function")
		}
		fnType, err := e.Resolve(fn, lang, src)
		if err != nil {
			return nil, err
		}
		ft, ok := fnType.(*FuncType)
		if !ok {
			return nil, fmt.Errorf("%s is not callable", text(fn))
		}
		switch len(ft.Results) {
		case 0:
			return nil, fmt.Errorf("%s returns no value", text(fn))
		case 1:
			return ft.Results[0], nil
		default:
			return &TupleType{Elems: ft.Results}, nil
		}

	case "composite_literal":
		typeNode := field("type")
		if typeNode == nil {
			return nil, fmt.Errorf("composite literal has no type")
		}
		return e.resolveTypeExpr(typeNode, lang, src)

	case "unary_expression":
		operator := field("operator")
		operand := field("operand")
		if operator == nil || operand == nil {
			return nil, fmt.Errorf("unary expression incomplete")
		}
		operandType, err := e.Resolve(operand, lang, src)
		if err != nil {
			return nil, err
		}
		switch text(operator) {
		case "&":
			return &PointerType{Elem: operandType}, nil
		case "*":
			if ptr, ok := operandType.(*PointerType); ok {
				return ptr.Elem, nil
			}
			return nil, fmt.Errorf("cannot dereference %s", operandType)
		case "<-":
			if ch, ok := operandType.(*ChanType); ok {
				return ch.Elem, nil
			}
			return nil, fmt.Errorf("cannot receive from %s", operandType)
		default:
			return operandType, nil
		}

	// --- Selector (pkg.Func or obj.Field) ---
	case "selector_expression":
		operand := field("operand")
		sel := field("field")
		if operand == nil || sel == nil {
			return nil, fmt.Errorf("incomplete selector")
		}
		if operand.Type(lang) == "identifier" {
			pkgName := text(operand)
			if fn, err := e.LookupImportedFunc(pkgName, text(sel)); err == nil {
				return fn, nil
			}
			if typ, err := e.LookupImportedType(pkgName, text(sel)); err == nil {
				return typ, nil
			}
			if typ, err := e.LookupImportedVar(pkgName, text(sel)); err == nil {
				return typ, nil
			}
		}
		objType, err := e.Resolve(operand, lang, src)
		if err != nil {
			return nil, err
		}
		return e.ResolveFieldAccess(objType, text(sel))

	// --- Try (error propagation) ---
	case "error_propagation":
		expr := field("expr")
		if expr == nil {
			return nil, fmt.Errorf("try has no expression")
		}
		typ, err := e.Resolve(expr, lang, src)
		if err != nil {
			return nil, err
		}
		tup, ok := typ.(*TupleType)
		if !ok {
			return typ, nil
		}
		if len(tup.Elems) >= 2 && tup.Elems[len(tup.Elems)-1].String() == "error" {
			remaining := tup.Elems[:len(tup.Elems)-1]
			if len(remaining) == 1 {
				return remaining[0], nil
			}
			return &TupleType{Elems: remaining}, nil
		}
		return typ, nil

	// --- Null coalesce ---
	case "null_coalesce":
		left := field("left")
		if left == nil {
			return nil, fmt.Errorf("?? has no left operand")
		}
		return e.Resolve(left, lang, src)

	// --- Safe navigation ---
	case "safe_navigation":
		obj := field("object")
		fld := field("field")
		if obj == nil || fld == nil {
			return nil, fmt.Errorf("?. incomplete")
		}
		objType, err := e.Resolve(obj, lang, src)
		if err != nil {
			return nil, err
		}
		return e.ResolveFieldAccess(objType, text(fld))

	// --- Ternary ---
	case "ternary_expression":
		cons := field("consequence")
		alt := field("alternative")
		if cons == nil || alt == nil {
			return nil, fmt.Errorf("ternary incomplete")
		}
		ct, err := e.Resolve(cons, lang, src)
		if err != nil {
			return nil, err
		}
		at, err := e.Resolve(alt, lang, src)
		if err != nil {
			return nil, err
		}
		return Unify(ct, at)

	// --- Match ---
	case "match_expression":
		var armTypes []Type
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type(lang) == "match_arm" {
				body := c.ChildByFieldName("body", lang)
				if body != nil {
					bt, err := e.Resolve(body, lang, src)
					if err != nil {
						return nil, err
					}
					armTypes = append(armTypes, bt)
				}
			}
		}
		if len(armTypes) == 0 {
			return nil, fmt.Errorf("match has no arms")
		}
		result := armTypes[0]
		for _, at := range armTypes[1:] {
			var err error
			result, err = Unify(result, at)
			if err != nil {
				return nil, fmt.Errorf("match arms have incompatible types: %w", err)
			}
		}
		return result, nil

	// --- Lambda ---
	case "lambda_expression":
		params := field("params")
		retType := field("return_type")
		paramTypes, paramErr := e.resolveLambdaParams(params, lang, src)
		if retType != nil {
			rt, err := e.resolveTypeExpr(retType, lang, src)
			if err != nil {
				return nil, err
			}
			if paramErr != nil {
				return nil, paramErr
			}
			return &FuncType{Params: paramTypes, Results: []Type{rt}}, nil
		}
		if paramErr != nil {
			return nil, &UnresolvedType{
				Msg: "cannot infer lambda type; add type annotations: fn(x: int) ...",
			}
		}
		return &FuncType{Params: paramTypes}, nil

	// --- List comprehension ---
	case "list_comprehension":
		iterableNode := field("iterable")
		varNode := field("var")
		exprNode := field("expression")
		if iterableNode == nil || varNode == nil || exprNode == nil {
			return nil, fmt.Errorf("list comprehension incomplete")
		}
		iterType, err := e.Resolve(iterableNode, lang, src)
		if err != nil {
			return nil, err
		}
		var elemType Type
		switch it := iterType.(type) {
		case *SliceType:
			elemType = it.Elem
		default:
			return nil, fmt.Errorf("for-in requires a slice, got %s", iterType)
		}
		e.PushScope()
		e.RegisterVar(text(varNode), elemType)
		exprType, err := e.Resolve(exprNode, lang, src)
		e.PopScope()
		if err != nil {
			return nil, err
		}
		return &SliceType{Elem: exprType}, nil

	// --- Fan in ---
	case "fan_in_expression":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			ct, err := e.Resolve(c, lang, src)
			if err != nil {
				continue
			}
			if ch, ok := ct.(*ChanType); ok {
				return &ChanType{Elem: ch.Elem, Dir: ChanRecv}, nil
			}
		}
		return nil, fmt.Errorf("fan in: no typed channels found")

	// --- Pipeline ---
	case "pipeline_expression":
		left := field("left")
		right := field("right")
		if left == nil || right == nil {
			return nil, fmt.Errorf("pipeline incomplete")
		}
		rightType, err := e.Resolve(right, lang, src)
		if err != nil {
			return nil, err
		}
		ft, ok := rightType.(*FuncType)
		if !ok {
			return nil, fmt.Errorf("right side of |> must be callable, got %s", rightType)
		}
		if len(ft.Results) == 0 {
			return nil, fmt.Errorf("pipeline stage returns no value")
		}
		return ft.Results[0], nil

	default:
		return nil, fmt.Errorf("cannot resolve type of %s node", n.Type(lang))
	}
}

// MustResolve is like Resolve but formats error with source location.
func (e *TypeEnv) MustResolve(n *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (Type, error) {
	typ, err := e.Resolve(n, lang, src)
	if err != nil {
		pos := n.StartPoint()
		return nil, fmt.Errorf("%s:%d:%d: type error: %s", e.filename, pos.Row+1, pos.Column+1, err)
	}
	if u, ok := typ.(*UnresolvedType); ok {
		pos := n.StartPoint()
		return nil, fmt.Errorf("%s:%d:%d: type error: %s", e.filename, pos.Row+1, pos.Column+1, u.Msg)
	}
	return typ, nil
}

// ResolveFieldAccess resolves the type of a field on a type.
func (e *TypeEnv) ResolveFieldAccess(objType Type, fieldName string) (Type, error) {
	if ptr, ok := objType.(*PointerType); ok {
		objType = ptr.Elem
	}
	if named, ok := objType.(*NamedType); ok {
		if named.Underlying == nil {
			return nil, fmt.Errorf("cannot access field %s on unresolved named type %s", fieldName, named.String())
		}
		objType = named.Underlying
	}
	switch t := objType.(type) {
	case *StructType:
		if ft, ok := t.Fields[fieldName]; ok {
			return ft, nil
		}
		return nil, fmt.Errorf("struct %s has no field %s", t.Name, fieldName)
	default:
		return nil, fmt.Errorf("cannot access field %s on %s", fieldName, objType)
	}
}

// resolveTypeExpr converts a CST type node to a Type.
func (e *TypeEnv) resolveTypeExpr(n *gotreesitter.Node, lang *gotreesitter.Language, src []byte) (Type, error) {
	if n == nil {
		return nil, fmt.Errorf("nil type expression")
	}

	text := string(src[n.StartByte():n.EndByte()])
	field := func(node *gotreesitter.Node, name string) *gotreesitter.Node {
		return node.ChildByFieldName(name, lang)
	}

	switch n.Type(lang) {
	case "type_identifier", "identifier":
		if isBuiltinTypeName(text) {
			return Primitive(text), nil
		}
		if st, err := e.LookupStruct(text); err == nil {
			return &NamedType{Name: text, Underlying: st}, nil
		}
		if en, err := e.LookupEnum(text); err == nil {
			return &NamedType{Name: text, Underlying: en}, nil
		}
		return &NamedType{Name: text}, nil
	case "qualified_type":
		pkgNode := field(n, "package")
		nameNode := field(n, "name")
		if pkgNode == nil || nameNode == nil {
			break
		}
		pkg := string(src[pkgNode.StartByte():pkgNode.EndByte()])
		name := string(src[nameNode.StartByte():nameNode.EndByte()])
		if typ, err := e.LookupImportedType(pkg, name); err == nil {
			return typ, nil
		}
		return &NamedType{Pkg: pkg, Name: name}, nil
	case "pointer_type":
		elem := n.NamedChild(0)
		if elem == nil {
			break
		}
		elemType, err := e.resolveTypeExpr(elem, lang, src)
		if err != nil {
			return nil, err
		}
		return &PointerType{Elem: elemType}, nil
	case "slice_type":
		elem := field(n, "element")
		if elem == nil {
			break
		}
		elemType, err := e.resolveTypeExpr(elem, lang, src)
		if err != nil {
			return nil, err
		}
		return &SliceType{Elem: elemType}, nil
	case "map_type":
		keyNode := field(n, "key")
		valueNode := field(n, "value")
		if keyNode == nil || valueNode == nil {
			break
		}
		keyType, err := e.resolveTypeExpr(keyNode, lang, src)
		if err != nil {
			return nil, err
		}
		valueType, err := e.resolveTypeExpr(valueNode, lang, src)
		if err != nil {
			return nil, err
		}
		return &MapType{Key: keyType, Value: valueType}, nil
	case "channel_type":
		valueNode := field(n, "value")
		if valueNode == nil {
			break
		}
		valueType, err := e.resolveTypeExpr(valueNode, lang, src)
		if err != nil {
			return nil, err
		}
		dir := ChanBidi
		switch {
		case len(text) >= 2 && text[:2] == "<-":
			dir = ChanRecv
		case len(text) >= 6 && text[:6] == "chan<-":
			dir = ChanSend
		}
		return &ChanType{Elem: valueType, Dir: dir}, nil
	case "generic_type":
		baseNode := field(n, "type")
		typeArgsNode := field(n, "type_arguments")
		if baseNode == nil || typeArgsNode == nil {
			break
		}
		typeArgs := make([]Type, 0, int(typeArgsNode.NamedChildCount()))
		for i := 0; i < int(typeArgsNode.NamedChildCount()); i++ {
			argNode := typeArgsNode.NamedChild(i)
			argType, err := e.resolveTypeExpr(argNode, lang, src)
			if err != nil {
				return nil, err
			}
			typeArgs = append(typeArgs, argType)
		}
		return &GenericType{
			Name:       string(src[baseNode.StartByte():baseNode.EndByte()]),
			TypeParams: typeArgs,
		}, nil
	}

	if parsed := parseTypeString(text); parsed != nil {
		return e.bindType(parsed), nil
	}
	return nil, fmt.Errorf("unknown type: %s", text)
}

// resolveLambdaParams extracts types from lambda params.
func (e *TypeEnv) resolveLambdaParams(params *gotreesitter.Node, lang *gotreesitter.Language, src []byte) ([]Type, error) {
	if params == nil {
		return nil, nil
	}
	var types []Type
	for i := 0; i < int(params.NamedChildCount()); i++ {
		c := params.NamedChild(i)
		switch c.Type(lang) {
		case "lambda_typed_param":
			typNode := c.ChildByFieldName("type", lang)
			if typNode == nil {
				return nil, fmt.Errorf("typed param missing type")
			}
			t, err := e.resolveTypeExpr(typNode, lang, src)
			if err != nil {
				return nil, err
			}
			types = append(types, t)
		case "identifier":
			return nil, fmt.Errorf("untyped lambda parameter: %s", string(src[c.StartByte():c.EndByte()]))
		}
	}
	return types, nil
}
