package ir

import (
	"fmt"
	"os"

	"github.com/jakechampion/lang/internal/ast"
)

// Producer-consumer fusion for recognized std/array pipelines (#9731): a chain
// of elementwise and selection stages ending in a reduction becomes one loop,
// and the intermediate arrays stop existing.
//
// docs/ARRAY-FUSION-OPERATORS.md is the contract this implements and the
// reason it is shaped as it is. Each operator contributes `init` / `step` /
// `finish` fragments, a `step` has three outcomes (pass through, rebind,
// skip), and the fused body is the CONCATENATION of the steps — so the pass
// must never branch on the stage sequence. A special case for a particular
// chain would be a bug against that document even if it emitted correct code,
// because the guarantee it discharges is quantified over any number of stages
// in any order.
//
// # Why this is safe to write by hand at the IR level
//
// Two facts, both established by reading the lowering rather than reasoning
// about it, and both of which make the emission far smaller than it looks.
//
// The fused form needs NO reference counting. `run_loop` in
// examples/array_pipeline/map_map_reduce.fern is the hand-written equivalent
// of the chain beside it, and it lowers with no `rc.inc` and no
// `__fern_arr_dec` at all: the input is a borrowed parameter and the loop
// builds nothing. So the RC delta between the combinator chain and its fusion
// is exactly "the intermediate's release goes away", and it goes away by
// itself because the whole op range is replaced.
//
// And calling an element function needs no closure ABI. The combinators
// themselves do it with `push args; push the function value; call_indirect`,
// with no env pointer in sight — so this emits the same three ops. Whether
// that call is later devirtualised is `Defunctionalise`'s business, exactly as
// it is for the unfused code.
//
// # What is refused
//
// Everything this does not understand. A stage outside the fragment
// vocabulary, an element function whose slot cannot be followed, an element
// width other than 8 bytes in the first slice — each leaves the pipeline
// exactly as it was. Refusing is always correct here; fusing something
// misunderstood is not.

// fusedPipeline is a chain this pass can emit as one loop.
type fusedPipeline struct {
	first, last int   // op range to replace, inclusive
	recvSlot    int32 // the array being traversed
	seedOp      Op    // the reduction's seed, evaluated before the loop
	stages      []fusedStage
	sinkFn      int32 // slot of the reduction's combining function
	elemBytes   int32
	// elemType and accType size the scratch slots. They matter because wasm
	// type-checks its locals and the register backends do not: slots declared
	// as the zero NumberType read as i32, which an arm64 build runs happily
	// and wasm rejects outright ("expected i32, found i64"). Carrying the
	// real types is what makes the same emission valid on every backend.
	elemType ast.Type
	accType  ast.Type
	// sinkVerb is "fold" or "reduce". They differ only in their init and
	// finish fragments: fold seeds from a constant and returns the
	// accumulator bare, reduce seeds from the first element to arrive and
	// wraps the answer in an Option. The steps in between are identical, so
	// the difference lives exactly where the operator document puts it.
	sinkVerb string
}

// fusedStage is one elementwise or selection stage.
type fusedStage struct {
	verb string // "map" or "filter"
	fn   int32  // slot holding the element function
}

// FuseArrayPipelines rewrites every fusible pipeline in the program and
// returns how many it fused. Callers that only want to know what is there
// should use RecognizeArrayPipelines, which changes nothing.
func FuseArrayPipelines(prog *Program, ptrW int) int {
	// An off switch, so a miscompilation suspected here can be confirmed or
	// ruled out in one run rather than by rebuilding the compiler.
	if os.Getenv("FERN_NO_ARRAY_FUSION") == "1" {
		return 0
	}
	n := 0
	effectful := effectfulFuncs(prog)
	for _, fn := range prog.Funcs {
		// std/array's own bodies are left alone: a combinator implemented in
		// terms of another is the library's business, and rewriting `map`
		// into a loop over itself is not what this is for.
		if _, isStdlibBody := arrayVerbOf(fn.Name); isStdlibBody {
			continue
		}
		for {
			p, ok := planFusion(fn, effectful)
			if !ok {
				break
			}
			emitFusion(fn, p, ptrW)
			n++
		}
	}
	return n
}

