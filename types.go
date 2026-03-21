package ferrouswheel

import "fmt"

// Type is the unified type representation for FW and Go types.
type Type interface {
	String() string
	typeTag()
}

// Primitive represents basic Go types (int, string, bool, float64, etc.).
type Primitive string

func (p Primitive) String() string { return string(p) }
func (p Primitive) typeTag()       {}

// ChanDir represents channel directionality.
type ChanDir int

const (
	ChanBidi ChanDir = iota
	ChanRecv
	ChanSend
)

// PointerType represents *T.
type PointerType struct{ Elem Type }

func (p *PointerType) String() string { return "*" + p.Elem.String() }
func (p *PointerType) typeTag()       {}

// SliceType represents []T.
type SliceType struct{ Elem Type }

func (s *SliceType) String() string { return "[]" + s.Elem.String() }
func (s *SliceType) typeTag()       {}

// MapType represents map[K]V.
type MapType struct {
	Key   Type
	Value Type
}

func (m *MapType) String() string { return "map[" + m.Key.String() + "]" + m.Value.String() }
func (m *MapType) typeTag()       {}

// ChanType represents chan T, <-chan T, or chan<- T.
type ChanType struct {
	Elem Type
	Dir  ChanDir
}

func (c *ChanType) String() string {
	switch c.Dir {
	case ChanRecv:
		return "<-chan " + c.Elem.String()
	case ChanSend:
		return "chan<- " + c.Elem.String()
	default:
		return "chan " + c.Elem.String()
	}
}
func (c *ChanType) typeTag() {}

// FuncType represents func(params) results.
type FuncType struct {
	Params  []Type
	Results []Type
}

func (f *FuncType) String() string {
	s := "func("
	for i, p := range f.Params {
		if i > 0 {
			s += ", "
		}
		s += p.String()
	}
	s += ")"
	switch len(f.Results) {
	case 0:
		// no return
	case 1:
		s += " " + f.Results[0].String()
	default:
		s += " ("
		for i, r := range f.Results {
			if i > 0 {
				s += ", "
			}
			s += r.String()
		}
		s += ")"
	}
	return s
}
func (f *FuncType) typeTag() {}

// TupleType represents multi-return values. Not first-class.
type TupleType struct{ Elems []Type }

func (t *TupleType) String() string {
	s := "("
	for i, e := range t.Elems {
		if i > 0 {
			s += ", "
		}
		s += e.String()
	}
	return s + ")"
}
func (t *TupleType) typeTag()           {}
func (t *TupleType) IsFirstClass() bool { return false }

// StructType represents a named struct.
type StructType struct {
	Name       string
	Fields     map[string]Type
	Comparable bool
}

func (s *StructType) String() string { return s.Name }
func (s *StructType) typeTag()       {}

// InterfaceType represents a named interface.
type InterfaceType struct {
	Name    string
	Methods map[string]*FuncType
}

func (i *InterfaceType) String() string { return i.Name }
func (i *InterfaceType) typeTag()       {}

// EnumType represents a FW enum (sum type).
type EnumType struct {
	Name     string
	Variants map[string][]Type
}

func (e *EnumType) String() string { return e.Name }
func (e *EnumType) typeTag()       {}

// GenericType represents a parameterized type.
type GenericType struct {
	Name       string
	TypeParams []Type
}

func (g *GenericType) String() string {
	s := g.Name + "["
	for i, tp := range g.TypeParams {
		if i > 0 {
			s += ", "
		}
		s += tp.String()
	}
	return s + "]"
}
func (g *GenericType) typeTag() {}

// TypeParamType represents a type parameter with constraint.
type TypeParamType struct {
	Name       string
	Constraint Type
}

func (tp *TypeParamType) String() string { return tp.Name }
func (tp *TypeParamType) typeTag()       {}

// NamedType represents a type from a specific package.
type NamedType struct {
	Pkg        string
	Name       string
	Underlying Type
}

func (n *NamedType) String() string {
	if n.Pkg != "" {
		return n.Pkg + "." + n.Name
	}
	return n.Name
}
func (n *NamedType) typeTag() {}

// UntypedKind classifies untyped constant kinds.
type UntypedKind int

