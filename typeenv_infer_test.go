package ferrouswheel

import (
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

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

func TestResolveWithExpectedBasic(t *testing.T) {
	env := NewTypeEnv()
	env.InferCtx = NewInferenceContext()
	env.RegisterVar("x", Primitive("int"))

	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("get language: %v", err)
	}
	parser := gotreesitter.NewParser(lang)
	src := []byte(`package main
func main() {
	_ = x
}
`)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Find the identifier "x" node
	root := tree.RootNode()
	var xNode *gotreesitter.Node
	var walk func(*gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n.Type(lang) == "identifier" && string(src[n.StartByte():n.EndByte()]) == "x" {
			xNode = n
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	if xNode == nil {
		t.Fatal("could not find x identifier node")
	}

	typ, err := env.ResolveWithExpected(xNode, lang, src, Primitive("int"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !TypeEquals(typ, Primitive("int")) {
		t.Errorf("expected int, got %s", typ)
	}
}

func TestResolveWithExpectedFallback(t *testing.T) {
	env := NewTypeEnv()
	env.InferCtx = NewInferenceContext()
	// "y" is NOT registered — Resolve will fail

	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("get language: %v", err)
	}
	parser := gotreesitter.NewParser(lang)
	src := []byte(`package main
func main() {
	_ = y
}
`)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	root := tree.RootNode()
	var yNode *gotreesitter.Node
	var walk func(*gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n.Type(lang) == "identifier" && string(src[n.StartByte():n.EndByte()]) == "y" {
			yNode = n
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	if yNode == nil {
		t.Fatal("could not find y identifier node")
	}

	// With an expected type, should fall back to the expected type
	typ, err := env.ResolveWithExpected(yNode, lang, src, Primitive("string"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !TypeEquals(typ, Primitive("string")) {
		t.Errorf("expected string (fallback), got %s", typ)
	}
}

func TestResolveWithExpectedNilExpected(t *testing.T) {
	env := NewTypeEnv()
	env.InferCtx = NewInferenceContext()
	env.RegisterVar("z", Primitive("float64"))

	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("get language: %v", err)
	}
	parser := gotreesitter.NewParser(lang)
	src := []byte(`package main
func main() {
	_ = z
}
`)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	root := tree.RootNode()
	var zNode *gotreesitter.Node
	var walk func(*gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n.Type(lang) == "identifier" && string(src[n.StartByte():n.EndByte()]) == "z" {
			zNode = n
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	if zNode == nil {
		t.Fatal("could not find z identifier node")
	}

	// With nil expected, should behave like Resolve
	typ, err := env.ResolveWithExpected(zNode, lang, src, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !TypeEquals(typ, Primitive("float64")) {
		t.Errorf("expected float64, got %s", typ)
	}
}
