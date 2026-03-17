package ferrouswheel

import (
	"fmt"
	"strings"
	"sync"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

var (
	fwLangOnce   sync.Once
	fwLangCached *gotreesitter.Language
	fwLangErr    error
)

func getFWLanguage() (*gotreesitter.Language, error) {
	fwLangOnce.Do(func() {
		fwLangCached, fwLangErr = GenerateLanguage(Grammar())
	})
	return fwLangCached, fwLangErr
}

// Transpile converts .fw source to valid Go code.
func Transpile(source []byte) (string, error) {
	lang, err := getFWLanguage()
	if err != nil {
		return "", fmt.Errorf("generate ferrous-wheel language: %w", err)
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	root := tree.RootNode()
	if root.HasError() {
		return "", fmt.Errorf("parse errors in ferrous-wheel source")
	}

	t := &fwTranspiler{src: source, lang: lang}
	result := t.emit(root)

	// Detect Result[T] and Option[T] usage in the transpiled output
	t.detectGenericTypes(result)

	result = t.injectImports(result)
	result = t.injectGenericTypes(result)
	return result, nil
}

type fwTranspiler struct {
	src             []byte
	lang            *gotreesitter.Language
	needsReflect    bool
	needsFmt        bool
	needsJSON       bool
	needsResultType bool
	needsOptionType bool
	needsUnsafe     bool
	needsRuntime    bool
	needsOS         bool
	needsSyscall    bool
	needsSync       bool
	needsTime       bool
	implReceiver    string // non-empty when inside an impl block
}

func (t *fwTranspiler) text(n *gotreesitter.Node) string {
	return string(t.src[n.StartByte():n.EndByte()])
}

func (t *fwTranspiler) nodeType(n *gotreesitter.Node) string {
	return n.Type(t.lang)
}

func (t *fwTranspiler) childByField(n *gotreesitter.Node, field string) *gotreesitter.Node {
	return n.ChildByFieldName(field, t.lang)
}

func (t *fwTranspiler) emit(n *gotreesitter.Node) string {
	switch t.nodeType(n) {
	case "enum_declaration":
		return t.emitEnum(n)
	case "let_declaration":
		return t.emitLet(n)
	case "let_multi_declaration":
		return t.emitLetMulti(n)
	case "ternary_expression":
		return t.emitTernary(n)
	case "match_expression":
		return t.emitMatch(n)
	case "null_coalesce":
		return t.emitNullCoalesce(n)
	case "error_propagation":
		return t.emitErrorProp(n)
	case "safe_navigation":
		return t.emitSafeNav(n)
	case "lambda_expression":
		return t.emitLambda(n)
	case "call_expression":
		return t.emitCall(n)
	case "derive_declaration":
		return t.emitDerive(n)
	case "if_let_statement":
		return t.emitIfLet(n)
	case "range_expression":
		return t.emitRange(n)
	case "for_in_statement":
		return t.emitForIn(n)
	case "for_in_index_statement":
		return t.emitForInIndex(n)
	case "fstring":
		return t.emitFString(n)
	case "guard_statement":
		return t.emitGuard(n)
	case "defer_error":
		return t.emitDeferError(n)
	case "impl_block":
		return t.emitImplBlock(n)
	case "unless_statement":
		return t.emitUnless(n)
	case "until_statement":
		return t.emitUntil(n)
	case "repeat_statement":
		return t.emitRepeat(n)
	case "list_comprehension":
		return t.emitListComprehension(n)
	case "swap_statement":
		return t.emitSwap(n)
	case "function_declaration":
		return t.emitFunctionDecl(n)
	// Low-level features
	case "arena_block":
		return t.emitArena(n)
	case "pin_statement":
		return t.emitPin(n)
	case "unpin_statement":
		return t.emitUnpin(n)
	case "unsafe_cast":
		return t.emitUnsafeCast(n)
	case "mmap_block":
		return t.emitMmap(n)
	case "packed_annotation":
		return t.emitPacked(n)
	case "vectorize_statement":
		return t.emitVectorize(n)
	// Concurrency features
	case "select_block":
		return t.emitSelectBlock(n)
	case "fan_out_block":
		return t.emitFanOut(n)
	case "fan_in_expression":
		return t.emitFanIn(n)
	case "pipeline_expression":
		return t.emitPipeline(n)
	case "concurrent_block":
		return t.emitConcurrent(n)
	case "throttle_block":
		return t.emitThrottle(n)
	case "retry_block":
		return t.emitRetry(n)
	case "breaker_block":
		return t.emitBreaker(n)
	default:
		return t.emitDefault(n)
	}
}

func (t *fwTranspiler) emitDefault(n *gotreesitter.Node) string {
	cc := int(n.ChildCount())
	if cc == 0 {
		return t.text(n)
	}
	var b strings.Builder
	prev := n.StartByte()
	for i := 0; i < cc; i++ {
		c := n.Child(i)
		if c.StartByte() > prev {
			b.Write(t.src[prev:c.StartByte()])
		}
		b.WriteString(t.emit(c))
		prev = c.EndByte()
	}
	if n.EndByte() > prev {
		b.Write(t.src[prev:n.EndByte()])
	}
	return b.String()
}

// enum Color { Red, Green, Blue(int) }
// -> type Color struct { tag int; blueVal0 int }
//
//	const (ColorRed = 0; ColorGreen = 1; ColorBlue = 2)
//	func Red() Color { return Color{tag: 0} }
//	func Green() Color { return Color{tag: 1} }
//	func Blue(v0 int) Color { return Color{tag: 2, blue0: v0} }
func (t *fwTranspiler) emitEnum(n *gotreesitter.Node) string {
	name := "Enum"
	if nameNode := t.childByField(n, "name"); nameNode != nil {
		name = t.text(nameNode)
	}

	// Collect variants
	type variant struct {
		name  string
		types []string
	}
	var variants []variant
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "enum_variant" {
			v := variant{}
			if vname := t.childByField(c, "name"); vname != nil {
				v.name = t.text(vname)
			}
			// Collect type params (all named children except the name identifier)
			for j := 0; j < int(c.NamedChildCount()); j++ {
				tc := c.NamedChild(j)
				if t.nodeType(tc) != "identifier" {
					v.types = append(v.types, t.text(tc))
				}
			}
			variants = append(variants, v)
		}
	}

	var b strings.Builder

	// Struct with tag + variant fields
	fmt.Fprintf(&b, "type %s struct {\n\ttag int\n", name)
	for _, v := range variants {
		for j, typ := range v.types {
			fmt.Fprintf(&b, "\t%s%d %s\n", strings.ToLower(v.name), j, typ)
		}
	}
	b.WriteString("}\n\n")

	// Constants
	b.WriteString("const (\n")
	for i, v := range variants {
		fmt.Fprintf(&b, "\t%s%s = %d\n", name, v.name, i)
	}
	b.WriteString(")\n\n")

	// Constructor functions
	for i, v := range variants {
		if len(v.types) == 0 {
			fmt.Fprintf(&b, "func %s() %s { return %s{tag: %d} }\n", v.name, name, name, i)
		} else {
			params := make([]string, len(v.types))
			args := make([]string, len(v.types))
			for j, typ := range v.types {
				params[j] = fmt.Sprintf("v%d %s", j, typ)
				args[j] = fmt.Sprintf("%s%d: v%d", strings.ToLower(v.name), j, j)
			}
			fmt.Fprintf(&b, "func %s(%s) %s { return %s{tag: %d, %s} }\n",
				v.name, strings.Join(params, ", "), name, name, i, strings.Join(args, ", "))
		}
	}

	return b.String()
}

// let x = 1 -> x := 1
func (t *fwTranspiler) emitLet(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	value := t.childByField(n, "value")
	if nameNode == nil || value == nil {
		return t.text(n)
	}
	return fmt.Sprintf("%s := %s", t.text(nameNode), t.emit(value))
}

// let (a, b) = f() -> a, b := f()
func (t *fwTranspiler) emitLetMulti(n *gotreesitter.Node) string {
	value := t.childByField(n, "value")
	if value == nil {
		return t.text(n)
	}

	// Collect all identifier children (the names in the tuple)
	var names []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "identifier" {
			names = append(names, t.text(c))
		}
	}
	if len(names) == 0 {
		return t.text(n)
	}

	return fmt.Sprintf("%s := %s", strings.Join(names, ", "), t.emit(value))
}

