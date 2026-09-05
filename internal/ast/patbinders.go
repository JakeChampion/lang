package ast

// EachArmBinder is the ONE answer to "what does this match arm bind".
//
// A pattern binds names in four places — the payload binders, the `@`
// whole-value name, a payload SUB-PATTERN (`Some(Ok(n))`), and a tuple
// pattern's elements, each of which is itself a full pattern position — and
// a consumer that reads only the first two treats the rest as free
// variables. Every place that got its own partial walk has gone wrong that
// way: modload mangled a tuple binder into a same-named module decl,
// shadowrename left it colliding on one IR slot, and constfold substituted a
// const over it (#8607, and #6993 for the `@` half before it).
//
// fn receives a POINTER to each non-empty name so a renaming pass can write
// through it; a collecting pass just reads. Empty names — the placeholder a
// slot carries when its sub-pattern supplies the bindings instead — are
// skipped, so no caller needs its own emptiness check.
func EachArmBinder(a *MatchArm, fn func(*string)) {
	if a == nil {
		return
	}
	eachArmBinder(a.Bindings, &a.AtBinding, a.Payloads, a.TupleElems, fn)
}

// EachArmExprBinder is EachArmBinder for the expression form. The two arm
// types carry the same pattern fields; only the body differs.
func EachArmExprBinder(a *MatchExprArm, fn func(*string)) {
	if a == nil {
		return
	}
	eachArmBinder(a.Bindings, &a.AtBinding, a.Payloads, a.TupleElems, fn)
}

func eachArmBinder(bindings []string, atBinding *string, payloads []*TuplePatElem, tupleElems []TuplePatElem, fn func(*string)) {
	for i := range bindings {
		if bindings[i] != "" {
			fn(&bindings[i])
		}
	}
	if *atBinding != "" {
		fn(atBinding)
	}
	for _, p := range payloads {
		if p != nil {
			EachPatElemBinder(p, fn)
		}
	}
	for i := range tupleElems {
		EachPatElemBinder(&tupleElems[i], fn)
	}
}

// EachPatElemBinder walks ONE pattern position — a tuple element or a
// payload slot, which are the same node — and everything nested inside it.
func EachPatElemBinder(el *TuplePatElem, fn func(*string)) {
	if el == nil {
		return
	}
	if el.Name != "" {
		fn(&el.Name)
	}
	if el.AtBinding != "" {
		fn(&el.AtBinding)
	}
	for i := range el.VariantBindings {
		if el.VariantBindings[i] != "" {
			fn(&el.VariantBindings[i])
		}
	}
	for _, p := range el.VariantPayloads {
		if p != nil {
			EachPatElemBinder(p, fn)
		}
	}
	for i := range el.Nested {
		EachPatElemBinder(&el.Nested[i], fn)
	}
}

// ArmBinderNames is EachArmBinder for a caller that only wants the names.
func ArmBinderNames(a *MatchArm) []string {
	var out []string
	EachArmBinder(a, func(p *string) { out = append(out, *p) })
	return out
}

// ArmExprBinderNames is ArmBinderNames for the expression form.
func ArmExprBinderNames(a *MatchExprArm) []string {
	var out []string
	EachArmExprBinder(a, func(p *string) { out = append(out, *p) })
	return out
}
