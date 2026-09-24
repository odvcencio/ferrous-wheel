package ferrouswheel

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

func TestResolveAtInfersExpressionTypesInSourceContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		expr     string
		nodeType string
		want     string
	}{
		{
			name: "pointer dereference",
			source: `package main
func f(value int) {
    let pointer = &value
    let result = *pointer
    _ = result
}`,
			expr: "*pointer", nodeType: "unary_expression", want: "int",
		},
		{
			name: "conditional value",
			source: `package main
func f(value int) {
    let result = true ? value : 1
    _ = result
}`,
			expr: "true ? value : 1", nodeType: "ternary_expression", want: "int",
		},
		{
			name: "pipeline result",
			source: `package main
func inc(value int) int { return value + 1 }
func f(value int) {
    let result = value |> inc
    _ = result
}`,
			expr: "value |> inc", nodeType: "pipeline_expression", want: "int",
		},
		{
			name: "list comprehension",
			source: `package main
func f() {
    let numbers: []int = []int{1, 2}
    let result = [value for value in numbers]
    _ = result
}`,
			expr: "[value for value in numbers]", nodeType: "list_comprehension", want: "[]int",
		},
		{
			name: "typed lambda",
			source: `package main
func f() {
    let add = fn(value: int) -> int { return value + 1 }
    _ = add
}`,
			expr: "fn(value: int) -> int { return value + 1 }", nodeType: "lambda_expression", want: "func(int) int",
		},
		{
			name: "null fallback",
			source: `package main
func f(pointer *int) {
    let result = pointer ?? pointer
    _ = result
}`,
			expr: "pointer ?? pointer", nodeType: "null_coalesce", want: "*int",
		},
		{
			name: "safe navigation",
			source: `package main
type Point struct { X int }
func f(point *Point) {
    let result = point?.X
    _ = result
}`,
			expr: "point?.X", nodeType: "safe_navigation", want: "int",
		},
		{
			name: "error propagation",
			source: `package main
func load() (int, error) { return 1, nil }
func f() (int, error) {
    let result = try load()
    return result, nil
}`,
			expr: "try load()", nodeType: "error_propagation", want: "int",
		},
		{
			name: "match result",
			source: `package main
func f(value int) {
    let result = match value { 1 => "one", _ => "other" }
    _ = result
}`,
			expr: "match value { 1 => \"one\", _ => \"other\" }", nodeType: "match_expression", want: "untyped string",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, err := resolveExpressionFixture(t, tc.source, tc.expr, tc.nodeType)
			if err != nil {
				t.Fatalf("resolve %s: %v", tc.expr, err)
			}
			if typ.String() != tc.want {
				t.Fatalf("%s type = %s, want %s", tc.expr, typ, tc.want)
			}
		})
	}
}

func TestResolveAtRejectsInvalidExpressionTypes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		expr     string
		nodeType string
		wantErr  string
	}{
		{
			name: "comprehension over number",
			source: `package main
func f() {
    let count = 3
    let result = [value for value in count]
    _ = result
}`,
			expr: "[value for value in count]", nodeType: "list_comprehension", wantErr: "for-in requires a slice, got int",
		},
		{
			name: "pipeline stage is not callable",
			source: `package main
func f() {
    let left = 1
    let right = 2
    let result = left |> right
    _ = result
}`,
			expr: "left |> right", nodeType: "pipeline_expression", wantErr: "right side of |> must be callable",
		},
		{
			name: "pipeline stage has no result",
			source: `package main
func consume(value int) {}
func f() {
    let left = 1
    let result = left |> consume
    _ = result
}`,
			expr: "left |> consume", nodeType: "pipeline_expression", wantErr: "pipeline stage returns no value",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveExpressionFixture(t, tc.source, tc.expr, tc.nodeType)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("resolution error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func resolveExpressionFixture(t *testing.T, source, expr, nodeType string) (Type, error) {
	t.Helper()
	src := []byte(source)
	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("language: %v", err)
	}
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	if err != nil || tree.RootNode().HasError() {
		t.Fatalf("parse: %v", err)
	}
	env, err := CollectTypes(src)
	if err != nil {
		t.Fatalf("collect types: %v", err)
	}
	start := strings.Index(source, expr)
	if start < 0 {
		t.Fatalf("expression %q missing from fixture", expr)
	}
	target := tree.RootNode().NamedDescendantForByteRange(uint32(start), uint32(start+len(expr)))
	if target == nil || target.Type(lang) != nodeType {
		got := "<nil>"
		if target != nil {
			got = target.Type(lang)
		}
		t.Fatalf("target node type = %s, want %s", got, nodeType)
	}
	return env.ResolveAt(tree.RootNode(), target, lang, src)
}