// cond ? trueVal : falseVal -> func() interface{} { if cond { return trueVal }; return falseVal }()
func (t *fwTranspiler) emitTernary(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	cons := t.childByField(n, "consequence")
	alt := t.childByField(n, "alternative")
	if cond == nil || cons == nil || alt == nil {
		return t.text(n)
	}
	return fmt.Sprintf("func() interface{} { if %s { return %s }; return %s }()",
		t.emit(cond), t.emit(cons), t.emit(alt))
}

// match val { 1 => "one", 2 => "two" }
// -> func() interface{} { switch val { case 1: return "one"; case 2: return "two" } }()
func (t *fwTranspiler) emitMatch(n *gotreesitter.Node) string {
	subject := t.childByField(n, "subject")
	if subject == nil {
		return t.text(n)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "func() interface{} { switch %s {\n", t.emit(subject))

	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "match_arm" {
			pattern := t.childByField(c, "pattern")
			guard := t.childByField(c, "guard")
			body := t.childByField(c, "body")
			if pattern != nil && body != nil {
				if guard != nil {
					// match x { n if n > 0 => "positive" }
					// -> case n: if n > 0 { return "positive" }
					fmt.Fprintf(&b, "case %s:\n\tif %s {\n\t\treturn %s\n\t}\n",
						t.emit(pattern), t.emit(guard), t.emit(body))
				} else {
					fmt.Fprintf(&b, "case %s:\n\treturn %s\n", t.emit(pattern), t.emit(body))
				}
			}
		}
	}

	b.WriteString("default:\n\tpanic(fmt.Sprintf(\"non-exhaustive match: no arm matched value %v\", ")
	b.WriteString(t.emit(subject))
	b.WriteString("))\n}\n}()")
	t.needsFmt = true
	return b.String()
}

// val ?? "default" -> zero-value coalescing (works for ALL types)
func (t *fwTranspiler) emitNullCoalesce(n *gotreesitter.Node) string {
	left := t.childByField(n, "left")
	right := t.childByField(n, "right")
	if left == nil || right == nil {
		return t.text(n)
	}
	t.needsReflect = true
	l := t.emit(left)
	return fmt.Sprintf("func() interface{} { _v := reflect.ValueOf(%s); if _v.IsValid() && !_v.IsZero() { return %s }; return %s }()", l, l, t.emit(right))
}

// try expr -> error propagation (standalone, not inside a call)
func (t *fwTranspiler) emitErrorProp(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	if expr == nil {
		return t.text(n)
	}
	e := t.emit(expr)
	return fmt.Sprintf("func() interface{} { _v, _err := %s; if _err != nil { return _err }; return _v }()", e)
}

