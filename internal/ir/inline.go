// Function inlining at the IR level.
//
// Replaces every OpCallDirect to a "small" callee with a copy of
// the callee's op list, with arguments bound to fresh local slots
// in the caller. Runs after Lower / before Fold / DCE so the
// constants exposed by a substituted body get folded in the same
// pipeline.
//
// Eligibility — deliberately conservative, but extended past the
// strictly-leaf shape so stdlib helpers with simple control flow
// or sub-calls still inline:
//
//   - body length ≤ inlineSizeLimit (skip blow-up cases).
//   - no OpCallIndirect / OpMakeClosure: closures bring an extra
//     layer of dispatch + a `__call_scratch` requirement; not
//     worth the per-callee book-keeping yet.
//   - no recursive call to the caller itself (inlineOps detects
//     this by name match against the enclosing function).
//
// What gets rewritten at a call site:
//
//   <args pushed to stack by previous ops>
//   OpCallDirect <callee> argc=P
//
// becomes
//
//   <args pushed by previous ops>            (unchanged)
//   OpStoreLocal <fresh slot for param P-1>  (rightmost arg → rightmost param)
//   …
//   OpStoreLocal <fresh slot for param 0>
//   OpBlock <result-type>                    (return-target wrapper)
//   <callee's body ops, with every OpLoadLocal / OpStoreLocal index
//    rewritten to add the offset of the fresh slot range AND every
//    OpReturn / OpReturnVoid translated to a br targeting the
//    wrapper>
//   OpEnd                                    (closes wrapper)
//
// The wrapper block lets early returns fall through to the
// inlined-body's continuation by branching out — same shape an
// optimising C compiler uses for inlined returns. Falls through to
// the wrapper end when the trailing terminator runs.
//
// The caller's ScratchTypes grows by the callee's full slot list
// (params + locals + scratches) — each entry's type carries
// through so codegen declares the right shape (i32 vs f32 vs
// i64 vs f64) for each WAT local. Each inline site gets its own
// slot range; the simple version doesn't try to coalesce ranges
// across consecutive inlines.

package ir