// effectfulFuncs names the functions that can reach an observable effect, by
// transitive closure over the direct-call edges.
//
// docs/ARRAY-ALGEBRA.md §1 makes this a precondition of fusing at all, and the
// reason is §2: fusion interleaves the stages. Unfused, `map(f).map(g)` runs
// every `f` and then every `g`; fused, it runs `f(x0) g(y0) f(x1) g(y1)`. The
// multiset of applications is the same either way — §2's rule 2 — but if `f`
// and `g` both write to the world, the ORDER of those writes changes, and that
// is observable. Restricting fusion to element functions that touch nothing
// makes the two orders indistinguishable.
//
// The test is "reaches a builtin", not "reaches a CAPABILITY-TAGGED builtin"
// as §1 spells it. `internal/caps` classifies what a package may be permitted
// to reach, which is a security question: `print` is deliberately ungated
// there and is exactly the effect that makes an interleave visible. So the
// allowed set is inverted instead — a program function (walked through) or a
// codegen runtime helper (array indexing, refcount traffic, arithmetic
// shims), and nothing else. An indirect call counts as effectful too: its
// target is not known here, so nothing can be said about what it reaches.
//
// This is deliberately conservative. A `map` whose element function calls a
// pure builtin does not fuse, which costs coverage and cannot cost
// correctness.
func effectfulFuncs(prog *Program) map[string]bool {
	known := make(map[string]bool, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		known[fn.Name] = true
	}
	bad := map[string]bool{}
	callers := map[string][]string{} // callee -> functions calling it
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case OpCallIndirect:
				bad[fn.Name] = true
			case OpCallDirect:
				switch {
				case known[op.Str]:
					callers[op.Str] = append(callers[op.Str], fn.Name)
				case op.Runtime:
					// A codegen helper: no effect of its own.
				default:
					bad[fn.Name] = true
				}
			}
		}
	}
	// Propagate to callers until nothing new is marked.
	queue := make([]string, 0, len(bad))
	for name := range bad {
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, caller := range callers[name] {
			if !bad[caller] {
				bad[caller] = true
				queue = append(queue, caller)
			}
		}
	}
	return bad
}

// FusibleArrayPipelines names the pipelines the fusion pass would rewrite, as
// `<function>#<first op>` keys. The report uses it to say per site whether
// fusion applies; the decision does not consult the pointer width, which
// shapes only the emission, so a report need not know the target to answer.
func FusibleArrayPipelines(prog *Program) map[string]bool {
	out := map[string]bool{}
	effectful := effectfulFuncs(prog)
	for _, fn := range prog.Funcs {
		if _, isStdlibBody := arrayVerbOf(fn.Name); isStdlibBody {
			continue
		}
		for _, pl := range recognizeInFunc(fn) {
			if _, ok := planOne(fn, pl, effectful); ok {
				out[arrayPipelineKey(fn.Name, pl)] = true
			}
		}
	}
	return out
}

// arrayPipelineKey identifies a pipeline within a program.
func arrayPipelineKey(fnName string, pl ArrayPipeline) string {
	return fmt.Sprintf("%s#%d", fnName, pl.Stages[0].Op)
}

// planFusion finds the first fusible chain in fn, or reports that there is
// none. One at a time, because emitting shifts every later op index.
func planFusion(fn *Func, effectful map[string]bool) (fusedPipeline, bool) {
	for _, pl := range recognizeInFunc(fn) {
		p, ok := planOne(fn, pl, effectful)
		if ok {
			return p, true
		}
	}
	return fusedPipeline{}, false
}

