package ferrouswheel

import (
	"strings"
	"testing"
)

func TestLineDirectivesInFunctionDecl(t *testing.T) {
	src := []byte("package main\n\nfunc hello() string {\n\treturn \"world\"\n}\n")
	goCode, _, err := TranspileWithOptions(src, TranspileOptions{SourceFile: "test.fw"})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "//line test.fw:") {
		t.Errorf("missing //line directive:\n%s", goCode)
	}
}

func TestNoLineDirectiveWithoutSourceFile(t *testing.T) {
	src := []byte("package main\n\nfunc hello() string {\n\treturn \"world\"\n}\n")
	goCode, err := Transpile(src)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if strings.Contains(goCode, "//line") {
		t.Error("should not have //line when no source file")
	}
}