// call_expression: check if the function is an error_propagation (try funcName)(args)
// In that case, wrap the entire call in the error-handling IIFE.
func (t *fwTranspiler) emitCall(n *gotreesitter.Node) string {
	if n.ChildCount() >= 2 {
		fn := n.Child(0)
		if t.nodeType(fn) == "error_propagation" {
			// try funcName(args) → func() interface{} { _v, _err := funcName(args); ... }()
			innerExpr := t.childByField(fn, "expr")
			if innerExpr != nil {
				funcName := t.emit(innerExpr)
				// Collect the rest of the call (argument list)
				var args string
				for i := 1; i < int(n.ChildCount()); i++ {
					args += t.emit(n.Child(i))
				}
				fullCall := funcName + args
				return fmt.Sprintf("func() interface{} { _v, _err := %s; if _err != nil { return _err }; return _v }()", fullCall)
			}
		}
	}
	return t.emitDefault(n)
}

// obj?.field -> nil-safe field access
func (t *fwTranspiler) emitSafeNav(n *gotreesitter.Node) string {
	obj := t.childByField(n, "object")
	field := t.childByField(n, "field")
	if obj == nil || field == nil {
		return t.text(n)
	}
	t.needsReflect = true
	o := t.emit(obj)
	f := t.text(field)
	return fmt.Sprintf("func() interface{} { _o := %s; if _o == nil { return nil }; return reflect.ValueOf(_o).Elem().FieldByName(%q).Interface() }()", o, f)
}

// fn(x, y) body -> func literal
func (t *fwTranspiler) emitLambda(n *gotreesitter.Node) string {
	params := t.childByField(n, "params")
	body := t.childByField(n, "body")
	if params == nil || body == nil {
		return t.text(n)
	}

	// Collect param names
	var paramNames []string
	for i := 0; i < int(params.NamedChildCount()); i++ {
		c := params.NamedChild(i)
		if t.nodeType(c) == "identifier" {
			paramNames = append(paramNames, t.text(c))
		}
	}

	// Build Go func literal with interface{} params
	var b strings.Builder
	b.WriteString("func(")
	for i, p := range paramNames {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s interface{}", p)
	}
	b.WriteString(") interface{} ")

	bodyText := t.emit(body)
	if t.nodeType(body) == "block" {
		b.WriteString(bodyText)
	} else {
		fmt.Fprintf(&b, "{ return %s }", bodyText)
	}

	return b.String()
}

// emitFunctionDecl handles function_declaration, injecting receiver when inside impl block.
func (t *fwTranspiler) emitFunctionDecl(n *gotreesitter.Node) string {
	if t.implReceiver == "" {
		return t.emitDefault(n)
	}
	// Inside an impl block, add receiver to function declarations.
	// function_declaration: func name(params) returnType { body }
	// Transform to: func (self Type) name(params) returnType { body }
	text := t.emitDefault(n)
	if strings.HasPrefix(text, "func ") {
		return "func (self " + t.implReceiver + ") " + text[5:]
	}
	return text
}

// derive Stringer for Color -> generate interface impl methods
func (t *fwTranspiler) emitDerive(n *gotreesitter.Node) string {
	traitNode := t.childByField(n, "trait")
	typeNode := t.childByField(n, "type")
	if traitNode == nil || typeNode == nil {
		return t.text(n)
	}
	trait := t.text(traitNode)
	typeName := t.text(typeNode)

	var b strings.Builder
	switch trait {
	case "Stringer":
		t.needsFmt = true
		fmt.Fprintf(&b, "func (x %s) String() string {\n", typeName)
		fmt.Fprintf(&b, "\treturn fmt.Sprintf(\"%s(%%v)\", x)\n", typeName)
		b.WriteString("}\n")
	case "JSON":
		t.needsJSON = true
		fmt.Fprintf(&b, "func (x %s) MarshalJSON() ([]byte, error) {\n", typeName)
		fmt.Fprintf(&b, "\treturn json.Marshal(struct{ Value %s }{x})\n", typeName)
		b.WriteString("}\n")
	case "Equal":
		fmt.Fprintf(&b, "func (x %s) Equal(other %s) bool {\n", typeName, typeName)
		fmt.Fprintf(&b, "\treturn x == other\n")
		b.WriteString("}\n")
	default:
		fmt.Fprintf(&b, "// derive %s for %s: unknown trait\n", trait, typeName)
	}
	return b.String()
}

// if let x = expr { body } -> if x := expr; x != nil { body }
func (t *fwTranspiler) emitIfLet(n *gotreesitter.Node) string {
	pattern := t.childByField(n, "pattern")
	value := t.childByField(n, "value")
	if pattern == nil || value == nil {
		return t.text(n)
	}
	varName := t.text(pattern)
	valExpr := t.emit(value)

	var b strings.Builder
	fmt.Fprintf(&b, "if %s := %s; %s != nil", varName, valExpr, varName)

	// Find the first block (then-block)
	blockFound := false
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			if !blockFound {
				b.WriteString(" ")
				b.WriteString(t.emit(c))
				blockFound = true
			} else {
				// else block
				b.WriteString(" else ")
				b.WriteString(t.emit(c))
			}
		}
	}

	return b.String()
}

// 0..10 -> (kept as-is; used by for_in to generate range loop)
func (t *fwTranspiler) emitRange(n *gotreesitter.Node) string {
	start := t.childByField(n, "start")
	end := t.childByField(n, "end")
	if start == nil || end == nil {
		return t.text(n)
	}
	// Range expression is primarily consumed by for_in; if standalone, emit as comment
	return fmt.Sprintf("/* range %s..%s */", t.emit(start), t.emit(end))
}

