package ferrouswheel

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestTranspileUntypedTopLevelConstNoCrash regression-protects the
// nil-text panic in collectConstDecl (and collectVarDecl). Untyped
// const/var declarations have no `type` field on the spec node, so
// `childByField(spec, "type")` returns nil. Before the fix, passing
// nil to collector.text() dereferenced a nil node inside
// gotreesitter.(*Node).StartByte() and SIGSEGV'd the whole CLI on any
// .fw file with a top-level `const x = 42` or `var y = "hi"`.
// Trivial to reproduce; previously took ferrous-wheel down for any
// non-trivial real-world script.
func TestTranspileUntypedTopLevelConstNoCrash(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "single untyped const",
			source: `package main

import "fmt"

const myConst = 42

func main() {
	fmt.Println(myConst)
}
`,
			want: "const myConst = 42",
		},
		{
			name: "block untyped const",
			source: `package main

import "fmt"

const (
	a = 1
	b = 2
)

func main() {
	fmt.Println(a, b)
}
`,
			want: "a = 1",
		},
		{
			name: "single untyped var",
			source: `package main

import "fmt"

var myVar = "hello"

func main() {
	fmt.Println(myVar)
}
`,
			want: `myVar = "hello"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goCode, err := Transpile([]byte(tc.source))
			if err != nil {
				t.Fatalf("transpile: %v", err)
			}
			if !strings.Contains(goCode, tc.want) {
				t.Fatalf("expected %q in output, got:\n%s", tc.want, goCode)
			}
		})
	}
}

func TestTranspileTryShortVarReturn(t *testing.T) {
	source := []byte(`package main

func load() (string, error) { return "ok", nil }

func f() (string, error) {
	x := try load()
	return x, nil
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "x, _fwTryErr0 := load()") {
		t.Fatalf("expected try binding rewrite, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, "return *new(string), _fwTryErr0") {
		t.Fatalf("expected function-level propagation return, got:\n%s", goCode)
	}
}

func TestTranspileTryRetryBinding(t *testing.T) {
	source := []byte(`package main

func load() (string, error) { return "ok", nil }

func main() {
	retry 3 {
		x := try load()
		_ = x
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "x, _fwTryErr0 := load()") {
		t.Fatalf("expected try binding inside retry, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, "return _fwTryErr0") {
		t.Fatalf("expected retry-local error propagation, got:\n%s", goCode)
	}
}

func TestTranspileTryTupleLetBinding(t *testing.T) {
	source := []byte(`package main

func load() (string, int, error) { return "ok", 7, nil }

func f() (string, error) {
	let (name, size) = try load()
	return name, nil
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "name, size, _fwTryErr0 := load()") {
		t.Fatalf("expected tuple try binding rewrite, got:\n%s", goCode)
	}
}

func TestTranspilePipelineIntoSelector(t *testing.T) {
	source := []byte(`package main

import "fmt"

func main() {
	x := "hello" |> fmt.Sprint
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, `fmt.Sprint("hello")`) {
		t.Fatalf("expected selector pipeline rewrite, got:\n%s", goCode)
	}
}

func TestTranspileSelectorExpressionFallsBackToPlainSelector(t *testing.T) {
	source := []byte(`package main

type user struct {
	Name string
}

func main() {
	u := user{Name: "alice"}
	_ = u.Name
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "u.Name") {
		t.Fatalf("expected plain selector expression to remain intact, got:\n%s", goCode)
	}
}

// TestTranspileStandaloneRangeExpression regression-protects the emitRange
// fix in transpile.go: a range expression used outside a for-in loop or
// list comprehension has no valid Go translation (Go has no range
// value/type usable in an arbitrary expression position), so it must be a
// clear transpile error — not the old silent `/* range 1..3 */` comment,
// which produced generated Go that doesn't parse (`_ = /* range 1..3 */`
// isn't a valid assignment). See also TestTranspileRangeIndexExpression
// for the fuzz-discovered variant of this same bug
// (arr[0..2] as a subscript).
func TestTranspileStandaloneRangeExpression(t *testing.T) {
	source := []byte(`package main

func main() {
	_ = 1 .. 3
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected an error for a standalone range expression, got nil")
	}
	if !strings.Contains(err.Error(), "only valid as the iterable") {
		t.Fatalf("expected a range-misuse error, got: %v", err)
	}
}

// TestTranspileRangeIndexExpression regression-protects against a range
// expression used as an index/subscript (`arr[0..2]`), found by
// FuzzTranspileProducesParsableGo as `0[0..0]` (which previously emitted
// `0[/* range 0..0 */]`, invalid Go — see fuzz corpus entry
// testdata/fuzz/FuzzTranspileProducesParsableGo/1b96d5120dea47ff).
func TestTranspileRangeIndexExpression(t *testing.T) {
	source := []byte(`package main

func main() {
	xs := []int{1, 2, 3}
	_ = xs[0 .. 2]
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected an error for a range expression used as an index, got nil")
	}
	if !strings.Contains(err.Error(), "only valid as the iterable") {
		t.Fatalf("expected a range-misuse error, got: %v", err)
	}
}

// TestTranspileRangeComprehensionNoFalseError regression-protects against a
// bug introduced (and caught before it shipped) alongside the emitRange
// fix: emitListComprehension used to unconditionally call t.emit(iterable)
// to build a default loop header string that gets discarded and replaced
// whenever the iterable is a range_expression. Since emitRange now records
// a transpileError as a side effect for any range not directly consumed by
// its caller, that speculative-then-discarded call wrongly flagged every
// range-based comprehension as misuse, even though the actual loop it
// produces is correct.
func TestTranspileRangeComprehensionNoFalseError(t *testing.T) {
	source := []byte(`package main

func main() {
	squares := [x * x for x in 0 .. 10]
	_ = squares
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "for x := 0; x < 10; x++") {
		t.Fatalf("expected a counting loop, got:\n%s", goCode)
	}
}

// TestTranspileForInBlankRangeVar regression-protects `for _ in 0..N` (and
// the comprehension equivalent `[... for _ in 0..N]`) — a reasonable way to
// write "repeat N times, discard the value" — which previously transpiled
// to `for _ := 0; _ < N; _++`, invalid Go since the blank identifier can be
// assigned but never read.
func TestTranspileForInBlankRangeVar(t *testing.T) {
	source := []byte(`package main
import "fmt"

func main() {
	for _ in 0 .. 3 {
		fmt.Println("tick")
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if strings.Contains(goCode, "for _ :=") || strings.Contains(goCode, "_++") {
		t.Fatalf("expected a synthetic counter variable, not a re-read/incremented blank identifier, got:\n%s", goCode)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "generated.go", goCode, parser.AllErrors); err != nil {
		t.Fatalf("generated Go should parse: %v\n%s", err, goCode)
	}
}

func TestTranspileRejectsTopLevelOnlyConstructsInsideFunctions(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "enum",
			source: `package main

func main() {
	enum Color { Red, Blue(int) }
	_ = 1
}
`,
			want: "enum_declaration must appear at top level",
		},
		{
			name: "derive",
			source: `package main

func main() {
	derive Stringer for Color
}
`,
			want: "derive_declaration must appear at top level",
		},
		{
			name: "impl",
			source: `package main

func main() {
	impl Color {
		func String() string { return "" }
	}
}
`,
			want: "impl_block must appear at top level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Transpile([]byte(tt.source))
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestTranspileRejectsConcurrentDeclarations(t *testing.T) {
	source := []byte(`package main

func fetchUsers() []string {
	return nil
}

func main() {
	concurrent {
		let users = fetchUsers()
	}
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "concurrent block cannot contain variable declarations") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "var users []string") {
		t.Fatalf("expected suggested predeclaration, got %v", err)
	}
	if !strings.Contains(err.Error(), "users = fetchUsers()") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTranspileRejectsBreakerReturns(t *testing.T) {
	source := []byte(`package main

func main() {
	breaker "svc" {
		return
	}
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "return_statement is not supported inside breaker blocks") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTranspileRejectsThrottleLoops(t *testing.T) {
	source := []byte(`package main

func main() {
	throttle 100 {
		for i in items {
			_ = i
		}
	}
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "for_in_statement is not supported inside throttle blocks") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTranspileRejectsMmapNonByteSliceTargets(t *testing.T) {
	source := []byte(`package main

func main() {
	mmap file "data.bin" as data int {
		_ = data
	}
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "mmap_block currently requires []byte target type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTranspileRejectsUnsupportedTrySites(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "standalone statement",
			source: `package main

func f() error {
	try write()
	return nil
}
`,
			want: "try is only supported on the right-hand side of let, tuple let, :=, or = assignments",
		},
		{
			name: "nested expression",
			source: `package main

func load() (string, error) { return "", nil }

func f() error {
	fmt.Println(try load())
	return nil
}
`,
			want: "try is only supported on the right-hand side of let, tuple let, :=, or = assignments",
		},
		{
			name: "non call expression",
			source: `package main

func f() error {
	x := try value
	_ = x
	return nil
}
`,
			want: "try currently only supports direct call expressions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Transpile([]byte(tt.source))
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestTranspileRejectsTryWithoutTrailingError(t *testing.T) {
	source := []byte(`package main

func load() (string, error) { return "", nil }

func f() {
	x := try load()
	_ = x
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "try requires the enclosing function_declaration to return a trailing error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTranspileRejectsTryAcrossUnsupportedBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "lambda",
			source: `package main

func load() (string, error) { return "", nil }

func f() error {
	g := fn(v) {
		v = try load()
		_ = v
	}
	_ = g
	return nil
}
`,
			want: "try is not supported inside lambda expressions",
		},
		{
			name: "concurrent",
			source: `package main

func load() (string, error) { return "", nil }

func f() error {
	var x string
	concurrent {
		x = try load()
		_ = x
	}
	return nil
}
`,
			want: "try is not supported directly inside concurrent_block",
		},
		{
			name: "breaker",
			source: `package main

func load() (string, error) { return "", nil }

func f() error {
	var x string
	breaker "svc" {
		x = try load()
		_ = x
	}
	return nil
}
`,
			want: "try is not supported directly inside breaker_block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Transpile([]byte(tt.source))
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestTranspileRejectsRecoveredGarbageTokens(t *testing.T) {
	source := []byte(`package mx n

func main() {
	let ai = 1
	_ = x
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "parse errors in ferrous-wheel source") {
		t.Fatalf("unexpected error: %v", err)
	}
}
