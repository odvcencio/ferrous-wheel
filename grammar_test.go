package ferrouswheel

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

var fwLang *gotreesitter.Language

func getFWLang(t *testing.T) *gotreesitter.Language {
	t.Helper()
	if fwLang != nil {
		return fwLang
	}
	lang, err := GenerateLanguage(Grammar())
	if err != nil {
		t.Fatalf("GenerateLanguage(Grammar): %v", err)
	}
	fwLang = lang
	return lang
}

func parseFW(t *testing.T, input string) string {
	t.Helper()
	lang := getFWLang(t)
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("Root node is nil")
	}
	return root.SExpr(lang)
}

func TestGoCompat(t *testing.T) {
	samples := []struct {
		name  string
		input string
	}{
		{
			"package_only",
			"package main\n",
		},
		{
			"hello_world",
			`package main

func main() {
	fmt.Println("hi")
}
`,
		},
		{
			"var_decl",
			`package main

var x int = 1
`,
		},
		{
			"if_else",
			`package main

func f() {
	if x > 0 {
		return
	} else {
		x = 0
	}
}
`,
		},
		{
			"for_loop",
			`package main

func f() {
	for i := 0; i < 10; i++ {
		_ = i
	}
}
`,
		},
	}
	for _, tt := range samples {
		t.Run(tt.name, func(t *testing.T) {
			sexp := parseFW(t, tt.input)
			t.Logf("SExpr: %s", sexp)
			if strings.Contains(sexp, "ERROR") {
				t.Errorf("pure Go should parse clean, got ERROR in: %s", sexp)
			}
		})
	}
}

func TestEnum(t *testing.T) {
	sexp := parseFW(t, `package main
enum Color {
	Red,
	Green,
	Blue(int),
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "enum_declaration") {
		t.Error("expected enum_declaration")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestEnumSimple(t *testing.T) {
	sexp := parseFW(t, `package main
enum Direction {
	North,
	South,
	East,
	West,
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "enum_declaration") {
		t.Error("expected enum_declaration")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestLet(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	let x = 42
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "let_declaration") {
		t.Error("expected let_declaration")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestMatch(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := match val {
		1 => "one",
		2 => "two",
	}
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "match_expression") {
		t.Error("expected match_expression")
	}
}

func TestNullCoalesce(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := val ?? "default"
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "null_coalesce") {
		t.Error("expected null_coalesce")
	}
}

func TestLambda(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	f := fn(x) x
	_ = f
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "lambda_expression") {
		t.Error("expected lambda_expression")
	}
}

func TestLambdaMultiParam(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	add := fn(x, y) x
	_ = add
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "lambda_expression") {
		t.Error("expected lambda_expression")
	}
	if !strings.Contains(sexp, "lambda_params") {
		t.Error("expected lambda_params")
	}
}

func TestErrorPropagation(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := try doSomething()
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "error_propagation") {
		t.Error("expected error_propagation")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestSafeNavigation(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := obj?.name
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "safe_navigation") {
		t.Error("expected safe_navigation")
	}
}

func TestLambdaBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	greet := fn(name) {
		fmt.Println(name)
	}
	_ = greet
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "lambda_expression") {
		t.Error("expected lambda_expression")
	}
}

func TestLambdaBinaryExpr(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	double := fn(x) x * 2
	_ = double
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "lambda_expression") {
		t.Error("expected lambda_expression")
	}
	// The body of the lambda should be a binary_expression, not just an identifier
	if !strings.Contains(sexp, "binary_expression") {
		t.Errorf("expected binary_expression in lambda body, body should capture 'x * 2' not just 'x'")
	}
}