// for v in iterable { body }
func (t *fwTranspiler) emitForIn(n *gotreesitter.Node) string {
	varNode := t.childByField(n, "var")
	iterable := t.childByField(n, "iterable")
	if varNode == nil || iterable == nil {
		return t.text(n)
	}

	varName := t.text(varNode)

	// Check if iterable is a range_expression (0..10)
	if t.nodeType(iterable) == "range_expression" {
		start := t.childByField(iterable, "start")
		end := t.childByField(iterable, "end")
		if start != nil && end != nil {
			// Find the block
			block := t.findBlock(n)
			if block != "" {
				return fmt.Sprintf("for %s := %s; %s < %s; %s++ %s",
					varName, t.emit(start), varName, t.emit(end), varName, block)
			}
		}
	}

	// General iterable: for _, v := range iterable { body }
	block := t.findBlock(n)
	return fmt.Sprintf("for _, %s := range %s %s", varName, t.emit(iterable), block)
}

// for i, v in iterable { body }
func (t *fwTranspiler) emitForInIndex(n *gotreesitter.Node) string {
	indexNode := t.childByField(n, "index")
	varNode := t.childByField(n, "var")
	iterable := t.childByField(n, "iterable")
	if indexNode == nil || varNode == nil || iterable == nil {
		return t.text(n)
	}

	block := t.findBlock(n)
	return fmt.Sprintf("for %s, %s := range %s %s",
		t.text(indexNode), t.text(varNode), t.emit(iterable), block)
}

// findBlock finds the first block child node and emits it.
func (t *fwTranspiler) findBlock(n *gotreesitter.Node) string {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			return t.emit(c)
		}
	}
	return "{}"
}

// f"hello {name}" -> fmt.Sprintf("hello %v", name)
// emitFString handles f"hello {name}" -> fmt.Sprintf("hello %v", name)
// The fstring node is a single token matching f"...", so we parse the text directly.
func (t *fwTranspiler) emitFString(n *gotreesitter.Node) string {
	raw := t.text(n) // e.g. f"hello {name}"
	if len(raw) < 3 || raw[0] != 'f' || raw[1] != '"' {
		return raw
	}
	inner := raw[2 : len(raw)-1] // strip f" and trailing "

	t.needsFmt = true

	var format strings.Builder
	var args []string
	i := 0
	for i < len(inner) {
		if inner[i] == '{' {
			// Find matching }
			j := i + 1
			depth := 1
			for j < len(inner) && depth > 0 {
				if inner[j] == '{' {
					depth++
				} else if inner[j] == '}' {
					depth--
				}
				j++
			}
			expr := inner[i+1 : j-1]
			format.WriteString("%v")
			args = append(args, expr)
			i = j
		} else {
			format.WriteByte(inner[i])
			i++
		}
	}

	if len(args) == 0 {
		return `"` + inner + `"` // no interpolation, return as regular string
	}

	return fmt.Sprintf("fmt.Sprintf(\"%s\", %s)", format.String(), strings.Join(args, ", "))
}

// guard cond else { return err } -> if !(cond) { return err }
func (t *fwTranspiler) emitGuard(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	if cond == nil {
		return t.text(n)
	}

	block := t.findBlock(n)
	return fmt.Sprintf("if !(%s) %s", t.emit(cond), block)
}

// defer! f.Close() -> defer func() { if _cerr := f.Close(); _cerr != nil && err == nil { err = _cerr } }()
func (t *fwTranspiler) emitDeferError(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	if expr == nil {
		return t.text(n)
	}
	e := t.emit(expr)
	return fmt.Sprintf("defer func() {\n\tif _cerr := %s; _cerr != nil && err == nil {\n\t\terr = _cerr\n\t}\n}()", e)
}

// impl Type { fn methods... } -> emit each function with (self Type) receiver
// Since Go's block rule parses `func Name()` as func_literal (not function_declaration),
// we extract the block text and perform string-level transformation.
func (t *fwTranspiler) emitImplBlock(n *gotreesitter.Node) string {
	typeNode := t.childByField(n, "type")
	if typeNode == nil {
		return t.text(n)
	}

	typeName := t.text(typeNode)

	// Find the block child and get its text
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			blockText := t.text(c)
			// Strip the outer { } braces
			if len(blockText) >= 2 && blockText[0] == '{' {
				blockText = blockText[1 : len(blockText)-1]
			}
			blockText = strings.TrimSpace(blockText)
			// Replace "func " with "func (self Type) " for each function in the block
			receiver := fmt.Sprintf("func (self %s) ", typeName)
			result := strings.ReplaceAll(blockText, "func ", receiver)
			return result + "\n"
		}
	}

	return t.text(n)
}

// unless cond { body } -> if !(cond) { body }
func (t *fwTranspiler) emitUnless(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	if cond == nil {
		return t.text(n)
	}
	block := t.findBlock(n)
	return fmt.Sprintf("if !(%s) %s", t.emit(cond), block)
}

// until cond { body } -> for !(cond) { body }
func (t *fwTranspiler) emitUntil(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	if cond == nil {
		return t.text(n)
	}
	block := t.findBlock(n)
	return fmt.Sprintf("for !(%s) %s", t.emit(cond), block)
}

// repeat 5 { body } -> for _i := 0; _i < 5; _i++ { body }
func (t *fwTranspiler) emitRepeat(n *gotreesitter.Node) string {
	count := t.childByField(n, "count")
	if count == nil {
		return t.text(n)
	}
	block := t.findBlock(n)
	return fmt.Sprintf("for _i := 0; _i < %s; _i++ %s", t.emit(count), block)
}

