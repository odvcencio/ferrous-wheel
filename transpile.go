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
	return t.emit(root), nil
}

type fwTranspiler struct {
	src  []byte
	lang *gotreesitter.Language
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
			body := t.childByField(c, "body")
			if pattern != nil && body != nil {
				fmt.Fprintf(&b, "case %s:\n\treturn %s\n", t.emit(pattern), t.emit(body))
			}
		}
	}

	b.WriteString("default:\n\treturn nil\n}\n}()")
	return b.String()
}

// val ?? "default" -> nil-coalescing
func (t *fwTranspiler) emitNullCoalesce(n *gotreesitter.Node) string {
	left := t.childByField(n, "left")
	right := t.childByField(n, "right")
	if left == nil || right == nil {
		return t.text(n)
	}
	l := t.emit(left)
	return fmt.Sprintf("func() interface{} { if %s != nil { return %s }; return %s }()", l, l, t.emit(right))
}

// try expr -> error propagation
func (t *fwTranspiler) emitErrorProp(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	if expr == nil {
		return t.text(n)
	}
	e := t.emit(expr)
	return fmt.Sprintf("func() interface{} { _v, _err := %s; if _err != nil { return _err }; return _v }()", e)
}

// obj?.field -> nil-safe field access
func (t *fwTranspiler) emitSafeNav(n *gotreesitter.Node) string {
	obj := t.childByField(n, "object")
	field := t.childByField(n, "field")
	if obj == nil || field == nil {
		return t.text(n)
	}
	o := t.emit(obj)
	f := t.text(field)
	return fmt.Sprintf("func() interface{} { if %s != nil { return %s.%s }; return nil }()", o, o, f)
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
