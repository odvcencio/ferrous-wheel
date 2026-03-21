package ferrouswheel

import (
	"strings"
	"testing"
)

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

func TestTranspileStandaloneRangeExpression(t *testing.T) {
	source := []byte(`package main

func main() {
	_ = 1 .. 3
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(goCode, "/* range 1..3 */") {
		t.Fatalf("expected standalone range comment, got:\n%s", goCode)
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
	if !strings.Contains(err.Error(), "let_declaration is not supported inside concurrent blocks") {
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