// [x*2 for x in items if x > 0] -> IIFE with range + filter
func (t *fwTranspiler) emitListComprehension(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	varNode := t.childByField(n, "var")
	iterable := t.childByField(n, "iterable")
	filter := t.childByField(n, "filter")
	if expr == nil || varNode == nil || iterable == nil {
		return t.text(n)
	}

	varName := t.text(varNode)
	var b strings.Builder
	fmt.Fprintf(&b, "func() []interface{} {\n")
	fmt.Fprintf(&b, "\tvar _result []interface{}\n")
	fmt.Fprintf(&b, "\tfor _, %s := range %s {\n", varName, t.emit(iterable))
	if filter != nil {
		fmt.Fprintf(&b, "\t\tif %s {\n", t.emit(filter))
		fmt.Fprintf(&b, "\t\t\t_result = append(_result, %s)\n", t.emit(expr))
		fmt.Fprintf(&b, "\t\t}\n")
	} else {
		fmt.Fprintf(&b, "\t\t_result = append(_result, %s)\n", t.emit(expr))
	}
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\treturn _result\n")
	fmt.Fprintf(&b, "}()")

	return b.String()
}

// swap(a, b) -> a, b = b, a
func (t *fwTranspiler) emitSwap(n *gotreesitter.Node) string {
	a := t.childByField(n, "a")
	b := t.childByField(n, "b")
	if a == nil || b == nil {
		return t.text(n)
	}
	return fmt.Sprintf("%s, %s = %s, %s", t.emit(a), t.emit(b), t.emit(b), t.emit(a))
}

// detectGenericTypes scans transpiled output for Result[T] and Option[T] usage.
func (t *fwTranspiler) detectGenericTypes(code string) {
	if strings.Contains(code, "Result[") || strings.Contains(code, "Ok[") || strings.Contains(code, "Err[") {
		t.needsResultType = true
	}
	if strings.Contains(code, "Option[") || strings.Contains(code, "Some[") || strings.Contains(code, "None[") {
		t.needsOptionType = true
	}
}

const resultTypeDef = `
// Result is a Rust-inspired Result type for explicit error handling.
type Result[T any] struct {
	val T
	err error
	ok  bool
}

func Ok[T any](v T) Result[T]    { return Result[T]{val: v, ok: true} }
func Err[T any](e error) Result[T] { return Result[T]{err: e} }
func (r Result[T]) Unwrap() T     { if !r.ok { panic(r.err) }; return r.val }
func (r Result[T]) UnwrapOr(def T) T {
	if !r.ok {
		return def
	}
	return r.val
}
func (r Result[T]) IsOk() bool  { return r.ok }
func (r Result[T]) IsErr() bool { return !r.ok }
func (r Result[T]) Map(f func(T) T) Result[T] {
	if r.ok {
		return Ok[T](f(r.val))
	}
	return r
}
func (r Result[T]) AndThen(f func(T) Result[T]) Result[T] {
	if r.ok {
		return f(r.val)
	}
	return r
}
`

const optionTypeDef = `
// Option is a Rust-inspired Option type for explicit nil handling.
type Option[T any] struct {
	val  T
	some bool
}

func Some[T any](v T) Option[T] { return Option[T]{val: v, some: true} }
func None[T any]() Option[T]    { return Option[T]{} }
func (o Option[T]) Unwrap() T   { if !o.some { panic("unwrap on None") }; return o.val }
func (o Option[T]) UnwrapOr(def T) T {
	if !o.some {
		return def
	}
	return o.val
}
func (o Option[T]) IsSome() bool { return o.some }
func (o Option[T]) IsNone() bool { return !o.some }
func (o Option[T]) Map(f func(T) T) Option[T] {
	if o.some {
		return Some[T](f(o.val))
	}
	return o
}
func (o Option[T]) Filter(f func(T) bool) Option[T] {
	if o.some && f(o.val) {
		return o
	}
	return None[T]()
}
`

// injectGenericTypes appends Result and Option type definitions at the end of
// the file when the transpiled code references them.
func (t *fwTranspiler) injectGenericTypes(code string) string {
	if !t.needsResultType && !t.needsOptionType {
		return code
	}
	var b strings.Builder
	b.WriteString(code)
	if t.needsResultType {
		b.WriteString(resultTypeDef)
	}
	if t.needsOptionType {
		b.WriteString(optionTypeDef)
	}
	return b.String()
}

// injectImports adds required imports (reflect, fmt) after transpilation.
// It looks for an existing import block and appends missing imports, or inserts
// a new import statement after the package clause if none exists.
func (t *fwTranspiler) injectImports(code string) string {
	var needed []string
	if t.needsReflect && !strings.Contains(code, `"reflect"`) {
		needed = append(needed, `"reflect"`)
	}
	if t.needsFmt && !strings.Contains(code, `"fmt"`) {
		needed = append(needed, `"fmt"`)
	}
	if t.needsJSON && !strings.Contains(code, `"encoding/json"`) {
		needed = append(needed, `"encoding/json"`)
	}
	if t.needsUnsafe && !strings.Contains(code, `"unsafe"`) {
		needed = append(needed, `"unsafe"`)
	}
	if t.needsRuntime && !strings.Contains(code, `"runtime"`) {
		needed = append(needed, `"runtime"`)
	}
	if t.needsOS && !strings.Contains(code, `"os"`) {
		needed = append(needed, `"os"`)
	}
	if t.needsSyscall && !strings.Contains(code, `"syscall"`) {
		needed = append(needed, `"syscall"`)
	}
	if t.needsSync && !strings.Contains(code, `"sync"`) {
		needed = append(needed, `"sync"`)
	}
	if t.needsTime && !strings.Contains(code, `"time"`) {
		needed = append(needed, `"time"`)
	}
	if len(needed) == 0 {
		return code
	}

	// Try to find an existing import block: import ( ... )
	if idx := strings.Index(code, "import ("); idx >= 0 {
		// Insert after the opening paren
		insertAt := idx + len("import (")
		var inject strings.Builder
		for _, imp := range needed {
			inject.WriteString("\n\t")
			inject.WriteString(imp)
		}
		return code[:insertAt] + inject.String() + code[insertAt:]
	}

	// Try to find a single import statement: import "pkg"
	if idx := strings.Index(code, "import "); idx >= 0 {
		// Find the end of this import line
		endIdx := strings.Index(code[idx:], "\n")
		if endIdx >= 0 {
			endIdx += idx
			var inject strings.Builder
			for _, imp := range needed {
				inject.WriteString("\nimport ")
				inject.WriteString(imp)
			}
			return code[:endIdx] + inject.String() + code[endIdx:]
		}
	}

	// No import at all — insert after package clause
	if idx := strings.Index(code, "\n"); idx >= 0 {
		var inject strings.Builder
		inject.WriteString("\n\nimport (")
		for _, imp := range needed {
			inject.WriteString("\n\t")
			inject.WriteString(imp)
		}
		inject.WriteString("\n)")
		return code[:idx] + inject.String() + code[idx:]
	}

	return code
}