func planOne(fn *Func, pl ArrayPipeline, effectful map[string]bool) (fusedPipeline, bool) {
	if len(pl.Stages) < 2 {
		return fusedPipeline{}, false // nothing to fuse into anything
	}
	// The vocabulary of the first slice: elementwise and selection stages,
	// then a `fold` or a `reduce`.
	sink := pl.Stages[len(pl.Stages)-1]
	if sink.Verb != "fold" && sink.Verb != "reduce" {
		return fusedPipeline{}, false
	}
	var stages []fusedStage
	for _, s := range pl.Stages[:len(pl.Stages)-1] {
		if s.Verb != "map" && s.Verb != "filter" {
			return fusedPipeline{}, false
		}
		stages = append(stages, fusedStage{verb: s.Verb})
	}

	calls := collectArrayCalls(fn)
	byOp := map[int]arrayCall{}
	for _, c := range calls {
		byOp[c.op] = c
	}
	// Element function slots, and the array the chain starts from.
	for i, s := range pl.Stages {
		c, ok := byOp[s.Op]
		// §1's purity boundary: the element function must be statically
		// resolved (a slot the matcher could follow to a construction here)
		// and must reach nothing capability-tagged.
		if !ok || c.fnSlot < 0 || c.element == "" || effectful[c.element] {
			return fusedPipeline{}, false
		}
		if i < len(stages) {
			stages[i].fn = c.fnSlot
		}
	}
	head := byOp[pl.Stages[0].Op]
	tail := byOp[pl.Stages[len(pl.Stages)-1].Op]
	if head.recv < 0 {
		return fusedPipeline{}, false
	}
	// Every stage's element type, not just the head's. A `map` may change it
	// — `(i64) => i32` is an ordinary map — and one `elem` slot cannot hold
	// both. Each stage's array argument is the previous stage's output, so
	// requiring all of them to be the same 8-byte element is what makes the
	// single slot sound.
	width, elemT, ok := elemWidthOf(fn.Ops[head.op])
	if !ok {
		return fusedPipeline{}, false
	}
	for _, s := range pl.Stages {
		c, found := byOp[s.Op]
		if !found {
			return fusedPipeline{}, false
		}
		w, et, okw := elemWidthOf(fn.Ops[c.op])
		if !okw || w != width || et != elemT {
			return fusedPipeline{}, false
		}
	}
	var seed Op
	accT := elemT
	if sink.Verb == "fold" {
		seed, ok = foldSeedOp(fn, tail)
		if !ok {
			return fusedPipeline{}, false
		}
		accT = seedType(seed)
	}
	first, ok := rangeStart(fn, head)
	if !ok {
		return fusedPipeline{}, false
	}
	last, ok := rangeEnd(fn, tail)
	if !ok {
		return fusedPipeline{}, false
	}
	return fusedPipeline{
		first: first, last: last, recvSlot: head.recv, seedOp: seed,
		stages: stages, sinkFn: byOp[sink.Op].fnSlot, elemBytes: width,
		elemType: elemT, accType: accT, sinkVerb: sink.Verb,
	}, true
}

// elemWidthOf reads the element size from the combinator call's recorded
// argument types. Only 8-byte elements are handled in the first slice: the
// index helper is chosen by width and each one wants its own test, so the
// others are refused rather than guessed at.
func elemWidthOf(op Op) (int32, ast.Type, bool) {
	for _, t := range op.ArgTypes() {
		if at, isArr := t.(ast.ArrayType); isArr {
			if n, isNum := at.Elem.(ast.NumberType); isNum && n.Width == 64 {
				return 8, n, true
			}
			return 0, nil, false
		}
	}
	return 0, nil, false
}

// seedType is the accumulator's type, read from the constant that seeds it.
// `fold[T, A]`'s A is the seed's type and need not be the element's.
func seedType(seed Op) ast.Type {
	if seed.Kind == OpConstI64 {
		return ast.NumberType{Width: 64, Signed: true}
	}
	return ast.NumberType{Width: 32, Signed: true}
}

// foldSeedOp returns the single op that produces `fold`'s seed. Only a
// constant seed is taken: an expression would have to be hoisted out of the
// chain with its own evaluation-order argument, and the fragment model says
// `init` runs once before the loop — which a constant trivially satisfies.
//
// The seed is found by ANCHORING on the sink's function argument rather than
// by scanning back for the nearest constant. `fold(seed, f)` pushes the
// receiver, the seed, then f, so the seed is the op immediately before f's
// construction — and a nearest-constant scan would take a constant that f's
// own closure construction emitted (a captured literal) as the seed instead,
// which is a wrong answer rather than a missed fusion.
func foldSeedOp(fn *Func, c arrayCall) (Op, bool) {
	k, ok := elementFuncStart(fn, c.op, c.fnSlot)
	if !ok || k == 0 {
		return Op{}, false
	}
	switch prev := fn.Ops[k-1]; prev.Kind {
	case OpConstI64, OpConstI32:
		return prev, true
	}
	return Op{}, false
}

