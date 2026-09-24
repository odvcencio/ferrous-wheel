package ferrouswheel

import (
	"strings"
	"testing"
)

func TestInferenceRejectsRecursiveTypes(t *testing.T) {
	tests := []struct {
		name  string
		build func(*InferenceContext, *TypeVar, *TypeVar)
	}{
		{
			name: "direct slice",
			build: func(ctx *InferenceContext, a, _ *TypeVar) {
				ctx.AddConstraint(a, &SliceType{Elem: a}, nil)
			},
		},
		{
			name: "indirect function result",
			build: func(ctx *InferenceContext, a, b *TypeVar) {
				ctx.AddConstraint(a, b, nil)
				ctx.AddConstraint(b, &FuncType{Results: []Type{a}}, nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewInferenceContext()
			a, b := ctx.Fresh("A"), ctx.Fresh("B")
			tt.build(ctx, a, b)
			err := ctx.Solve()
			if err == nil || !strings.Contains(err.Error(), "recursive") {
				t.Fatalf("Solve() error = %v, want recursive type diagnostic", err)
			}
		})
	}
}

func TestContextUnificationRejectsRecursiveType(t *testing.T) {
	ctx := NewInferenceContext()
	variable := ctx.Fresh("Element")
	if _, err := UnifyWithContext(ctx, variable, &MapType{Key: Primitive("string"), Value: variable}); err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("UnifyWithContext recursive map error = %v, want recursive type diagnostic", err)
	}
}
