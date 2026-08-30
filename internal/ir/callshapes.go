// How many operand-stack entries a call moves.
//
// Both models of the operand stack need this answer, and neither can
// derive it from the op alone: an argument's slot count depends on the
// ABI, and a result's depends on the callee's return type, which lives
// in the Program rather than at the call site. `internal/ssa`'s lift
// used the IR's ARGUMENT count for both, which is right only when every
// argument is one word and every callee returns exactly one.
//
// A second copy of these rules would agree with the verifier by
// accident rather than by construction — the drift #7803 is about — so
// this is the one definition, and `stackChecker` uses it too.
package ir

import "github.com/jakechampion/lang/internal/ast"

// CallShapes answers how many operand-stack entries a call-shaped op
// takes and leaves, for one program under one string ABI.
type CallShapes struct {
	ptrW       int
	twoWordStr bool
	known      map[string]*Func
	externs    map[string]*ExternFunc
}

// NewCallShapes indexes p's functions and externs.
//
// The ABI comes off the Program, never from `ast.UseTwoWordStrings`:
// that reads a global the lowering sets and RESTORES, so a program
// inspected afterwards answers one-word.
func NewCallShapes(p *Program) *CallShapes {
	c := &CallShapes{
		ptrW:       p.PtrW,
		twoWordStr: p.TwoWordStr,
		known:      make(map[string]*Func, len(p.Funcs)),
		externs:    make(map[string]*ExternFunc, len(p.Externs)),
	}
	for _, f := range p.Funcs {
		c.known[f.Name] = f
	}
	for _, e := range p.Externs {
		c.externs[e.Name] = e
	}
	return c
}

// callShapesFrom builds a CallShapes over indexes the caller already
// holds.
func callShapesFrom(known map[string]*Func, externs map[string]*ExternFunc, ptrW int, twoWordStr bool) *CallShapes {
	return &CallShapes{ptrW: ptrW, twoWordStr: twoWordStr, known: known, externs: externs}
}

// ArgSlots is how many entries op's arguments occupy, or a reason the
// shape is unknown.
//
// Under the one-word ABI every argument is one entry, so the IR's own
// count is already right. Under two words it is not, and the answer
// comes from whichever description of the callee is available — the
// call site's own argument types first, since a generic instantiation
// records them there and nowhere else.
func (c *CallShapes) ArgSlots(op Op) (int, string) {
	n := int(op.I32)
	if n < 0 {
		return 0, "negative argument count"
	}
	if !c.twoWordStr {
		return n, ""
	}
	if at := op.ArgTypes(); len(at) == n {
		if anyErased(at) {
			return 0, "call with an erased argument type"
		}
		return c.slotCount(at), ""
	}
	if sig := op.Sig(); sig != nil && len(sig.Params) == n {
		if anyErased(sig.Params) {
			return 0, "call with an erased argument type"
		}
		return c.slotCount(sig.Params), ""
	}
	if callee, ok := c.known[op.Str]; ok && len(callee.Params) == n {
		total := 0
		for _, pm := range callee.Params {
			if erased(pm.Type) {
				return 0, "call with an erased argument type"
			}
			total += len(c.typeSlots(pm.Type))
		}
		return total, ""
	}
	if sig, ok := providedSigs[op.Str]; ok && sig.argSlots >= 0 {
		return sig.argSlots, ""
	}
	return 0, "unknown argument shape for callee " + op.Str
}

// ResultSlots is how many entries op leaves behind, or a reason the
// shape is unknown. Zero is a real answer: a void callee pushes
// nothing.
func (c *CallShapes) ResultSlots(op Op) (int, string) {
	kinds, bail := c.resultKinds(op)
	return len(kinds), bail
}

// resultKinds is ResultSlots with the slot classes the stack checker
// needs. Keeping one implementation is the point of this file.
func (c *CallShapes) resultKinds(op Op) ([]valKind, string) {
	switch op.Kind {
	case OpCallIndirect, OpCallDyn:
		sig := op.Sig()
		if sig == nil {
			return nil, "indirect call with no signature"
		}
		if erased(sig.Result) {
			return nil, "call through an erased result type"
		}
		return c.typeSlots(sig.Result), ""
	case OpCallDirectPair:
		return []valKind{kInt, kInt}, ""
	}
	if callee, ok := c.known[op.Str]; ok {
		if erased(callee.ReturnType) {
			return nil, "call to " + op.Str + ", whose result type is an unresolved type parameter"
		}
		return c.typeSlots(callee.ReturnType), ""
	}
	if e, ok := c.externs[op.Str]; ok {
		return c.typeSlots(e.ReturnType), ""
	}
	if sig, ok := providedSigs[op.Str]; ok {
		return sig.result.slots(c.twoWordStr), ""
	}
	return nil, "unknown result shape for callee " + op.Str
}

// typeSlots and slotCount are stackChecker's, against this ABI.
func (c *CallShapes) typeSlots(t ast.Type) []valKind {
	return typeSlotsABI(t, c.ptrW, c.twoWordStr)
}

func (c *CallShapes) slotCount(ts []ast.Type) int {
	n := 0
	for _, t := range ts {
		n += len(c.typeSlots(t))
	}
	return n
}
