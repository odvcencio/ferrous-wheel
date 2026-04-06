package ferrouswheel

import "testing"

func TestInferenceContextBasic(t *testing.T) {
	ctx := NewInferenceContext()
	tv := ctx.Fresh("T")

	ctx.AddConstraint(tv, Primitive("int"), nil)
	err := ctx.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	resolved := ctx.Apply(tv)
	if !TypeEquals(resolved, Primitive("int")) {
		t.Errorf("expected int, got %s", resolved)
	}
}

func TestInferenceContextFuncUnification(t *testing.T) {
	ctx := NewInferenceContext()
	tv1 := ctx.Fresh("T")
	tv2 := ctx.Fresh("U")

	left := &FuncType{Params: []Type{tv1}, Results: []Type{tv2}}
	right := &FuncType{Params: []Type{Primitive("int")}, Results: []Type{Primitive("string")}}

	ctx.AddConstraint(left, right, nil)
	err := ctx.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	if !TypeEquals(ctx.Apply(tv1), Primitive("int")) {
		t.Errorf("T should be int, got %s", ctx.Apply(tv1))
	}
	if !TypeEquals(ctx.Apply(tv2), Primitive("string")) {
		t.Errorf("U should be string, got %s", ctx.Apply(tv2))
	}
}

func TestInferenceContextConflict(t *testing.T) {
	ctx := NewInferenceContext()
	tv := ctx.Fresh("T")

	ctx.AddConstraint(tv, Primitive("int"), nil)
	ctx.AddConstraint(tv, Primitive("string"), nil)

	err := ctx.Solve()
	if err == nil {
		t.Fatal("expected error for conflicting constraints")
	}
}

func TestInferenceContextApplyNested(t *testing.T) {
	ctx := NewInferenceContext()
	tv := ctx.Fresh("T")

	ctx.AddConstraint(tv, Primitive("int"), nil)
	ctx.Solve()

	// Apply to a composite type
	sliceType := &SliceType{Elem: tv}
	resolved := ctx.Apply(sliceType)
	if resolved.String() != "[]int" {
		t.Errorf("expected []int, got %s", resolved)
	}
}

func TestUnifyWithContextNilCtx(t *testing.T) {
	// When ctx is nil, should delegate to regular Unify
	result, err := UnifyWithContext(nil, Primitive("int"), Primitive("int"))
	if err != nil {
		t.Fatalf("unify: %v", err)
	}
	if !TypeEquals(result, Primitive("int")) {
		t.Errorf("expected int, got %s", result)
	}
}

func TestUnifyWithContextTypeVar(t *testing.T) {
	ctx := NewInferenceContext()
	tv := ctx.Fresh("T")

	result, err := UnifyWithContext(ctx, tv, Primitive("string"))
	if err != nil {
		t.Fatalf("unify: %v", err)
	}
	if !TypeEquals(result, Primitive("string")) {
		t.Errorf("expected string, got %s", result)
	}
	// TypeVar should be resolved in context
	if !TypeEquals(ctx.Apply(tv), Primitive("string")) {
		t.Errorf("T should resolve to string, got %s", ctx.Apply(tv))
	}
}

func TestInferenceContextApplyPointer(t *testing.T) {
	ctx := NewInferenceContext()
	tv := ctx.Fresh("T")

	ctx.AddConstraint(tv, Primitive("int"), nil)
	ctx.Solve()

	ptrType := &PointerType{Elem: tv}
	resolved := ctx.Apply(ptrType)
	if resolved.String() != "*int" {
		t.Errorf("expected *int, got %s", resolved)
	}
}

func TestInferenceContextApplyMap(t *testing.T) {
	ctx := NewInferenceContext()
	tvK := ctx.Fresh("K")
	tvV := ctx.Fresh("V")

	ctx.AddConstraint(tvK, Primitive("string"), nil)
	ctx.AddConstraint(tvV, Primitive("int"), nil)
	ctx.Solve()

	mapType := &MapType{Key: tvK, Value: tvV}
	resolved := ctx.Apply(mapType)
	if resolved.String() != "map[string]int" {
		t.Errorf("expected map[string]int, got %s", resolved)
	}
}

func TestInferenceContextChainedVars(t *testing.T) {
	ctx := NewInferenceContext()
	tv1 := ctx.Fresh("T")
	tv2 := ctx.Fresh("U")

	// T = U, U = int => T should resolve to int
	ctx.AddConstraint(tv1, tv2, nil)
	ctx.AddConstraint(tv2, Primitive("int"), nil)

	err := ctx.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	if !TypeEquals(ctx.Apply(tv1), Primitive("int")) {
		t.Errorf("T should be int, got %s", ctx.Apply(tv1))
	}
	if !TypeEquals(ctx.Apply(tv2), Primitive("int")) {
		t.Errorf("U should be int, got %s", ctx.Apply(tv2))
	}
}

func TestUnifyWithContextBothTypeVars(t *testing.T) {
	ctx := NewInferenceContext()
	tv1 := ctx.Fresh("A")
	tv2 := ctx.Fresh("B")

	result, err := UnifyWithContext(ctx, tv1, tv2)
	if err != nil {
		t.Fatalf("unify: %v", err)
	}
	// One should be bound to the other
	if result == nil {
		t.Error("result should not be nil")
	}
}

func TestInferenceContextFreshIDs(t *testing.T) {
	ctx := NewInferenceContext()
	a := ctx.Fresh("A")
	b := ctx.Fresh("B")
	c := ctx.Fresh("C")

	if a.ID == b.ID || b.ID == c.ID || a.ID == c.ID {
		t.Error("Fresh should produce unique IDs")
	}
}