// =============================================
// LOW-LEVEL MEMORY MANAGEMENT EMIT HANDLERS
// =============================================

// arena scratch { body } or arena scratch 1024*1024 { body }
// -> bump allocator with make([]byte, 0, size)
func (t *fwTranspiler) emitArena(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	t.needsUnsafe = true

	sizeExpr := "1 << 20" // default 1MB
	if sizeNode := t.childByField(n, "size"); sizeNode != nil {
		sizeExpr = t.emit(sizeNode)
	}

	block := t.findBlock(n)

	var b strings.Builder
	fmt.Fprintf(&b, "_arenaSize := %s\n", sizeExpr)
	fmt.Fprintf(&b, "_arena_%s := make([]byte, 0, _arenaSize)\n", name)
	fmt.Fprintf(&b, "_arenaAlloc_%s := func(size int) unsafe.Pointer {\n", name)
	fmt.Fprintf(&b, "\toff := len(_arena_%s)\n", name)
	fmt.Fprintf(&b, "\t_arena_%s = _arena_%s[:off+size]\n", name, name)
	fmt.Fprintf(&b, "\treturn unsafe.Pointer(&_arena_%s[off])\n", name)
	fmt.Fprintf(&b, "}\n")
	fmt.Fprintf(&b, "_ = _arenaAlloc_%s\n", name)
	fmt.Fprintf(&b, "defer func() { _arena_%s = nil }()\n", name)
	b.WriteString(block)
	return b.String()
}

// pin data -> runtime.KeepAlive + SetFinalizer(nil)
func (t *fwTranspiler) emitPin(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	t.needsRuntime = true

	var b strings.Builder
	fmt.Fprintf(&b, "_pin_%s := &%s\n", name, name)
	fmt.Fprintf(&b, "runtime.SetFinalizer(_pin_%s, nil)\n", name)
	fmt.Fprintf(&b, "defer runtime.KeepAlive(%s)", name)
	return b.String()
}

// unpin data -> runtime.KeepAlive at this point
func (t *fwTranspiler) emitUnpin(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	t.needsRuntime = true

	return fmt.Sprintf("runtime.KeepAlive(%s)", name)
}

// unsafe cast(expr, TargetType) -> *(*TargetType)(unsafe.Pointer(&expr))
func (t *fwTranspiler) emitUnsafeCast(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	targetType := t.childByField(n, "target_type")
	if expr == nil || targetType == nil {
		return t.text(n)
	}
	t.needsUnsafe = true

	return fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", t.emit(targetType), t.emit(expr))
}

// mmap file "data.bin" as data []byte { body }
func (t *fwTranspiler) emitMmap(n *gotreesitter.Node) string {
	pathNode := t.childByField(n, "path")
	nameNode := t.childByField(n, "name")
	if pathNode == nil || nameNode == nil {
		return t.text(n)
	}
	t.needsOS = true
	t.needsSyscall = true

	pathStr := t.text(pathNode)
	name := t.text(nameNode)
	block := t.findBlock(n)

	var b strings.Builder
	fmt.Fprintf(&b, "_f, _ := os.Open(%s)\n", pathStr)
	b.WriteString("defer _f.Close()\n")
	b.WriteString("_fi, _ := _f.Stat()\n")
	fmt.Fprintf(&b, "%s, _ := syscall.Mmap(int(_f.Fd()), 0, int(_fi.Size()), syscall.PROT_READ, syscall.MAP_SHARED)\n", name)
	fmt.Fprintf(&b, "defer syscall.Munmap(%s)\n", name)
	b.WriteString(block)
	return b.String()
}

// packed struct Foo { ... } -> pass through with alignment comment
func (t *fwTranspiler) emitPacked(n *gotreesitter.Node) string {
	decl := t.childByField(n, "decl")
	if decl == nil {
		return t.text(n)
	}
	return "// packed: manual alignment required\n" + t.emit(decl)
}

// vectorize for v in items { body } -> for loop with vectorize hint comment
func (t *fwTranspiler) emitVectorize(n *gotreesitter.Node) string {
	varNode := t.childByField(n, "var")
	rangeNode := t.childByField(n, "range")
	if varNode == nil || rangeNode == nil {
		return t.text(n)
	}

	varName := t.text(varNode)
	block := t.findBlock(n)

	// Check if range is a range_expression (0..N)
	if t.nodeType(rangeNode) == "range_expression" {
		start := t.childByField(rangeNode, "start")
		end := t.childByField(rangeNode, "end")
		if start != nil && end != nil {
			return fmt.Sprintf("// vectorize: compiler hint\nfor %s := %s; %s < %s; %s++ %s",
				varName, t.emit(start), varName, t.emit(end), varName, block)
		}
	}

	return fmt.Sprintf("// vectorize: compiler hint\nfor _, %s := range %s %s", varName, t.emit(rangeNode), block)
}

