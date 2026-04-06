package ferrouswheel

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

func TestFormatLetSpacing(t *testing.T) {
	input := `package main

func main() {
	let   x   =   1
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "let x = 1") {
		t.Errorf("expected normalized spacing, got:\n%s", formatted)
	}
}

func TestFormatIdempotent(t *testing.T) {
	input := `package main

func main() {
	let x = 1
	let mut y = 2
}
`
	first, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("first format: %v", err)
	}
	second, err := Format(first)
	if err != nil {
		t.Fatalf("second format: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("formatter is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestFormatMatchAlignment(t *testing.T) {
	input := `package main

func main() {
	let x = match v {
		1 => "one",
		2 => "two",
	}
	_ = x
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "=>") {
		t.Error("expected match arms with => preserved")
	}
}

func TestFormatPreservesComments(t *testing.T) {
	input := `package main

// this is a comment
func main() {
	let x = 1 // inline comment
	_ = x
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "// this is a comment") {
		t.Error("expected comment preservation")
	}
	if !strings.Contains(string(formatted), "// inline comment") {
		t.Error("expected inline comment preservation")
	}
}

func TestFormatLetMutSpacing(t *testing.T) {
	input := `package main

func main() {
	let  mut  x  =  1
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "let mut x = 1") {
		t.Errorf("expected normalized let mut spacing, got:\n%s", formatted)
	}
}

func TestFormatIdempotentAlreadyFormatted(t *testing.T) {
	// Already well-formatted input should be unchanged.
	input := `package main

func main() {
	let x = 1
	let mut y = 2
	let z = match v {
		1 => "one",
		2 => "two",
	}
	_ = x
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if string(formatted) != input {
		t.Errorf("well-formatted input should be unchanged.\ninput:\n%s\ngot:\n%s", input, formatted)
	}
}

func TestFormatLetWithTypeAnnotation(t *testing.T) {
	input := `package main

func main() {
	let  x:  int  =  1
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "let x: int = 1") {
		t.Errorf("expected normalized type-annotated let, got:\n%s", formatted)
	}
}

func TestFormatMatchAlignArrows(t *testing.T) {
	input := `package main

func main() {
	let x = match v {
		1 => "one",
		200 => "two hundred",
	}
	_ = x
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	result := string(formatted)
	// Both => should be aligned (200 is 3 chars, 1 is 1 char, so "1" arm gets 2 spaces padding)
	lines := strings.Split(result, "\n")
	var arrowCols []int
	for _, line := range lines {
		idx := strings.Index(line, "=>")
		if idx >= 0 {
			arrowCols = append(arrowCols, idx)
		}
	}
	if len(arrowCols) >= 2 && arrowCols[0] != arrowCols[1] {
		t.Errorf("expected aligned =>, got columns %v in:\n%s", arrowCols, result)
	}
}

func TestFormatLetMultiDecl(t *testing.T) {
	input := `package main

func main() {
	let  (a,  b)  =  f()
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "let (a, b) = f()") {
		t.Errorf("expected normalized let multi decl, got:\n%s", formatted)
	}
}

func TestFormatIdempotentBadlyFormatted(t *testing.T) {
	// Start from badly formatted input; formatting twice must be stable.
	input := `package main

func main() {
	let   x   =   1
	let  mut  y  =  2
}
`
	first, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("first format: %v", err)
	}
	second, err := Format(first)
	if err != nil {
		t.Fatalf("second format: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("formatter is not idempotent on badly-formatted input:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestFormatPreservesParseTree(t *testing.T) {
	// Formatted output must parse to the same CST as the input.
	input := `package main

func main() {
	let   x   =   1
	let  mut  y  =  2
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}

	lang := getFWLang(t)
	parser := gotreesitter.NewParser(lang)

	tree1, err := parser.Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse input: %v", err)
	}
	tree2, err := parser.Parse(formatted)
	if err != nil {
		t.Fatalf("parse formatted: %v", err)
	}

	sexpr1 := tree1.RootNode().SExpr(lang)
	sexpr2 := tree2.RootNode().SExpr(lang)
	if sexpr1 != sexpr2 {
		t.Errorf("parse trees differ:\ninput tree:     %s\nformatted tree: %s", sexpr1, sexpr2)
	}
}

func TestFormatEmptyInput(t *testing.T) {
	formatted, err := Format([]byte(""))
	if err != nil {
		t.Fatalf("format empty: %v", err)
	}
	if len(formatted) != 0 {
		t.Errorf("expected empty output, got: %q", formatted)
	}
}

func TestFormatCommentBeforeFunc(t *testing.T) {
	input := `package main

// Doc comment for main.
func main() {
	let x = 1
	_ = x
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(formatted), "// Doc comment for main.") {
		t.Error("expected doc comment preservation")
	}
	// Should be unchanged since it's already well-formatted.
	if string(formatted) != input {
		t.Errorf("well-formatted input changed:\n%s", formatted)
	}
}

func TestFormatMatchWithGuard(t *testing.T) {
	input := `package main

func main() {
	let x = match v {
		n if n > 0 => "pos",
		n if n < 0 => "neg",
		_ => "zero",
	}
	_ = x
}
`
	formatted, err := Format([]byte(input))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	result := string(formatted)
	if !strings.Contains(result, "=>") {
		t.Error("expected match arms with => preserved")
	}
	// Check idempotency on formatted result.
	second, err := Format(formatted)
	if err != nil {
		t.Fatalf("second format: %v", err)
	}
	if string(formatted) != string(second) {
		t.Errorf("not idempotent on match with guard:\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}
}