// rangeStart is the op that begins the chain: the load of the receiver.
func rangeStart(fn *Func, head arrayCall) (int, bool) {
	for k := head.op - 1; k >= 0; k-- {
		if fn.Ops[k].Kind == OpLoadLocal && fn.Ops[k].I32 == head.recv {
			return k, true
		}
	}
	return 0, false
}

// rangeEnd is the last op of the sink's epilogue — the releases of the
// intermediates and the closures, which the fused form does not need for the
// intermediates and re-emits for the closures.
func rangeEnd(fn *Func, tail arrayCall) (int, bool) {
	end := tail.op
	for k := tail.op + 1; k < len(fn.Ops); k++ {
		switch fn.Ops[k].Kind {
		case OpLoadLocal, OpConstI32, OpDrop, OpLine:
			continue
		case OpCallDirect:
			if isReclaimCallee(fn.Ops[k]) || fn.Ops[k].Str == "__drop_closure_value" {
				end = k + 1 // the drop that follows it
				continue
			}
			return end, true
		default:
			return end, true
		}
	}
	return end, true
}

// emitFusion replaces the chain with one loop.
func emitFusion(fn *Func, p fusedPipeline, ptrW int) {
	base := int32(len(fn.Params)) + int32(len(fn.Locals)) + int32(len(fn.ScratchTypes))
	acc, idx, elem := base, base+1, base+2
	// `reduce` needs three more: whether anything has arrived yet, the box
	// being built, and the Option the two arms of the finish agree on.
	seen, boxBase, result := base+3, base+4, base+5
	i32 := ast.NumberType{Width: 32, Signed: true}
	fn.ScratchTypes = append(fn.ScratchTypes,
		p.accType,  // the accumulator
		i32,        // the cursor
		p.elemType) // the element in flight
	if p.sinkVerb == "reduce" {
		fn.ScratchTypes = append(fn.ScratchTypes, i32, i32, i32)
	}

	var out []Op
	add := func(ops ...Op) { out = append(out, ops...) }

	// The closures the stages call are already built by the ops this range
	// replaces, so they are rebuilt here in the same order and released in
	// the epilogue exactly as before.
	for k := p.first; k <= p.last; k++ {
		if op := fn.Ops[k]; op.Kind == OpMakeClosure || op.Kind == OpConstFunc {
			add(op)
			if k+1 <= p.last && fn.Ops[k+1].Kind == OpStoreLocal {
				add(fn.Ops[k+1])
			}
		}
	}

	// init: the cursor starts at zero, and the accumulator takes fold's seed.
	// reduce has no seed — its accumulator is the first element to arrive, so
	// its init is the flag that says none has.
	if p.sinkVerb == "fold" {
		add(p.seedOp, Op{Kind: OpStoreLocal, I32: acc})
	} else {
		add(Op{Kind: OpConstI32}, Op{Kind: OpStoreLocal, I32: seen})
	}
	add(Op{Kind: OpConstI32}, Op{Kind: OpStoreLocal, I32: idx})

	// while idx < len(xs)
	add(Op{Kind: OpBlock}, Op{Kind: OpLoop})
	add(Op{Kind: OpLoadLocal, I32: idx})
	add(Op{Kind: OpLoadLocal, I32: p.recvSlot}, Op{Kind: OpConstI32, I32: 4}, Op{Kind: OpSub}, Op{Kind: OpLoad})
	add(Op{Kind: OpLtS, Width: 32}, Op{Kind: OpNot}, Op{Kind: OpBrIf, I32: 1})

	// elem = xs[idx]
	add(Op{Kind: OpLoadLocal, I32: p.recvSlot}, Op{Kind: OpLoadLocal, I32: idx})
	add(Op{Kind: OpCallDirect, Str: "__arr_idx_8_nc", Width: ResAddr, I32: 2},
		Op{Kind: OpLoad, Width: 64})
	add(Op{Kind: OpStoreLocal, I32: elem})

	// The steps, concatenated. This loop is the whole of the compositional
	// claim: it does not look at what came before or after.
	for _, s := range p.stages {
		switch s.verb {
		case "map":
			add(Op{Kind: OpLoadLocal, I32: elem}, Op{Kind: OpLoadLocal, I32: s.fn})
			add(Op{Kind: OpCallIndirect, I32: 1}, Op{Kind: OpStoreLocal, I32: elem})
		case "filter":
			// skip: the predicate says no, so advance and take the next.
			add(Op{Kind: OpLoadLocal, I32: elem}, Op{Kind: OpLoadLocal, I32: s.fn})
			add(Op{Kind: OpCallIndirect, I32: 1}, Op{Kind: OpNot})
			add(Op{Kind: OpIf})
			add(Op{Kind: OpLoadLocal, I32: idx}, Op{Kind: OpConstI32, I32: 1}, Op{Kind: OpAdd, Width: 32})
			add(Op{Kind: OpStoreLocal, I32: idx}, Op{Kind: OpBr, I32: 1})
			add(Op{Kind: OpEnd})
		}
	}

	// the sink's step: acc = h(acc, elem). reduce's first arrival seeds the
	// accumulator instead of combining into it — `h` is never called on one
	// element, which is what makes `reduce` on a singleton return that
	// element rather than `h(x, x)`.
	combine := []Op{
		{Kind: OpLoadLocal, I32: acc}, {Kind: OpLoadLocal, I32: elem}, {Kind: OpLoadLocal, I32: p.sinkFn},
		{Kind: OpCallIndirect, I32: 2}, {Kind: OpStoreLocal, I32: acc},
	}
	if p.sinkVerb == "fold" {
		add(combine...)
	} else {
		add(Op{Kind: OpLoadLocal, I32: seen}, Op{Kind: OpConstI32}, Op{Kind: OpEq, Width: 32})
		add(Op{Kind: OpIf, I32: BlockTypeVoid})
		add(Op{Kind: OpLoadLocal, I32: elem}, Op{Kind: OpStoreLocal, I32: acc})
		add(Op{Kind: OpConstI32, I32: 1}, Op{Kind: OpStoreLocal, I32: seen})
		add(Op{Kind: OpElse})
		add(combine...)
		add(Op{Kind: OpEnd})
	}

	// idx++
	add(Op{Kind: OpLoadLocal, I32: idx}, Op{Kind: OpConstI32, I32: 1}, Op{Kind: OpAdd, Width: 32})
	add(Op{Kind: OpStoreLocal, I32: idx})
	add(Op{Kind: OpBr}, Op{Kind: OpEnd}, Op{Kind: OpEnd})

	// finish: the reduction's answer, left where the chain's was. fold's is
	// the accumulator; reduce's is `Some(acc)`, or `None` when the loop never
	// ran — an empty input, or a filter that admitted nothing.
	if p.sinkVerb == "fold" {
		add(Op{Kind: OpLoadLocal, I32: acc})
	} else {
		add(Op{Kind: OpLoadLocal, I32: seen}, Op{Kind: OpConstI32}, Op{Kind: OpEq, Width: 32})
		add(Op{Kind: OpIf, I32: BlockTypeVoid})
		add(Op{Kind: OpEnumSentinel, I32: 1}, Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
		add(Op{Kind: OpStoreLocal, I32: result})
		add(Op{Kind: OpElse})
		add(optionSomeOps(p.accType, acc, boxBase, ptrW)...)
		add(Op{Kind: OpStoreLocal, I32: result})
		add(Op{Kind: OpEnd})
		add(Op{Kind: OpLoadLocal, I32: result})
	}

	// The closure releases the replaced range carried, re-emitted verbatim —
	// and AFTER the finish, because that is where the lowering puts them. A
	// combinator call leaves its result on the stack and the reclaim traffic
	// runs underneath it, so the store that consumes the result comes last.
	//
	// The order is not cosmetic. `collectArrayCalls` takes a chain's receiver
	// to be the first local load in the window since the previous non-array
	// call, and a bare `local.load acc` sitting after the drops is the first
	// thing a LATER chain in the same function sees. It was read as that
	// chain's receiver, which made the second fusion in a function traverse
	// the first one's accumulator and swallow its finish.
	for k := p.first; k <= p.last; k++ {
		if fn.Ops[k].Kind == OpCallDirect && fn.Ops[k].Str == "__drop_closure_value" {
			add(fn.Ops[k-1], fn.Ops[k], Op{Kind: OpDrop})
		}
	}

	next := make([]Op, 0, len(fn.Ops)+len(out))
	next = append(next, fn.Ops[:p.first]...)
	next = append(next, out...)
	next = append(next, fn.Ops[p.last+1:]...)
	fn.Ops = next
}
