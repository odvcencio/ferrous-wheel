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

// --- Feature 1: Ternary Operator ---

func TestTernaryExpression(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := true ? 1 : 0
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "ternary_expression") {
		t.Error("expected ternary_expression")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestTernaryNested(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := a ? b : c ? d : e
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "ternary_expression") {
		t.Error("expected ternary_expression")
	}
}

// --- Feature 3: Let Multi-Declaration ---

func TestLetMultiDeclaration(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	let (a, b) = getPair()
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "let_multi_declaration") {
		t.Error("expected let_multi_declaration")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestLetMultiThreeVars(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	let (x, y, z) = getTriple()
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "let_multi_declaration") {
		t.Error("expected let_multi_declaration")
	}
}

// --- Feature 4: Match Guards ---

func TestMatchGuard(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := match val {
		n if n > 0 => "positive",
		n if n < 0 => "negative",
		0 => "zero",
	}
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "match_expression") {
		t.Error("expected match_expression")
	}
	if !strings.Contains(sexp, "match_arm") {
		t.Error("expected match_arm")
	}
}

// === New Feature Parse Tests ===

func TestDerive(t *testing.T) {
	sexp := parseFW(t, `package main
derive Stringer for Color
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "derive_declaration") {
		t.Error("expected derive_declaration")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestDeriveJSON(t *testing.T) {
	sexp := parseFW(t, `package main
derive JSON for Config
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "derive_declaration") {
		t.Error("expected derive_declaration")
	}
}

func TestDeriveEqual(t *testing.T) {
	sexp := parseFW(t, `package main
derive Equal for Point
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "derive_declaration") {
		t.Error("expected derive_declaration")
	}
}

func TestIfLet(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	if let x = getValue() {
		_ = x
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "if_let_statement") {
		t.Error("expected if_let_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestIfLetElse(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	if let x = getValue() {
		_ = x
	} else {
		panic("nil")
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "if_let_statement") {
		t.Error("expected if_let_statement")
	}
}

func TestForIn(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	for v in items {
		_ = v
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "for_in_statement") {
		t.Error("expected for_in_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestForInRange(t *testing.T) {
	// Note: spaces around ".." are required to avoid Go float literal conflict
	sexp := parseFW(t, `package main
func f() {
	for i in 0 .. 10 {
		_ = i
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "for_in_statement") {
		t.Error("expected for_in_statement")
	}
	if !strings.Contains(sexp, "range_expression") {
		t.Error("expected range_expression")
	}
}

func TestForInIndex(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	for i, v in items {
		_ = i
		_ = v
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "for_in_index_statement") {
		t.Error("expected for_in_index_statement")
	}
}

func TestFString(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := f"hello {name}"
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "fstring") {
		t.Error("expected fstring")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestGuard(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	guard x > 0 else {
		return
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "guard_statement") {
		t.Error("expected guard_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestDeferError(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	defer! f.Close()
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "defer_error") {
		t.Error("expected defer_error")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestImplBlock(t *testing.T) {
	sexp := parseFW(t, `package main
impl Point {
	func Area() int {
		return 0
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "impl_block") {
		t.Error("expected impl_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestUnless(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	unless x > 0 {
		return
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "unless_statement") {
		t.Error("expected unless_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestUntil(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	until x > 10 {
		x = x + 1
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "until_statement") {
		t.Error("expected until_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestRepeat(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	repeat 5 {
		fmt.Println("hi")
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "repeat_statement") {
		t.Error("expected repeat_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestListComprehension(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := [x for x in items]
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "list_comprehension") {
		t.Error("expected list_comprehension")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestListComprehensionFilter(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := [x for x in items if x > 0]
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "list_comprehension") {
		t.Error("expected list_comprehension")
	}
}

func TestSwap(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	swap(a, b)
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "swap_statement") {
		t.Error("expected swap_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestDeriveInFunction(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	derive Stringer for Color
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "derive_declaration") {
		t.Error("expected derive_declaration in function body")
	}
}

// =============================================
// LOW-LEVEL MEMORY MANAGEMENT PARSE TESTS
// =============================================

func TestArenaBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	arena scratch {
		x := 1
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "arena_block") {
		t.Error("expected arena_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestArenaBlockWithSize(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	arena scratch 1024 {
		x := 1
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "arena_block") {
		t.Error("expected arena_block with size")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestPinStatement(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	pin data
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "pin_statement") {
		t.Error("expected pin_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestUnpinStatement(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	unpin data
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "unpin_statement") {
		t.Error("expected unpin_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestUnsafeCast(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := unsafe cast(s, int)
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "unsafe_cast") {
		t.Error("expected unsafe_cast")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestMmapBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	mmap file "data.bin" as data int {
		_ = data
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "mmap_block") {
		t.Error("expected mmap_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestPackedAnnotation(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	packed let x = 1
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "packed_annotation") {
		t.Error("expected packed_annotation")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestVectorizeStatement(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	vectorize for v in items {
		_ = v
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "vectorize_statement") {
		t.Error("expected vectorize_statement")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

// =============================================
// CONCURRENCY PARSE TESTS
// =============================================

func TestSelectBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	select! {
		msg from inbox => process(msg),
		timeout 5 => log(x),
		default => noop(),
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "select_block") {
		t.Error("expected select_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestFanOutBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	fan out workers, 10 {
		doWork()
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "fan_out_block") {
		t.Error("expected fan_out_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestFanInExpression(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	merged := fan in [ch1, ch2, ch3]
	_ = merged
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "fan_in_expression") {
		t.Error("expected fan_in_expression")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestPipelineExpression(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	x := data |> transform
	_ = x
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "pipeline_expression") {
		t.Error("expected pipeline_expression")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestConcurrentBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	concurrent {
		fetch("url1")
		fetch("url2")
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "concurrent_block") {
		t.Error("expected concurrent_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestThrottleBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	throttle 100 {
		doWork()
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "throttle_block") {
		t.Error("expected throttle_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestRetryBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	retry 3 {
		doWork()
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "retry_block") {
		t.Error("expected retry_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}

func TestBreakerBlock(t *testing.T) {
	sexp := parseFW(t, `package main
func f() {
	breaker "myservice" {
		callService()
	}
}
`)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "breaker_block") {
		t.Error("expected breaker_block")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("ERROR: %s", sexp)
	}
}
