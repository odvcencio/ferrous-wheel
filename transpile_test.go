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
