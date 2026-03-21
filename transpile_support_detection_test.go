package ferrouswheel

import (
	"strings"
	"testing"
)

func TestTranspileGenericHelpersIgnoreStringLiteralMatches(t *testing.T) {
	goCode := compileCheck(t, `package main

import "fmt"

func main() {
	fmt.Println("Result[int] Ok(1) Option[string] Some(\"alice\") None()")
}
`)

	if strings.Contains(goCode, "type Result[T any] struct") {
		t.Fatalf("did not expect Result helper injection from string literals:\n%s", goCode)
	}
	if strings.Contains(goCode, "type Option[T any] struct") {
		t.Fatalf("did not expect Option helper injection from string literals:\n%s", goCode)
	}
}

func TestTranspileGenericHelpersRespectUserDefinedResultType(t *testing.T) {
	goCode := compileCheck(t, `package main

type Result[T any] struct {
	Value T
}

func main() {
	r := Result[int]{Value: 42}
	_ = r
}
`)

	if strings.Contains(goCode, "// Result is a Rust-inspired Result type for explicit error handling.") {
		t.Fatalf("did not expect built-in Result helper when user defines Result:\n%s", goCode)
	}
}

func TestTranspileGenericHelpersRespectUserDefinedOptionType(t *testing.T) {
	goCode := compileCheck(t, `package main

type Option[T any] struct {
	Value T
}

func main() {
	o := Option[string]{Value: "alice"}
	_ = o
}
`)

	if strings.Contains(goCode, "// Option is a Rust-inspired Option type for explicit nil handling.") {
		t.Fatalf("did not expect built-in Option helper when user defines Option:\n%s", goCode)
	}
}
