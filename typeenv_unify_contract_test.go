package ferrouswheel

import (
	"strings"
	"testing"
)

func TestUnifyRejectsConstantKindsThatCannotUseTargetType(t *testing.T) {
	for _, tt := range []struct {
		name     string
		constant UntypedKind
		target   Type
	}{
		{"nil as integer", UntypedNil, Primitive("int")},
		{"boolean as integer", UntypedBool, Primitive("int")},
		{"text as integer", UntypedString, Primitive("int")},
		{"integer as boolean", UntypedInt, Primitive("bool")},
		{"floating point as integer", UntypedFloat, Primitive("int")},
		{"nil as named integer", UntypedNil, &NamedType{Name: "Count", Underlying: Primitive("int")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				var left, right Type = &UntypedConstType{Kind: tt.constant}, tt.target
				if reverse {
					left, right = right, left
				}
				if got, err := Unify(left, right); err == nil || got != nil || !strings.Contains(err.Error(), "incompatible") {
					t.Fatalf("Unify(%s, %s) = %v, %v; want incompatible type error", left, right, got, err)
				}
			}
		})
	}
}

func TestUnifyAcceptsCompatibleUntypedConstants(t *testing.T) {
	for _, tt := range []struct {
		constant UntypedKind
		target   Type
	}{
		{UntypedInt, Primitive("int64")},
		{UntypedInt, Primitive("float64")},
		{UntypedFloat, Primitive("float32")},
		{UntypedRune, Primitive("rune")},
		{UntypedString, Primitive("string")},
		{UntypedBool, Primitive("bool")},
		{UntypedNil, &PointerType{Elem: Primitive("int")}},
		{UntypedNil, &NamedType{Name: "StringMap", Underlying: &MapType{Key: Primitive("string"), Value: Primitive("string")}}},
	} {
		got, err := Unify(&UntypedConstType{Kind: tt.constant}, tt.target)
		if err != nil || !TypeEquals(got, tt.target) {
			t.Errorf("Unify(untyped kind %d, %s) = %v, %v", tt.constant, tt.target, got, err)
		}
	}
}

func TestUnifyRejectsIncompatibleUntypedKinds(t *testing.T) {
	for _, pair := range [][2]UntypedKind{
		{UntypedNil, UntypedInt},
		{UntypedBool, UntypedInt},
		{UntypedString, UntypedRune},
	} {
		left := &UntypedConstType{Kind: pair[0]}
		right := &UntypedConstType{Kind: pair[1]}
		if _, err := Unify(left, right); err == nil || !strings.Contains(err.Error(), "incompatible") {
			t.Errorf("Unify(%s, %s) error = %v, want incompatible types", left, right, err)
		}
	}
}

func TestResolveRejectsIncompatibleTernaryBranches(t *testing.T) {
	source := `package main
func pick(flag bool) {
    let value = flag ? 1 : false
    _ = value
}`
	_, err := resolveExpressionFixture(t, source, "flag ? 1 : false", "ternary_expression")
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("ternary type error = %v, want incompatible branch types", err)
	}
}