import (
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// inlineSizeLimit caps how many ops a callee can have to inline at a
// call site OUTSIDE any loop. Tuned to allow the bulk of stdlib
// helpers (e.g. __substr_eq, __map_hash, the small hex / b64
// char-classifiers) to inline through their internal control flow.
// Tweak as real workloads emerge.
const inlineSizeLimit = 80

// inlineLoopSizeLimit is the boosted cap for call sites INSIDE a loop
// (#4412 Rec §7's cheap loop-depth slice of the cost-model inliner):
// the call overhead there is paid every iteration, so a larger body
// still profits. Candidacy admits bodies up to this cap; siteAllows
// applies the depth-appropriate one per call site. Code growth stays
// bounded — the boost only fires inside loops and inlineMaxPasses
// still caps chain depth.
const inlineLoopSizeLimit = 160

// inlineMaxUnitOps is the whole-program op count above which the general
// size policy stops applying — the analogue of GCC's `--param
// large-unit-insns`, and the reason the pass can run on the native backends.
//
// The per-callee caps above bound each SITE; nothing bounds the SUM, and
// on a program with thousands of small helpers the sum is the whole
// story. Measured on the self-hosted compiler (2.70M ops, 5,285
// functions) emitting x86-64: unbudgeted inlining grew the assembly 2.70x
// (106 MB -> 285 MB) and its emit 2.31x, for a 2-3% runtime LOSS
// (docs/PERFORMANCE-AUDIT-2026-08.md §7 item 6). Capping whole-program
// growth at 30% still cost 1.74x the assembly and 1.71x the emit, and at
// 0% growth the pass's own six walks cost 37% of the emit to change the
// output by half a percent. There is no setting at which it pays on a
// unit that size, so the general policy stops here; inlineTinyLeaves is
// the narrower thing such a unit gets instead.
//
// Below the ceiling it pays and the pass is unchanged: on examples/bench
// (31-2,202 ops) retired instructions fall 4.95% on average, up to 23.9%
// on call_overhead. Real programs sit far from the line — the bench
// corpus tops out at 2.2k ops and the compiler's smallest module is 15k,
// with nothing measured between 15k and 691k.
const inlineMaxUnitOps = 20000

// inlineTinyLeafOps is the callee size that stays eligible above the
// ceiling — see inlineTinyLeaves. Swept on the two over-ceiling programs
// the repo has, coreutils/sort.fern (44,205 ops) and
// examples/self_host/fern.fern (2,672,218 ops), against the same sources
// with the pass declining as it did before:
//
//	cap    sort -n Ir   sort -k2,2n Ir   sort .text   self-host .text
//	  8       -2.23%         -1.87%        -1.54%         not measured
//	 24       -2.90%        -12.29%        -0.74%           +0.09%
//	 32       -2.90%        -12.29%        +0.14%           +0.80%
//
// 24 is the knee. It covers the byte classifiers a comparison loop calls
// once per byte — `is_blank_b` and `fold_up` at 18 ops, `key_numeric` at 21
// — which is the whole of the `-k2,2n` column, and stops below the bodies
// with real work in them (`__method_u64_checked_mul` at 70) that are the
// general policy's territory. 32 buys no measurable instruction and costs
// 8.5x the self-host assembly growth.
//
// sort's own text comes out SMALLER, which is the shape of the win rather
// than a rounding artefact: inlining EVERY site of a call-free helper
// leaves the original unreferenced, so the dead-function cull takes it, and
// the call sequence it replaced — argument spills, `call`, `ret`, the
// callee frame — was larger than the body. Admitting only sites inside a
// loop inverts that. Measured at the same 24: text +0.62% instead of
// -0.74%, because every partly-inlined helper has to stay, and `sort -n`
// keeps a quarter of its win. So there is no loop-depth tier here; the
// general policy (siteAllows) is where one earns its keep.
const inlineTinyLeafOps = 24

// inlineTinyBudgetDivisor caps what the tiny-leaf mode may add to an
// over-ceiling unit at 1/N of that unit's op count.
//
// It never binds on either measured program — sort wants 3.3% of its ops
// and the self-host 0.56% — which is the point: the sum is naturally
// small because tiny leaves are few and their call sites are countable.
// The budget is the guard against a unit where they are not, so that the
// worst case is a bounded growth rather than an unbounded one. Inline runs
// twice per battery, so the whole-battery bound is twice this.
const inlineTinyBudgetDivisor = 10

// Inline rewrites every OpCallDirect to an eligible callee in prog
// as the callee's op list with parameters bound to fresh local
// slots. The pass is conservative — programs without inlineable
// callees are unchanged in O(N) walk time.
//
// Iterates up to inlineMaxPasses times: a single pass substitutes
// the OUTER callee (e.g. inlining `s.contains(...)` into main),
// but the inlined body may itself contain calls to other eligible
// callees (`__substr_eq` from inside `contains`). Without a
// second pass, those inner calls survive — the stdlib is full of
// "small helper calls another small helper" chains. Cap the
// iteration count to bound code growth on the worst case.
//
// Order in the production pipeline: Lower → Inline → Fold → DCE →
// emit. Inlining first exposes constant
// arithmetic in substituted bodies (e.g. `dbl(7)` becomes `7 * 2`)
// for Fold to collapse, then DCE drops anything unreachable that
// surfaces.
func Inline(prog *Program) {
	unit := 0
	for _, fn := range prog.Funcs {
		unit += len(fn.Ops)
	}
	if unit > inlineMaxUnitOps {
		inlineTinyLeaves(prog, unit)
		return
	}
	mode := &inlineMode{}
	for i := 0; i < inlineMaxPasses; i++ {
		candidates := findInlineCandidates(prog, mode)
		if len(candidates) == 0 {
			return
		}
		changed := false
		for _, fn := range prog.Funcs {
			before := len(fn.Ops)
			fn.Ops = inlineOps(fn, fn.Ops, candidates, mode)
			if len(fn.Ops) != before {
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// inlineTinyLeaves is what a unit over inlineMaxUnitOps gets instead of
// nothing: the one shape whose cost the ceiling's evidence does not cover.
//
// The ceiling exists because the general policy's 80/160-op bodies, summed
// over thousands of helpers, multiply the assembly and the emit for a
// runtime loss. That argument does not reach a call-free helper of a few
// ops — `is_digit(c)`, `line_len(p)` — where the call sequence is most of
// what the site costs. Measured on coreutils/sort.fern (44,205 ops) and the
// self-hosted compiler (2,672,218 ops), inlining every such site adds 1,477
// and 14,892 ops — +3.3% and +0.56% — where the unbudgeted general policy
// produces 2.7x the assembly.
//
// Three properties keep it that cheap, and all three are load-bearing:
//
//   - A LEAF callee contains no call of any kind, so splicing it creates no
//     new call site. One walk is therefore a fixpoint — no equivalent of
//     inlineMaxPasses, and no chain multiplying one helper's body by
//     another's.
//   - TINY bounds each site's growth by the callee's own op count, at a cap
//     small enough that the spliced body measures no larger than the call
//     sequence it replaces (inlineTinyLeafOps).
//   - The BUDGET bounds the sum. Real programs sit far under it (above);
//     it is the guard against a program with a hundred thousand sites, not
//     a tuning knob.
//
// The eligibility rules the general path applies for correctness —
// @noinline, self-recursion, closure drop thunks, the wasm dyn-slot
// splice — all still apply: this widens WHICH units the pass considers,
// never what it is willing to splice.
func inlineTinyLeaves(prog *Program, unit int) {
	mode := &inlineMode{tinyLeaf: true, budget: unit / inlineTinyBudgetDivisor}
	if mode.budget <= 0 {
		return
	}
	candidates := findInlineCandidates(prog, mode)
	if len(candidates) == 0 {
		return
	}
	for _, fn := range prog.Funcs {
		if mode.budget <= 0 {
			return
		}
		fn.Ops = inlineOps(fn, fn.Ops, candidates, mode)
	}
}

// inlineMaxPasses caps the iteration depth. Three passes covers
// the deepest call chain the migrated stdlib builds today
// (`Map.set` → `__map_grow` / `__map_hash` → no further inlineable
// callees). Bumping further has diminishing returns and risks
// runaway code growth on pathological inputs.
const inlineMaxPasses = 3

// inlineMode carries the size policy one Inline call runs under, and the
// growth budget it spends. The zero value is the general policy that
// applies below inlineMaxUnitOps; tinyLeaf is the carve-out above it.
type inlineMode struct {
	tinyLeaf bool
	budget   int // ops left to insert; consulted only when tinyLeaf
}

// admits reports whether fn may be a candidate at all under this mode.
// The general mode takes whatever isInlineable passed; the tiny-leaf mode
// additionally requires a call-free body within inlineTinyLeafOps — and
// applies that size test itself, since isInlineable waives its own cap for
// an @inline hint and this mode's bounds are what make it safe over the
// ceiling.
func (m *inlineMode) admits(fn *Func) bool {
	if !m.tinyLeaf {
		return true
	}
	return len(fn.Ops) <= inlineTinyLeafOps && isCallFree(fn)
}

// allows applies the per-call-site policy. Under the general mode that is
// siteAllows; under the tiny-leaf mode candidacy has already bounded the
// body, so all that is left per site is the budget.
func (m *inlineMode) allows(cand inlineCandidate, loopDepth int, constArgs bool) bool {
	if !m.tinyLeaf {
		return siteAllows(cand, loopDepth, constArgs)
	}
	return len(cand.body) <= m.budget
}

// spend records a splice against the budget. A no-op under the general
// mode, whose bound is inlineMaxUnitOps plus the per-site size caps.
func (m *inlineMode) spend(ops int) {
	if m.tinyLeaf {
		m.budget -= ops
	}
}

// isCallFree reports whether fn's body reaches no other function: no call
// of any kind, and no op that hands a function's address out for someone
// else to call. Splicing such a body introduces no call site, which is why
// the tiny-leaf mode needs only one walk. The property is IR-level: an op a
// backend expands into a runtime helper is not a call here, and what bounds
// that body is the size cap rather than this predicate.
func isCallFree(fn *Func) bool {
	for _, op := range fn.Ops {
		if op.Kind == OpCallIndirect || namesCallee(op.Kind) {
			return false
		}
	}
	return true
}

// namesCallee reports whether an op of this kind carries a function name in
// Str — a direct call of any form, a closure body, or an address-of. It is
// the same set dead_funcs.go's reachability walk keeps alive, minus the rc
// ops, whose Str names a runtime helper rather than an IR func.
func namesCallee(k OpKind) bool {
	switch k {
	case OpCallDirect, OpCallDirectPair, OpCallClosureDirect,
		OpMakeClosure, OpMakeEnv, OpConstFunc:
		return true
	}
	return false
}

// inlineCandidate is an eligible callee snapshot taken before any
// rewriting begins. Storing the body slice keeps the inliner from
// observing rewrites done to the callee itself when it appears
// later in the prog.Funcs list.
type inlineCandidate struct {
	fn   *Func
	body []Op // body up to and including the trailing OpReturn / OpReturnVoid
	// slotTypes lists the type of every slot the callee uses, in
	// slot order: params first, then user locals, then scratches.
	// Inlining appends these to the caller's ScratchTypes so
	// codegen declares each new local with the right WAT type.
	slotTypes []ast.Type
	// returnBlockType describes the wrapper block's result type
	// matched against the callee's return type. Void functions
	// open a void wrapper and use plain `br`; value-returning
	// functions open a typed wrapper so the br carries the
	// returned value through.
	returnBlockType int32
	// refs is the total number of references to this callee (direct calls,
	// closure-direct calls, and OpConstFunc address-of), counted across the
	// whole program PLUS one for an externally-reachable function.
	//
	// siteAllows admits a refs == 1 callee over the flat size cap because
	// inlining then moves the sole reference's body to that call site and
	// the original becomes dead — net-neutral on code size. That rationale
	// depends on the original dying, which is exactly what an export does
	// not do: its definition is rooted by the dead-function cull because a
	// caller outside the program reaches it. Counting that invisible caller
	// keeps such a function off the net-neutral path, so it is admitted only
	// on the flat cap's own terms. Inlining an export into an internal
	// caller is still fine — the standalone copy simply survives alongside.
	refs int
}

// findInlineCandidates scans every function in prog and returns
// the subset that meets the eligibility rules. The map's key is
// the function name so Inline can resolve OpCallDirect.Str
// directly.
func findInlineCandidates(prog *Program, mode *inlineMode) map[string]inlineCandidate {
	out := map[string]inlineCandidate{}
	ptrW := prog.PtrW
	if ptrW == 0 {
		ptrW = 4
	}
	// The reference tally is a second whole-program walk, and it feeds only
	// siteAllows' net-neutral single-reference rule — which the tiny-leaf
	// mode does not use. Skip it there: over the ceiling this walk is over
	// millions of ops, and a walk that answers nothing is exactly the cost
	// the ceiling was drawn to avoid.
	var refs map[string]int
	if !mode.tinyLeaf {
		refs = programRefCounts(prog)
	}
	for _, fn := range prog.Funcs {
		if !isInlineable(fn) || !mode.admits(fn) {
			continue
		}
		slots := make([]ast.Type, 0, len(fn.Params)+len(fn.Locals)+len(fn.ScratchTypes))
		for _, p := range fn.Params {
			slots = append(slots, p.Type)
		}
		for _, v := range fn.Locals {
			slots = append(slots, v.Type)
		}
		slots = append(slots, fn.ScratchTypes...)
		// Two-word inline `dyn Trait` slot (wasm, ptrW==4): a dyn local is one
		// IR slot but a two-value `[data, vtable]` operand, and its exit-sweep
		// reclaim loads that pair and hands it to __drop_dyn_<set> as two args.
		// The splice mis-orders that pair against the slot's own store — the
		// reclaim runs on the slot's *pre-init* value and the fresh box is
		// stored un-swept, corrupting the freelist (a struct/enum-behind-dyn
		// churn traps: memory fault at 0xffffffff — #4786). Two-word STRINGS
		// inline fine (their reclaim is a single-arg __fern_str_dec, no split
		// pair), so this is dyn-specific. Boxed natives (ptrW==8) hold the dyn
		// value in one word and are unaffected. Skip such callees — the
		// closure-drop-thunk exclusion above is the same "don't inline this
		// shape" precedent.
		if ptrW == 4 && slotsHaveDynTrait(slots) {
			continue
		}
		out[fn.Name] = inlineCandidate{
			fn:              fn,
			body:            fn.Ops,
			slotTypes:       slots,
			returnBlockType: returnBlockTypeFor(fn.ReturnType, ptrW),
			refs:            refs[fn.Name],
		}
	}
	return out
}

// programRefCounts tallies, per function name, how many ops across the
// whole program reference it. It counts EVERY op kind that keeps a
// function alive in dead_funcs.go's reachability walk — direct /
// direct-pair / closure-direct calls, OpMakeClosure / OpMakeEnv closure
// bodies, and OpConstFunc address-of — so a count of 1 guarantees the
// function is dead after its sole call site is inlined (net-neutral code
// size). Counting a subset would risk calling a closure-referenced
// function "single-use" and growing code instead.
func programRefCounts(prog *Program) map[string]int {
	refs := map[string]int{}
	for _, fn := range prog.Funcs {
		// The caller an export has outside the program appears in no op
		// stream; count it so the "sole reference, original dies" size
		// shortcut cannot fire on a function whose definition must stay.
		if fn.ExternallyReachable {
			refs[fn.Name]++
		}
		for _, op := range fn.Ops {
			if namesCallee(op.Kind) && op.Str != "" {
				refs[op.Str]++
			}
		}
	}
	return refs
}

// slotsHaveDynTrait reports whether any slot type is a `dyn Trait` value —
// the two-word inline representation on wasm the inliner mis-splices (#4786).
func slotsHaveDynTrait(slots []ast.Type) bool {
	for _, t := range slots {
		if _, ok := t.(ast.DynTraitType); ok {
			return true
		}
	}
	return false
}

// dropWalksElements reports whether a generated drop helper loops over
// elements — an array, map or closure-capture walk — rather than releasing
// a fixed set of fields.
func dropWalksElements(fn *Func) bool {
	for _, op := range fn.Ops {
		if op.Kind == OpLoop {
			return true
		}
	}
	return false
}

// isInlineable reports whether fn meets every eligibility rule.
// Internal control flow (block / loop / if / br / brif) and direct
// calls to other functions are allowed; OpCallIndirect /
// OpMakeClosure / oversized bodies disqualify.
func isInlineable(fn *Func) bool {
	// Source-level hints (#4412 Rec §14): @noinline is absolute;
	// @inline lifts only the SIZE cap — every shape-safety exclusion
	// below (closure drop thunks, indirect calls / closure makes, the
	// wasm dyn-slot case in findInlineCandidates) still applies, since
	// those guard correctness or unimplemented splice mechanics, not
	// cost.
	if fn.InlineHint == ast.InlineHintNever {
		return false
	}
	if len(fn.Ops) == 0 {
		return false
	}
	// A body that calls itself is never a candidate: every copy carries
	// the recursive sites into its host, and each pass inlines those
	// again — a 135-op recursive enum drop with three self-calls turned
	// a 400-op function into 51,000 ops in three passes. Its own sites
	// were already refused (cand.fn == fn); this refuses everyone else's.
	for _, op := range fn.Ops {
		if (op.Kind == OpCallDirect || op.Kind == OpCallClosureDirect) && !op.Runtime && op.Str == fn.Name {
			return false
		}
	}
	// Candidacy admits up to the LOOP cap — the flat cap is applied
	// per call site by siteAllows, so an 81..160-op helper can inline
	// where it's called from a loop while staying a call elsewhere.
	if fn.InlineHint != ast.InlineHintAlways && len(fn.Ops) > inlineLoopSizeLimit {
		return false
	}
	// Never inline a per-closure drop thunk: it reads captures at
	// [env+offset], and ElideClosurePair only recognises the closure
	// drop as a benign reader (so the closure can become a bare env)
	// when the thunk is a single OpCallDirect. Inlining it splices
	// raw [slot+off] capture loads that disqualify elision, leaving a
	// {fn,env} PAIR the inlined thunk then misreads as an env.
	if strings.HasPrefix(fn.Name, "__closure_drop_") {
		return false
	}
	last := fn.Ops[len(fn.Ops)-1].Kind
	if last != OpReturn && last != OpReturnVoid && last != OpReturnPair {
		return false
	}
	for _, op := range fn.Ops {
		switch op.Kind {
		case OpCallIndirect, OpMakeClosure:
			return false
		}
	}
	return true
}

// inlineOps walks ops linearly and substitutes every OpCallDirect
// (and OpCallClosureDirect — defunctionalisation produces those
// at known target names too) to a known candidate. Calls to
// non-candidate functions are left untouched. The fn argument is
// the caller, mutated in place: each substitution appends the
// callee's slot types to fn.ScratchTypes.
func inlineOps(fn *Func, ops []Op, candidates map[string]inlineCandidate, mode *inlineMode) []Op {
	// out stays nil until the first substitution, so a function with no
	// inlineable call site is returned untouched instead of copied. Most
	// functions in a large unit are that function, and the copy is what
	// made the pass's own walks expensive enough to price it out.
	var out []Op
	// Track loop depth through the structured-control scope stack so
	// each call site sees its depth-appropriate size cap (siteAllows).
	// OpElse switches an if's arm without opening a scope, so only the
	// three openers push; OpEnd pops whichever opener is innermost.
	var scopes []OpKind
	loopDepth := 0
	for i, op := range ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			scopes = append(scopes, op.Kind)
			if op.Kind == OpLoop {
				loopDepth++
			}
		case OpEnd:
			if n := len(scopes); n > 0 {
				if scopes[n-1] == OpLoop {
					loopDepth--
				}
				scopes = scopes[:n-1]
			}
		}
		cand, ok := inlineTarget(fn, op, candidates)
		if ok {
			// The ops emitted so far: the rewritten prefix once a
			// substitution has forced the copy, the original's own until then.
			emitted := out
			if emitted == nil {
				emitted = ops[:i]
			}
			ok = mode.allows(cand, loopDepth, allConstArgs(emitted, int(op.I32)))
		}
		if !ok {
			if out != nil {
				out = append(out, op)
			}
			continue
		}
		if out == nil {
			out = append(make([]Op, 0, len(ops)+len(cand.body)), ops[:i]...)
		}
		mode.spend(len(cand.body))
		out = append(out, expandInline(fn, cand)...)
	}
	if out == nil {
		return ops
	}
	return out
}

// inlineTarget resolves op to the candidate whose body may replace it, if
// op is a call to one at all.
func inlineTarget(caller *Func, op Op, candidates map[string]inlineCandidate) (inlineCandidate, bool) {
	if op.Kind != OpCallDirect && op.Kind != OpCallClosureDirect {
		return inlineCandidate{}, false
	}
	if op.Runtime {
		// The lowering meant the BACKEND's helper of this name, not a
		// program function that happens to share it. Candidates are keyed
		// by name, so without this a program defining `__fern_str_append`
		// gets its body spliced into the string-append lowering's own call
		// site — which is how that program built a module whose `main`
		// carried a bare `i32.const 1 / i32.add` where an append belonged.
		return inlineCandidate{}, false
	}
	cand, ok := candidates[op.Str]
	if !ok || cand.fn == caller {
		return inlineCandidate{}, false
	}
	return cand, true
}

// allConstArgs reports whether a call's `argc` arguments are all
// compile-time numeric constants — i.e. the last `argc` ops already
// emitted into `out` are each a single numeric const-push
// (OpConstI32/I64/F32/F64). A const push adds one operand and pops
// nothing, so `argc` consecutive const-pushes are exactly the top
// `argc` stack entries the call consumes as its args. Requires
// argc >= 1: a 0-arg call has no params to fold, so no partial-
// evaluation benefit. Only NUMERIC consts count — those are what Fold
// propagates into arithmetic after substitution (string/func consts
// fold nothing, so they don't justify lifting the size cap).
func allConstArgs(out []Op, argc int) bool {
	if argc < 1 || argc > len(out) {
		return false
	}
	for _, op := range out[len(out)-argc:] {
		switch op.Kind {
		case OpConstI32, OpConstI64, OpConstF32, OpConstF64:
		default:
			return false
		}
	}
	return true
}

// siteAllows applies the per-call-site size policy (#4412 Rec §7).
// Beyond the flat cap everywhere, three reasons to admit an 81..160-op
// body:
//   - the callee is referenced exactly once program-wide, so inlining
//     moves its body to that sole site and dead-func elimination drops
//     the original — a net-neutral code-size move regardless of loop
//     depth or argument shape;
//   - the site is inside a loop, where the per-call overhead recurs
//     every iteration (the #5143 loop-depth slice);
//   - every argument is a compile-time numeric constant, because
//     inlining substitutes those constants for the param loads and the
//     following Fold pass collapses the arithmetic — the effective
//     inlined size is far below the body's op count, so the flat cap
//     over-counts it. This is the partial-evaluation case (a helper
//     called with literals, e.g. a compile-time-computed layout).
//
// @inline candidates bypass all of these (the hint already passed
// candidacy). Bodies over the loop cap are still never inlined here.
func siteAllows(cand inlineCandidate, loopDepth int, constArgs bool) bool {
	if cand.fn.InlineHint == ast.InlineHintAlways {
		return true
	}
	if len(cand.body) <= inlineSizeLimit {
		return true
	}
	if len(cand.body) > inlineLoopSizeLimit {
		return false
	}
	return cand.refs == 1 || loopDepth > 0 || constArgs
}

// expandInline produces the op slice that replaces a single
// OpCallDirect site. Argument bindings, the return-target wrapper,
// and the body splice with renumbered slots + return-translation
// all happen here.
func expandInline(caller *Func, cand inlineCandidate) []Op {
	base := int32(len(caller.Params)) + int32(len(caller.Locals)) + int32(len(caller.ScratchTypes))
	caller.ScratchTypes = append(caller.ScratchTypes, cand.slotTypes...)

	// Bind arguments. Caller pushed args left-to-right, so the
	// rightmost argument is on top of the operand stack —
	// popping into reverse-order param slots lands each value in
	// the right place.
	out := make([]Op, 0, len(cand.body)+len(cand.slotTypes)+2)
	for p := int32(len(cand.fn.Params)) - 1; p >= 0; p-- {
		out = append(out, Op{Kind: OpStoreLocal, I32: base + p})
	}

	// Skip the wrapper block on the easy case: bodies with no
	// mid-body OpReturn / OpReturnVoid don't need an early-exit
	// target. Keeps the linear-leaf path bit-identical to the
	// pre-control-flow inliner so downstream peephole +
	// tee-folding still see the un-blocked load / store / op
	// stream they were tuned for.
	wrap := needsReturnWrapper(cand.body)
	if wrap {
		out = append(out, Op{Kind: OpBlock, I32: cand.returnBlockType})
	}

	// Splice the callee body with slot indices rebased onto the
	// caller's fresh range, control-flow depth tracked so each
	// Return translates to a br with the correct relative depth,
	// and the trailing terminator dropped (control falls through
	// to the wrapper's End — or to the caller continuation
	// directly when there's no wrapper).
	depth := int32(0)
	for i, op := range cand.body {
		isTrailing := i == len(cand.body)-1 &&
			(op.Kind == OpReturn || op.Kind == OpReturnVoid || op.Kind == OpReturnPair)
		if isTrailing {
			break
		}
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			out = append(out, op)
			depth++
		case OpEnd:
			out = append(out, op)
			depth--
		case OpLoadLocal, OpStoreLocal, OpTeeLocal:
			op.I32 += base
			out = append(out, op)
		case OpReturn, OpReturnVoid, OpReturnPair:
			// Translate to a branch out of the wrapper block.
			// `depth` is the relative distance from this op to
			// the wrapper. The OpReturn's value (if any) is
			// already on the operand stack — wasm's br
			// semantics carry it through to the wrapper's
			// continuation.
			out = append(out, Op{Kind: OpBr, I32: depth})
		default:
			out = append(out, op)
		}
	}

	if wrap {
		out = append(out, Op{Kind: OpEnd})
	}
	return out
}

// needsReturnWrapper reports whether body contains any OpReturn /
// OpReturnVoid before its trailing terminator. Mid-body returns
// require the wrapper; pure straight-line + nested control flow
// without internal returns can splice cleanly with the trailing
// terminator just dropped.
func needsReturnWrapper(body []Op) bool {
	if len(body) == 0 {
		return false
	}
	for i, op := range body[:len(body)-1] {
		_ = i
		if op.Kind == OpReturn || op.Kind == OpReturnVoid || op.Kind == OpReturnPair {
			return true
		}
	}
	return false
}
