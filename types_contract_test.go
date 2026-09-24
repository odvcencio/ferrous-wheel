package ferrouswheel

import (
	"go/parser"
	"strings"
	"testing"
)

func TestZeroExprProducesGoExpressionsForKnownTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  Type
		want string
	}{
		{"number", Primitive("int64"), "0"},
		{"text", Primitive("string"), `""`},
		{"flag", Primitive("bool"), "false"},
		{"error", Primitive("error"), "nil"},
		{"slice", &SliceType{Elem: Primitive("int")}, "nil"},
		{"pointer", &PointerType{Elem: Primitive("int")}, "nil"},
		{"comparable struct", &StructType{Name: "Point", Comparable: true}, "(Point{})"},
		{"named integer", &NamedType{Name: "Count", Underlying: Primitive("int")}, "0"},
		{"named pointer", &NamedType{Name: "Handle", Underlying: &PointerType{Elem: Primitive("int")}}, "nil"},
		{"named struct", &NamedType{Name: "pkg.Point", Underlying: &StructType{Name: "Point", Comparable: true}}, "(pkg.Point{})"},
		{"enum", &EnumType{Name: "State"}, "(State{})"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ZeroExpr(tc.typ)
			if err != nil {
				t.Fatalf("zero expression: %v", err)
			}
			if got != tc.want {
				t.Fatalf("zero expression = %q, want %q", got, tc.want)
			}
			if _, err := parser.ParseExpr(got); err != nil {
				t.Fatalf("zero expression %q is invalid Go: %v", got, err)
			}
		})
	}
}

func TestZeroExprRejectsUnsafeOrUnknownTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  Type
		want string
	}{
		{"noncomparable struct", &StructType{Name: "Record", Comparable: false}, "non-comparable fields"},
		{"unresolved named type", &NamedType{Name: "Mystery"}, "cannot determine zero value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ZeroExpr(tc.typ)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("zero expression error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTypeEqualsHonorsStructureAndNamedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b Type
		want bool
	}{
		{"same map", &MapType{Key: Primitive("string"), Value: &SliceType{Elem: Primitive("int")}}, &MapType{Key: Primitive("string"), Value: &SliceType{Elem: Primitive("int")}}, true},
		{"different map value", &MapType{Key: Primitive("string"), Value: Primitive("int")}, &MapType{Key: Primitive("string"), Value: Primitive("bool")}, false},
		{"different channel direction", &ChanType{Elem: Primitive("int"), Dir: ChanRecv}, &ChanType{Elem: Primitive("int"), Dir: ChanSend}, false},
		{"same function", &FuncType{Params: []Type{Primitive("int")}, Results: []Type{Primitive("string")}}, &FuncType{Params: []Type{Primitive("int")}, Results: []Type{Primitive("string")}}, true},
		{"different function result", &FuncType{Params: []Type{Primitive("int")}, Results: []Type{Primitive("string")}}, &FuncType{Params: []Type{Primitive("int")}, Results: []Type{Primitive("bool")}}, false},
		{"same tuple", &TupleType{Elems: []Type{Primitive("int"), Primitive("string")}}, &TupleType{Elems: []Type{Primitive("int"), Primitive("string")}}, true},
		{"different tuple element", &TupleType{Elems: []Type{Primitive("int"), Primitive("string")}}, &TupleType{Elems: []Type{Primitive("int"), Primitive("bool")}}, false},
		{"same generic", &GenericType{Name: "Result", TypeParams: []Type{Primitive("int"), Primitive("error")}}, &GenericType{Name: "Result", TypeParams: []Type{Primitive("int"), Primitive("error")}}, true},
		{"different generic argument", &GenericType{Name: "Result", TypeParams: []Type{Primitive("int")}}, &GenericType{Name: "Result", TypeParams: []Type{Primitive("string")}}, false},
		{"different generic name", &GenericType{Name: "Result", TypeParams: []Type{Primitive("int")}}, &GenericType{Name: "Option", TypeParams: []Type{Primitive("int")}}, false},
		{"named type differs from primitive", &NamedType{Name: "int", Underlying: Primitive("int")}, Primitive("int"), false},
		{"same name in different packages", &NamedType{Pkg: "one", Name: "ID"}, &NamedType{Pkg: "two", Name: "ID"}, false},
		{"same enum name", &EnumType{Name: "State"}, &EnumType{Name: "State"}, true},
		{"different interface name", &InterfaceType{Name: "Read"}, &InterfaceType{Name: "Write"}, false},
		{"different untyped kinds", &UntypedConstType{Kind: UntypedInt}, &UntypedConstType{Kind: UntypedRune}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TypeEquals(tc.a, tc.b); got != tc.want {
				t.Fatalf("TypeEquals(%s, %s) = %t, want %t", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestInferenceSolvesNestedTupleGenericAndChannelTypes(t *testing.T) {
	ctx := NewInferenceContext()
	key := ctx.Fresh("Key")
	value := ctx.Fresh("Value")
	left := &GenericType{Name: "Pair", TypeParams: []Type{&TupleType{Elems: []Type{key, &ChanType{Elem: value, Dir: ChanRecv}}}}}
	right := &GenericType{Name: "Pair", TypeParams: []Type{&TupleType{Elems: []Type{Primitive("string"), &ChanType{Elem: Primitive("int"), Dir: ChanRecv}}}}}
	ctx.AddConstraint(left, right, nil)
	if err := ctx.Solve(); err != nil {
		t.Fatalf("solve nested type: %v", err)
	}
	if got := ctx.Apply(left).String(); got != right.String() {
		t.Fatalf("substituted type = %s, want %s", got, right)
	}
}

func TestInferenceRejectsIncompatibleTypeShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b Type
	}{
		{"channel direction", &ChanType{Elem: Primitive("int"), Dir: ChanRecv}, &ChanType{Elem: Primitive("int"), Dir: ChanSend}},
		{"function arity", &FuncType{Params: []Type{Primitive("int")}}, &FuncType{Params: []Type{Primitive("int"), Primitive("int")}}},
		{"tuple length", &TupleType{Elems: []Type{Primitive("int")}}, &TupleType{Elems: []Type{Primitive("int"), Primitive("string")}}},
		{"generic name", &GenericType{Name: "One", TypeParams: []Type{Primitive("int")}}, &GenericType{Name: "Two", TypeParams: []Type{Primitive("int")}}},
		{"map key", &MapType{Key: Primitive("string"), Value: Primitive("int")}, &MapType{Key: Primitive("int"), Value: Primitive("int")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewInferenceContext()
			ctx.AddConstraint(tc.a, tc.b, nil)
			if err := ctx.Solve(); err == nil {
				t.Fatalf("incompatible types %s and %s unified", tc.a, tc.b)
			}
		})
	}
}
