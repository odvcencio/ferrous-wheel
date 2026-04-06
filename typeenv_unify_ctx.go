package ferrouswheel

// UnifyWithContext performs constraint-based unification, handling TypeVars.
// When both types are concrete, delegates to the existing Unify function.
// If ctx is nil, falls back to plain Unify.
func UnifyWithContext(ctx *InferenceContext, a, b Type) (Type, error) {
	if ctx == nil {
		return Unify(a, b)
	}
	// Apply current substitutions first
	a = ctx.Apply(a)
	b = ctx.Apply(b)
	// If either is a TypeVar, bind it and return
	if tv, ok := a.(*TypeVar); ok {
		ctx.subst[tv.ID] = b
		return b, nil
	}
	if tv, ok := b.(*TypeVar); ok {
		ctx.subst[tv.ID] = a
		return a, nil
	}
	// Both concrete -- delegate to existing Unify
	return Unify(a, b)
}
