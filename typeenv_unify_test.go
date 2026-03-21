package ferrouswheel

import "testing"

func TestUnifyIdenticalTypes(t *testing.T) {
	result, err := Unify(Primitive("int"), Primitive("int"))
	if err != nil {
		t.Fatalf("Unify: %v", err)
	}
	if result.String() != "int" {
		t.Errorf("got %s", result)
	}
}

func TestUnifyUntypedWithConcrete(t *testing.T) {
	result, err := Unify(&UntypedConstType{Kind: UntypedInt}, Primitive("int64"))
	if err != nil {
		t.Fatalf("Unify: %v", err)
	}
	if result.String() != "int64" {
		t.Errorf("got %s, want int64", result)
	}
}

func TestUnifyTwoUntyped(t *testing.T) {
	result, err := Unify(
		&UntypedConstType{Kind: UntypedInt},
		&UntypedConstType{Kind: UntypedFloat},
	)
	if err != nil {
		t.Fatalf("Unify: %v", err)
	}
	if result.String() != "float64" {
		t.Errorf("got %s, want float64", result)
	}
}

func TestUnifyNilWithPointer(t *testing.T) {
	ptr := &PointerType{Elem: Primitive("int")}
	result, err := Unify(&UntypedConstType{Kind: UntypedNil}, ptr)
	if err != nil {
		t.Fatalf("Unify: %v", err)
	}
	if result.String() != "*int" {
		t.Errorf("got %s, want *int", result)
	}
}

func TestUnifyMismatchErrors(t *testing.T) {
	_, err := Unify(Primitive("int"), Primitive("string"))
	if err == nil {
		t.Error("expected error for int vs string")
	}
}

func TestUnifyNamedVsUnderlying(t *testing.T) {
	named := &NamedType{Name: "MyInt", Underlying: Primitive("int")}
	_, err := Unify(named, Primitive("int"))
	if err == nil {
		t.Error("expected error: named types don't unify with underlying")
	}
}

func TestUnifyNilWithSlice(t *testing.T) {
	sl := &SliceType{Elem: Primitive("int")}
	result, err := Unify(&UntypedConstType{Kind: UntypedNil}, sl)
	if err != nil {
		t.Fatalf("Unify: %v", err)
	}
	if result.String() != "[]int" {
		t.Errorf("got %s", result)
	}
}
