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

	if !strings.Contains(goCode, "!= nil") {
		t.Error("expected nil check")
	}
}
