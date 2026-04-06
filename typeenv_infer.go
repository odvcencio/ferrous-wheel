package ferrouswheel

import (
	"fmt"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// Constraint records that two types must be equal.
type Constraint struct {
	Left   Type
	Right  Type
	Origin *gotreesitter.Node // for error reporting (may be nil)
}

// InferenceContext manages type variables and constraints for bidirectional inference.
type InferenceContext struct {
	constraints []Constraint
	subst       map[int]Type // TypeVar ID -> resolved Type
	nextID      int
}

// NewInferenceContext creates a fresh inference context.
func NewInferenceContext() *InferenceContext {
	return &InferenceContext{
		subst: make(map[int]Type),
	}
}

// Fresh creates a new type variable with the given name.
func (ctx *InferenceContext) Fresh(name string) *TypeVar {
	tv := &TypeVar{ID: ctx.nextID, Name: name}
	ctx.nextID++
	return tv
}

// AddConstraint records that left and right must unify.
func (ctx *InferenceContext) AddConstraint(left, right Type, origin *gotreesitter.Node) {
	ctx.constraints = append(ctx.constraints, Constraint{Left: left, Right: right, Origin: origin})
}

// Solve processes all constraints, building the substitution map.
func (ctx *InferenceContext) Solve() error {
	for _, c := range ctx.constraints {
		if err := ctx.unify(c.Left, c.Right); err != nil {
			return err
		}
	}
	return nil
}

// Apply recursively replaces all TypeVars in a type with their resolved values.
func (ctx *InferenceContext) Apply(t Type) Type {
	switch v := t.(type) {
	case *TypeVar:
		if resolved, ok := ctx.subst[v.ID]; ok {
			return ctx.Apply(resolved)
		}
		return t
	case *FuncType:
		params := make([]Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = ctx.Apply(p)
		}
		results := make([]Type, len(v.Results))
		for i, r := range v.Results {
			results[i] = ctx.Apply(r)
		}
		return &FuncType{Params: params, Results: results}
	case *PointerType:
		return &PointerType{Elem: ctx.Apply(v.Elem)}
	case *SliceType:
		return &SliceType{Elem: ctx.Apply(v.Elem)}
	case *MapType:
		return &MapType{Key: ctx.Apply(v.Key), Value: ctx.Apply(v.Value)}
	case *ChanType:
		return &ChanType{Elem: ctx.Apply(v.Elem), Dir: v.Dir}
	case *TupleType:
		elems := make([]Type, len(v.Elems))
		for i, e := range v.Elems {
			elems[i] = ctx.Apply(e)
		}
		return &TupleType{Elems: elems}
	case *GenericType:
		args := make([]Type, len(v.TypeParams))
		for i, a := range v.TypeParams {
			args[i] = ctx.Apply(a)
		}
		return &GenericType{Name: v.Name, TypeParams: args}
	default:
		// Primitive, NamedType, StructType, EnumType, InterfaceType,
		// UntypedConstType, UnresolvedType, TypeParamType: return as-is
		return t
	}
}

// unify recursively walks two type trees, binding TypeVars and checking structural equality.
func (ctx *InferenceContext) unify(a, b Type) error {
	// Follow existing substitutions
	a = ctx.resolve(a)
	b = ctx.resolve(b)

	// If both are the same TypeVar, nothing to do
	if av, ok := a.(*TypeVar); ok {
		if bv, ok := b.(*TypeVar); ok && av.ID == bv.ID {
			return nil
		}
	}

	// If a is a TypeVar, bind it
	if av, ok := a.(*TypeVar); ok {
		ctx.subst[av.ID] = b
		return nil
	}

	// If b is a TypeVar, bind it
	if bv, ok := b.(*TypeVar); ok {
		ctx.subst[bv.ID] = a
		return nil
	}

	// Both are concrete -- structural unification
	switch av := a.(type) {
	case Primitive:
		if bv, ok := b.(Primitive); ok && av == bv {
			return nil
		}
	case *PointerType:
		if bv, ok := b.(*PointerType); ok {
			return ctx.unify(av.Elem, bv.Elem)
		}
	case *SliceType:
		if bv, ok := b.(*SliceType); ok {
			return ctx.unify(av.Elem, bv.Elem)
		}
	case *MapType:
		if bv, ok := b.(*MapType); ok {
			if err := ctx.unify(av.Key, bv.Key); err != nil {
				return err
			}
			return ctx.unify(av.Value, bv.Value)
		}
	case *ChanType:
		if bv, ok := b.(*ChanType); ok && av.Dir == bv.Dir {
			return ctx.unify(av.Elem, bv.Elem)
		}
	case *FuncType:
		if bv, ok := b.(*FuncType); ok {
			if len(av.Params) != len(bv.Params) || len(av.Results) != len(bv.Results) {
				return fmt.Errorf("cannot unify %s with %s: arity mismatch", a, b)
			}
			for i := range av.Params {
				if err := ctx.unify(av.Params[i], bv.Params[i]); err != nil {
					return err
				}
			}
			for i := range av.Results {
				if err := ctx.unify(av.Results[i], bv.Results[i]); err != nil {
					return err
				}
			}
			return nil
		}
	case *TupleType:
		if bv, ok := b.(*TupleType); ok {
			if len(av.Elems) != len(bv.Elems) {
				return fmt.Errorf("cannot unify %s with %s: element count mismatch", a, b)
			}
			for i := range av.Elems {
				if err := ctx.unify(av.Elems[i], bv.Elems[i]); err != nil {
					return err
				}
			}
			return nil
		}
	case *GenericType:
		if bv, ok := b.(*GenericType); ok && av.Name == bv.Name {
			if len(av.TypeParams) != len(bv.TypeParams) {
				return fmt.Errorf("cannot unify %s with %s: type parameter count mismatch", a, b)
			}
			for i := range av.TypeParams {
				if err := ctx.unify(av.TypeParams[i], bv.TypeParams[i]); err != nil {
					return err
				}
			}
			return nil
		}
	case *NamedType:
		if bv, ok := b.(*NamedType); ok && av.Pkg == bv.Pkg && av.Name == bv.Name {
			return nil
		}
	case *StructType:
		if bv, ok := b.(*StructType); ok && av.Name == bv.Name {
			return nil
		}
	case *EnumType:
		if bv, ok := b.(*EnumType); ok && av.Name == bv.Name {
			return nil
		}
	case *InterfaceType:
		if bv, ok := b.(*InterfaceType); ok && av.Name == bv.Name {
			return nil
		}
	case *UntypedConstType:
		if bv, ok := b.(*UntypedConstType); ok && av.Kind == bv.Kind {
			return nil
		}
		// Allow untyped to unify with concrete (delegate to existing Unify logic)
		if _, err := Unify(a, b); err == nil {
			return nil
		}
	}

	// Also try existing Unify for untyped-on-right
	if _, ok := b.(*UntypedConstType); ok {
		if _, err := Unify(a, b); err == nil {
			return nil
		}
	}

	return fmt.Errorf("cannot unify %s with %s", a, b)
}

// resolve follows the substitution chain for a TypeVar.
func (ctx *InferenceContext) resolve(t Type) Type {
	tv, ok := t.(*TypeVar)
	if !ok {
		return t
	}
	if resolved, ok := ctx.subst[tv.ID]; ok {
		final := ctx.resolve(resolved)
		// Path compression
		ctx.subst[tv.ID] = final
		return final
	}
	return t
}
