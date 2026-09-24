package ferrouswheel

import (
	"strings"
	"testing"
)

func TestTranspileRejectsNonMethodImplBody(t *testing.T) {
	const source = "package A0\nimpl A{0}0"
	generated, err := Transpile([]byte(source))
	if err == nil {
		t.Fatalf("expected a source diagnostic for a non-method impl body; generated %q", generated)
	}
	if !strings.Contains(err.Error(), "2:") || !strings.Contains(err.Error(), "impl") {
		t.Fatalf("expected an impl diagnostic at line 2, got %v", err)
	}
}

func TestTranspileRejectsAdjacentIdentifierString(t *testing.T) {
	const source = "package A0000\nfunc A000()A{A000000\"00000\"}0"
	generated, err := Transpile([]byte(source))
	if err == nil {
		t.Fatalf("expected a source diagnostic for adjacent identifier and string; generated %q", generated)
	}
	if !strings.Contains(err.Error(), "2:") || !strings.Contains(err.Error(), "unknown log level") {
		t.Fatalf("expected a log-level diagnostic at line 2, got %v", err)
	}
}

func TestTranspileRejectsMalformedGeneratedGo(t *testing.T) {
	const source = "package A\nfunc A(){0,0\n=0}0"
	generated, _, err := TranspileWithOptions([]byte(source), TranspileOptions{SourceFile: "bad.fw"})
	if err == nil {
		t.Fatalf("expected an emission diagnostic; generated %q", generated)
	}
	if !strings.Contains(err.Error(), "bad.fw:2:") || !strings.Contains(err.Error(), "generated Go") {
		t.Fatalf("expected a source-linked generated-Go diagnostic, got %v", err)
	}
}