// =============================================
// CONCURRENCY EMIT HANDLERS
// =============================================

// select! { arm, arm, ... } -> Go select statement
func (t *fwTranspiler) emitSelectBlock(n *gotreesitter.Node) string {
	var b strings.Builder
	b.WriteString("select {\n")

	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) != "select_arm" {
			continue
		}

		// Check which kind of arm: var from chan, timeout duration, or default
		varNode := t.childByField(c, "var")
		chanNode := t.childByField(c, "chan")
		durNode := t.childByField(c, "duration")
		bodyNode := t.childByField(c, "body")

		if bodyNode == nil {
			continue
		}

		if varNode != nil && chanNode != nil {
			// var from chan => body
			fmt.Fprintf(&b, "case %s := <-%s:\n\t%s\n",
				t.text(varNode), t.emit(chanNode), t.emit(bodyNode))
		} else if durNode != nil {
			// timeout duration => body
			t.needsTime = true
			fmt.Fprintf(&b, "case <-time.After(%s):\n\t%s\n",
				t.emit(durNode), t.emit(bodyNode))
		} else {
			// default => body
			fmt.Fprintf(&b, "default:\n\t%s\n", t.emit(bodyNode))
		}
	}

	b.WriteString("}")
	return b.String()
}

// fan out workers, 10 { body } -> WaitGroup + goroutines
func (t *fwTranspiler) emitFanOut(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	countNode := t.childByField(n, "count")
	if nameNode == nil || countNode == nil {
		return t.text(n)
	}
	t.needsSync = true

	block := t.findBlock(n)

	var b strings.Builder
	fmt.Fprintf(&b, "var _wg_%s sync.WaitGroup\n", t.text(nameNode))
	fmt.Fprintf(&b, "for _wi := 0; _wi < %s; _wi++ {\n", t.emit(countNode))
	fmt.Fprintf(&b, "\t_wg_%s.Add(1)\n", t.text(nameNode))
	b.WriteString("\tgo func() {\n")
	fmt.Fprintf(&b, "\t\tdefer _wg_%s.Done()\n", t.text(nameNode))
	fmt.Fprintf(&b, "\t\t%s\n", block)
	b.WriteString("\t}()\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "_wg_%s.Wait()", t.text(nameNode))
	return b.String()
}

// fan in [ch1, ch2, ch3] -> merge channels IIFE
func (t *fwTranspiler) emitFanIn(n *gotreesitter.Node) string {
	t.needsSync = true

	// Collect all channel expressions
	var channels []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) != "comment" {
			channels = append(channels, t.emit(c))
		}
	}
	if len(channels) == 0 {
		return t.text(n)
	}

	var b strings.Builder
	b.WriteString("func() <-chan interface{} {\n")
	b.WriteString("\tout := make(chan interface{})\n")
	b.WriteString("\tvar wg sync.WaitGroup\n")
	fmt.Fprintf(&b, "\tfor _, _ch := range []<-chan interface{}{%s} {\n", strings.Join(channels, ", "))
	b.WriteString("\t\twg.Add(1)\n")
	b.WriteString("\t\tgo func(c <-chan interface{}) {\n")
	b.WriteString("\t\t\tdefer wg.Done()\n")
	b.WriteString("\t\t\tfor v := range c { out <- v }\n")
	b.WriteString("\t\t}(_ch)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tgo func() { wg.Wait(); close(out) }()\n")
	b.WriteString("\treturn out\n")
	b.WriteString("}()")
	return b.String()
}

// left |> right -> right(left)
func (t *fwTranspiler) emitPipeline(n *gotreesitter.Node) string {
	left := t.childByField(n, "left")
	right := t.childByField(n, "right")
	if left == nil || right == nil {
		return t.text(n)
	}
	return fmt.Sprintf("%s(%s)", t.emit(right), t.emit(left))
}

// concurrent { stmt1; stmt2 } -> WaitGroup wrapping each statement
func (t *fwTranspiler) emitConcurrent(n *gotreesitter.Node) string {
	t.needsSync = true

	// Find the block, then find the statement_list inside it
	var stmtListNode *gotreesitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			// Inside a block, look for statement_list
			for j := 0; j < int(c.NamedChildCount()); j++ {
				sc := c.NamedChild(j)
				if t.nodeType(sc) == "statement_list" {
					stmtListNode = sc
					break
				}
			}
			if stmtListNode == nil {
				stmtListNode = c // fallback to block itself
			}
			break
		}
	}
	if stmtListNode == nil {
		return t.text(n)
	}

	// Collect all statement children
	var stmts []string
	for i := 0; i < int(stmtListNode.NamedChildCount()); i++ {
		c := stmtListNode.NamedChild(i)
		stmts = append(stmts, t.emit(c))
	}
	if len(stmts) == 0 {
		return "// concurrent: empty block"
	}

	var b strings.Builder
	b.WriteString("var _wg sync.WaitGroup\n")
	fmt.Fprintf(&b, "_wg.Add(%d)\n", len(stmts))
	for _, stmt := range stmts {
		fmt.Fprintf(&b, "go func() {\n\tdefer _wg.Done()\n\t%s\n}()\n", stmt)
	}
	b.WriteString("_wg.Wait()")
	return b.String()
}

