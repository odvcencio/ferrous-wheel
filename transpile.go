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
	src            []byte
	lang           *gotreesitter.Language
	needsReflect   bool
	needsFmt       bool
	needsResultType bool
	needsOptionType bool
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
