package ferrouswheel

import (
	"strings"
	"testing"
)

func TestTranspileLet(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = 42
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "x := 42") {
		t.Error("expected x := 42")
	}
	if strings.Contains(goCode, "let") {
		t.Error("should not contain 'let'")
	}
}

func TestTranspileEnum(t *testing.T) {
	source := []byte(`package main

enum Color {
	Red,
	Green,
	Blue(int),
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "type Color struct") {
		t.Error("expected Color struct")
	}
	if !strings.Contains(goCode, "ColorRed") {
		t.Error("expected ColorRed constant")
	}
	if !strings.Contains(goCode, "func Blue(") {
		t.Error("expected Blue constructor")
	}
}

func TestTranspileNullCoalesce(t *testing.T) {
	source := []byte(`package main

func f() {
	x := val ?? "default"
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected reflect.ValueOf zero-value check")
	}
	if !strings.Contains(goCode, `"reflect"`) {
		t.Error("expected reflect import to be injected")
	}
}

func TestTranspileNullCoalesceNonNil(t *testing.T) {
	source := []byte(`package main

func f() {
	let name = ""
	x := name ?? "anonymous"
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected reflect.ValueOf zero-value check (works for non-nillable string)")
	}
	if !strings.Contains(goCode, "IsZero") {
		t.Error("expected IsZero check for zero-value detection")
	}
	if !strings.Contains(goCode, `"reflect"`) {
		t.Error("expected reflect import to be injected")
	}
}

func TestTranspileLambdaExpr(t *testing.T) {
	// fn(x) x * 2 should parse with the body capturing the full binary expression
	source := []byte(`package main

func f() {
	double := fn(x) x * 2
	_ = double
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func(x interface{}) interface{}") {
		t.Error("expected func literal with interface{} param")
	}
	// The body should contain the full expression "x * 2", not just "x"
	if !strings.Contains(goCode, "return x * 2") {
		t.Errorf("expected lambda body to capture full binary expression 'x * 2', got:\n%s", goCode)
	}
}

func TestTranspileMatchExhaustive(t *testing.T) {
	source := []byte(`package main

func f() {
	x := match val {
		1 => "one",
		2 => "two",
	}
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "panic(") {
		t.Error("expected panic in default case for exhaustive match")
	}
	if !strings.Contains(goCode, "non-exhaustive match") {
		t.Error("expected descriptive panic message")
	}
}

func TestTranspileSafeNavInjectsReflect(t *testing.T) {
	source := []byte(`package main

func f() {
	x := obj?.name
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected reflect.ValueOf in safe navigation")
	}
	if !strings.Contains(goCode, `"reflect"`) {
		t.Error("expected reflect import to be auto-injected")
	}
}

// --- Feature 1: Ternary Operator ---

func TestTranspileTernary(t *testing.T) {
	source := []byte(`package main

func f() {
	x := true ? 1 : 0
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func() interface{}") {
		t.Error("expected IIFE wrapper for ternary")
	}
	if !strings.Contains(goCode, "if true") {
		t.Error("expected condition in if statement")
	}
	if !strings.Contains(goCode, "return 1") {
		t.Error("expected consequence return")
	}
	if !strings.Contains(goCode, "return 0") {
		t.Error("expected alternative return")
	}
	if strings.Contains(goCode, "?") && !strings.Contains(goCode, "func()") {
		t.Error("ternary operator should be fully transpiled away")
	}
}

func TestTranspileTernaryNested(t *testing.T) {
	source := []byte(`package main

func f() {
	x := a ? b : c ? d : e
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// Should contain nested IIFE calls
	if strings.Count(goCode, "func() interface{}") < 2 {
		t.Error("expected at least 2 IIFE wrappers for nested ternary")
	}
}

// --- Feature 2: Result/Option Types ---

func TestTranspileResultType(t *testing.T) {
	source := []byte(`package main

func divide(a, b int) Result[int] {
	if b == 0 {
		return Err[int](fmt.Errorf("division by zero"))
	}
	return Ok[int](a / b)
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "type Result[T any] struct") {
		t.Error("expected Result type definition to be injected")
	}
	if !strings.Contains(goCode, "func Ok[T any]") {
		t.Error("expected Ok constructor to be injected")
	}
	if !strings.Contains(goCode, "func Err[T any]") {
		t.Error("expected Err constructor to be injected")
	}
	if !strings.Contains(goCode, "func (r Result[T]) Unwrap()") {
		t.Error("expected Unwrap method to be injected")
	}
}

func TestTranspileOptionType(t *testing.T) {
	source := []byte(`package main

func findUser(id int) Option[string] {
	if id == 1 {
		return Some[string]("alice")
	}
	return None[string]()
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "type Option[T any] struct") {
		t.Error("expected Option type definition to be injected")
	}
	if !strings.Contains(goCode, "func Some[T any]") {
		t.Error("expected Some constructor to be injected")
	}
	if !strings.Contains(goCode, "func None[T any]") {
		t.Error("expected None constructor to be injected")
	}
	if !strings.Contains(goCode, "func (o Option[T]) Unwrap()") {
		t.Error("expected Unwrap method to be injected")
	}
}

func TestTranspileNoResultOptionWhenNotUsed(t *testing.T) {
	source := []byte(`package main

func f() {
	let x = 42
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if strings.Contains(goCode, "type Result[T any]") {
		t.Error("Result type should NOT be injected when not used")
	}
	if strings.Contains(goCode, "type Option[T any]") {
		t.Error("Option type should NOT be injected when not used")
	}
}

// --- Feature 3: Let Multi-Declaration ---

func TestTranspileLetMulti(t *testing.T) {
	source := []byte(`package main

func f() {
	let (a, b) = getPair()
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "a, b := getPair()") {
		t.Errorf("expected 'a, b := getPair()' but got:\n%s", goCode)
	}
	if strings.Contains(goCode, "let") {
		t.Error("should not contain 'let' keyword in output")
	}
}

func TestTranspileLetMultiThreeVars(t *testing.T) {
	source := []byte(`package main

func f() {
	let (x, y, z) = getTriple()
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "x, y, z := getTriple()") {
		t.Errorf("expected 'x, y, z := getTriple()' but got:\n%s", goCode)
	}
}

// --- Feature 4: Match Guards ---

func TestTranspileMatchGuard(t *testing.T) {
	source := []byte(`package main

func f() {
	x := match val {
		1 if val > 0 => "positive one",
		0 => "zero",
	}
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "case 1:") {
		t.Error("expected case 1")
	}
	if !strings.Contains(goCode, "if val > 0") {
		t.Error("expected guard clause 'if val > 0'")
	}
	if !strings.Contains(goCode, "case 0:") {
		t.Error("expected case 0 (non-guarded arm)")
	}
}

// --- Feature 5: CLI run command (unit-testable part) ---

func TestTranspileFullProgram(t *testing.T) {
	// Tests that a full .fw program with main package transpiles correctly
	// and includes all the new features working together.
	source := []byte(`package main

import "fmt"

func f() {
	let x = 42
	y := x > 0 ? "positive" : "non-positive"
	let (a, b) = getPair()
	z := match x {
		42 if true => "the answer",
		0 => "zero",
	}
	fmt.Println(y, a, b, z)
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// let -> :=
	if !strings.Contains(goCode, "x := 42") {
		t.Error("expected let transpilation")
	}
	// ternary -> IIFE
	if !strings.Contains(goCode, "func() interface{}") {
		t.Error("expected ternary IIFE")
	}
	// let multi -> multi :=
	if !strings.Contains(goCode, "a, b := getPair()") {
		t.Error("expected let multi transpilation")
	}
	// match with guard -> switch with if
	if !strings.Contains(goCode, "if true") {
		t.Error("expected match guard transpilation")
	}
}