// throttle 100 { body } -> time.Ticker rate limiting
func (t *fwTranspiler) emitThrottle(n *gotreesitter.Node) string {
	rateNode := t.childByField(n, "rate")
	if rateNode == nil {
		return t.text(n)
	}
	t.needsTime = true

	block := t.findBlock(n)

	// Configurable: throttle 100 burst 10 { }
	// Default burst: 1 (no burst)
	burst := ""
	if b := t.childByField(n, "burst"); b != nil {
		burst = t.emit(b)
	}

	rate := t.emit(rateNode)
	var b strings.Builder
	if burst != "" {
		fmt.Fprintf(&b, "// throttle: %s/s, burst: %s\n", rate, burst)
		fmt.Fprintf(&b, "_throttleBurst := %s\n", burst)
		fmt.Fprintf(&b, "_ = _throttleBurst\n")
	} else {
		fmt.Fprintf(&b, "// throttle: %s/s\n", rate)
	}
	fmt.Fprintf(&b, "_ticker := time.NewTicker(time.Second / time.Duration(%s))\n", rate)
	b.WriteString("defer _ticker.Stop()\n")
	b.WriteString("<-_ticker.C\n")
	b.WriteString(block)
	return b.String()
}

// retry 3 { body } -> retry loop with exponential backoff
func (t *fwTranspiler) emitRetry(n *gotreesitter.Node) string {
	countNode := t.childByField(n, "count")
	if countNode == nil {
		return t.text(n)
	}
	t.needsTime = true

	block := t.findBlock(n)

	// Configurable: retry 5 delay 500 backoff 2 { }
	// Defaults: 100ms initial delay, 2x exponential backoff
	delay := "100"
	backoff := "2"
	if d := t.childByField(n, "delay"); d != nil {
		delay = t.emit(d)
	}
	if b := t.childByField(n, "backoff"); b != nil {
		backoff = t.emit(b)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// retry %s times (delay: %sms, backoff: %sx)\n", t.emit(countNode), delay, backoff)
	b.WriteString("var _retryErr error\n")
	fmt.Fprintf(&b, "_retryDelay := time.Duration(%s) * time.Millisecond\n", delay)
	fmt.Fprintf(&b, "for _attempt := 0; _attempt < %s; _attempt++ {\n", t.emit(countNode))
	fmt.Fprintf(&b, "\t_retryErr = func() error {\n\t\t%s\n\t\treturn nil\n\t}()\n", block)
	b.WriteString("\tif _retryErr == nil { break }\n")
	b.WriteString("\ttime.Sleep(_retryDelay)\n")
	fmt.Fprintf(&b, "\t_retryDelay = time.Duration(float64(_retryDelay) * %s)\n", backoff)
	b.WriteString("}\n")
	b.WriteString("_ = _retryErr")
	return b.String()
}

// breaker "service" { body } -> circuit breaker logic
func (t *fwTranspiler) emitBreaker(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	t.needsTime = true
	t.needsSync = true

	nameStr := t.text(nameNode)
	varName := strings.Trim(nameStr, `"`)
	varName = strings.NewReplacer("-", "_", " ", "_").Replace(varName)
	block := t.findBlock(n)

	// Configurable: breaker "name" threshold 10 cooldown 60 { }
	// Defaults: 5 failures, 30 seconds cooldown
	threshold := "5"
	cooldown := "30"
	if th := t.childByField(n, "threshold"); th != nil {
		threshold = t.emit(th)
	}
	if cd := t.childByField(n, "cooldown"); cd != nil {
		cooldown = t.emit(cd)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// Circuit breaker %s (threshold: %s failures, cooldown: %ss)\n", nameStr, threshold, cooldown)
	fmt.Fprintf(&b, "var _breaker_%s_mu sync.Mutex\n", varName)
	fmt.Fprintf(&b, "var _breaker_%s_failures int\n", varName)
	fmt.Fprintf(&b, "var _breaker_%s_lastFail time.Time\n", varName)
	fmt.Fprintf(&b, "_breaker_%s_mu.Lock()\n", varName)
	fmt.Fprintf(&b, "_breaker_%s_open := _breaker_%s_failures >= %s && time.Since(_breaker_%s_lastFail) < %s*time.Second\n",
		varName, varName, threshold, varName, cooldown)
	fmt.Fprintf(&b, "_breaker_%s_mu.Unlock()\n", varName)
	fmt.Fprintf(&b, "if !_breaker_%s_open {\n", varName)
	fmt.Fprintf(&b, "\tfunc() {\n")
	fmt.Fprintf(&b, "\t\tdefer func() {\n")
	fmt.Fprintf(&b, "\t\t\t_breaker_%s_mu.Lock()\n", varName)
	fmt.Fprintf(&b, "\t\t\tdefer _breaker_%s_mu.Unlock()\n", varName)
	fmt.Fprintf(&b, "\t\t\tif r := recover(); r != nil {\n")
	fmt.Fprintf(&b, "\t\t\t\t_breaker_%s_failures++\n", varName)
	fmt.Fprintf(&b, "\t\t\t\t_breaker_%s_lastFail = time.Now()\n", varName)
	fmt.Fprintf(&b, "\t\t\t} else {\n")
	fmt.Fprintf(&b, "\t\t\t\t_breaker_%s_failures = 0\n", varName)
	fmt.Fprintf(&b, "\t\t\t}\n")
	fmt.Fprintf(&b, "\t\t}()\n")
	fmt.Fprintf(&b, "\t\t%s\n", block)
	fmt.Fprintf(&b, "\t}()\n")
	fmt.Fprintf(&b, "}")
	return b.String()
}
