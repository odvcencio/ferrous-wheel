package main

import (
	"strings"
	"testing"
)

func TestSandboxHelloWorld(t *testing.T) {
	code := `package main

import "fmt"

func main() {
	fmt.Println("hello from sandbox")
}
`
	result, err := runSandbox(code)
	if err != nil {
		t.Fatalf("sandbox: %v", err)
	}
	if !strings.Contains(result.Stdout, "hello from sandbox") {
		t.Errorf("expected hello output, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestSandboxTimeout(t *testing.T) {
	code := `package main

func main() {
	for {}
}
`
	result, err := runSandbox(code)
	if err != nil {
		t.Fatalf("sandbox error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected timeout error")
	}
}

func TestSandboxCompileError(t *testing.T) {
	code := `package main

func main() {
	x :=
}
`
	result, err := runSandbox(code)
	if err != nil {
		t.Fatalf("sandbox should handle compile failures: %v", err)
	}
	if result.Stderr == "" && result.Error == "" {
		t.Error("expected compile error in stderr or error field")
	}
}