const (
	UntypedInt UntypedKind = iota
	UntypedFloat
	UntypedString
	UntypedBool
	UntypedRune
	UntypedNil
)

// UntypedConstType represents an untyped constant.
type UntypedConstType struct{ Kind UntypedKind }

func (u *UntypedConstType) String() string {
	return "untyped " + u.Default().String()
}
func (u *UntypedConstType) typeTag() {}

// Default returns the concrete type this untyped constant defaults to.
func (u *UntypedConstType) Default() Type {
	switch u.Kind {
	case UntypedInt:
		return Primitive("int")
	case UntypedFloat:
		return Primitive("float64")
	case UntypedString:
		return Primitive("string")
	case UntypedBool:
		return Primitive("bool")
	case UntypedRune:
		return Primitive("rune")
	case UntypedNil:
		return nil
	default:
		return Primitive("int")
	}
}

// UnresolvedType is the poison pill -- triggers compile error at emit time.
type UnresolvedType struct {
	File string
	Line int
	Col  int
	Msg  string
}

func (u *UnresolvedType) String() string { return "<unresolved>" }
func (u *UnresolvedType) typeTag()       {}
func (u *UnresolvedType) Error() string {
	return fmt.Sprintf("%s:%d:%d: type error: %s", u.File, u.Line, u.Col, u.Msg)
}

// TypeEquals checks structural equality of two types.
// NamedType{"int"} is NOT equal to Primitive{"int"} -- this enforces Go's named type distinction.
func TypeEquals(a, b Type) bool {
	switch a := a.(type) {
	case Primitive:
		b, ok := b.(Primitive)
		return ok && a == b
	case *PointerType:
		b, ok := b.(*PointerType)
		return ok && TypeEquals(a.Elem, b.Elem)
	case *SliceType:
		b, ok := b.(*SliceType)
		return ok && TypeEquals(a.Elem, b.Elem)
	case *MapType:
		b, ok := b.(*MapType)
		return ok && TypeEquals(a.Key, b.Key) && TypeEquals(a.Value, b.Value)
	case *ChanType:
		b, ok := b.(*ChanType)
		return ok && a.Dir == b.Dir && TypeEquals(a.Elem, b.Elem)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) || len(a.Results) != len(b.Results) {
			return false
		}
		for i := range a.Params {
			if !TypeEquals(a.Params[i], b.Params[i]) {
				return false
			}
		}
		for i := range a.Results {
			if !TypeEquals(a.Results[i], b.Results[i]) {
				return false
			}
		}
		return true
	case *NamedType:
		b, ok := b.(*NamedType)
		return ok && a.Pkg == b.Pkg && a.Name == b.Name
	case *StructType:
		b, ok := b.(*StructType)
		return ok && a.Name == b.Name
	case *EnumType:
		b, ok := b.(*EnumType)
		return ok && a.Name == b.Name
	case *InterfaceType:
		b, ok := b.(*InterfaceType)
		return ok && a.Name == b.Name
	case *UntypedConstType:
		b, ok := b.(*UntypedConstType)
		return ok && a.Kind == b.Kind
	default:
		return false
	}
}

// ZeroExpr returns the Go zero-value expression for a type.
// Returns error for non-comparable structs.
func ZeroExpr(typ Type) (string, error) {
	switch t := typ.(type) {
	case Primitive:
		switch string(t) {
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "byte", "rune", "uintptr":
			return "0", nil
		case "string":
			return `""`, nil
		case "bool":
			return "false", nil
		case "error":
			return "nil", nil
		default:
			return "0", nil
		}
	case *PointerType, *SliceType, *MapType, *ChanType, *InterfaceType, *FuncType:
		return "nil", nil
	case *StructType:
		if !t.Comparable {
			return "", fmt.Errorf("?? requires a comparable type; %s contains non-comparable fields", t.Name)
		}
		return "(" + t.Name + "{})", nil
	case *NamedType:
		return ZeroExpr(t.Underlying)
	case *EnumType:
		return "(" + t.Name + "{})", nil
	default:
		return "", fmt.Errorf("cannot determine zero value for %s", typ)
	}
}
