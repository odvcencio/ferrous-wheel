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

func TestTranspileGenericEnum(t *testing.T) {
	source := []byte(`package main

enum Option[T any] {
	Some(T),
	None,
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if !strings.Contains(goCode, "type Option[T any] struct") {
		t.Error("expected generic enum type declaration")
	}
	if !strings.Contains(goCode, "func Some[T any](v0 T) Option[T]") {
		t.Error("expected generic enum constructor")
	}
	if !strings.Contains(goCode, "func None[T any]() Option[T]") {
		t.Error("expected generic enum zero-value constructor")
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

func TestTranspileNullCoalesceReportsWarning(t *testing.T) {
	source := []byte(`package main

func f() {
	x := val ?? "default"
	_ = x
}
`)
	goCode, warnings, err := TranspileWithOptions(source, TranspileOptions{SourceFile: "test.fw"})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %+v", warnings)
	}
	if warnings[0].Line != 4 {
		t.Fatalf("expected warning on line 4, got %+v", warnings[0])
	}
	if warnings[0].Col <= 0 {
		t.Fatalf("expected warning column to be set, got %+v", warnings[0])
	}
	if !strings.Contains(warnings[0].Message, "unresolved for ??") {
		t.Fatalf("unexpected warning message: %+v", warnings[0])
	}
	if !strings.Contains(goCode, "// fw:warn:") {
		t.Fatalf("expected inline warning comment in generated code:\n%s", goCode)
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

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("did not expect reflect fallback for typed local binding")
	}
	if !strings.Contains(goCode, `if name != ""`) {
		t.Error("expected typed zero-value check for string binding")
	}
	if strings.Contains(goCode, `"reflect"`) {
		t.Error("did not expect reflect import for typed local binding")
	}
}

func TestTranspileNullCoalesceImportedAliasAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func req() *h.Request {
	return nil
}

func f() {
	x := req() ?? &h.Request{}
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed null-coalesce path, not reflect fallback")
	}
	if strings.Contains(goCode, `"reflect"`) {
		t.Error("did not expect reflect import for typed null-coalesce")
	}
	if !strings.Contains(goCode, "func() *h.Request") {
		t.Error("expected typed null-coalesce return type")
	}
	if !strings.Contains(goCode, "return &h.Request{}") {
		t.Error("expected typed fallback expression")
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

func TestTranspileTypedLambdaParamAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func f() {
	getHost := fn(req: *h.Request) -> string { return req?.Host }
	_ = getHost
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed lambda param path, not reflect fallback")
	}
	if !strings.Contains(goCode, "func(req *h.Request) string") {
		t.Error("expected typed lambda signature")
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

func TestTranspileSafeNavReportsWarning(t *testing.T) {
	source := []byte(`package main

func f() {
	x := obj?.name
	_ = x
}
`)
	goCode, warnings, err := TranspileWithOptions(source, TranspileOptions{SourceFile: "test.fw"})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %+v", warnings)
	}
	if warnings[0].Line != 4 {
		t.Fatalf("expected warning on line 4, got %+v", warnings[0])
	}
	if warnings[0].Col <= 0 {
		t.Fatalf("expected warning column to be set, got %+v", warnings[0])
	}
	if !strings.Contains(warnings[0].Message, "unresolved for ?.") {
		t.Fatalf("unexpected warning message: %+v", warnings[0])
	}
	if !strings.Contains(goCode, "// fw:warn:") {
		t.Fatalf("expected inline warning comment in generated code:\n%s", goCode)
	}
}

func TestTranspileSafeNavImportedAliasAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func req() *h.Request {
	return nil
}

func f() {
	x := req()?.Host
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed safe-navigation path, not reflect fallback")
	}
	if strings.Contains(goCode, `"reflect"`) {
		t.Error("did not expect reflect import for typed safe-navigation")
	}
	if !strings.Contains(goCode, "func() string") {
		t.Error("expected typed safe-navigation return type")
	}
	if !strings.Contains(goCode, ".Host") {
		t.Error("expected direct field access on imported type")
	}
}

func TestTranspileSafeNavFunctionParamAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func f(req *h.Request) string {
	x := req?.Host
	return x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed parameter path, not reflect fallback")
	}
	if !strings.Contains(goCode, "func() string") {
		t.Error("expected typed safe-navigation return type")
	}
}

func TestTranspileSafeNavShortVarAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func req() *h.Request {
	return nil
}

func f() {
	request := req()
	x := request?.Host
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed short-var path, not reflect fallback")
	}
}

func TestTranspileSafeNavBlockScopeRestoresOuterBinding(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func f(req *h.Request) string {
	if true {
		let req = 1
		_ = req
	}
	x := req?.Host
	return x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected outer binding to survive inner block scope")
	}
	if !strings.Contains(goCode, "func() string") {
		t.Error("expected typed safe-navigation after inner block")
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

	if strings.Contains(goCode, "interface{}") {
		t.Error("output should not contain interface{}")
	}
	if !strings.Contains(goCode, "func()") {
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

func TestTranspileResultTypeFromInferredOkCall(t *testing.T) {
	source := []byte(`package main

func f() {
	r := Ok(42)
	_ = r
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if !strings.Contains(goCode, "type Result[T any] struct") {
		t.Error("expected Result type definition to be injected for inferred Ok call")
	}
	if !strings.Contains(goCode, "func Ok[T any]") {
		t.Error("expected Ok constructor to be injected for inferred Ok call")
	}
}

func TestTranspileOptionTypeFromInferredSomeCall(t *testing.T) {
	source := []byte(`package main

func f() {
	o := Some("alice")
	_ = o
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if !strings.Contains(goCode, "type Option[T any] struct") {
		t.Error("expected Option type definition to be injected for inferred Some call")
	}
	if !strings.Contains(goCode, "func Some[T any]") {
		t.Error("expected Some constructor to be injected for inferred Some call")
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
	// ternary -> IIFE (typed: func() string, not interface{})
	if !strings.Contains(goCode, "func()") {
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

// === New Feature Transpile Tests ===

func TestTranspileDerive(t *testing.T) {
	source := []byte(`package main
derive Stringer for Color
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func (x Color) String() string") {
		t.Error("expected Stringer method")
	}
	if strings.Contains(goCode, "derive") {
		t.Error("should not contain 'derive' keyword in output")
	}
}

func TestTranspileDeriveGenericEqual(t *testing.T) {
	source := []byte(`package main

type Box[T comparable] struct {
	Value T
}

derive Equal for Box[T]
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if !strings.Contains(goCode, "func (x Box[T]) Equal(other Box[T]) bool") {
		t.Errorf("expected generic Equal receiver, got:\n%s", goCode)
	}
}

func TestTranspileDeriveJSON(t *testing.T) {
	source := []byte(`package main
derive JSON for Config
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func (x Config) MarshalJSON() ([]byte, error)") {
		t.Error("expected MarshalJSON method")
	}
	if !strings.Contains(goCode, "func (x *Config) UnmarshalJSON(data []byte) error") {
		t.Error("expected UnmarshalJSON method")
	}
	if !strings.Contains(goCode, "json.Marshal") {
		t.Error("expected json.Marshal call")
	}
	if !strings.Contains(goCode, "json.Unmarshal") {
		t.Error("expected json.Unmarshal call")
	}
}

func TestTranspileDeriveGenericJSON(t *testing.T) {
	source := []byte(`package main

type Box[T any] struct {
	Value T
}

derive JSON for Box[T]
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if !strings.Contains(goCode, "func (x Box[T]) MarshalJSON() ([]byte, error)") {
		t.Errorf("expected generic MarshalJSON receiver, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, "type _fwJSONAlias Box[T]") {
		t.Errorf("expected local JSON alias for generic type, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, "func (x *Box[T]) UnmarshalJSON(data []byte) error") {
		t.Errorf("expected generic UnmarshalJSON receiver, got:\n%s", goCode)
	}
}

func TestTranspileDeriveEqual(t *testing.T) {
	source := []byte(`package main
derive Equal for Point
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func (x Point) Equal(other Point) bool") {
		t.Error("expected Equal method")
	}
	if !strings.Contains(goCode, "return x == other") {
		t.Error("expected equality check")
	}
}

func TestTranspileIfLet(t *testing.T) {
	source := []byte(`package main
func f() {
	if let x = getValue() {
		_ = x
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "if x := getValue(); x != nil") {
		t.Errorf("expected 'if x := getValue(); x != nil', got:\n%s", goCode)
	}
	if strings.Contains(goCode, "let") {
		t.Error("should not contain 'let' in output")
	}
}

func TestTranspileIfLetElse(t *testing.T) {
	source := []byte(`package main
func f() {
	if let x = getValue() {
		_ = x
	} else {
		panic("nil")
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "x != nil") {
		t.Error("expected nil check")
	}
	if !strings.Contains(goCode, "else") {
		t.Error("expected else block")
	}
}

func TestTranspileIfLetBindingAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func f(req *h.Request) {
	if let value = req {
		x := value?.Host
		_ = x
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed if-let binding path, not reflect fallback")
	}
}

func TestTranspileForIn(t *testing.T) {
	source := []byte(`package main
func f() {
	for v in items {
		_ = v
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "for _, v := range items") {
		t.Errorf("expected 'for _, v := range items', got:\n%s", goCode)
	}
	if strings.Contains(goCode, " in ") {
		t.Error("should not contain 'in' keyword in output")
	}
}

func TestTranspileForInRange(t *testing.T) {
	source := []byte(`package main
func f() {
	for i in 0 .. 10 {
		_ = i
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "for i := 0; i < 10; i++") {
		t.Errorf("expected C-style for loop, got:\n%s", goCode)
	}
}

func TestTranspileForInIndex(t *testing.T) {
	source := []byte(`package main
func f() {
	for i, v in items {
		_ = i
		_ = v
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "for i, v := range items") {
		t.Errorf("expected 'for i, v := range items', got:\n%s", goCode)
	}
}

func TestTranspileForInLoopVarAvoidsReflect(t *testing.T) {
	source := []byte(`package main

import h "net/http"

func f(reqs []*h.Request) {
	for req in reqs {
		x := req?.Host
		_ = x
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected typed loop binding path, not reflect fallback")
	}
}

func TestTranspileFString(t *testing.T) {
	source := []byte(`package main
func f() {
	x := f"hello {name}"
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "fmt.Sprintf") {
		t.Error("expected fmt.Sprintf")
	}
	if !strings.Contains(goCode, "hello %v") {
		t.Error("expected format string with percent-v placeholder")
	}
	if !strings.Contains(goCode, "name") {
		t.Error("expected name argument")
	}
}

func TestTranspileFStringNoInterpolation(t *testing.T) {
	source := []byte(`package main
func f() {
	x := f"hello world"
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `"hello world"`) {
		t.Error("expected plain string when no interpolation")
	}
}

func TestTranspileGuard(t *testing.T) {
	source := []byte(`package main
func f() {
	guard x > 0 else {
		return
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "if !(x > 0)") {
		t.Errorf("expected negated condition, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, "return") {
		t.Error("expected return in guard body")
	}
	if strings.Contains(goCode, "guard") {
		t.Error("should not contain 'guard' in output")
	}
}

func TestTranspileDeferError(t *testing.T) {
	source := []byte(`package main
func f() {
	defer! f.Close()
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "defer func()") {
		t.Error("expected defer func()")
	}
	if !strings.Contains(goCode, "_cerr") {
		t.Error("expected _cerr variable in error-capturing defer")
	}
	if !strings.Contains(goCode, "f.Close()") {
		t.Error("expected f.Close() call")
	}
}

func TestTranspileImplBlock(t *testing.T) {
	source := []byte(`package main
impl Point {
	func Area() int {
		return 0
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func (self Point)") {
		t.Errorf("expected receiver on function, got:\n%s", goCode)
	}
	if strings.Contains(goCode, "impl") {
		t.Error("should not contain 'impl' in output")
	}
}

func TestTranspileGenericImplBlock(t *testing.T) {
	source := []byte(`package main

type Box[T any] struct {
	Value T
}

impl Box[T] {
	func Get() T {
		return self.Value
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	if !strings.Contains(goCode, "func (self Box[T]) Get() T") {
		t.Errorf("expected generic impl receiver, got:\n%s", goCode)
	}
}

func TestTranspileUnless(t *testing.T) {
	source := []byte(`package main
func f() {
	unless x > 0 {
		return
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "if !(x > 0)") {
		t.Errorf("expected negated if, got:\n%s", goCode)
	}
	if strings.Contains(goCode, "unless") {
		t.Error("should not contain 'unless' in output")
	}
}

func TestTranspileUntil(t *testing.T) {
	source := []byte(`package main
func f() {
	until x > 10 {
		x = x + 1
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "for !(x > 10)") {
		t.Errorf("expected negated for, got:\n%s", goCode)
	}
	if strings.Contains(goCode, "until") {
		t.Error("should not contain 'until' in output")
	}
}

func TestTranspileRepeat(t *testing.T) {
	source := []byte(`package main
func f() {
	repeat 5 {
		fmt.Println("hi")
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "for _i := 0; _i < 5; _i++") {
		t.Errorf("expected counted for loop, got:\n%s", goCode)
	}
	if strings.Contains(goCode, "repeat") {
		t.Error("should not contain 'repeat' in output")
	}
}

func TestTranspileListComprehension(t *testing.T) {
	source := []byte(`package main
func f() {
	x := [x for x in items]
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func() []interface{}") {
		t.Error("expected IIFE returning slice")
	}
	if !strings.Contains(goCode, "range items") {
		t.Error("expected range over items")
	}
	if !strings.Contains(goCode, "_result = append(_result") {
		t.Error("expected append to result")
	}
}

func TestTranspileListComprehensionFilter(t *testing.T) {
	source := []byte(`package main
func f() {
	x := [x for x in items if x > 0]
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "if x > 0") {
		t.Error("expected filter condition")
	}
	if !strings.Contains(goCode, "range items") {
		t.Error("expected range over items")
	}
}

func TestTranspileSwap(t *testing.T) {
	source := []byte(`package main
func f() {
	swap(a, b)
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "a, b = b, a") {
		t.Errorf("expected 'a, b = b, a', got:\n%s", goCode)
	}
	if strings.Contains(goCode, "swap") {
		t.Error("should not contain 'swap' in output")
	}
}

// =============================================
// LOW-LEVEL MEMORY MANAGEMENT TRANSPILE TESTS
// =============================================

func TestTranspileArena(t *testing.T) {
	source := []byte(`package main
func f() {
	arena scratch {
		x := 1
		_ = x
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "_arena_scratch") {
		t.Error("expected _arena_scratch variable")
	}
	if !strings.Contains(goCode, "_arenaAlloc_scratch") {
		t.Error("expected _arenaAlloc_scratch function")
	}
	if !strings.Contains(goCode, `panic("arena scratch out of memory")`) {
		t.Error("expected arena overflow guard")
	}
	if !strings.Contains(goCode, "unsafe.Pointer") {
		t.Error("expected unsafe.Pointer in arena allocator")
	}
	if !strings.Contains(goCode, `"unsafe"`) {
		t.Error("expected unsafe import")
	}
	if strings.Contains(goCode, "arena") && !strings.Contains(goCode, "_arena") {
		t.Error("should not contain raw 'arena' keyword in output")
	}
}

func TestTranspileArenaWithSize(t *testing.T) {
	source := []byte(`package main
func f() {
	arena scratch 2048 {
		x := 1
		_ = x
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "_arenaSize := 2048") {
		t.Errorf("expected custom size 2048, got:\n%s", goCode)
	}
}

func TestTranspilePin(t *testing.T) {
	source := []byte(`package main
func f() {
	pin data
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "runtime.SetFinalizer") {
		t.Error("expected runtime.SetFinalizer")
	}
	if !strings.Contains(goCode, "runtime.KeepAlive(data)") {
		t.Error("expected runtime.KeepAlive(data)")
	}
	if !strings.Contains(goCode, `"runtime"`) {
		t.Error("expected runtime import")
	}
}

func TestTranspileUnpin(t *testing.T) {
	source := []byte(`package main
func f() {
	unpin data
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "runtime.KeepAlive(data)") {
		t.Error("expected runtime.KeepAlive(data)")
	}
}

func TestTranspileUnsafeCast(t *testing.T) {
	source := []byte(`package main
func f() {
	x := unsafe cast(s, int)
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "_fwUnsafeCast[int](s)") {
		t.Error("expected helper-backed unsafe cast")
	}
	if !strings.Contains(goCode, `"unsafe"`) {
		t.Error("expected unsafe import")
	}
	if !strings.Contains(goCode, "func _fwUnsafeCast[To any, From any]") {
		t.Error("expected unsafe cast helper to be injected")
	}
}

func TestTranspileMmap(t *testing.T) {
	source := []byte(`package main
func f() {
	mmap file "data.bin" as data []byte {
		_ = data
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `os.Open("data.bin")`) {
		t.Error("expected os.Open call")
	}
	if !strings.Contains(goCode, "if _fwMmapErr != nil") {
		t.Error("expected mmap error handling")
	}
	if !strings.Contains(goCode, "syscall.Mmap") {
		t.Error("expected syscall.Mmap call")
	}
	if !strings.Contains(goCode, "syscall.Munmap") {
		t.Error("expected syscall.Munmap cleanup")
	}
	if !strings.Contains(goCode, `"os"`) {
		t.Error("expected os import")
	}
	if !strings.Contains(goCode, `"syscall"`) {
		t.Error("expected syscall import")
	}
}

func TestTranspileMmapWritable(t *testing.T) {
	source := []byte(`package main
func f() {
	mmap file "data.bin" writable as data []byte {
		_ = data
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `os.OpenFile("data.bin", os.O_RDWR, 0)`) {
		t.Error("expected writable mmap to use os.OpenFile")
	}
	if !strings.Contains(goCode, "syscall.PROT_READ|syscall.PROT_WRITE") {
		t.Error("expected writable mmap protections")
	}
}

func TestTranspilePacked(t *testing.T) {
	source := []byte(`package main
func f() {
	packed let x = 1
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "// packed: manual alignment required") {
		t.Error("expected packed comment annotation")
	}
	if !strings.Contains(goCode, "x := 1") {
		t.Error("expected let to transpile to :=")
	}
}

func TestTranspilePackedTopLevelStructAddsSizeAssertion(t *testing.T) {
	source := []byte(`package main

packed type Packet struct {
	B uint16
	A uint8
	C uint8
}

func main() {}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "unsafe.Sizeof(Packet{})") {
		t.Fatal("expected packed size assertion")
	}
	if !strings.Contains(goCode, "expected size 4") {
		t.Fatal("expected computed packed size")
	}
	if !strings.Contains(goCode, `panic(fmt.Sprintf("packed struct Packet:`) {
		t.Fatal("expected descriptive packed struct panic")
	}
}

func TestTranspilePackedSkipsAssertionForUnknownFieldSize(t *testing.T) {
	source := []byte(`package main

packed type Packet struct {
	Name string
}

func main() {}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Contains(goCode, "unsafe.Sizeof(Packet{})") {
		t.Fatal("did not expect packed size assertion for unknown field size")
	}
}

func TestTranspileVectorize(t *testing.T) {
	source := []byte(`package main
func f() {
	vectorize for v in items {
		_ = v
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "// vectorize: compiler hint") {
		t.Error("expected vectorize hint comment")
	}
	if !strings.Contains(goCode, "range items") {
		t.Error("expected range loop over items")
	}
	if strings.Contains(goCode, "vectorize for") {
		t.Error("should not contain raw 'vectorize for' in output")
	}
}

// =============================================
// CONCURRENCY TRANSPILE TESTS
// =============================================

func TestTranspileSelectBlock(t *testing.T) {
	source := []byte(`package main
func f() {
	select! {
		msg from inbox => process(msg),
		timeout 5 => log(x),
		default => noop(),
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "select {") {
		t.Error("expected Go select statement")
	}
	if !strings.Contains(goCode, "case msg := <-inbox") {
		t.Error("expected channel receive case")
	}
	if !strings.Contains(goCode, "time.After") {
		t.Error("expected time.After for timeout")
	}
	if !strings.Contains(goCode, "default:") {
		t.Error("expected default case")
	}
}

func TestTranspileFanOut(t *testing.T) {
	source := []byte(`package main
func f() {
	fan out workers, 10 {
		doWork()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "sync.WaitGroup") {
		t.Error("expected sync.WaitGroup")
	}
	if !strings.Contains(goCode, "_wi < 10") {
		t.Error("expected worker count loop")
	}
	if !strings.Contains(goCode, "go func()") {
		t.Error("expected goroutine launch")
	}
	if !strings.Contains(goCode, ".Wait()") {
		t.Error("expected WaitGroup.Wait()")
	}
	if !strings.Contains(goCode, `"sync"`) {
		t.Error("expected sync import")
	}
}

func TestTranspileFanIn(t *testing.T) {
	source := []byte(`package main
func f() {
	merged := fan in [ch1, ch2, ch3]
	_ = merged
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "_fwFanIn(ch1, ch2, ch3)") {
		t.Error("expected fan-in helper call")
	}
	if !strings.Contains(goCode, "sync.WaitGroup") {
		t.Error("expected sync.WaitGroup in fan-in")
	}
	if !strings.Contains(goCode, "reflect.ValueOf") {
		t.Error("expected reflect.ValueOf for heterogeneous channel fan-in")
	}
}

func TestTranspilePipeline(t *testing.T) {
	source := []byte(`package main
func f() {
	x := data |> transform
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "transform(data)") {
		t.Errorf("expected 'transform(data)', got:\n%s", goCode)
	}
}

func TestTranspileConcurrent(t *testing.T) {
	source := []byte(`package main
func f() {
	concurrent {
		fetch("url1")
		fetch("url2")
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "sync.WaitGroup") {
		t.Error("expected sync.WaitGroup")
	}
	if !strings.Contains(goCode, "go func()") {
		t.Error("expected goroutine launches")
	}
	if !strings.Contains(goCode, "_wg.Wait()") {
		t.Error("expected WaitGroup.Wait()")
	}
}

func TestTranspileThrottle(t *testing.T) {
	source := []byte(`package main
func f() {
	throttle 100 {
		doWork()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `_fwThrottleWait("throttle_`) {
		t.Error("expected site-scoped throttle helper call")
	}
	if !strings.Contains(goCode, "func _fwThrottleWait(name string, rate int, burst int)") {
		t.Error("expected throttle helper to be injected")
	}
	if !strings.Contains(goCode, `"time"`) {
		t.Error("expected time import")
	}
	if !strings.Contains(goCode, `"sync"`) {
		t.Error("expected sync import")
	}
}

func TestTranspileThrottleBurst(t *testing.T) {
	source := []byte(`package main
func f() {
	throttle 100 burst 3 {
		doWork()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "// throttle: 100/s, burst: 3") {
		t.Error("expected throttle comment to preserve burst")
	}
	if !strings.Contains(goCode, `int(3)`) {
		t.Error("expected burst to be passed to throttle helper")
	}
}

func TestTranspileRetry(t *testing.T) {
	source := []byte(`package main
func f() {
	retry 3 {
		doWork()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "_attempt < 3") {
		t.Error("expected retry count of 3")
	}
	if !strings.Contains(goCode, "time.Sleep") {
		t.Error("expected exponential backoff sleep")
	}
	if !strings.Contains(goCode, "_retryErr") {
		t.Error("expected _retryErr variable")
	}
}

func TestTranspileBreaker(t *testing.T) {
	source := []byte(`package main
func f() {
	breaker "myservice" {
		callService()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "Circuit breaker") {
		t.Error("expected circuit breaker comment")
	}
	if !strings.Contains(goCode, `_fwBreakerDo("myservice"`) {
		t.Error("expected breaker helper call")
	}
	if !strings.Contains(goCode, "time.Since") {
		t.Error("expected time.Since check")
	}
}

func TestTranspileLetMut(t *testing.T) {
	source := []byte(`package main
func main() {
	let mut x = 1
	x = 2
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "x := 1") {
		t.Error("expected x := 1")
	}
	if !strings.Contains(goCode, "x = 2") {
		t.Error("expected x = 2 (mutable reassignment)")
	}
}

func TestTranspileLetImmutableReassignError(t *testing.T) {
	source := []byte(`package main
func main() {
	let x = 1
	x = 2
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected error for reassignment of immutable let binding")
	}
	if !strings.Contains(err.Error(), "cannot assign to immutable binding") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTranspileLetImmutableCompoundAssignError(t *testing.T) {
	source := []byte(`package main
func main() {
	let x = 1
	x += 2
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected error for compound assignment of immutable let binding")
	}
	if !strings.Contains(err.Error(), "cannot assign to immutable binding") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTranspileLetMutMulti(t *testing.T) {
	source := []byte(`package main
func f() (int, int) { return 1, 2 }
func main() {
	let mut (a, b) = f()
	a = 3
	_ = a
	_ = b
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "a, b :=") {
		t.Error("expected a, b := destructuring")
	}
}

func TestTranspileGoNativeAssignmentStillWorks(t *testing.T) {
	source := []byte(`package main
func main() {
	x := 1
	x = 2
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "x = 2") {
		t.Error("Go-native := should allow reassignment")
	}
}

func TestTranspileCompilerGeneratedTempsNotTracked(t *testing.T) {
	source := []byte(`package main
import "os"
func main() error {
	let f = try os.Open("a")
	let g = try os.Open("b")
	_ = f
	_ = g
	return nil
}
`)
	_, err := Transpile(source)
	if err != nil {
		t.Fatalf("compiler-generated temps should not cause immutability errors: %v", err)
	}
}

func TestTranspileIfExpression(t *testing.T) {
	source := []byte(`package main
func main() {
	let x = if true { 1 } else { 2 }
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "func()") {
		t.Error("expected IIFE wrapper for if-expression")
	}
	if !strings.Contains(goCode, "return 1") {
		t.Error("expected return 1 in true branch")
	}
	if !strings.Contains(goCode, "return 2") {
		t.Error("expected return 2 in else branch")
	}
}

func TestTranspileIfExpressionElseIf(t *testing.T) {
	source := []byte(`package main
func main() {
	let x = if true { "a" } else if false { "b" } else { "c" }
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "func()") {
		t.Error("expected IIFE for if-expression")
	}
}

func TestTranspileIfExpressionMissingElseError(t *testing.T) {
	source := []byte(`package main
func main() {
	let x = if true { 1 }
	_ = x
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected error for if-expression without else")
	}
	if !strings.Contains(err.Error(), "if-expression requires an else branch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTranspileIfStatementStillWorks(t *testing.T) {
	source := []byte(`package main
func main() {
	if true {
		_ = 1
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if strings.Contains(goCode, "func()") {
		t.Error("regular if should not become IIFE")
	}
}

func TestTranspileIfExpressionNested(t *testing.T) {
	source := []byte(`package main
func main() {
	let x = if true { if false { 1 } else { 2 } } else { 3 }
	_ = x
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "func()") {
		t.Error("expected IIFE for nested if-expression")
	}
}

func TestTranspileIfExpressionNoTryInBranch(t *testing.T) {
	source := []byte(`package main
import "os"
func main() error {
	let x = if true { try os.Open("f") } else { nil }
	_ = x
	return nil
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected error for try inside if-expression branch")
	}
}

func TestTranspilePostfixTry(t *testing.T) {
	source := []byte(`package main

import "os"

func main() error {
	let f = os.Open("test")?
	_ = f
	return nil
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "!= nil") {
		t.Error("expected nil check for error propagation")
	}
	if !strings.Contains(goCode, "return") {
		t.Error("expected return statement for error propagation")
	}
}

func TestTranspilePostfixTryCoexistsWithPrefixTry(t *testing.T) {
	source := []byte(`package main

import "os"

func main() error {
	let f = try os.Open("test")
	_ = f
	return nil
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "!= nil") {
		t.Error("prefix try should still work")
	}
}

func TestTranspilePostfixTryInNonErrorFuncErrors(t *testing.T) {
	source := []byte(`package main

import "os"

func main() {
	let f = os.Open("test")?
	_ = f
}
`)
	_, err := Transpile(source)
	if err == nil {
		t.Fatal("expected error: ? in function that doesn't return error")
	}
}
