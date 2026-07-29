package ir

// Perceus RC decision analyses — the "plan" side of reference counting,
// carved out of the AST→IR lowering builder (issue #4393, slice 1 of the
// RC-pass extraction; see docs/RC-NATIVE-PASS-EXTRACTION.md).
//
// Everything in this file DECIDES where retains / releases / precise drops
// / moves / reuse belong; nothing in it emits an Op. Each analysis is a
// pure function over the (monomorphised, closure-converted) AST plus the
// checker tables, producing a side-table keyed by local name / AST node
// that lowering then consults. computeRcAnalyses is the single per-function
// entry point; inferParamEscapes / findReturnsNoParamEscape /
// computeReadOnlyComparators are their whole-program siblings, called once
// from LowerWith. The Op-emitting half (the exit dec sweep, precise-drop /
// reuse emission, alias incs) still lives interleaved in ir.go — extracting
// it behind this boundary is the follow-up slice.

import (
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// rcPlan is the per-function Perceus RC plan: every decision table the
// analyses in this file compute, grouped so lowering consults one named
// component (and so the goal-2 port has a single struct to mirror as the
// self-host's parallel arrays). Computed by computeRcAnalyses before any
// Op is emitted; preciseDrops is filled at its later lowerFunc call site
// (drop-fn registry-order constraint, see computeRcAnalyses). All tables
// are read-only during lowering — the C2 consuming-match pairing that used
// to register in reuseSources mid-lowering is folded into the plan too
// (computeConsumingMatchReuse), so the plan is immutable once
// computeRcAnalyses returns (#4475).
type rcPlan struct {
	// consumedParams[name] is true for a pointer-shaped struct/tuple/enum
	// PARAMETER that the borrow model would keep borrowed (its type is not
	// owned-by-default — e.g. it carries a string field) but that the function
	// THREADS: it is reassigned in the body (`s = s.emit(..)`, `ctx =
	// check_stmt(.., ctx)`). The reassignment-overwrite dec's the old value, so
	// a borrowed such param is over-released (the caller's value dec'd with no
	// caller-side inc). computeConsumedParams promotes it to callee-owned: it is
	// NOT borrow-tainted in computeFreeEligible (so it becomes freeEligible and
	// the overwrite / exit-sweep deep-drop it), and lowerFunc emits a single
	// entry-inc so the first overwrite-dec balances. This is purely
	// callee-internal — the call ABI is unchanged (the caller still passes it
	// borrowed), so no caller-side coordination is needed. It is the
	// borrow-inference fix for the O(N^2) self-reassign accumulator leak: the
	// self-host SSA builder / emitter thread their string-bearing accumulators
	// through such params, which previously could not deep-drop the old value.
	consumedParams map[string]bool
	// freeEligible[name] is true for array-typed locals the
	// borrow-aware analysis proved are OWNED — safe for the array
	// dec sites to return to the freelist at rc==0. Borrowed /
	// borrowed-derived locals are absent (false) and use a plain
	// dec instead. Computed once by computeFreeEligible. See
	// docs/RC-PERCEUS-PLAN.md (the borrow⇄free resolution).
	freeEligible map[string]bool
	// movedLocals[name] is true for an owned rc local whose LAST
	// occurrence is a top-level alias that always executes (Phase 4
	// move-on-alias): the alias skips its transfer inc and the exit
	// sweep skips the local's dec (a net-zero pair). Computed by
	// computeMovedLocals.
	movedLocals map[string]bool
	// moveSites[stmt] is true for the specific *ast.Var / *ast.Assign
	// alias statement that is a move (skips its transfer inc). Keyed
	// per-site so only the local's LAST alias moves — earlier aliases
	// of the same local keep their inc.
	moveSites map[ast.Node]bool
	// arraySetInc[call] is true for a `__method_Array_set` (`.with`) call
	// whose receiver is LIVE after the call (read again, and not a
	// reassign-to-self), so emitArraySet must rc-inc the receiver buffer
	// before __fern_arr_cow_inplace to force the copy path — otherwise the
	// rc==1 in-place reuse aliases/mutates the still-live receiver (#2832).
	arraySetInc map[*ast.Call]bool
	// reuseSources pairs a construction site C (the *ast.StructLit or
	// *ast.TupleLit node) with the name of a dead, owned struct/tuple local D
	// whose box C reuses in place — the general FBIP win (Perceus reuse
	// token threaded D's drop → C's alloc, across DIFFERENT locals, beyond
	// the self-overwrite tryStructReuseOverwrite). reuseConsumed[D] marks
	// such a D so computePreciseDrops doesn't ALSO drop it (the reuse
	// already consumes D's box / dec's it on the shared path). See
	// computeReuseSources.
	reuseSources  map[ast.Expr]string
	reuseConsumed map[string]bool
	// consumingMatchReuse marks a construction C (an arm's variant constructor)
	// that reuses a CONSUMING match's scrutinee box in place (C2 — true
	// zero-alloc FBIP): instead of freeing the consumed `own` box and allocating
	// a fresh one for the arm's `return Ctor(..)`, the box shell is handed
	// straight to C via the reuse token. The scrutinee's old payloads were MOVED
	// into the arm bindings (reclaimed downstream), so unlike a general reuse C
	// must NOT drop the box's old fields — this flag tells emitEnumNew to skip
	// emitReuseOldFieldDrops. Rides on RcReuseEnabled. Filled by
	// computeConsumingMatchReuse, which also records the ctor→scrutinee
	// pairing in reuseSources (#4475).
	consumingMatchReuse map[*ast.Call]bool
	// consumingOwnedMatches maps a `match` statement to its scrutinee's param
	// name when the scrutinee is an OWNED-BY-DEFAULT enum parameter consumed by
	// the match (#4400 — the Koka-style consuming match for counted owners, the
	// non-`own` sibling of ownParamEnumScrutinee). The lowering then emits the
	// drop-specialized per-arm scrutinee release: on the unique branch the box
	// is shallow-freed (the extracted bindings inherit the box's payload counts
	// — the dup/dec pairs cancel statically), on the shared branch each pointer
	// binding in consumingBindings is dup'd (__fern_rc_inc) and the box is
	// flat-dec'd. Either way the param slot is zeroed so the exit sweep's
	// deep-drop no-ops (guarded/wildcard arms skip the release and leave the
	// box to that sweep — no leak). Filled by computeConsumingOwnedMatches.
	consumingOwnedMatches map[*ast.Match]string
	// consumingBindings maps an arm-binding name of a consuming owned match to
	// its (pointer-shaped) payload type, for names that become COUNTED OWNERS:
	// every binding occurrence of the name in the whole function sits in an
	// unguarded non-wildcard arm of a consumingOwnedMatches match, the name
	// shadows no param / declared local / loop var, and its type is a sweepable
	// box (enum / user struct / tuple) consistent across occurrences. These
	// names are NOT borrow-tainted in computeFreeEligible (unlike ordinary
	// match bindings), get a pre-allocated zeroed slot in the prologue, and are
	// deep-dropped by the exit sweep exactly like owned locals. Every pointer
	// payload of every releasing arm of a consumingOwnedMatches match is in
	// this table — a match with an untrackable pointer binding is dropped from
	// the plan instead (see the fixpoint in computeConsumingOwnedMatches).
	// Filled by computeConsumingOwnedMatches.
	consumingBindings map[string]ast.Type
	// preciseDrops[stmtIdx] lists the owned locals to deep-drop + zero right
	// after lowering that top-level statement (Perceus garbage-free precise
	// drops — computePreciseDrops).
	preciseDrops map[int][]string
	// Dead-alias dup/drop cancellation (#4402 opt 1): borrowedAlias[y] marks a
	// `var y = x` alias local proven to be a pure BORROWED VIEW of an owned
	// local x for its whole life — its transfer inc and its exit-sweep dec are
	// a guaranteed net-zero pair, so both are elided (the fusion Koka calls
	// dup/drop cancellation, done in the analysis layer where the emission
	// sites are known). borrowedAliasSites keys the specific *ast.Var so the
	// lowering skips exactly that inc; borrowSources[x] pins the source: x
	// must release ONLY at the exit sweep (never precise-dropped, never a
	// reuse donor) so the borrow can never outlive the buffer.
	borrowedAlias      map[string]bool
	borrowedAliasSites map[ast.Node]bool
	borrowSources      map[string]bool
	// dynBorrowedViews[name] marks a `dyn Trait` local bound (or ever
	// reassigned) from an UNCOUNTED alias shape — an element read
	// (`var x = xs[i]`), a field read, or a bare dyn-to-dyn ident — with no
	// DynCoercion recorded on the source expr (a coercion packs a FRESH
	// {data, vtable} cell at the site, which the binding owns; an alias
	// shape just copies the owner's cell pointer). Dyn cells carry no rc
	// header, so there is no retain to balance: sweeping such a view drops
	// the owner's cell out from under it (double free with the owning
	// array's / local's own drop — #4787). The exit sweep and the reinit
	// drop skip these; the true owner releases the cell. Over-marking is
	// leak-safe (a skipped owned cell merely leaks), never a UAF.
	dynBorrowedViews map[string]bool
	// dynAliasElemArrays[name] marks a `dyn Trait[]` local whose literal
	// received a bare pre-coerced dyn LOCAL as an element (`[d]` — an
	// uncounted cell move; see computeDynBorrowedViews). The array keeps
	// its exit-sweep drop (it owns the moved cells) but skips the
	// loop-reinit drop: re-declaring it per iteration would free the
	// still-live source local's cell.
	dynAliasElemArrays map[string]bool
	// borrowedMapFieldResults[name] marks a local bound to a Map COW-mutator
	// call whose RECEIVER is a field access (`var m = s.m.insert(k, v)`). On
	// the rc==1 in-place path the mutator returns the SAME handle the
	// container's field `s.m` still holds, so `m` aliases the container's
	// buffer. Wrapping such a local into a fresh struct field
	// (`Wrapper { m: m }`) and later dropping the container then frees the
	// buffer out from under the new struct — the var-indirected twin of the
	// #2763 direct-construction clone (issue #4871). The StructLit lowering
	// clones a Map field initialised by such a local, exactly as #2763 clones
	// a Map field initialised by a direct mutator call, so the new container
	// owns an independent buffer. Filled by computeBorrowedMapFieldResults.
	borrowedMapFieldResults map[string]bool
}

// computeRcAnalyses runs every per-function Perceus RC decision analysis, in
// dependency order, and stores the resulting side-tables on the builder for
// lowering to consult. This is the one place the per-function RC "plan" is
// computed; nothing below emits an Op.
func (b *builder) computeRcAnalyses() {
	// Consumed-threaded params (borrow-inference fix): a borrowed struct/tuple/
	// enum param that the function reassigns is promoted to callee-owned so its
	// reassignment-overwrite can deep-drop the old value without over-releasing
	// (paired with the entry-inc emitted by lowerFunc). Computed before
	// freeEligible, which consults it (a consumed param is not borrow-tainted).
	b.rc.consumedParams = b.computeConsumedParams()
	// Koka-style consuming matches on owned-by-default enum params (#4400).
	// Computed before freeEligible, which consults consumingBindings (a
	// qualifying binding becomes a counted owner instead of a tainted borrow).
	b.rc.consumingOwnedMatches, b.rc.consumingBindings = b.computeConsumingOwnedMatches()
	// Borrow-aware free analysis: which array locals are OWNED and
	// thus safe to return to the freelist at rc==0. Borrowed /
	// borrowed-derived locals are excluded (only the owner frees).
	b.rc.freeEligible = b.computeFreeEligible()
	b.rc.moveSites = map[ast.Node]bool{}
	b.rc.movedLocals = b.computeMovedLocals()
	b.rc.borrowedMapFieldResults = b.computeBorrowedMapFieldResults()
	b.rc.arraySetInc = b.computeArraySetIncs()
	b.computeBorrowedAliases()
	b.rc.dynAliasElemArrays = map[string]bool{}
	b.rc.dynBorrowedViews = b.computeDynBorrowedViews()
	b.rc.reuseSources, b.rc.reuseConsumed = b.computeReuseSources()
	b.rc.consumingMatchReuse = b.computeConsumingMatchReuse()
}

// computeDynBorrowedViews finds `dyn Trait` locals that are pure borrowed
// VIEWS of a cell some other value owns (see rc.dynBorrowedViews). A dyn
// local OWNS its cell only when the cell was freshly packed at the binding —
// an init with a DynCoercion recorded (a concrete coerced into the dyn slot)
// or a non-alias shape (a call result / match-expr moving a cell in). An
// Ident / Index / FieldAccess init WITHOUT a coercion copies an existing
// cell pointer uncounted (dyn cells have no rc header, so needsRcIncOnAlias
// deliberately never incs them), making the binding a borrow the sweep must
// not drop. A dyn local ASSIGNED such a shape anywhere is marked too — the
// slot may hold a borrow at exit, and skipping an owned cell only leaks.
func (b *builder) computeDynBorrowedViews() map[string]bool {
	out := map[string]bool{}
	if b.fn.Body == nil {
		return out
	}
	isUncoercedAlias := func(init ast.Expr) bool {
		if init == nil {
			return false
		}
		switch init.(type) {
		case *ast.Ident, *ast.Index, *ast.FieldAccess:
		default:
			return false
		}
		if b.info != nil && b.info.DynCoercions != nil {
			if _, coerced := b.info.DynCoercions[init]; coerced {
				return false
			}
		}
		return true
	}
	localIsDyn := func(name string) bool {
		t, ok := b.localDeclType(name)
		if !ok {
			return false
		}
		_, isDyn := t.(ast.DynTraitType)
		return isDyn
	}
	// A dyn LOCAL flowing bare into an ARRAY LITERAL element (`[d]` where d
	// is already dyn — no coercion, so no fresh cell is packed) MOVES its
	// cell into the array uncounted: the array's drop walk
	// (__drop_arr_dyn_<set>) frees the cell, so the source local must not
	// be swept too. The array itself keeps its exit-sweep drop (it owns the
	// cells now) but must skip the loop-reinit drop (dynAliasElemArrays):
	// re-declaring `var xs = [d]` per iteration would free d's cell on
	// iteration 2 while d is still live.
	markDynAliasElems := func(init ast.Expr) bool {
		al, ok := init.(*ast.ArrayLit)
		if !ok {
			return false
		}
		found := false
		for _, el := range al.Elems {
			if id, ok := el.(*ast.Ident); ok && localIsDyn(id.Name) && isUncoercedAlias(el) {
				out[id.Name] = true
				found = true
			}
		}
		return found
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if localIsDyn(x.Name) && isUncoercedAlias(x.Init) {
				out[x.Name] = true
			}
			if markDynAliasElems(x.Init) {
				b.rc.dynAliasElemArrays[x.Name] = true
			}
		case *ast.Assign:
			if id, ok := x.Target.(*ast.Ident); ok {
				if localIsDyn(id.Name) && isUncoercedAlias(x.Value) {
					out[id.Name] = true
				}
				if markDynAliasElems(x.Value) {
					b.rc.dynAliasElemArrays[id.Name] = true
				}
			}
		}
		return true
	})
	return out
}

func findReturnsNoParamEscape(prog *ast.Program, info *checker.Info) map[string]bool {
	// Variant-constructor name -> payload types, for the construction recursion.
	variantPayloads := map[string][]ast.Type{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			variantPayloads[v.Name] = v.Payloads
		}
	}
	q := map[string]bool{}
	for _, fn := range prog.Funcs {
		q[fn.Name] = true
	}
	for {
		changed := false
		for _, fn := range prog.Funcs {
			if !q[fn.Name] || fn.Body == nil {
				continue
			}
			freshLocals := computeFreshLocals(fn, info, variantPayloads, q)
			ok := true
			ast.Walk(fn.Body, func(n ast.Node) bool {
				if r, isRet := n.(*ast.Return); isRet && r.Value != nil {
					if !exprNoParamEscape(r.Value, fn.ReturnType, info, variantPayloads, q, freshLocals) {
						ok = false
					}
				}
				return true
			})
			if !ok {
				q[fn.Name] = false
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return q
}

// inferParamEscapes computes, per function and per POINTER parameter, whether
// that parameter's heap value can ESCAPE the function — flow out through the
// return value, or be stored into a caller-visible container (a retain sink such
// as `m.set` / `arr.push`, or an `own` argument the callee itself lets escape).
// A NON-escaping parameter is reclaim-safe: under an owned-by-default model the
// callee may free it at the end without transferring ownership out, and if it is
// additionally only read it may be borrowed. This is the foundation analysis for
// ownership / borrow inference — Slice 0 of docs/OWNERSHIP-INFERENCE-PLAN.md. It
// does NOT change codegen yet.
//
// Greatest fixpoint over the call graph (optimistic: nothing escapes; a
// parameter flips to "escapes" once and never back, so it terminates). A value
// passed to a borrowing callee position does not escape through that call;
// passed to a position the callee escapes, to a retain sink, or returned as part
// of the result, it does. Unknown / builtin callees are treated conservatively
// (assume they escape a tainted argument) so the result is a sound
// under-approximation of "borrowable".
func inferParamEscapes(prog *ast.Program, info *checker.Info) map[string][]bool {
	variantPayloads := map[string][]ast.Type{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			variantPayloads[v.Name] = v.Payloads
		}
	}
	escapes := map[string][]bool{}
	for _, fn := range prog.Funcs {
		escapes[fn.Name] = make([]bool, len(fn.Params))
	}
	for {
		changed := false
		for _, fn := range prog.Funcs {
			if fn.Body == nil {
				continue
			}
			for i, p := range fn.Params {
				if escapes[fn.Name][i] || !ast.IsPointerType(p.Type) {
					continue
				}
				if paramEscapesInFn(fn, p.Name, info, variantPayloads, escapes) {
					escapes[fn.Name][i] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return escapes
}

// computeReadOnlyComparators collects the mangled names of every `eq` /
// `hash` receiver method (the Eq / Hash trait methods). They are read-only
// by contract, so the IR borrows their params even under the owned model —
// see builder.readOnlyComparators / paramBorrowable. Keyed off the method
// name being exactly `eq` / `hash` (the methodKey suffix), so an unrelated
// method like `compute_hash` (`<T>.compute_hash`) is excluded.
func computeReadOnlyComparators(info *checker.Info) map[string]bool {
	if info == nil {
		return nil
	}
	out := map[string]bool{}
	for key, mangled := range info.Methods {
		if strings.HasSuffix(key, ".eq") || strings.HasSuffix(key, ".hash") {
			out[mangled] = true
		}
	}
	return out
}

// computeConsumedParams identifies pointer-shaped struct / tuple / enum
// PARAMETERS that this function THREADS — reassigns somewhere in its body
// (`s = s.emit(..)`, `ctx = check_stmt(.., ctx)`) — but that the borrow model
// would otherwise keep borrowed because their type is not owned-by-default
// (typically because it carries a string field, which isOwnedByDefaultType
// excludes). Reassigning such a param dec's the OLD value on the overwrite, so
// a BORROWED such param is over-released: the caller's value is dec'd with no
// caller-side retain inc. Promoting it to callee-owned — not borrow-tainted in
// computeFreeEligible (so it becomes freeEligible and the overwrite / exit
// sweep deep-drop it) plus one entry-inc in lowerFunc — keeps the rc accurate
// (a still-shared value stays rc>1 and is only flat-dec'd, never freed) and
// lets the old value's nested heap be reclaimed. That is the borrow-inference
// fix for the O(N^2) self-reassign accumulator leak (the self-host SSA builder
// / emitter thread their string-bearing BState / EmitState through such params).
//
// The promotion is purely callee-internal: the call ABI is unchanged (the
// caller still passes the arg borrowed), so calleeParamOwnedByDefault stays
// false and no caller-side coordination is needed. TRMC functions are excluded
// (their exit bypasses the param sweep).
// inferParamCountedRetain computes, per user function, which STRING parameters
// are retained ONLY through counted constructions — i.e. every appearance of the
// parameter in the body is as a field / element value of a StructLit, TupleLit
// or ArrayLit, each of which inc's what it stores (needsRcIncOnAlias at the
// construction site; a param is never a moveSite, since markConstructionMoves
// only marks owned rc LOCALS).
//
// This is what lets computeFreeEligible stop tainting a string argument passed
// to such a callee. That blanket taint exists because a callee may retain an
// argument UNCOUNTED — stored into a container it returns, which the
// intraprocedural analysis cannot see — so freeing it caller-side would dangle
// the retained copy. When every retention is counted, the callee's construction
// holds a reference of its own and the caller's release is balanced: the value
// stays at rc>=1 for exactly as long as the constructed value does.
//
// Deliberately narrow. The parameter must appear ONLY in counted-construction
// value positions: one bare `return name`, one `var s = name`, one
// `xs.append(name)`, one call passing it on, even one `name.len()` — anything
// else at all — and the summary is false and the caller keeps the conservative
// taint. That is enough for the shape this targets: the lexer's eight `*_tok`
// helpers and the parser's `e_*` node constructors, each of which does nothing
// with its string parameter but store it in the node it returns. Widening it
// (transitive calls, pure reads, locals that only flow into constructions,
// variant-constructor payloads) needs its own fixpoint and is a follow-up.
//
// A parameter shadowed by a local / match binding of the same name disqualifies
// it: the occurrence counting below cannot tell the two apart.
func inferParamCountedRetain(prog *ast.Program, info *checker.Info) map[string][]bool {
	// Precompute the shadowed-name set per function once (match / match-expr
	// bindings that reuse a parameter name).
	type fnCtx struct {
		fn       *ast.FuncDecl
		shadowed map[string]bool
	}
	ctxs := make([]fnCtx, 0, len(prog.Funcs))
	out := map[string][]bool{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		sh := map[string]bool{}
		for _, v := range info.Locals[fn] {
			sh[v.Name] = true
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Match:
				for _, arm := range x.Arms {
					for _, nm := range arm.Bindings {
						sh[nm] = true
					}
				}
			case *ast.MatchExpr:
				for _, arm := range x.Arms {
					for _, nm := range arm.Bindings {
						sh[nm] = true
					}
				}
			}
			return true
		})
		ctxs = append(ctxs, fnCtx{fn, sh})
		out[fn.Name] = make([]bool, len(fn.Params))
	}
	// Least-fixpoint: struct-param crediting consults the summary for the
	// arg-position rule (a `p` passed as argument i to callee C is counted iff
	// C's parameter i is counted), so a param credited this round can credit a
	// caller next round. Start all-false and only ever add credits — the
	// classifier marks a use safe only on positive local evidence or a
	// callee already proven counted — so the iteration is monotone and
	// converges to the grounded fixpoint (a mutual-recursion cycle with no
	// grounding stays uncredited, the conservative direction).
	for {
		changed := false
		for _, c := range ctxs {
			fn, sh := c.fn, c.shadowed
			flags := make([]bool, len(fn.Params))
			for i, p := range fn.Params {
				// A parameter carrying no heap (i32 / bool / f64 / …) can never
				// be retained at all — mark it so a scalar argument doesn't
				// disqualify a call the way a pointer one would. Conditioned
				// below on EVERY pointer param being counted too: a scalar can't
				// alias, but the RESULT can still alias an unproven pointer
				// param, and a tainted scalar argument is what was keeping such a
				// call's result tainted (`grow(m, i + 2)` returning a Map that
				// shares the caller's buffer — TestX86_64MapIntermediateReclaim's
				// param-receiver negative).
				if !rcTrackedSlotType(p.Type) {
					flags[i] = true
					continue
				}
				if p.Own || sh[p.Name] {
					continue
				}
				switch pt := p.Type.(type) {
				case ast.StringType:
					flags[i] = stringParamCounted(fn, p.Name)
				case ast.StructType:
					// Struct-param generalisation: credit `p` when every one of
					// its appearances is a counted store, a non-retaining read,
					// or a counted call argument — so a result built from it
					// holds only counted references. This is what lets the
					// scalar-arg exemption fire for a scanner threaded through
					// field projections and pure-read methods (lexer.tokenize;
					// docs/SELFHOST-AST-RETIREMENT.md).
					flags[i] = structParamProjectionsSafe(fn, p.Name, pt.Name, info, out)
				}
			}
			ptrAllCounted := true
			for i, p := range fn.Params {
				if rcTrackedSlotType(p.Type) && !flags[i] {
					ptrAllCounted = false
					break
				}
			}
			if !ptrAllCounted {
				for i, p := range fn.Params {
					if !rcTrackedSlotType(p.Type) {
						flags[i] = false
					}
				}
			}
			if !boolSliceEqual(out[fn.Name], flags) {
				out[fn.Name] = flags
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return out
}

func boolSliceEqual(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pureReadReceiverBuiltin names the builtin methods that READ their receiver
// (Args[0]) and return a scalar / fresh value without retaining it — so a
// pointer argument in receiver position is not aliased out and does not
// disqualify a counted-retain param. Mutating / receiver-returning builtins
// (`__method_Array_push` / `_set`, `__method_Map_set` / `_clear`) are
// deliberately absent — those DO thread the receiver.
func pureReadReceiverBuiltin(name string) bool {
	switch name {
	case "__method_string_len", "__method_Array_len", "__method_slice_len",
		"__method_Array_sum":
		return true
	}
	return false
}

// stringParamCounted reports whether string parameter `pn` of fn is retained
// only through counted constructions or non-retaining reads — every appearance
// is a bare-ident value of a StructLit / TupleLit / ArrayLit slot, or the
// receiver of a pure-read builtin (`s.len()`). Conservative: a param qualifies
// only when every occurrence is credited.
func stringParamCounted(fn *ast.FuncDecl, pn string) bool {
	safe := map[*ast.Ident]bool{}
	mark := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Name == pn {
			safe[id] = true
		}
	}
	total := 0
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == pn {
				total++
			}
		case *ast.StructLit:
			for _, f := range x.Fields {
				mark(f.Value)
			}
		case *ast.TupleLit:
			for _, el := range x.Elems {
				mark(el)
			}
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				mark(el)
			}
		case *ast.Call:
			// `s.len()` — a pure-read builtin reads the receiver and returns a
			// scalar, retaining nothing, so the receiver occurrence is safe.
			if id, ok := x.Callee.(*ast.Ident); ok && pureReadReceiverBuiltin(id.Name) && len(x.Args) > 0 {
				mark(x.Args[0])
			}
		}
		return true
	})
	return total > 0 && total == len(safe)
}

// structParamProjectionsSafe reports whether every occurrence of struct
// parameter `pn` (declared struct type `sn`) in fn is a COUNTED store, a
// NON-RETAINING read, or a COUNTED call argument — the struct-param
// generalisation of the string counted-retain summary, closed over the
// interprocedural `summary` for the arg-position rule. Conservative by
// construction: a p-occurrence is credited only when the walk positively
// proves it safe, and the param qualifies only when EVERY occurrence is
// credited (`total == len(safe)`), so any unhandled or escaping use
// disqualifies the whole param.
//
// Credited (safe) occurrences:
//   - a bare `p` or `p.field` stored as a StructLit / TupleLit / ArrayLit slot
//     value — the construction inc's a pointer field / copies a scalar;
//   - a SCALAR field read `p.scalarField` anywhere — a value copy;
//   - the SOURCE of a string slice `p.strField[a:b]` / string index
//     `p.strField[i]` — a copying read;
//   - a bare `p` / `p.field` passed as argument i to a call whose callee
//     parameter i is itself counted-retain (`summary[C][i]`) — the method
//     receiver `l.at_end()` and the self-reassign source `l.advance()`;
//   - `p` as the TARGET of an assignment (`p = …`) — a rebind, not a retention;
//     the old value's fate is decided by the RHS classification and the
//     reassigned-param overwrite dec (computeConsumedParams).
//
// Everything else (a bare `p` outside a slot, a pointer field read that
// escapes, `p` passed to an UNCOUNTED / builtin argument, `x = p`, `return p`,
// an array-slice source) is left uncredited and disqualifies — which is what
// keeps `grow(m, k): Map { m = m.insert(k, …); return m; }` out: `m` reaches a
// builtin `__method_Map_set` argument (never in `summary`) and a bare
// `return m`, so it is never credited and its scalar `k` is never exempted.
func structParamProjectionsSafe(fn *ast.FuncDecl, pn, sn string, info *checker.Info, summary map[string][]bool) bool {
	sd, ok := info.Structs[sn]
	if !ok {
		return false
	}
	fieldType := func(name string) (ast.Type, bool) {
		for _, f := range sd.Fields {
			if f.Name == name {
				return f.Type, true
			}
		}
		return nil, false
	}
	safe := map[*ast.Ident]bool{}
	// markSlotValue credits a p-use that sits directly in a counted position —
	// a construction slot or a counted call argument: a bare `p` (the whole
	// struct is inc'd in) or a `p.field` (a pointer field is inc'd, a scalar is
	// copied).
	markSlotValue := func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.Ident:
			if v.Name == pn {
				safe[v] = true
			}
		case *ast.FieldAccess:
			if id, ok := v.Target.(*ast.Ident); ok && id.Name == pn {
				safe[id] = true
			}
		}
	}
	total := 0
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == pn {
				total++
			}
		case *ast.StructLit:
			for _, f := range x.Fields {
				markSlotValue(f.Value)
			}
		case *ast.TupleLit:
			for _, el := range x.Elems {
				markSlotValue(el)
			}
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				markSlotValue(el)
			}
		case *ast.FieldAccess:
			// A scalar field read is a pure value copy — safe wherever it
			// appears, not only in a slot.
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == pn {
				if ft, ok := fieldType(x.Field); ok && !ast.IsPointerType(ft) {
					safe[id] = true
				}
			}
		case *ast.SliceExpr:
			// A string slice copies into a fresh buffer, so its source read
			// retains nothing. An array/other slice is a view that shares the
			// buffer — left uncredited.
			if x.IsString {
				markSlotValue(x.Source)
			}
		case *ast.Index:
			// A string byte read yields a scalar — the source read retains
			// nothing.
			if x.IsString {
				markSlotValue(x.Array)
			}
		case *ast.Call:
			// A `p` / `p.field` passed as argument i to a call whose callee
			// parameter i is counted-retain is inc'd (or read) there, not
			// aliased out — so it is safe, exactly like a construction slot.
			// A builtin / external callee is absent from `summary`, so its
			// arguments stay uncredited (the map-mutator receiver guard).
			if id, ok := x.Callee.(*ast.Ident); ok {
				if pureReadReceiverBuiltin(id.Name) && len(x.Args) > 0 {
					markSlotValue(x.Args[0])
				}
				if cs, ok := summary[id.Name]; ok {
					for i, a := range x.Args {
						if i < len(cs) && cs[i] {
							markSlotValue(a)
						}
					}
				}
			}
		case *ast.Assign:
			// A rebind of the param slot (`p = p.advance()`) is not a
			// retention of the OLD value — the RHS is classified normally and
			// the overwrite dec is emitted by computeConsumedParams.
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == pn {
				safe[id] = true
			}
		case *ast.Return:
			// Returning the bare param (or a field of it) is a COUNTED
			// retention: a borrowed value returned is inc'd on the way out
			// (the caller receives an owned reference while the original
			// borrower keeps its own), so the result holds a counted — not
			// uncounted — reference to the param. This is what credits the
			// cursor methods whose early path returns the receiver unchanged
			// (`advance_to(l): if (end <= l.i) { return l; }`). The map-mutator
			// negatives still exclude a builder that returns a param-derived
			// map, because its `m.insert(...)` reaches a builtin argument first.
			markSlotValue(x.Value)
		}
		return true
	})
	return total > 0 && total == len(safe)
}

func (b *builder) computeConsumedParams() map[string]bool {
	res := map[string]bool{}
	if !ast.RcFreeEnabled || b.fn.Body == nil || b.trmcFuncs[b.fn.Name] {
		return res
	}
	reassigned := map[string]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		}
		return true
	})
	for i, p := range b.fn.Params {
		// Skip params the borrow model already balances: an `own` param (caller
		// move) or an owned-by-default one (caller inc).
		if p.Own || b.paramOwnedByDefault(p.Type, i) {
			continue
		}
		if !reassigned[p.Name] {
			continue
		}
		// Structs / tuples / enums (incl. unions). Whatever the shape, a
		// reassignment of a param slot emits the overwrite dec — so leaving a
		// reassigned param on the borrow baseline releases a reference the
		// caller never handed over. Enums were excluded here until the
		// parse_postfix under-count (`base = e_unary_at(op, base, …)` on a
		// borrowed `Expr` param, whose new node KEEPS the old value) showed the
		// exclusion is exactly the escape hatch the paragraph below closes for
		// scalar-only structs: same one-reference undercount, same early free
		// through a live alias. See TestX86_64UnionThreadedParam.
		switch p.Type.(type) {
		case ast.StructType, ast.TupleType, ast.EnumType:
		default:
			continue
		}
		// Only types owned-by-default EXCLUDES — string/array-bearing ones.
		// A string/array-free struct is already handled by owned-by-default
		// (when that flag is on) and must stay on the borrow baseline when it
		// is off; promoting it here would diverge the OwnedByDefault-vs-borrow
		// differential gate. consumedDropWired keeps Map / slice / unwired
		// shapes out (their deep drop is incomplete).
		//
		// EXCEPT when borrow inference DEMOTED the param (verdict Borrowed):
		// the caller then passes without an inc, and the reassignment's
		// overwrite dec releases a reference the callee never owned — the
		// caller's box rc undercounts, its own later is_unique-gated drop
		// frees early, and the still-live aliases double-free (the
		// `c = c2;` cursor-threading loop in a recursive `(T, Cur)`-tuple
		// reader was the repro: freelist link clobbered by a reused block's
		// rc header). String/array-BEARING params never hit this because
		// this very promotion already covered them; the scalar-only shape
		// needs it exactly when the borrow demotion applies. The
		// OwnedByDefault-vs-borrow differential gate is unaffected: with
		// the flag off the verdict is NotOwnedType, not Borrowed, and the
		// skip below still fires.
		if b.typeIsStringArrayFree(p.Type, map[string]bool{}) &&
			b.paramVerdict(b.fn.Name, p.Type, i) != paramVerdictBorrowed {
			continue
		}
		if !consumedDropWired(p.Type, b.info, map[string]bool{}) {
			continue
		}
		res[p.Name] = true
	}
	return res
}

// emitRcDecLocalsAtExit emits __fern_rc_dec for every
// array-typed parameter and local in the current function.
// Phase 1d-v balances the inc emissions from Phase 1d-i
// through 1d-iv: each alias-bind / call-arg / reassignment
// bumped the rc on the underlying buffer, and the function
// exit returns those references to the caller.
//
// Phase 1d-v doesn't special-case the returned value yet:
// if the function returns a local of array type, that
// local's rc drops to 0 at the exit, but no free happens
// (the bump allocator in Phase 1 doesn't reclaim). The
// caller-side rc is tracked independently via the call-arg
// inc emitted in Phase 1d-iv, so the caller's reference
// stays consistent.
//
// Phase 2's freelist + mutate-or-copy rc check will need an
// "owned return" pass to keep the returned value's rc at 1
// instead of 0 — that lands together with `arr.push`'s
// mutate-in-place fast path. For now, the rc just goes
// briefly to zero on the returned ptr, harmless under the
// no-free regime.
// computeFreeEligible runs the borrow-aware free analysis: it
// returns the set of array-typed locals that are OWNED — every value
// ever written to them is freshly owned (an array literal, or a call
// whose arguments are all owned) — so the array dec sites may safely
// return their buffer to the freelist at rc==0. Borrowed values flow
// in without a caller-side inc (the Phase 2d borrow model), so the rc
// undercounts them; freeing one would use-after-free a buffer a live
// borrow still holds (the self-host VM's compile_stmt/compile_block
// `ops` threading). The analysis taints such values and excludes
// them; only the owner frees (Perceus's rule).
//
// Taint sources: parameters; for-in / match / if-let / let-else /
// destructure bindings; locals that ESCAPE into a container (stored
// as a map/array element, struct/tuple/enum payload — retained
// without an inc, so the owner must not free out from under them).
// Taint propagates through assignment: a local becomes tainted if
// it's ever assigned a tainted Ident, a field / index / slice access
// (which alias their container), or a call that receives a tainted
// argument or receiver (the result may alias it). It also flows
// backward across bare-Ident aliasing (`tmp = arr`) so freeing the
// source can't strand a tainted alias.
// Array literals and calls with only owned arguments produce owned
// values. The default for an unrecognised RHS is tainted (sound:
// over-tainting only costs reclamation, never safety). Fixpoint to a
// stable set since taint can flow backward through `x = f(y)`.
func (b *builder) computeFreeEligible() map[string]bool {
	tainted := map[string]bool{}
	for i, p := range b.fn.Params {
		// `own` (consuming) params are OWNED by the callee — they're
		// freeEligible (reclaimed / reused here) rather than borrowed, the
		// reverse of the default. The caller transferred ownership (move-on-call
		// + the E051 guard), so the callee is the sole owner; the rest of the
		// escape analysis still re-taints an own param that escapes (stored /
		// returned-as-alias), so an owned-but-escaping param leaks safely
		// instead of double-freeing. Move-on-consume (passing it onward to
		// another `own` param) skips its drop via computeMovedLocals.
		if !p.Own && !b.paramOwnedByDefault(p.Type, i) && !b.rc.consumedParams[p.Name] {
			tainted[p.Name] = true
		}
	}
	// assigns[name] = list of RHS expressions ever written to it.
	assigns := map[string][]ast.Expr{}
	markBindings := func(names []string) {
		for _, n := range names {
			tainted[n] = true
		}
	}
	// escape taints a local that flows into a retain sink: a value
	// stored into a container (map/array element, struct/tuple/enum
	// payload) is RETAINED without a caller-side inc (the Phase 2d
	// borrow model — only the owner counts). Freeing the local at
	// scope exit would then use-after-free the alias the container
	// still holds (e.g. `var arr = [val]; m.set(k, arr)` in
	// std/url's __query_pair).
	//
	// A pointer-shaped value read OUT of a container and retained into
	// a sink — `def_body.push(body[k])`, `m.set(k, row[i])`,
	// `Arr(grid[j])` — copies the pointer without an inc too, so the
	// SOURCE container (`body` / `row` / `grid`) must not free it out
	// from under the sink either. escape unwraps such projection chains
	// (index / field / array-slice) to the root local and taints that.
	// The unwrap is gated on the projected value being pointer-shaped:
	// a scalar element (`i32[]`) can't alias, so its source stays
	// reclaimable. A string slice copies into a fresh owned buffer
	// (not a view), so it isn't unwrapped.
	var escape func(e ast.Expr)
	escape = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.Ident:
			tainted[x.Name] = true
		case *ast.Index:
			if ast.IsPointerType(b.exprType(x)) {
				escape(x.Array)
			}
		case *ast.FieldAccess:
			if ast.IsPointerType(b.exprType(x)) {
				escape(x.Target)
			}
		case *ast.SliceExpr:
			if !x.IsString {
				escape(x.Source)
			}
		}
	}
	// escapeOwned is the variant for the INC-ing sinks (StructLit /
	// TupleLit construction dups every stored pointer value), so only a
	// direct-Ident source can strand an uncounted alias — a projection
	// (`Holder { items: p.items }`) is inc'd into the box, so its
	// container stays reclaimable, and tainting it would needlessly
	// defeat constructor reuse (TestStructReuseFiresForPointerField).
	escapeOwned := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			// A consuming-match binding (#4400) is a COUNTED owner: every
			// inc-ing sink dups it (needsRcIncOnAlias fires — bindings are
			// never moveSites, those cover declared locals only) and the
			// exit-sweep dec balances, so there is no uncounted alias to
			// strand. Tainting it would skip the sweep and leak the dup —
			// the shape `Cons(h, t) => return Cons(h + 1, t)` would leak
			// the whole tail per call. The UNCOUNTED sinks (escape) still
			// taint these names.
			if _, owned := b.rc.consumingBindings[id.Name]; owned {
				return
			}
			tainted[id.Name] = true
		}
	}
	// escapeCountedYield is the if-/match-expr yield variant (#4399 sink
	// 4): the arm's emitCountedYield inc covers the needsRcIncOnAlias
	// shapes, so only uncovered yields (slice views, scalar / untracked
	// projections) keep the escape walk.
	escapeCountedYield := func(e ast.Expr) {
		switch e.(type) {
		case *ast.Ident, *ast.FieldAccess, *ast.Index:
			if needsRcIncOnAlias(e, b) {
				return // counted: the arm's alias inc covers it
			}
		}
		escape(e)
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.Var:
			if s.Init != nil {
				assigns[s.Name] = append(assigns[s.Name], s.Init)
			}
		case *ast.Assign:
			if id, ok := s.Target.(*ast.Ident); ok {
				assigns[id.Name] = append(assigns[id.Name], s.Value)
			} else {
				// Storing into an existing capture cell (`cap = v`)
				// retains the value without an inc, so the source
				// local escapes into the container.
				//
				// Index / FieldAccess targets are NOT sinks anymore:
				// the immutability migration banned both at the
				// checker (`a[i] = v` is E056, `p.f = v` is E048 —
				// unconditionally, and every internal desugar builds
				// Ident-target assigns only), so no program that
				// reaches lowering contains them. Their taint arms
				// were dead case-law and are deleted (#4399 sink 3);
				// the mutation idioms that replaced them are the
				// counted `.with` / functional-update stores handled
				// above and at StructLit.
				if _, isCap := s.Target.(*ast.CaptureRef); isCap {
					escape(s.Value)
				}
			}
		case *ast.IfExpr:
			// If- and match-expr yields are COUNTED for the
			// needsRcIncOnAlias shapes (#4399 sink 4): emitCountedYield
			// incs an aliased bare ident / field / index yield per arm,
			// so the expression's result is owned whichever arm ran and
			// the source local stays reclaimable — no move-site
			// suppression applies (computeMovedLocals never keys arm
			// yields), so even a direct-Ident yield is fully counted.
			// Only the shapes the inc does NOT cover keep the escape
			// taint: a slice yield is an uncounted view into its source,
			// and a scalar / untracked projection falls through escape's
			// pointer gate as before.
			escapeCountedYield(s.Then)
			escapeCountedYield(s.Else)
		case *ast.Destructure:
			// Destructure bindings are NOT tainted: the lowering dups
			// (rc_inc) each extracted pointer-shaped element, so the
			// binding becomes a counted OWNER and reclaims through its own
			// type's machinery at scope exit (array → arr_dec, struct →
			// __drop_struct_, …). The matching dec is the tuple's
			// deep-drop. Match / IfLet / LetElse bindings stay tainted —
			// they alias enum payloads with no projection dup.
		case *ast.Match:
			// Consuming owned match (#4400): a qualifying arm binding is a
			// COUNTED owner — the per-arm scrutinee release either transfers
			// the box's payload count to it (unique branch) or dups it
			// (shared branch) — so it is not borrow-tainted; the exit sweep
			// deep-drops it like an owned local. consumingBindings names are
			// by construction bound ONLY in qualifying arms, so the skip is
			// exact; every other binding keeps the taint.
			for _, arm := range s.Arms {
				for _, nm := range arm.Bindings {
					if _, owned := b.rc.consumingBindings[nm]; !owned {
						tainted[nm] = true
					}
				}
			}
		case *ast.MatchExpr:
			for _, arm := range s.Arms {
				markBindings(arm.Bindings)
				// Match-expr yields are counted exactly like if-expr
				// yields (#4399 sink 4b — every arm-body yield site in
				// all three match-expr lowering routes goes through
				// emitCountedYield), so the covered shapes drop the
				// escape taint; slice yields and untracked shapes keep
				// it. Arm BINDINGS stay tainted above — they alias enum
				// payloads with no projection dup.
				escapeCountedYield(arm.Body)
			}
		case *ast.IfLet:
			markBindings(s.Bindings)
		case *ast.LetElse:
			markBindings(s.Bindings)
		case *ast.Call:
			// Retain sinks the checker lowers to Calls. None of
			// these inc the stored rc value (unlike StructLit /
			// TupleLit construction, which do — see
			// needsRcIncOnAlias at the alias sites), so a local
			// that flows in is retained uncounted and must not be
			// freed at scope exit.
			if id, ok := s.Callee.(*ast.Ident); ok {
				switch {
				case id.Name == "__method_Map_set":
					// Args[0] is the map (mutated in place), not a
					// retained value — skip it; taint key + value.
					for _, a := range s.Args[1:] {
						escape(a)
					}
				case id.Name == "__method_Array_push":
					// Args[0] is the receiver array (threaded /
					// reassigned), not retained. The element is a COUNTED
					// store (#4399 sink 1): emitArrayPush emits the
					// needsRcIncOnAlias element inc (the same Ident /
					// FieldAccess / Index shapes `escape` walks), and the
					// buffer's deep drop decs elements — so a PROJECTION
					// source (`out.push(rows[i])`) is co-owned by the
					// buffer and its container stays reclaimable; only a
					// direct-Ident element keeps the taint (the moveSites
					// shapes transfer instead of inc'ing — same rule as
					// StructLit / TupleLit / rc-eligible enum payloads,
					// escapeOwned).
					if len(s.Args) == 2 {
						escapeOwned(s.Args[1])
					}
				case id.Name == "__method_Array_set":
					// `arr.with(i, v)` — Args[0] is the receiver array
					// (threaded / reassigned into the buffer, not retained),
					// Args[1] is the scalar index; Args[2] is the element
					// stored into the buffer.
					//
					// For POINTER-SHAPED elements (arrElemIsRcTracked) this
					// is a COUNTED store (#4399 sink 2): emitArraySet inc's
					// an aliased element (the same Ident / FieldAccess /
					// Index shapes `escape` walks), drops the OLD element it
					// overwrites, and the CoW copy path retains via
					// __fern_arr_cow_inplace_ptr — so a projection source
					// stays reclaimable and only a direct-Ident element
					// keeps the taint (escapeOwned, the Array_push rule).
					//
					// STRING elements are NOT counted there (the rcTracked
					// gate in emitArraySet excludes them — no inc, no
					// old-element drop), so they keep the full escape taint;
					// a scalar element can't alias but also can't strand
					// anything, so the escape walk's pointer gate already
					// no-ops it.
					if len(s.Args) == 3 {
						if len(s.TypeArgs) == 1 && arrElemIsRcTracked(s.TypeArgs[0]) {
							escapeOwned(s.Args[2])
						} else {
							escape(s.Args[2])
						}
					}
				default:
					// Variant constructor (`Arr(xs)`): under the move model
					// emitEnumNew stores the payload without an inc, so a local
					// passed as a payload escapes into the box (full escape). Under
					// EnumRcPayloads it inc's like StructLit, so only a direct-Ident
					// source can strand an uncounted alias — escapeOwned (a
					// projection is inc'd, so its container stays reclaimable).
					if _, isLocal := b.locals[id.Name]; !isLocal {
						if en, _, _, isVariant := b.lookupVariant(id.Name); isVariant {
							rc := b.enumRcPayloadsEligible(en)
							for _, a := range s.Args {
								if rc {
									escapeOwned(a)
								} else {
									escape(a)
								}
							}
						} else if !ast.UseTwoWordStrings(b.ptrW) {
							// User-function call: a native single-word STRING local passed as an
							// argument may be RETAINED by the callee (stored into a container it
							// returns — a struct field / array element that flows back out), which
							// the intraprocedural escape analysis cannot see. Freeing it caller-
							// side at last use then dangles the retained copy (observed in a
							// self-host codegen helper that stores a string arg into an array
							// field of the returned struct, where the native caller-side str_dec
							// recycled its box — corrupting nested control-flow codegen).
							// Conservatively taint string-typed idents passed to a user function
							// so they are not reclaimed here; the retained copy stays live (a leak
							// at worst, never a use-after-free). Gated to native — the two-word
							// ABIs' string reclaim is already correct and stays byte-identical.
							// #4174 follow-up.
							argStart := 0
							if strings.HasPrefix(id.Name, "__method_") {
								argStart = 1
							}
							counted := b.paramCountedRetain[id.Name]
							for ai, a := range s.Args[argStart:] {
								if aid, ok := a.(*ast.Ident); ok {
									if _, isStr := b.exprType(aid).(ast.StringType); isStr {
										// ...unless the callee retains this
										// parameter only through counted
										// constructions, which hold a reference
										// of their own — then the caller's
										// release is balanced, not a dangle.
										// This is the lexer's per-token leak:
										// eight `*_tok` helpers each store their
										// string param into the Token they
										// return, and the blanket taint stranded
										// the caller's reference on every one
										// (docs/SELFHOST-AST-RETIREMENT.md).
										pi := ai + argStart
										if pi < len(counted) && counted[pi] {
											continue
										}
										tainted[aid.Name] = true
									}
								}
							}
						}
					}
				}
			}
		case *ast.StructLit:
			for _, f := range s.Fields {
				escapeOwned(f.Value)
			}
		case *ast.TupleLit:
			for _, e := range s.Elems {
				escapeOwned(e)
			}
		case *ast.MapLit:
			for _, ent := range s.Entries {
				escape(ent.Key)
				escape(ent.Value)
			}
		case *ast.EnumLit:
			rc := b.enumRcPayloadsEligible(s.EnumName)
			for _, a := range s.Args {
				if rc {
					escapeOwned(a)
				} else {
					escape(a)
				}
			}
		case *ast.CastExpr:
			// Casting a pointer-shaped local to a raw integer (`buf as usize`)
			// hands out an address the rc analysis can't follow: code then
			// reads / writes through that raw pointer (random_bytes fills
			// `buf as usize`; int_to_string indexes an `as usize` scratch), so
			// the source buffer must stay live — freeing it at scope exit would
			// reclaim memory the raw pointer still uses. Taint the cast source
			// (escape unwraps any projection to the root local). This is the
			// load-bearing guard that lets the scalar-arg untaint below stay
			// safe: without it, untainting a literal / scalar-binary size arg
			// would make an `__alloc_u8(...) as usize` buffer eligible and
			// over-release it. Pointer→pointer casts keep rc tracking and are
			// left alone.
			it := s.InnerType
			if it == nil {
				it = b.exprType(s.Inner)
			}
			if ast.IsPointerType(it) && !ast.IsPointerType(s.Target) {
				escape(s.Inner)
			}
		}
		return true
	})
	for {
		changed := false
		for name, rhss := range assigns {
			for _, rhs := range rhss {
				if !tainted[name] && b.rhsTainted(rhs, tainted) {
					tainted[name] = true
					changed = true
				}
				// Backward alias propagation: a tainted local
				// assigned a bare Ident shares that source's
				// buffer, so the source must not be freed either
				// (`tmp = arr; m.set(k, tmp)` taints arr too).
				if tainted[name] {
					if src, ok := rhs.(*ast.Ident); ok && !tainted[src.Name] {
						tainted[src.Name] = true
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	elig := map[string]bool{}
	for _, v := range b.info.Locals[b.fn] {
		if tainted[v.Name] {
			continue
		}
		switch t := v.Type.(type) {
		case ast.ArrayType:
			elig[v.Name] = true
		case ast.StructType:
			// A Map local (runtime handle "Map") frees its buf + handle;
			// a user struct (has a StructDecl) frees its box. Both at
			// the last reference, when owned (untainted). Other runtime
			// handles (Reader/Writer/MapIter) have no StructDecl and no
			// drop handler — not eligible.
			if t.Name == "Map" {
				elig[v.Name] = true
			} else if _, isUser := b.info.Structs[t.Name]; isUser {
				elig[v.Name] = true
			}
		case ast.EnumType:
			// An owned enum frees its box when its layout is uniform
			// (emitDec gates on uniformEnumDropLoads + uniformEnumBoxSize;
			// non-uniform / generic enums keep the plain box dec). The
			// eligibility just grants permission — the same borrow-aware
			// taint as arrays/structs.
			elig[v.Name] = true
		case *ast.FuncType:
			// An owned closure frees its env / pair rc1 block at the
			// last reference (emitDec → __fern_closure_drop). Same
			// borrow-aware taint: a closure that escapes (returned via
			// alias, stored into a container, passed as a retained arg)
			// is tainted and falls back to the plain dec.
			elig[v.Name] = true
		case ast.TupleType:
			// An owned tuple returns its box to the freelist at the
			// last reference (emitDec → __fern_box_free). Same
			// borrow-aware taint as the others; box reclamation only
			// (elements keep their own rc, freed where they're owned).
			elig[v.Name] = true
		case ast.StringType:
			// Fresh owned heap string (concat / slice — rhsTainted whitelists
			// exactly those) frees at its last reference via __fern_str_dec on
			// EVERY ptrW; emitDec's StringType arm carries the native branch.
			// Balanced: aliases are retained by needsRcIncOnAlias, and every
			// uncounted-alias source (FieldAccess/Index/Call) is tainted.
			elig[v.Name] = true
		case ast.DynTraitType:
			// An owned `dyn Trait` local reclaims its erased concrete
			// `data` object through the per-set __drop_dyn_<set> helper at
			// its last reference (emitDec / emitVarReinitDropOld's
			// DynTraitType arm) — docs/DYN-TRAITS.md §4.4. Same borrow-
			// aware taint: a `dyn` that escapes (stored into a container,
			// returned, passed as a retained arg) is tainted above and
			// falls back. wasm (ptrW==4, slice 4a) + x86-64 (DynSupported,
			// slice 4b) reclaim; arm64 leaks `dyn` and never sets rcTracked
			// for it, so eligibility there is moot.
			if b.dynReclaim() {
				elig[v.Name] = true
			}
		}
	}
	// Owned (`own`) params get the SAME borrow-aware eligibility as an owned
	// local — the callee reclaims them — but params aren't in info.Locals, so
	// the loop above never reaches them. Add each untainted own param of a
	// reclaimable type (the un-taint at the top kept them out of `tainted`; an
	// own param that escapes was re-tainted and is skipped here).
	for i, p := range b.fn.Params {
		// `own` params and (Slice 2) owned-by-default params are both reclaimed
		// by the callee; a consumed-threaded param (computeConsumedParams) is
		// promoted to callee-owned the same way. An escaped one was re-tainted
		// and is skipped here.
		if (!p.Own && !b.paramOwnedByDefault(p.Type, i) && !b.rc.consumedParams[p.Name]) || tainted[p.Name] {
			continue
		}
		switch t := p.Type.(type) {
		case ast.ArrayType, ast.EnumType, *ast.FuncType, ast.TupleType:
			elig[p.Name] = true
		case ast.StructType:
			if t.Name == "Map" {
				elig[p.Name] = true
			} else if _, isUser := b.info.Structs[t.Name]; isUser {
				elig[p.Name] = true
			}
		case ast.StringType:
			if ast.UseTwoWordStrings(b.ptrW) {
				elig[p.Name] = true
			}
		case ast.DynTraitType:
			// An OWNED `dyn` param (one that escapes — stored / returned /
			// retained, so paramOwnedByDefault held above) reclaims through
			// __drop_dyn_<set> at exit, like an owned `dyn` local. A
			// dispatched-only `dyn` param is borrowed (paramOwnedByDefault
			// false), never reaches this switch, and is never dropped — the
			// borrow contract of docs/DYN-TRAITS.md §4.4 (no double-free).
			// wasm (slice 4a) + x86-64 (slice 4b).
			if b.dynReclaim() {
				elig[p.Name] = true
			}
		}
	}
	// Consuming-owned-match bindings (#4400) are counted owners — the per-arm
	// scrutinee release transferred (unique) or dup'd (shared) their payload
	// reference — so they get the same borrow-aware eligibility as owned
	// locals. They live in neither info.Locals nor Params, so neither loop
	// above reaches them. An escaping binding was re-tainted by the walk and
	// stays out (the transferred count then leaks — sound).
	for nm := range b.rc.consumingBindings {
		if !tainted[nm] {
			elig[nm] = true
		}
	}
	return elig
}

// rhsTainted reports whether the value produced by `e` may alias a
// borrowed (tainted) value, given the current taint set. See
// computeFreeEligible. Conservative: unknown shapes are tainted.
func (b *builder) rhsTainted(e ast.Expr, tainted map[string]bool) bool {
	switch x := e.(type) {
	case *ast.ArrayLit:
		return false
	case *ast.StructLit:
		return false
	case *ast.TupleLit:
		// A freshly-built tuple (rc=1) owns its box, like an array /
		// struct literal — not an alias of a borrowed value, so the
		// tuple local is eligible to free its box at the last reference.
		// Escapes are still caught: a returned tuple takes move-on-return
		// and one stored into a container is escape-tainted at the sink.
		return false
	case *ast.MakeClosure:
		// A freshly-built closure (rc=1), like an array literal — it
		// owns its env, not an alias of a borrowed value. Owned, so the
		// FuncType local is eligible to free its env/captures at the
		// last reference (closure reclamation Stages 2-3). Escapes are
		// still caught: a returned closure takes move-on-return, and one
		// stored into a container is escape-tainted at the sink.
		return false
	case *ast.Ident:
		return tainted[x.Name]
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit:
		// A scalar literal aliases nothing, so a fresh owned result whose only
		// "borrowed" input is a literal arg is reclaimable — e.g.
		// int_to_string's `__alloc_u8(16)` buffer, or split's literal-length
		// allocations. Without this the literal fell through to the tainted
		// default and stranded those buffers (unbounded leak in a loop). The
		// raw-pointer liveness such code threads via `buf as usize` is held by
		// the CastExpr escape taint in computeFreeEligible, not here.
		return false
	case *ast.StringLit:
		// A string literal has Static ownership (ExprResultOwnership) — it
		// aliases nothing, so a local initialised from a bare literal is
		// eligible, exactly like the scalar-literal case above. This is what
		// makes the `var s = ""; loop { s = s + p }` accumulator reclaim each
		// intermediate concat: the dec-on-overwrite (ir.go ~17183) requires
		// freeEligible[s], and without this case `""` fell to the tainted
		// default and stranded every intermediate buffer (O(n²) bump-heap
		// growth on the hot response-assembly path). Dec'ing the literal itself
		// is safe — the overwrite str_dec / __fern_rc_dec sentinel + SSO guards
		// no-op on non-heap (literal / Static) sources, and only the later
		// fresh heap concats actually reclaim. An escaping accumulator is still
		// caught by the escape taint in computeFreeEligible, same as the other
		// untainted-owned cases.
		return false
	case *ast.FieldAccess:
		// Reading a pointer field out of a struct-typed LOCAL is a COUNTED
		// alias, not a borrow: the binding site inc's it (needsRcIncOnAlias
		// fires for a pointer field), and both the destination and the
		// struct source itself deep-drop at scope exit, so the read owns its
		// own reference rather than aliasing the container's uncounted. So
		// the destination stays reclaimable. This is the `l = r.lex` result-
		// struct threading in lexer.tokenize (docs/SELFHOST-AST-RETIREMENT.md):
		// a scanner returns `Res { lex, tok }`, the caller extracts `l = r.lex`
		// to thread the cursor, and the conservative FieldAccess taint
		// stranded `l` — and through it the whole result-struct cluster —
		// every iteration. Gated to a struct LOCAL source: a container this
		// function reasons about and that itself reclaims. A param, a
		// projection chain (`r.a.b`, whose Target is a FieldAccess), or a
		// non-struct source keeps the conservative taint. The escape sink
		// walk is unchanged, so a projection flowing into an UNCOUNTED sink
		// (`m.set(k, r.field)`) still taints its source there.
		if id, ok := x.Target.(*ast.Ident); ok {
			if _, isLocal := b.locals[id.Name]; isLocal {
				if _, isStruct := b.exprType(id).(ast.StructType); isStruct {
					return false
				}
			}
		}
		return true
	case *ast.Index:
		return true
	case *ast.SliceExpr:
		// A STRING slice copies its bytes into a fresh owned heap buffer
		// (the wasm runtime always allocates), so it's reclaimable — not
		// a view. Array / other slices share the source buffer → tainted.
		if _, ok := b.exprType(x).(ast.StringType); ok {
			return false
		}
		return true
	case *ast.Binary:
		// String concat (`a + b`) copies both operands into a fresh owned
		// heap buffer regardless of operand provenance, so the result is
		// always reclaimable. Non-concat binaries stay tainted (the original
		// conservative default): untainting a scalar-binary SIZE arg
		// (`__alloc_u8(out_len)`, out_len from `k + 1`) made buffers eligible
		// that the escape/move analysis can't prove safe to reclaim — it
		// over-released int_to_string_radix's result buffer (to_rgb_hex
		// returned the wrong hex). The win is in the NumberLit case above
		// (literal-sized temps); the scalar-binary case is marginal — most
		// such buffers are `as usize`-threaded or moved into the return — and
		// not worth the over-release risk.
		return !x.IsStringConcat
	case *ast.Call:
		// Slice 1b: under EnumRcPayloads a variant constructor is a FRESH
		// rc=1 box that inc's its pointer payloads (like StructLit), so the
		// constructed value is reclaimable regardless of payload taint — return
		// false, mirroring the StructLit/TupleLit cases. Without this the generic
		// any-arg-tainted recursion below propagates a tainted nullary-variant
		// arg (`Nil`) up to the enum local, leaving it permanently ineligible.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isLocal := b.locals[id.Name]; !isLocal {
				if en, _, _, isVar := b.lookupVariant(id.Name); isVar && b.enumRcPayloadsEligible(en) {
					return false
				}
			}
		}
		// Map builtins return the MAP HANDLE, which aliases only the
		// receiver (cow) — never the stored key/value args. The generic
		// any-arg-tainted rule below would taint every map handle (the
		// cap/key/value args are routinely tainted — params, literals via
		// the default case), leaving map locals permanently ineligible and
		// their buf/handle + values unreclaimed. The rc inc-on-set /
		// dec-on-drop balance makes freeing an owned map's storage safe.
		if id, ok := x.Callee.(*ast.Ident); ok {
			switch id.Name {
			case "map_new":
				return false // fresh owned handle
			case "cell_new":
				// A fresh rc=1 cell box (emitCellNew) that RETAINS its element
				// (string args are inc'd on construction), like map_new /
				// StructLit — reclaimable regardless of the arg's taint. The
				// any-arg-tainted rule below would otherwise taint a
				// Cell[string] built from a literal / borrowed string and leave
				// it permanently ineligible (its slot buffer + box unreclaimed).
				// An escaping cell is still caught by the escape / move analysis.
				return false
			case "__method_Map_set", "__method_Map_clear":
				// Aliases the receiver (Args[0]) only.
				return len(x.Args) > 0 && b.rhsTainted(x.Args[0], tainted)
			case "__method_Array_set":
				// `arr.with(i, v)` returns the receiver buffer (cow), aliasing
				// Args[0] only — never the index/value args. The generic
				// any-arg-tainted rule below would taint the result via a
				// tainted scalar-binary value (`b.with(0, i % 200)`), leaving
				// the buffer permanently ineligible and unreclaimed at loop
				// scope (the wasm LiteralAllocReclaim / OwnInplaceSort leak).
				return len(x.Args) > 0 && b.rhsTainted(x.Args[0], tainted)
			case "__method_Array_push":
				// `arr.append(v)` is `.with`'s sibling: the result is the
				// receiver buffer (grown in place at rc==1, else a fresh copy),
				// aliasing Args[0] only. The pushed element is a COUNTED store
				// — emitArrayPush inc's an aliased element and the buffer's deep
				// drop decs it — so an element's taint says nothing about the
				// buffer's own provenance. Without this, `var out = []` followed
				// by `out = out.append(tok)` in a loop tainted `out` from the
				// first append onward, stranding the whole accumulated buffer;
				// it is what leaves lexer.tokenize reclaiming 8.8% of its blocks
				// (docs/SELFHOST-AST-RETIREMENT.md). Sound only alongside the
				// _move_ grow helpers: an ALIASED receiver (`var lg = g; lg =
				// lg.append(v)`) makes both halves reclaimable, and the plain
				// helper's non-retaining copy then let both walk-drops release
				// the same elements (#3457).
				return len(x.Args) > 0 && b.rhsTainted(x.Args[0], tainted)
			case "random_bytes":
				// random_bytes returns a string the two-word backends
				// (arm64 / wasm) allocate as RAW n bytes with NO rc header
				// (__fern_alloc, not __fern_alloc_rc1), so it is not a
				// reclaimable owned string — str_dec'ing it at scope exit
				// reads garbage for the rc and corrupts / over-releases the
				// buffer (TestArm64RandomBytes: 83 bytes, want 16). It must
				// stay ineligible. This was protected only by accident before
				// the scalar-arg untaint below — the literal size arg used to
				// taint the result; now it's marked explicitly. (The runtime
				// fills the buffer through a raw pointer too, which the
				// CastExpr escape taint can't see — it lives in asm, not Fern
				// source.)
				return true
			}
		}
		// #4357: a call to a user function whose RETURN provably aliases no
		// param (findReturnsNoParamEscape — every return expression is built
		// from scalars and FRESH constructions whose pointer-typed slots are
		// themselves qualifying, transitively) hands back a fresh owned value
		// regardless of receiver/arg taint: the tainted inputs are only READ
		// to build it. This is the same oracle the nested-call temp reclaim
		// (rc_insert.go stage-b) already trusts to free a call result.
		// Without it, `var t = f(x)` over any param-derived x stays
		// permanently ineligible, and the dead intermediate in
		// `var t = f(x); var u = g(t); return u;` leaks once per call — the
		// self-compile RSS driver (docs/SELFHOST-BSTATE-RECLAIM-PLAN.md
		// "The real leak").
		if id, ok := x.Callee.(*ast.Ident); ok && b.returnsNoParamEscape[id.Name] {
			if _, isLocal := b.locals[id.Name]; !isLocal {
				return false
			}
		}
		if fa, ok := x.Callee.(*ast.FieldAccess); ok && b.rhsTainted(fa.Target, tainted) {
			return true // method receiver is tainted
		}
		// The counted-retain sibling of the rule above: findReturnsNoParamEscape
		// demands the result alias NO param, which a node constructor
		// (`mkTok(name, line) -> Tok { name: name, … }`) can never satisfy — yet
		// its result IS a fresh owned box, because the only param it carries out
		// is carried by a counted construction that inc'd it. Untaint when every
		// tainted argument sits in such a position: the result then owns its
		// references, and dropping it decs them exactly once.
		var counted []bool
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isLocal := b.locals[id.Name]; !isLocal {
				counted = b.paramCountedRetain[id.Name]
			}
		}
		for i, a := range x.Args {
			if !b.rhsTainted(a, tainted) {
				continue
			}
			if i >= len(counted) || !counted[i] {
				return true
			}
		}
		return false
	case *ast.IfExpr:
		return b.rhsTainted(x.Then, tainted) || b.rhsTainted(x.Else, tainted)
	case *ast.MatchExpr:
		// A match-expression is owned iff every arm body is — the exact
		// mirror of IfExpr. Without this case it fell through to the tainted
		// default, leaving `var s = match (k) { 0 => a + b, _ => b + a }` (all
		// arms fresh concats) permanently ineligible and unreclaimed (leaked
		// 240000 → 2400000 in a loop). A bare-local arm is still caught: the
		// escape(arm.Body) in computeFreeEligible taints that local, so
		// rhsTainted reads it back as tainted here and the match stays
		// protected — same belt-and-suspenders as IfExpr.
		for _, arm := range x.Arms {
			if b.rhsTainted(arm.Body, tainted) {
				return true
			}
		}
		return false
	case *ast.TryOp:
		// `f()?` MOVES the success payload out of a fresh call-result box —
		// the TryOp lowering frees the box shallow (emitTryBoxFree), so a
		// STRING payload's counted reference transfers to the binding: owned,
		// exactly like a fresh concat (the construction-side alias-inc under
		// EnumRcPayloads keeps an `Ok(pre)`-style aliased payload balanced —
		// see reclaimableTryScrutinee, whose gate this mirrors so analysis
		// and lowering can never disagree). Non-reclaimable inners (a bare
		// local `r?`, field / index projections, pair-form callees) and
		// non-string payloads keep the conservative taint.
		if _, ok := b.reclaimableTryScrutinee(x); ok {
			if _, isStr := x.Type.(ast.StringType); isStr {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func (b *builder) computeMovedLocals() map[string]bool {
	moved := map[string]bool{}
	if b.fn.Body == nil {
		return moved
	}
	order := identOrderOf(b.fn.Body)
	sawReturn := false
	for _, st := range b.fn.Body.Stmts {
		if !sawReturn {
			// The lowering checks b.rc.moveSites on the Var node or the
			// inner Assign node (assignments are ExprStmt-wrapped), so
			// key the site on whichever the lowering will see.
			var rhs *ast.Ident
			var site ast.Node
			var val ast.Expr
			switch s := st.(type) {
			case *ast.Var:
				rhs, _ = s.Init.(*ast.Ident)
				site = s
				val = s.Init
			case *ast.ExprStmt:
				if a, ok := s.Expr.(*ast.Assign); ok {
					if _, tok := a.Target.(*ast.Ident); tok {
						rhs, _ = a.Value.(*ast.Ident)
						site = a
						val = a.Value
					}
				}
			case *ast.Return:
				val = s.Value
			case *ast.Destructure:
				// `var (a, b) = t` aliases the source tuple into the
				// destructure temp (inc at the alias site below). When t
				// is an owned rc local at its last use, that inc and t's
				// exit-sweep dec cancel — move t into the temp, which
				// frees the box once. Keyed on the Destructure node (the
				// lowering checks b.rc.moveSites[n] there).
				rhs, _ = s.Init.(*ast.Ident)
				site = s
			}
			if rhs != nil && b.isOwnedRcLocal(rhs.Name) && order.isLast(rhs) {
				moved[rhs.Name] = true
				b.rc.moveSites[site] = true
			}
			// Move-on-construction: a struct literal built at this
			// top-level statement that consumes an owned rc local at the
			// local's last use moves it into the field (see
			// markConstructionMoves).
			if val != nil {
				b.markConstructionMoves(val, order, moved, nil)
			}
		}
		if stmtContainsReturn(st) {
			sawReturn = true
		}
	}

	// The walk above is TOP-LEVEL only, which left a leak: a container built
	// in a LOOP body from a bare ident never took the move, so the
	// construction alias-inc was emitted with nothing releasing the source's
	// own reference per iteration — one leaked element per iteration,
	// unbounded (#5879). Loop bodies get the same treatment under a stricter
	// dominance rule.
	b.markLoopBodyConstructionMoves(order, moved)

	// Move-on-call: an `own` argument (one of THIS function's owned params)
	// passed at its last use to an `own` PARAMETER of a callee is consumed —
	// the callee now owns and drops it — so skip the caller's drop. There's no
	// inc to elide (a call arg is passed without one), so only the exit-sweep /
	// precise drop is suppressed via `moved`. Gated on the E051 guard, which is
	// what guarantees an `own`-position arg is an owned, transferable value.
	if len(b.info.OwnFuncs) > 0 {
		ownParam := map[string]bool{}
		for _, p := range b.fn.Params {
			if p.Own {
				ownParam[p.Name] = true
			}
		}
		if len(ownParam) > 0 {
			// A consuming match (`match (own_param) { … }`) consumes the
			// scrutinee — its box is shallow-freed at the match — so the exit
			// sweep must not ALSO deep-drop it. Mark the own-param scrutinee
			// moved (its last use is the match).
			markScrutinee := func(tag ast.Expr) {
				if id, ok := tag.(*ast.Ident); ok && ownParam[id.Name] &&
					order.isLast(id) {
					moved[id.Name] = true
				}
			}
			ast.Walk(b.fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Match:
					markScrutinee(x.Tag)
				case *ast.MatchExpr:
					markScrutinee(x.Tag)
				case *ast.Call:
					id, ok := x.Callee.(*ast.Ident)
					if !ok {
						return true
					}
					flags, isOwn := b.info.OwnFuncs[id.Name]
					if !isOwn {
						return true
					}
					for i := 0; i < len(x.Args) && i < len(flags); i++ {
						if !flags[i] {
							continue
						}
						if arg, ok := x.Args[i].(*ast.Ident); ok &&
							ownParam[arg.Name] && order.isLast(arg) {
							moved[arg.Name] = true
						}
					}
				}
				return true
			})
		}
	}
	return moved
}

// markConstructionMoves implements the move-on-construction slice of
// Phase 4 pair-cancellation: when a struct literal built at a
// dominating top-level statement consumes an OWNED rc local in a
// non-string rc-tracked field at the local's LAST use
// (`var s = Wrap{ inner: x }`, `x` dead after), the field-init inc and
// x's exit-sweep dec cancel — x's single reference is moved into the
// struct's field. Skipping the inc (gated on b.rc.moveSites[fieldIdent] at
// the StructLit lowering) and x's dec (moved[x] excludes it from the
// exit sweep) leaves the struct owning x; the struct's own field-drop
// (emitDec) releases it exactly once, so the net rc is unchanged.
//
// The eligibility mirrors the inc/drop sides exactly: the field must be
// `arrElemIsRcTracked` (array / struct / enum / closure / tuple — the
// fields the StructLit inc's AND emitDec dec's; strings are excluded,
// their two-word retain/release diverges per backend), and the value
// must be an owned rc local (isOwnedRcLocal) whose occurrence here is
// its max pre-order index. The caller has already established the
// dominance guards — for the top-level walk, a top-level statement with no
// preceding return — so, exactly as move-on-alias, x is moved on every path
// to an exit.
//
// `allow`, when non-nil, further restricts which local names may be moved.
// markLoopBodyConstructionMoves passes it to limit moves to vars declared
// inside the same loop body, whose lifetime is one iteration; it carries its
// own dominance guards, documented there. A nil `allow` admits every name,
// which is the top-level caller's behaviour.
func (b *builder) markConstructionMoves(val ast.Expr, order identOrder, moved map[string]bool, allow func(string) bool) {
	// mark moves the Ident when it's an owned rc local at its last use.
	// The caller has established the dominance guards; the per-container
	// drop (struct field-drop / array drop_arr_ptr) releases the moved
	// value exactly once, balancing the skipped construction inc.
	mark := func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok || !b.isOwnedRcLocal(id.Name) || !order.isLast(id) {
			return
		}
		if allow != nil && !allow(id.Name) {
			return
		}
		moved[id.Name] = true
		b.rc.moveSites[id] = true
	}
	switch lit := val.(type) {
	case *ast.StructLit:
		sd, ok := b.info.Structs[lit.TypeName]
		if !ok {
			return
		}
		for _, f := range lit.Fields {
			// Only fields the StructLit inc's AND emitDec dec's on drop
			// (arrElemIsRcTracked; strings excluded).
			if arrElemIsRcTracked(fieldType(sd.Fields, f.Name)) {
				mark(f.Value)
			}
		}
	case *ast.ArrayLit:
		// An array of rc-tracked elements: each element is inc'd on
		// construction and dec'd by __fern_drop_arr_ptr at the array's
		// drop, so a moved element balances. Plain-scalar arrays never
		// reach the element inc — mark is a no-op there (isOwnedRcLocal
		// is false for scalars).
		for _, el := range lit.Elems {
			mark(el)
		}
	case *ast.TupleLit:
		// A tuple with rc-tracked elements: each is inc'd on
		// construction and dec'd by __drop_tuple_<...> at the tuple's
		// drop (tupleNeedsDrop / dropFnNameFor), so a moved element
		// balances — same shape as the struct/array cases. Only mark
		// owned rc locals; mark self-filters non-pointer elements via
		// isOwnedRcLocal.
		for _, el := range lit.Elems {
			mark(el)
		}
	case *ast.MakeClosure:
		// A closure capturing rc-tracked locals: each is inc'd at
		// MakeEnv (Phase 1d-vii) and dec'd by the closure's drop
		// (__closure_drop_<name> / __fern_closure_drop at its last
		// reference), so a moved capture balances — same shape as the
		// other containers. Eligibility matches hasRcCapture
		// (arrElemIsRcTracked; strings are reclaimed by the thunk too
		// but excluded here for the same single-word-temp reason as the
		// struct/array cases). mark self-filters via isOwnedRcLocal.
		// Eliding an inc only REMOVES ops, which the Defunctionalise /
		// ElideClosurePair passes tolerate (they already treat the inc
		// as a value-preserving pass-through when chasing alias chains);
		// the defunc/elide unit tests + self-host VM gate this.
		for _, cap := range lit.Captures {
			if arrElemIsRcTracked(b.exprType(cap)) {
				mark(cap)
			}
			// `dyn Trait` capture (docs/DYN-TRAITS.md §7.8 — closure-capture
			// kind). A captured `dyn` is move-only (needsRcIncOnAlias declines
			// it, so NO inc at MakeEnv), and the closure's drop thunk reclaims
			// it (genClosureDropThunk's dyn arm), so the source local MUST be
			// suppressed from the exit sweep — otherwise both the source-local
			// drop AND the thunk reclaim the same cell (a use-after-free when
			// the closure ESCAPES: the source drop frees the cell the returned
			// closure still derefs). NATIVES ONLY (b.dynRcSupported → boxed
			// one-word cell, single owner after the move); wasm's inline two-
			// word `dyn` keeps its prior correct-but-leaking capture behaviour
			// (its env copy isn't reclaimed, the thunk doesn't reclaim it, and
			// the source local stays swept) — gating here on dynRcSupported (NOT
			// dynReclaim, which includes wasm) keeps the suppress/reclaim pair
			// consistent with hasRcCapture + the thunk. `mark` can't be reused —
			// it gates on isOwnedRcLocal, which (deliberately) excludes `dyn` —
			// so apply the last-use guard inline.
			if _, isDyn := b.exprType(cap).(ast.DynTraitType); isDyn && b.dynRcSupported {
				if id, ok := cap.(*ast.Ident); ok && order.isLast(id) {
					moved[id.Name] = true
					b.rc.moveSites[id] = true
				}
			}
		}
	case *ast.Call:
		// Slice 1b: an enum variant constructor — emitEnumNew now inc's an
		// aliased pointer payload and the enum's deep drop dec's it, so a moved
		// last-use OWNED-LOCAL payload balances (mark self-filters via
		// isOwnedRcLocal — own params aren't locals, so they're inc'd and
		// balanced by the exit-sweep dec, exactly like a struct field). Only
		// variant-constructor calls.
		if id, ok := lit.Callee.(*ast.Ident); ok {
			if en, _, _, isVar := b.lookupVariant(id.Name); isVar && b.enumRcPayloadsEligible(en) {
				for _, a := range lit.Args {
					mark(a)
				}
			}
		}
	}
}

// computeArraySetIncs decides, for each `.with` call, whether emitArraySet
// must rc-inc the receiver before __fern_arr_cow_inplace (forcing a copy).
// The inc is needed exactly when the receiver is a bare local that is read
// AGAIN after this call and is NOT the target of a reassign-to-self
// (`a = a.with(...)`), where the old buffer is overwritten by the result.
// A receiver at its last use, a non-ident receiver (a fresh temp), or a
// reassign-to-self is a MOVE — left to the in-place rc==1 fast path, so the
// canonical allocation-free idiom is unaffected (#2832).
func (b *builder) computeArraySetIncs() map[*ast.Call]bool {
	incs := map[*ast.Call]bool{}
	if b.fn.Body == nil {
		return incs
	}
	order := identOrderOf(b.fn.Body)
	// reassign-to-self: `A = A.with(...)` — the receiver's old value is
	// overwritten by the result, so reuse is sound (no inc).
	reassignSelf := map[*ast.Call]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		a, ok := n.(*ast.Assign)
		if !ok {
			return true
		}
		tid, tok := a.Target.(*ast.Ident)
		c, cok := a.Value.(*ast.Call)
		if tok && cok && isArraySetCall(c) {
			if rid, rok := c.Args[0].(*ast.Ident); rok && rid.Name == tid.Name {
				reassignSelf[c] = true
			}
		}
		return true
	})
	// Borrowed parameters: a non-`own`, non-owned-by-default, non-consumed
	// param is a BORROW — the caller still owns its buffer (no caller-side inc
	// on the borrow), so the buffer's rc is 1 (the caller's). An in-place cow
	// at rc==1 would mutate the caller's array out from under it
	// (`function f(xs: i32[]): i32[] { return xs.with(0, 9); }` must not change
	// the caller's array). This holds even at the param's last use and for a
	// reassign-to-self (`xs = xs.with(...)`): the local rebind is fine, but the
	// underlying buffer is the caller's. Same borrow predicate as
	// computeFreeEligible (which runs before this — b.rc.consumedParams /
	// b.rc.freeEligible are already populated). Force the inc so cow copies.
	borrowedParam := map[string]bool{}
	for i, p := range b.fn.Params {
		if !p.Own && !b.paramOwnedByDefault(p.Type, i) && !b.rc.consumedParams[p.Name] {
			borrowedParam[p.Name] = true
		}
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || !isArraySetCall(c) {
			return true
		}
		if rid, rok := c.Args[0].(*ast.Ident); rok && borrowedParam[rid.Name] {
			incs[c] = true
			return true
		}
		if reassignSelf[c] {
			incs[c] = false
			return true
		}
		rid, rok := c.Args[0].(*ast.Ident)
		if !rok {
			// A non-ident receiver is only a MOVE-able fresh temp when it is
			// genuinely owned (a call result `f().with(...)`, dead after the
			// call). A PROJECTION out of another live value — an array element
			// `a[0]`, a struct field `s.b`, or a slice `a[i:j]` — is a BORROW:
			// the inner buffer is still owned by its container, so an in-place
			// cow would corrupt the container (`a[0].with(0,9)` must not mutate
			// `a[0]`). Force the inc on a projection so cow copies instead.
			switch c.Args[0].(type) {
			case *ast.Index, *ast.FieldAccess, *ast.SliceExpr:
				incs[c] = true
			default:
				incs[c] = false
			}
			return true
		}
		// Live after the call iff this occurrence is NOT the receiver
		// name's last use.
		incs[c] = !order.isLast(rid)
		return true
	})
	return incs
}

// computeReuseSources is the general-FBIP pairing analysis: it matches a
// construction site C (a `var c = T{…}` / `c = (…)` whose RHS is a plain
// StructLit or a TupleLit) with a DEAD, OWNED struct/tuple local D whose box C
// can reuse in place — the Perceus reuse token threaded from D's drop to C's
// allocation, generalised beyond the self-overwrite tryStructReuseOverwrite
// (where D == C). Returns the C→D map (keyed by the StructLit / TupleLit node)
// and the set of consumed D names (so computePreciseDrops won't ALSO drop
// them). D and C must be the same KIND (struct↔struct or tuple↔tuple).
//
// First cut — deliberately narrow and obviously sound:
//   - D and C are the SAME `structReuseEligible` struct type, so the box sizes
//     match exactly. Fields may be i32-class scalars OR single-word rc-tracked
//     pointers (array / struct / Map / enum / closure / tuple — strings and
//     wide/float scalars are still excluded, same gate as the self-overwrite
//     5c path). D is DEAD at C, so C never carries a field from D: every one
//     of D's old pointer-field references is RELEASED (deep freeing drop) on
//     the reuse branch before C's stores overwrite them, and each of C's new
//     pointer fields is retained on eval as normal StructLit construction.
//   - D and C are in the SAME statement list (block): the function body OR any
//     nested block (loop body, if arm). Pairing within a loop body is the
//     high-value case — a per-iteration `var a = T{…}; …; var b = T{…}` reuses
//     a's box for b every iteration. A block-scoped D dies at block exit, so
//     "dead from C within the block" is sufficient.
//   - D is a `var`, declared before C in the list, never reassigned,
//     name-unique (no shadowing ambiguity), and `freeEligible` (OWNED — never
//     a borrowed param; the runtime is_unique check at the reuse site is the
//     second gate, so a shared D copies).
//   - D is DEAD from C onward within its block: referenced in no statement at
//     or after C's index (so C's fields don't read D, and nothing observes D's
//     box after C repurposes it). The reuse zeroes D's slot, so the exit sweep
//     — and any path that DOESN'T reach C — null-guards to a no-op / drops D
//     normally. A mispaired or shared D degrades to dec-then-fresh-alloc
//     (never unsound, never a leak — the same invariant as __alloc_reuse).
//
// markLoopBodyConstructionMoves extends move-on-construction into loop bodies
// (#5879). `markConstructionMoves`'s own dominance guard is "top-level
// statement, no preceding return", so before this a container built inside a
// loop from a bare ident at its last use kept the construction alias-inc while
// nothing released the source's reference per iteration:
//
//	while (k < n) {
//	    var xs: i32[] = [1, 2, 3];
//	    var t = (xs, 99);        // xs inc'd here, never dec'd per iteration
//	    …
//	}
//
// The function-exit sweep emits the flat dec the loop body is missing, so only
// the FINAL iteration was reclaimed and the leak was (n-1) elements, linear and
// unbounded. Replacing the same element with a fresh literal (`([1,2,3], 99)`)
// was already clean, because a literal names no local and so takes no inc.
//
// The move is sound here for exactly the reason it is at top level — the
// source's single reference passes to the container, whose drop releases it
// once — but the per-iteration lifetime needs guards the top-level walk does
// not:
//
//   - The ident must name a var DECLARED EARLIER IN THIS BODY. A var declared
//     OUTSIDE the loop lives across iterations: moving it would let the first
//     iteration's container drop free a buffer the second iteration still
//     reads (a use-after-free, not a leak).
//   - The body must contain no `return` / `break` / `continue`. Those make the
//     construction conditional, so the exit sweep suppression `moved` implies
//     could outlive a path on which no construction ran.
//   - The name must be declared exactly once in the function (localNameUnique).
//     `moved` is name-keyed, so a shadowed name would suppress the exit-sweep
//     dec of an unrelated same-name local.
//
// Nested loops are covered: ast.Walk visits every loop, and each body is
// judged against the vars declared in THAT body.
func (b *builder) markLoopBodyConstructionMoves(order identOrder, moved map[string]bool) {
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		var body ast.Stmt
		switch x := n.(type) {
		case *ast.While:
			body = x.Body
		case *ast.Loop:
			body = x.Body
		case *ast.For:
			body = x.Body
		case *ast.ForEach:
			body = x.Body
		default:
			return true
		}
		blk, ok := body.(*ast.Block)
		if !ok || blockHasEarlyExit(blk) {
			return true
		}
		// Vars declared so far in THIS body — the only names a construction
		// here may move, and only once their declaration precedes it.
		declared := map[string]bool{}
		allow := func(name string) bool {
			return declared[name] && b.localNameUnique(name)
		}
		for _, st := range blk.Stmts {
			switch s := st.(type) {
			case *ast.Var:
				if s.Init != nil {
					b.markConstructionMoves(s.Init, order, moved, allow)
				}
				declared[s.Name] = true
			case *ast.ExprStmt:
				if a, ok := s.Expr.(*ast.Assign); ok {
					b.markConstructionMoves(a.Value, order, moved, allow)
				}
			}
		}
		return true
	})
}

// blockHasEarlyExit reports whether `blk` contains a `return`, `break` or
// `continue` anywhere within it, including nested statements. Used as the
// loop-body dominance guard: with any of them present a construction later in
// the body is conditional, so its move cannot be assumed to happen on every
// iteration.
func blockHasEarlyExit(blk *ast.Block) bool {
	found := false
	ast.Walk(blk, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.Return, *ast.Break, *ast.Continue:
			found = true
			return false
		}
		return !found
	})
	return found
}

func (b *builder) computeReuseSources() (map[ast.Expr]string, map[string]bool) {
	sources := map[ast.Expr]string{}
	consumed := map[string]bool{}
	if !ast.RcFreeEnabled || !ast.RcReuseEnabled || b.fn.Body == nil {
		return sources, consumed
	}

	// Reassigned-at-any-depth set (a reassigned D's box at C is still owned,
	// but excluding them keeps the first cut simple, matching precise-drop).
	reassigned := map[string]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		}
		return true
	})

	const rcHeaderBytes = 8
	// reuseClassOf returns a local's box "kind" (struct / tuple / enum), its
	// type name (empty for tuples), and its freelist class — (alloc+15)&-16 of
	// data+rc-header, within the exact-fit ≤ 2048 range — for any general-FBIP
	// reuse-eligible struct / tuple / enum local. ok=false for anything else
	// (non-box type, a string/wide-scalar field/element, a non-uniform enum, or
	// > 2048).
	reuseClassOf := func(name string) (kind string, typeName string, class int32, ok bool) {
		t, ok2 := b.localDeclType(name)
		if !ok2 {
			return "", "", 0, false
		}
		switch tt := t.(type) {
		case ast.StructType:
			sd, ok3 := b.info.Structs[tt.Name]
			if !ok3 || !structReuseEligible(sd) {
				return "", "", 0, false
			}
			_, size := structFieldLayout(sd.Fields, b.ptrW)
			alloc := size + rcHeaderBytes
			if alloc > 2048 {
				return "", "", 0, false
			}
			return "struct", tt.Name, (alloc + 15) &^ 15, true
		case ast.TupleType:
			if !tupleReuseEligible(tt.Elems) {
				return "", "", 0, false
			}
			_, size := tupleElemLayout(tt.Elems, b.ptrW)
			alloc := size + rcHeaderBytes
			if alloc > 2048 {
				return "", "", 0, false
			}
			return "tuple", "", (alloc + 15) &^ 15, true
		case ast.EnumType:
			ed, ok3 := b.info.Enums[tt.Name]
			if !ok3 {
				return "", "", 0, false
			}
			if len(tt.Args) > 0 {
				ed = substituteEnumDecl(ed, tt.Args)
			}
			_, size, eok := b.enumReuseLoads(ed)
			if !eok {
				return "", "", 0, false
			}
			alloc := size + rcHeaderBytes
			if alloc > 2048 {
				return "", "", 0, false
			}
			return "enum", tt.Name, (alloc + 15) &^ 15, true
		}
		return "", "", 0, false
	}
	// constructionAt extracts (targetName, constructionNode) from a
	// `var c = T{…}` / `c = (…)` / `c = Variant(…)` (or the assign forms) whose
	// RHS is a plain (non-update) StructLit, a TupleLit, or a payload-carrying
	// enum variant constructor call. The node keys reuseSources.
	constructionAt := func(st ast.Stmt) (string, ast.Expr) {
		rhsConstruction := func(e ast.Expr) ast.Expr {
			switch v := e.(type) {
			case *ast.StructLit:
				if v.Base == nil {
					return v
				}
			case *ast.TupleLit:
				return v
			case *ast.Call:
				// A payload-carrying enum variant constructor (`Wrap(x)`).
				// Payloadless variants lower to a shared sentinel (no box to
				// reuse), and a shadowing local rules out a constructor ref.
				if callee, ok := v.Callee.(*ast.Ident); ok {
					if _, isLocal := b.locals[callee.Name]; !isLocal {
						if _, _, pc, isVar := b.lookupVariant(callee.Name); isVar && pc > 0 {
							return v
						}
					}
				}
			}
			return nil
		}
		switch s := st.(type) {
		case *ast.Var:
			if c := rhsConstruction(s.Init); c != nil {
				return s.Name, c
			}
		case *ast.ExprStmt:
			if a, ok := s.Expr.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok {
					if c := rhsConstruction(a.Value); c != nil {
						return id.Name, c
					}
				}
			}
		}
		return "", nil
	}

	// attemptPair tries to pair construction C (cName / cNode) with a dead,
	// owned source D drawn from `declIdx` (name → declaration index in some
	// statement list), where D must be declared before `k` and dead from `k`
	// onward per `deadFrom`. Used by BOTH the same-block pass (declIdx/k/deadFrom
	// scoped to C's own block) and the cross-block pass (scoped to the function
	// body, with k the top-level statement that ENCLOSES a nested C). Records the
	// pairing in `sources`/`consumed` and returns true on success.
	attemptPair := func(cName string, cNode ast.Expr, declIdx map[string]int, k int, deadFrom func(string, int) bool) bool {
		cKind, cTypeName, cClass, ok := reuseClassOf(cName)
		if !ok {
			return false
		}
		switch cn := cNode.(type) {
		case *ast.StructLit:
			if cKind != "struct" || cTypeName != cn.TypeName {
				return false
			}
		case *ast.TupleLit:
			if cKind != "tuple" {
				return false
			}
		case *ast.Call:
			if cKind != "enum" {
				return false
			}
		}
		// Choose deterministically (smallest decl index, tie-broken by name):
		// Go map iteration is per-process randomised, so picking the "first"
		// eligible D would make codegen non-reproducible — fatal for the
		// byte-equal self-host gate when two D's qualify for one C.
		bestD, bestDi := "", -1
		for dName, di := range declIdx {
			if di >= k || dName == cName || consumed[dName] || reassigned[dName] {
				continue
			}
			// A D whose box was MOVED into another live container (an array /
			// struct / tuple / closure element, per markConstructionMoves) is
			// freeEligible but no longer owns its box — that box is now reachable
			// through the container. Reusing it in place for C would alias C's
			// fresh value onto the element the container still points at
			// (observed: `var a=[d]; var c=T{…}` made a[0] read as c). The exit
			// sweep already excludes movedLocals (computePreciseDrops); the reuse
			// pass must too.
			if b.rc.movedLocals[dName] {
				continue
			}
			// A borrow source must stay alive to the exit sweep (a live
			// borrowed view reads through it — #4402 opt 1); a borrowed
			// alias owns nothing to donate.
			if b.rc.borrowSources[dName] || b.rc.borrowedAlias[dName] {
				continue
			}
			if !b.rc.freeEligible[dName] || !b.localNameUnique(dName) {
				continue
			}
			dKind, dTypeName, dClass, ok := reuseClassOf(dName)
			if !ok || dKind != cKind {
				continue
			}
			// Same NAMED struct/enum pairs at any size (D's box is reused as
			// itself). Otherwise (a different struct type, or a tuple) pair
			// only when D and C fall in the SAME freelist class — C's box
			// fits D's reused block and __alloc_reuse's runtime class check
			// matches. D's old fields are released and C's stored using each
			// one's OWN layout (see the hooks). Enums require the same type
			// (their old-payload free walks D's uniform drop loads; pairing a
			// different enum of equal class is left to a later cut).
			sameNamed := (cKind == "struct" || cKind == "enum") && dTypeName == cTypeName
			if cKind == "enum" && !sameNamed {
				continue
			}
			if !sameNamed && dClass != cClass {
				continue
			}
			if !deadFrom(dName, k) {
				continue
			}
			if bestD == "" || di < bestDi || (di == bestDi && dName < bestD) {
				bestD, bestDi = dName, di
			}
		}
		if bestD != "" {
			sources[cNode] = bestD
			consumed[bestD] = true
			return true
		}
		return false
	}

	// declIndices / deadFromIn build the per-statement-list machinery shared by
	// both passes: declIdx maps a top-level `var` name to its index, and
	// deadFrom reports whether a name is referenced in NO statement at index
	// >= k of that list.
	declIndices := func(stmts []ast.Stmt) map[string]int {
		declIdx := map[string]int{}
		for i, st := range stmts {
			if v, ok := st.(*ast.Var); ok {
				if _, dup := declIdx[v.Name]; !dup {
					declIdx[v.Name] = i
				}
			}
		}
		return declIdx
	}
	deadFromIn := func(stmts []ast.Stmt) func(string, int) bool {
		return func(name string, k int) bool {
			for i := k; i < len(stmts); i++ {
				if stmtReferencesName(stmts[i], name) {
					return false
				}
			}
			return true
		}
	}

	// hooks packages the shared closures for the drop-guided strategy
	// (rc_dropguided.go, ast.RcReuseDropGuided — plan E3). Both strategies
	// propose pairs exclusively through attemptPair, so every gate and all
	// bookkeeping is common; only the selection scan differs.
	hooks := reusePairingHooks{
		attemptPair:    attemptPair,
		constructionAt: constructionAt,
		declIndices:    declIndices,
		deadFromIn:     deadFromIn,
		reuseClassOk: func(name string) bool {
			_, _, _, ok := reuseClassOf(name)
			return ok
		},
		sources: sources,
	}

	// SAME-BLOCK pass: every block in the function — the body and each nested
	// loop / if arm — is its own statement list with block-scoped locals. A
	// construction C pairs with a D declared earlier in (and dead from C onward
	// within) the SAME list. This is the high-value case (loop-body churn).
	// Under the drop-guided flag the same lists are scanned token-major
	// instead (drop-order FIFO claiming — dropGuidedSameList).
	if ast.RcReuseDropGuided {
		b.dropGuidedSameList(hooks)
	} else {
		ast.Walk(b.fn.Body, func(n ast.Node) bool {
			if blk, ok := n.(*ast.Block); ok {
				declIdx := declIndices(blk.Stmts)
				deadFrom := deadFromIn(blk.Stmts)
				for k, st := range blk.Stmts {
					if cName, cNode := constructionAt(st); cNode != nil {
						attemptPair(cName, cNode, declIdx, k, deadFrom)
					}
				}
			}
			return true
		})
	}

	// CROSS-BLOCK pass: a block-top-level local D dominates and outlives a
	// construction C NESTED inside a LATER top-level statement of that same
	// block (an if / loop / nested block). D pairs with C when D is dead from
	// that enclosing top-level statement onward across the WHOLE block —
	// deadFrom over the block's stmts rejects any use of D after k on any path
	// (a sibling branch, the rest of C's block, or a post-merge use), so
	// reusing D's box on the C-path and zeroing its slot can never strand a
	// live read; the not-taken path leaves D's slot intact for the exit sweep /
	// the next-iteration reinit drop. The args-alias hazard is excluded
	// structurally by freeEligible[D] (a D whose field/element aliases a live
	// local is tainted out) — and arrays are never reuse sources.
	//
	// This generalises the function-body case to EVERY block, so the dominant
	// shape — a loop-body D (`var a = …`) reused by a construction nested in an
	// `if` inside the loop — fires every iteration (D is block-scoped, so it's
	// re-declared and the slot reinit-dropped each turn). Only C's not already
	// paired (same-block, or a CLOSER cross-block ancestor) are considered:
	// blocks are visited descendant-before-ancestor (reversed pre-order), so the
	// innermost eligible D — the most natural, per-iteration reuse — wins.
	var blocks []*ast.Block
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if blk, ok := n.(*ast.Block); ok {
			blocks = append(blocks, blk)
		}
		return true
	})
	for i := len(blocks) - 1; i >= 0; i-- {
		blkStmts := blocks[i].Stmts
		declIdx := declIndices(blkStmts)
		deadFrom := deadFromIn(blkStmts)
		for k, st := range blkStmts {
			ast.Walk(st, func(n ast.Node) bool {
				inner, ok := n.(ast.Stmt)
				if !ok || ast.Node(inner) == ast.Node(st) {
					return true // skip the enclosing top-level statement itself
				}
				cName, cNode := constructionAt(inner)
				if cNode == nil {
					return true
				}
				if _, done := sources[cNode]; done {
					return true // already paired (same-block, or a closer ancestor)
				}
				attemptPair(cName, cNode, declIdx, k, deadFrom)
				return true
			})
		}
	}

	// DROP-GUIDED arm pass (flag only): a token born at a drop point INSIDE
	// a dominated, non-loop arm flows forward to a later construction in the
	// same arm — the shape neither pass above can see (D declared in the
	// parent list, still referenced inside the enclosing statement). See
	// dropGuidedArmPass for the soundness argument and gates.
	if ast.RcReuseDropGuided {
		b.dropGuidedArmPass(hooks)
	}
	return sources, consumed
}

// computePreciseDrops implements the Perceus "garbage-free" precise-drop
// placement for the STRAIGHT-LINE subset: an owned, free-eligible rc local
// (array / struct / Map / enum / tuple) whose every reference is a top-level
// statement (none inside a nested if / while / for / match block) and that
// isn't moved or reassigned is dropped right AFTER its last top-level use,
// instead of waiting for the function-exit sweep. Freeing the value at its
// last use rather than at scope end lowers peak memory — a later same-shaped
// allocation reuses the freed block instead of bumping a new one (measured:
// two sequentially-dead 2 KiB arrays go 4128 → ~2064 B high-water on wasm,
// four go 8256 → ~2064).
//
// Soundness: the drop is the SAME deep-drop the exit sweep emits, followed by
// ZEROING the slot. Zeroing makes it control-flow-robust and fail-loud:
//   - the function-exit sweep (and any earlier `return`'s sweep) loads the
//     zeroed slot and null-guards to a no-op, so there's no double-drop on
//     any path — a `return` BEFORE the precise point still drops the live
//     value via that sweep, a `return` after sees the zeroed slot.
//   - correctness never depends on the drop being the TRUE last use: it's a
//     dec, freeing only at rc 0, so a value aliased (inc'd) into a container
//     survives; and a mis-analysis surfaces as a null-slot read (trap / wrong
//     value caught by the differential corpus), not a silent UAF.
//
// Conservative gates (single declaration, no reassignment, no nested-block
// use) keep slice 1 obviously sound; control-flow-aware placement inside
// branches is a later slice. Returns stmtIndex → names to drop after lowering
// that top-level statement.
func (b *builder) computePreciseDrops() map[int][]string {
	if !ast.RcFreeEnabled || b.fn.Body == nil {
		return nil
	}
	stmts := b.fn.Body.Stmts
	declIdx := map[string]int{}
	reassigned := map[string]bool{}
	for i, st := range stmts {
		switch s := st.(type) {
		case *ast.Var:
			if _, dup := declIdx[s.Name]; dup {
				reassigned[s.Name] = true // shadowed redeclaration — bail
			} else {
				declIdx[s.Name] = i
			}
		case *ast.ExprStmt:
			if a, ok := s.Expr.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok {
					reassigned[id.Name] = true
				}
			}
		}
	}
	// A precise drop now allows the last use to sit inside a nested block
	// (control-flow-aware placement — slice 5), so reassignment must be
	// detected at ANY depth: a `name = ...` inside an `if`/`while` rebinds the
	// slot, and precise-dropping the post-loop value at its last use is only
	// sound if the slot wasn't re-overwritten on some path in a way the
	// straight-line `last` index can't see. Conservatively bail on any
	// assignment to the local anywhere in the body.
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		}
		return true
	})
	out := map[int][]string{}
	for name, di := range declIdx {
		if reassigned[name] || b.rc.movedLocals[name] || !b.rc.freeEligible[name] || !b.localNameUnique(name) {
			continue
		}
		// #4402 opt 1: a borrow source releases ONLY at the exit sweep (a
		// live borrowed view reads through its buffer until then); a
		// borrowed alias is a view and is never dropped at all.
		if b.rc.borrowSources[name] || b.rc.borrowedAlias[name] {
			continue
		}
		// A local whose box is handed off to a general-FBIP reuse site
		// (computeReuseSources) is already consumed there (its box taken, or
		// dec'd on the shared path, and its slot zeroed) — dropping it again
		// here would double-release. The reuse site subsumes its drop.
		if b.rc.reuseConsumed[name] {
			continue
		}
		if !b.preciseDroppableType(name) {
			continue
		}
		// The local's INIT may itself produce an uncounted alias of a still-
		// live value — a slice (view), a pointer-typed if/match expr, or a
		// call whose result could BE a pointer argument (`var v3 = id(v2)`).
		// Precise-dropping such a local would free a buffer the source still
		// holds. A scalar-arg call (`fill(100)`) returns a fresh value and
		// stays eligible (the common builder-call win). The OTHER end — the
		// source flowing INTO the call — is handled by flowsIntoUncountedAlias
		// below.
		if v, ok := stmts[di].(*ast.Var); ok {
			if b.initMayAliasLive(v.Init) {
				continue
			}
			// Slice 1 targets dead OWNED values (fresh literals / scalar-arg
			// builder calls) — the clear peak-memory win. A local whose init
			// is a counted ALIAS (`var y = x` / `x.field` / `x[i]` —
			// needsRcIncOnAlias) is excluded: precise-dropping it only cancels
			// the alias inc (sound, but a marginal win that needlessly churns
			// the rc-count golden tests). Dead-alias cancellation is a later
			// slice.
			if needsRcIncOnAlias(v.Init, b) {
				continue
			}
		}
		unsafe := false
		last := -1
		for i := di + 1; i < len(stmts); i++ {
			if !stmtReferencesName(stmts[i], name) {
				continue
			}
			// Control-flow-aware placement (slice 5): the last use may now sit
			// INSIDE a nested if / while / for / match. We still drop the local
			// right after the whole top-level statement that contains its last
			// use — by then the local is dead on EVERY path through that
			// statement, so a single top-level drop + zero-slot is sound, and
			// any early `return` on a path keeps the value live to its own exit
			// sweep (the zeroed slot makes the post-statement drop a no-op on
			// the paths that already returned). Slightly less precise than a
			// per-branch drop, but it reclaims before the (often long) tail
			// after an `if`, which is where the win is.
			//
			// A reference inside a pointer-producing call / slice / if-expr /
			// match-expr can create an UNCOUNTED alias of `name` that outlives
			// the drop point (e.g. `var v3 = id(v2)` — a generic identity
			// returns its borrowed arg with no inc). The inc'd-alias sites
			// (`var y = x` / `x.field` / `x[i]`, container literals) are SAFE —
			// the precise drop only decs there. Bail on the uncounted-alias
			// shapes (flowsIntoUncountedAlias walks the whole nested statement).
			if b.flowsIntoUncountedAlias(stmts[i], name) {
				unsafe = true
				break
			}
			last = i
		}
		if unsafe {
			continue
		}
		if last < 0 {
			// Declared but never used after — drop right after the decl
			// (a dead owned alloc reclaims immediately).
			last = di
		}
		// Control-flow placement guard: when the last use sits INSIDE a
		// nested control-flow statement (if / while / for / match / block),
		// the precise drop fires after that whole top-level statement —
		// the slice-5 extension over the straight-line slices 1-3, which
		// only placed drops after a simple top-level use. That extension is
		// only enabled for PRIMITIVE-element arrays (i32[] / f64[] / …): a
		// dead `int[]` freed early is the clean peak-memory win (the
		// headline two-KiB-array case) with no per-element rc to balance.
		//
		// A pointer-element array (string[] / struct[] / T[][] / tuple[])
		// is EXCLUDED from this nested placement: its deep drop dec's each
		// element, and an element aliased out across the drop point (e.g.
		// the self-host driver's `entry_path = av[1]` / `root = av[2]` from
		// `var av: string[] = args()`, last-used at `av[2]` inside an `if`)
		// relies on the per-element retain/release balancing exactly on
		// EVERY backend. On arm64 two-word heap strings that balance rides
		// the native heap-string reclamation path the plan still defers
		// (slice 5g, "arm64 native heap-string rc — verify on hardware"),
		// so an early drop there corrupts under allocation-reuse pressure
		// (the args buffer reclaimed and reused while a still-live element
		// alias points into it). Falling back to the exit sweep for these
		// keeps the nested-use win for primitive arrays without crossing
		// that unverified arm64 boundary. Straight-line (simple top-level
		// last use) placement keeps the full slice 1-3 element scope.
		if b.isControlFlowStmt(stmts[last]) && !b.safeForControlFlowDrop(name) {
			continue
		}
		// A `return` whose value is this local is handled by the Return
		// lowering's own move-on-return / sweep; dropping after it is dead
		// code. Skip — the value reclaims at the return instead.
		if _, isRet := stmts[last].(*ast.Return); isRet {
			continue
		}
		out[last] = append(out[last], name)
	}
	return out
}

// isControlFlowStmt reports whether `st` is a control-flow statement whose
// body holds a nested block — an if / while / for / match / bare block. A
// reference to a local inside one of these is a "nested" use, so a precise
// drop placed after the whole statement is the slice-5 control-flow
// extension (vs a simple top-level Var / ExprStmt / Return use).
func (b *builder) isControlFlowStmt(st ast.Node) bool {
	switch st.(type) {
	case *ast.If, *ast.While, *ast.Loop, *ast.For, *ast.Match, *ast.LetElse, *ast.Block:
		return true
	}
	return false
}

// safeForControlFlowDrop reports whether `name`'s declared type may take the
// slice-5 control-flow precise-drop placement (a drop after the whole top-level
// if/while/for/match that holds its last use, vs the function-exit sweep).
// Allowed for:
//   - PRIMITIVE-element arrays (i32[] / f64[] / …) — the original slice-5 scope;
//     no per-element rc to balance across the early drop.
//   - enum / struct / tuple values whose deep-drop touches NO string or array
//     buffer (typeIsStringArrayFree) — the FBIP list/tree-of-scalars case. Their
//     generated `__drop_*` helper is is_unique-gated and verified on every
//     backend, and being string/array-free keeps the deferred arm64 two-word
//     heap-string reclamation path (slice 5g) out of the early-drop window —
//     exactly the hazard that excludes pointer-element arrays and strings.
//
// Everything else (pointer-element arrays, strings, anything transitively
// containing them, Map, generics) falls back to the exit sweep. Unknown ⇒ false.
func (b *builder) safeForControlFlowDrop(name string) bool {
	t, ok := b.localDeclType(name)
	if !ok {
		return false
	}
	switch ty := t.(type) {
	case ast.ArrayType:
		return !ast.IsPointerType(ty.Elem)
	case ast.EnumType, ast.StructType, ast.TupleType:
		return b.typeIsStringArrayFree(t, map[string]bool{})
	}
	return false
}

// flowsIntoUncountedAlias reports whether `name` appears inside an
// expression that produces an UNCOUNTED pointer alias of it within `st`: a
// pointer-returning call (the result may BE the arg, e.g. `id(x)` / a
// borrowed-param-returning function, with no inc at the binding), a slice
// (always a view into its source), or a pointer-typed if/match expression
// (whose value position aliases a branch operand without an inc). References
// via needsRcIncOnAlias shapes (bare ident / field / index) and container
// literals are NOT flagged — those inc the value, so a precise drop only
// decs and the alias survives. Used to gate precise-drop placement.
func (b *builder) flowsIntoUncountedAlias(st ast.Node, name string) bool {
	hasName := func(n ast.Node) bool { return stmtReferencesName(n, name) }
	bad := false
	ast.Walk(st, func(n ast.Node) bool {
		if bad {
			return false
		}
		switch e := n.(type) {
		case *ast.SliceExpr:
			if hasName(e) {
				bad = true
			}
		case *ast.Call:
			if b.mayAliasResult(e) && hasName(e) {
				bad = true
			}
		case *ast.IfExpr:
			if b.mayAliasResult(e) && hasName(e) {
				bad = true
			}
		case *ast.MatchExpr:
			if b.mayAliasResult(e) && hasName(e) {
				bad = true
			}
		}
		return !bad
	})
	return bad
}

// mayAliasResult reports whether expression `e`'s result may be a heap pointer
// that aliases one of its operands — conservatively treating an UNRESOLVED
// generic result (a `ParamType`, e.g. `id[T]`'s `T`) or an unknown type as
// pointer-shaped, since `b.exprType` doesn't instantiate generic call results.
// Concrete scalar results (i32 / bool / float) are not aliasing.
func (b *builder) mayAliasResult(e ast.Expr) bool {
	t := b.exprType(e)
	if t == nil {
		return true
	}
	if _, isParam := t.(ast.ParamType); isParam {
		return true
	}
	return ast.IsPointerType(t)
}

// initMayAliasLive reports whether a Var initialiser may bind an UNCOUNTED
// pointer alias of a value that outlives it: a slice (a view into its
// source), a pointer-typed if / match expression (aliases a branch operand),
// or a pointer-returning call with at least one pointer-typed argument /
// receiver (the result may be that argument — `id(v2)` returns its arg). A
// call with only scalar args (`fill(100)` / `make(n)`) returns a fresh value
// that can't alias a live pointer local, so it stays precise-droppable.
func (b *builder) initMayAliasLive(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SliceExpr:
		return true
	case *ast.IfExpr:
		return ast.IsPointerType(b.exprType(x))
	case *ast.MatchExpr:
		return ast.IsPointerType(b.exprType(x))
	case *ast.Call:
		// The local is droppable (pointer-shaped), so the call's result is
		// pointer regardless of what b.exprType reports for a generic. The
		// alias risk is a pointer-shaped ARGUMENT / receiver the result could
		// be (`id(v2)` returns its arg); a scalar-only-arg call (`fill(100)`)
		// returns a fresh value.
		for _, a := range x.Args {
			if b.mayAliasResult(a) {
				return true
			}
		}
		if fa, ok := x.Callee.(*ast.FieldAccess); ok && b.mayAliasResult(fa.Target) {
			return true
		}
		return false
	}
	return false
}

// preciseDroppableType reports whether `name`'s declared type is in the
// precise-drop scope: any owned ARRAY. emitOwnedSlotDrop reclaims every
// element kind fully — primitive via `__fern_arr_dec` (pure buffer free,
// slice 1); rc-tracked (`struct[]` / `enum[]` / `T[][]` / `tuple[]`) via the
// deep `__drop_arr_*` loop (slice 2); and `string[]` via `__fern_drop_arr_str`
// / `__fern_drop_arr_ptr` (slice 3 — str_dec each element, then the buffer).
// Each per-element drop is_unique-gates, so a counted alias of an element only
// DECs. Non-array box types (structs / enums / tuples — small boxes whose deep
// drops dec shared fields and churn the `__rc_get` golden tests) are deferred.
func (b *builder) preciseDroppableType(name string) bool {
	t, ok := b.localDeclType(name)
	if !ok {
		return false
	}
	if et, isEnum := t.(ast.EnumType); isEnum {
		// ENUMs are precise-droppable only under Slice 1b (rc-eligible enums):
		// once enum construction rc-counts its pointer payloads (like StructLit)
		// the deep drop is rc-protected exactly like a struct, and the
		// escape-taint that kept enum locals ineligible is lifted in tandem.
		// Under the default move model (or for Map-containing enums) they stay
		// excluded (payloads carry no counted box reference).
		return b.enumRcPayloadsEligible(et.Name)
	}
	switch tt := t.(type) {
	case ast.ArrayType:
		// `dyn Trait[]` is NEVER precise-droppable (#4787): an element read
		// (`xs[i]`, the for-in loop var) is an UNCOUNTED borrow — dyn cells
		// carry no rc header, so needsRcIncOnAlias deliberately has no dyn
		// arm and the element-view binding takes no retain. The dyn-array
		// drop (__drop_arr_dyn_<set>) frees every cell + runs the concrete
		// dtor UNCONDITIONALLY, so an early drop at xs's last syntactic use
		// frees the cell a live element view still points at (segfault on
		// the natives / garbage dispatch). The "x[i] alias sites are SAFE —
		// the precise drop only decs there" argument in computePreciseDrops
		// holds only for rc-headered elements. Falls back to the exit
		// sweep, which runs after every read.
		if _, elemDyn := tt.Elem.(ast.DynTraitType); elemDyn {
			return false
		}
		// Arrays of every other element kind (slices 1–3): emitOwnedSlotDrop
		// reclaims fully and is_unique-gates; freeEligible (the taint set)
		// excludes any value whose nested fields/payloads alias a live
		// local; and the init/use alias gates exclude boxes bound from /
		// flowing into an uncounted alias.
		return true
	case ast.StructType, ast.TupleType:
		// STRUCT / Map / tuple boxes (slice 4). Struct & tuple construction
		// INC their pointer fields/elements (StructLit / TupleLit), so a
		// precise drop is rc-protected — the same reason slice-2 rc-element
		// arrays are sound. Non-droppable runtime handles (Reader / Writer /
		// MapIter) aren't freeEligible, so they never reach here.
		return true
	}
	return false
}

// isOwnedRcLocal reports whether `name` is a declared rc-tracked local
// (array / struct incl. Map / enum / closure) that the exit sweep would
// dec. Params are borrowed (not in info.Locals, never swept) so they're
// excluded. FuncType (closure) locals now free their env/pair block at
// the last reference (__fern_closure_drop), so they participate in
// move-on-return / move-on-alias like the other owned types — the
// transfer and the sweep-dec genuinely cancel.
func (b *builder) isOwnedRcLocal(name string) bool {
	for _, v := range b.info.Locals[b.fn] {
		if v.Name != name {
			continue
		}
		switch v.Type.(type) {
		case ast.ArrayType, ast.StructType, ast.EnumType, *ast.FuncType, ast.TupleType:
			return true
		case ast.StringType:
			// Two-word string ABIs (wasm + arm64-TwoWordOverride) and
			// native single-word (x86_64) all participate in move-on-
			// return / move-on-alias now that the rc-tracked predicate is
			// uniform for strings: a returned string local cancels its
			// transfer-inc against the exit-sweep dec (no free under the
			// caller). The arm64 unblock landed __fern_str_inc / dec, so
			// the boxed-string case applies too.
			return true
		}
		return false
	}
	return false
}

// localIsDynTrait reports whether `name` is a declared local of `dyn Trait`
// type. The exit sweep drops those through __drop_dyn_<set> when the backend
// reclaims dyn (dynReclaim), so a returned one must be excluded from the
// sweep (move-on-return in the Return lowering) or the caller receives a
// freed cell. Deliberately NOT folded into isOwnedRcLocal: dyn values carry
// no rc header, so they must never take the __fern_rc_inc/dec traffic the
// isOwnedRcLocal/needsRcIncOnAlias pairing implies.
func (b *builder) localIsDynTrait(name string) bool {
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			_, isDyn := v.Type.(ast.DynTraitType)
			return isDyn
		}
	}
	return false
}

func needsRcIncOnAlias(e ast.Expr, b *builder) bool {
	switch e.(type) {
	case *ast.Ident, *ast.FieldAccess, *ast.Index:
		// continue
	default:
		return false
	}
	t := b.exprType(e)
	if _, isArr := t.(ast.ArrayType); isArr {
		return true
	}
	// Phase 1e-struct-ii: user-declared struct values now carry
	// rc headers (either real rc=1 from StructLit lowering or
	// the static-sentinel 0x80000000 from runtime helpers like
	// __fern_make_handle / map_new_impl / __map_iter_impl).
	// Either way, calling __fern_rc_inc/dec on a struct pointer
	// is safe — the inc/dec helpers short-circuit on the high
	// bit so runtime-owned values stay shareable, and user-
	// allocated values pick up real rc tracking.
	if _, isStruct := t.(ast.StructType); isStruct {
		return true
	}
	// Phase 1e-enums-ii: aliasing an enum-typed ident / field /
	// index inc's the box. The value is always a heap pointer
	// (headered box) or a header-carrying static sentinel, so
	// __fern_rc_inc short-circuits on the sentinel and bumps a
	// real rc on a user box.
	if _, isEnum := t.(ast.EnumType); isEnum {
		return true
	}
	// Phase 1e-closures-ii: aliasing a FuncType (closure) value
	// inc's its rc=1 heap header; static cells short-circuit
	// (sentinel on natives, low-address guard on wasm).
	if _, isFunc := t.(*ast.FuncType); isFunc {
		return true
	}
	// Tuple reclamation: aliasing a tuple-typed ident / field / index
	// inc's its box (rc=1 header from TupleLit lowering). Balances the
	// box dec the exit sweep emits for tuple locals, and — critically —
	// keeps the box alive when a tuple flows into a container that
	// outlives the source local (no inc would let the source's box_free
	// strand the container's reference).
	if _, isTuple := t.(ast.TupleType); isTuple {
		return true
	}
	// wasm two-word strings: aliasing inc's the heap buffer's rc via
	// __fern_str_inc (emitAliasInc picks the two-word helper). Lets two
	// eligible string locals share a buffer safely (the dec's is_unique
	// gate frees once) and protects a string flowing into a container
	// that outlives the source. Native single-word strings (x86_64,
	// !TwoWordOverride) inc via __fern_rc_inc (emitAliasInc fall-
	// through). SSO inline-tag low-bit guard in __fern_rc_inc (added
	// during Slice 8) keeps short inline strings safe. arm64
	// (TwoWordOverride boxed) excluded — no native str_inc / str_dec
	// runtime, same gating as the rest of the native string-reclaim
	// path.
	if _, isStr := t.(ast.StringType); isStr {
		// arm64 now has __fern_str_inc, so the wasm two-word retain path
		// applies there too. All non-zero ptrW with strings is alias-retained.
		return true
	}
	return false
}

// computeConsumingMatchReuse decides the C2 consuming-match reuse pairings up
// front (#4475): for every enum match on an `own` (consuming) parameter, an
// unguarded non-wildcard arm whose body is exactly `return Ctor(..)`
// constructing a payloadful variant of the SAME (uniform-box-sized) enum hands
// the consumed scrutinee box straight to that construction via the reuse token
// instead of freeing it (true zero-alloc FBIP). This used to be registered
// mid-lowering by the match arm's reuseCtor hook; deciding it here keeps the
// rcPlan immutable during lowering.
//
// The gates reproduce the lowering-time hook exactly: `ownParamEnumScrutinee`
// requires the tag to be an *ast.Ident naming an `own` enum param (an Ident
// tag is never pair-form, so the hook's !pairFormScrutinee is implied), the
// hook only ran for unguarded non-wildcard arms, and a ctor node already
// paired by computeReuseSources keeps its general-FBIP donor (the hook's
// `already` check — such an arm falls back to the C1 box free). Ordering vs.
// the arm body is preserved trivially: registration now precedes ALL
// lowering, and emitEnumNew reads the same tables by node identity.
func (b *builder) computeConsumingMatchReuse() map[*ast.Call]bool {
	out := map[*ast.Call]bool{}
	if !ast.RcReuseEnabled || b.fn.Body == nil {
		return out
	}
	ast.Walk(b.fn.Body, func(node ast.Node) bool {
		m, ok := node.(*ast.Match)
		if !ok {
			return true
		}
		scrutIdent, scrutIsIdent := m.Tag.(*ast.Ident)
		if !scrutIsIdent {
			return true
		}
		consumeEnum, consumeScrut := b.ownParamEnumScrutinee(m.Tag)
		if !consumeScrut {
			return true
		}
		for _, arm := range m.Arms {
			if arm.IsWildcard || arm.Guard != nil {
				continue
			}
			reuseCtor := b.consumingReuseCtor(arm, consumeEnum)
			if reuseCtor == nil {
				continue
			}
			if _, already := b.rc.reuseSources[reuseCtor]; already {
				continue
			}
			b.rc.reuseSources[reuseCtor] = scrutIdent.Name
			out[reuseCtor] = true
		}
		return true
	})
	return out
}

// computeConsumingOwnedMatches finds the Koka-style consuming matches (#4400):
// a `match` statement whose scrutinee is a bare reference to an
// OWNED-BY-DEFAULT enum parameter at the name's LAST occurrence in the body.
// Such a match CONSUMES the scrutinee — the lowering releases the box per arm
// (dup the pointer bindings on the shared branch, shallow-free on the unique
// branch — emitOwnedConsumingArmDrop) instead of holding it to the exit sweep,
// and the extracted bindings become counted owners (consumingBindings). This is
// the counted-model sibling of the `own`-param consuming match: `own` moves its
// payloads (no rc traffic, E051-guarded), while an owned-by-default scrutinee
// keeps every reference counted, so the transform is the classic Perceus
// drop-specialization — dup children, drop box, cancel the pairs statically on
// the unique branch.
//
// Match gates (all conservative — a miss just keeps today's exit-sweep
// reclamation):
//   - the function has no defers (their exit paths re-route returned values
//     through synthetic slots; keep them out of scope);
//   - the scrutinee is an *ast.Ident naming a non-`own` param with
//     paramOwnedByDefault (which implies isOwnedByDefaultType: rc-eligible,
//     string/array-free, uniform-box enum) and no same-named declared local;
//   - the ident is the name's last occurrence (identOrder.isLast — dead after
//     the match and unused in arm bodies/guards), so zeroing the slot after the
//     per-arm release can't strand a later read;
//   - the match is not inside a loop (a loop re-executes the release on an
//     already-freed box).
//
// Binding gates (per NAME):
//   - every binding occurrence of the name in the whole function (Match /
//     MatchExpr / IfLet / LetElse arms) is in an unguarded non-wildcard arm of
//     a qualifying match;
//   - the name shadows no param, declared local, or ForEach loop var (those
//     share slots via b.locals — a double sweep would over-release);
//   - the binding type is a sweepable box (enum / user struct / tuple —
//     guaranteed pointer-shaped, single-word) and consistent across arms.
//
// The two admissions are mutually dependent: a match qualifies only when every
// pointer payload of every unguarded non-wildcard arm is an admissible NAMED
// binding (a `_` discard or an inadmissible name would strand that payload's
// transferred count — a per-call leak of the whole sub-tree where today's exit
// sweep reclaims it), and a name qualifies only against the surviving match
// set. Resolved by a small monotone fixpoint below.
func (b *builder) computeConsumingOwnedMatches() (map[*ast.Match]string, map[string]ast.Type) {
	matches := map[*ast.Match]string{}
	bindings := map[string]ast.Type{}
	if !ast.RcFreeEnabled || b.fn.Body == nil {
		return matches, bindings
	}
	declared := map[string]bool{}
	for _, p := range b.fn.Params {
		declared[p.Name] = true
	}
	for _, v := range b.info.Locals[b.fn] {
		declared[v.Name] = true
	}
	hasDefer := false
	inLoop := map[*ast.Match]bool{}
	// rebound[name] — the name is (re)introduced by SOME binding construct
	// (match / match-expr arm, if-let, let-else). Binding slots are resolved
	// BY NAME (bindingSlot reuses b.locals[name] — including a param's slot),
	// so a param whose name is ever rebound may hold a BORROWED payload at
	// its match, not the caller's transferred argument; consuming it would
	// free a value the true owner still holds. Any rebinding of a param name
	// disqualifies that param's matches outright.
	rebound := map[string]bool{}
	markRebound := func(names []string) {
		for _, nm := range names {
			if nm != "" && nm != "_" {
				rebound[nm] = true
			}
		}
	}
	markLoop := func(body ast.Node) {
		if body == nil {
			return
		}
		ast.Walk(body, func(n ast.Node) bool {
			if m, ok := n.(*ast.Match); ok {
				inLoop[m] = true
			}
			return true
		})
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Defer:
			hasDefer = true
		case *ast.While:
			markLoop(x.Body)
		case *ast.Loop:
			markLoop(x.Body)
		case *ast.For:
			markLoop(x.Body)
		case *ast.ForEach:
			declared[x.Var] = true
			markLoop(x.Body)
		case *ast.Match:
			for _, arm := range x.Arms {
				markRebound(arm.Bindings)
			}
		case *ast.MatchExpr:
			for _, arm := range x.Arms {
				markRebound(arm.Bindings)
			}
		case *ast.IfLet:
			markRebound(x.Bindings)
		case *ast.LetElse:
			markRebound(x.Bindings)
		}
		return true
	})
	if hasDefer {
		return matches, bindings
	}
	order := identOrderOf(b.fn.Body)
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		m, ok := n.(*ast.Match)
		if !ok || inLoop[m] {
			return true
		}
		id, ok := m.Tag.(*ast.Ident)
		if !ok || !order.isLast(id) {
			return true
		}
		pi := -1
		for i, p := range b.fn.Params {
			if p.Name == id.Name {
				pi = i
				break
			}
		}
		if pi < 0 || b.fn.Params[pi].Own || rebound[id.Name] {
			return true
		}
		// A declared local sharing the param's name would make the ident
		// resolution ambiguous here — skip (params are also in `declared`,
		// so compare against locals only).
		for _, v := range b.info.Locals[b.fn] {
			if v.Name == id.Name {
				return true
			}
		}
		if !b.paramOwnedByDefault(b.fn.Params[pi].Type, pi) {
			return true
		}
		et, ok := b.fn.Params[pi].Type.(ast.EnumType)
		if !ok || !b.enumRcPayloadsEligible(et.Name) {
			return true
		}
		ed, ok := b.info.Enums[et.Name]
		if !ok {
			return true
		}
		if len(et.Args) > 0 {
			ed = substituteEnumDecl(ed, et.Args)
		}
		if _, ok := uniformEnumBoxSize(ed, b.ptrW); !ok {
			return true
		}
		matches[m] = id.Name
		return true
	})
	if len(matches) == 0 {
		return matches, bindings
	}
	// Binding pass, to a fixpoint: a NAME is admissible only when every
	// binding occurrence in the function is in a qualifying arm (unguarded,
	// non-wildcard, of a still-candidate match), with a consistent sweepable
	// box type and no shadowed declaration; a MATCH stays a candidate only
	// when every pointer payload of every unguarded non-wildcard arm is an
	// admissible NAMED binding — a release with an untracked pointer payload
	// (a `_` discard, a shadowed name, an inconsistent type) would strand
	// that payload's count (a per-call leak of the whole sub-tree where
	// today's exit sweep reclaims it), so the whole match falls back instead.
	// Dropping a match demotes its arms to non-qualifying occurrences, which
	// can invalidate names shared with other matches — hence the fixpoint
	// (monotone: matches only leave, names only get worse; terminates).
	sweepable := func(t ast.Type) bool {
		switch tt := t.(type) {
		case ast.EnumType:
			return true
		case ast.StructType:
			_, isUser := b.info.Structs[tt.Name]
			return isUser
		case ast.TupleType:
			return true
		}
		return false
	}
	for {
		bad := map[string]bool{}
		cand := map[string]ast.Type{}
		disqualify := func(names []string) {
			for _, nm := range names {
				if nm != "" && nm != "_" {
					bad[nm] = true
				}
			}
		}
		admit := func(arm *ast.MatchArm) {
			for i, nm := range arm.Bindings {
				if nm == "" || nm == "_" {
					continue
				}
				var bt ast.Type
				if i < len(arm.BindingTypes) {
					bt = arm.BindingTypes[i]
				}
				if bt == nil || !ast.IsPointerType(bt) {
					// Scalars need no ownership tracking, but a same-named
					// pointer binding elsewhere would clash on the shared
					// slot — the name can never be a counted owner.
					bad[nm] = true
					continue
				}
				if declared[nm] || !sweepable(bt) {
					bad[nm] = true
					continue
				}
				if prev, seen := cand[nm]; seen && !ast.Equal(prev, bt) {
					bad[nm] = true
					continue
				}
				cand[nm] = bt
			}
		}
		ast.Walk(b.fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Match:
				_, consuming := matches[x]
				for _, arm := range x.Arms {
					if consuming && !arm.IsWildcard && arm.Guard == nil && arm.Literal == nil {
						admit(arm)
					} else {
						disqualify(arm.Bindings)
					}
				}
			case *ast.MatchExpr:
				for _, arm := range x.Arms {
					disqualify(arm.Bindings)
				}
			case *ast.IfLet:
				disqualify(x.Bindings)
			case *ast.LetElse:
				disqualify(x.Bindings)
			}
			return true
		})
		for nm := range bad {
			delete(cand, nm)
		}
		// Drop candidates with an untracked pointer payload in a releasing arm.
		removed := false
		for m := range matches {
			ok := true
			for _, arm := range m.Arms {
				if arm.IsWildcard || arm.Guard != nil || arm.Literal != nil {
					continue
				}
				for i, nm := range arm.Bindings {
					var bt ast.Type
					if i < len(arm.BindingTypes) {
						bt = arm.BindingTypes[i]
					}
					if bt == nil || !ast.IsPointerType(bt) {
						continue
					}
					if nm == "" || nm == "_" {
						ok = false
						break
					}
					if _, tracked := cand[nm]; !tracked {
						ok = false
						break
					}
				}
				if !ok {
					break
				}
			}
			if !ok {
				delete(matches, m)
				removed = true
			}
		}
		if !removed {
			// Keep only names actually bound by a surviving match — a name
			// whose every occurrence sat in dropped matches has no arm to
			// transfer it ownership, so sweeping it would over-release.
			used := map[string]bool{}
			for m := range matches {
				for _, arm := range m.Arms {
					if arm.IsWildcard || arm.Guard != nil || arm.Literal != nil {
						continue
					}
					for _, nm := range arm.Bindings {
						used[nm] = true
					}
				}
			}
			for nm, bt := range cand {
				if used[nm] {
					bindings[nm] = bt
				}
			}
			return matches, bindings
		}
	}
}

// stmtReferencesName reports whether any *ast.Ident named `name` appears
// anywhere in the subtree `st` — the shared occurrence predicate behind the
// last-use / deadness scans (#4480). Previously duplicated verbatim inside
// computeReuseSources, computePreciseDrops, and flowsIntoUncountedAlias.
func stmtReferencesName(st ast.Node, name string) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// identOrder is the shared ident-occurrence-order fact (#4480): every
// *ast.Ident in the function body numbered in pre-order (ast.Walk visit
// order), plus each name's highest occurrence number. `isLast` is the
// last-use test the move analyses hang off — an occurrence is the local's
// LAST when no later occurrence of the same name exists anywhere in the
// body. Previously built verbatim by both computeMovedLocals and
// computeArraySetIncs and threaded pairwise into markConstructionMoves.
// The statement-INDEX deadness scans (computePreciseDrops /
// computeReuseSources) are a different fact by design — top-level /
// per-block statement position, not ident occurrence — and stay separate.
type identOrder struct {
	idx  map[*ast.Ident]int
	last map[string]int
}

func identOrderOf(body ast.Node) identOrder {
	o := identOrder{idx: map[*ast.Ident]int{}, last: map[string]int{}}
	n := 0
	ast.Walk(body, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			n++
			o.idx[id] = n
			if n > o.last[id.Name] {
				o.last[id.Name] = n
			}
		}
		return true
	})
	return o
}

// isLast reports whether this occurrence is the highest-numbered occurrence
// of its name — the "last use anywhere in the body" test.
func (o identOrder) isLast(id *ast.Ident) bool {
	return o.idx[id] == o.last[id.Name]
}

// pushOnIdent returns the `__method_Array_push(ident, …)` call and its
// bare-ident receiver when `e` has exactly that shape, else (nil, nil).
// The receiver shape check shared by isSelfArrayPushLocal (which adds the
// borrowed-param exclusion its overwrite-free reclaim needs) and
// inPlacePushes (which deliberately does not — see below).
func pushOnIdent(e ast.Expr) (*ast.Call, *ast.Ident) {
	call, ok := e.(*ast.Call)
	if !ok {
		return nil, nil
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || callee.Name != "__method_Array_push" || len(call.Args) == 0 {
		return nil, nil
	}
	recv, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return nil, nil
	}
	return call, recv
}

// inPlacePushes returns the append calls in `body` that may stay on the
// rc-gated in-place grow even though their bare-ident operand is not its
// textually LAST occurrence (identOrder.isLast) — the two shapes where no
// LATER intra-function read can observe the in-place mutation, so the
// #4827 forced copy (emitArrayPush forceCopy) is pure waste:
//
//   - self-reassign: `x = x.append(v)`. The binding is rebound to the
//     append result in the same statement, so every later read of `x`
//     sees the appended value whether the grow mutated in place or
//     copied. selfPushMoveCall already exempts the local / `own`-param
//     form; this covers the BORROWED-param form isSelfArrayPushLocal
//     must reject (its overwrite-dec would free the caller's buffer) —
//     here no dec is added, only the forced copy is skipped, exactly
//     the pre-#4827 runtime behaviour.
//
//   - return position: `return … x.append(v) …` where `x` occurs exactly
//     once in the whole return expression (the operand itself) and is
//     never referenced under a `defer` or a lambda anywhere in the body.
//     After the return expression evaluates the function exits — only a
//     defer (or a closure that captured `x`) could still read `x`, and
//     those names are excluded. This is the accumulator-threading shape
//     (`return acc.append(id.name)`) the self-host compiler's AST
//     walkers use once per visited node; forcing a full copy there is
//     O(n²) bytes in the leak-mode arena (the #4838 CI OOM).
//
// Both shapes retain the rc-gate itself: a runtime rc > 1 operand still
// takes the copy path, exactly as before #4827.
func inPlacePushes(body ast.Node) map[*ast.Call]bool {
	ok := map[*ast.Call]bool{}
	// Names readable after the enclosing statement completes: anything
	// referenced under a defer action or inside a lambda body (a closure
	// can run later and read a captured binding). Conservative — any
	// occurrence of the name disqualifies its return-position pushes.
	esc := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		var sub ast.Node
		switch d := n.(type) {
		case *ast.Defer:
			sub = d.Expr
		case *ast.Lambda:
			sub = n
		default:
			return true
		}
		ast.Walk(sub, func(m ast.Node) bool {
			if id, isIdent := m.(*ast.Ident); isIdent {
				esc[id.Name] = true
			}
			return true
		})
		// Descend anyway: a Defer's own subtree holds no Return/Assign
		// exprs to mark, and marking inside a Lambda is skipped below,
		// but nested statements still need the esc scan above.
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.Lambda:
			// A push inside a lambda body executes when the closure is
			// CALLED, not here — never mark it from the enclosing fn.
			return false
		case *ast.Assign:
			if t, isIdent := st.Target.(*ast.Ident); isIdent {
				if call, recv := pushOnIdent(st.Value); call != nil && recv.Name == t.Name {
					ok[call] = true
				}
			}
		case *ast.Return:
			if st.Value == nil {
				return true
			}
			counts := map[string]int{}
			ast.Walk(st.Value, func(m ast.Node) bool {
				if id, isIdent := m.(*ast.Ident); isIdent {
					counts[id.Name]++
				}
				return true
			})
			ast.Walk(st.Value, func(m ast.Node) bool {
				if _, isLambda := m.(*ast.Lambda); isLambda {
					return false
				}
				if e, isExpr := m.(ast.Expr); isExpr {
					if call, recv := pushOnIdent(e); call != nil &&
						counts[recv.Name] == 1 && !esc[recv.Name] {
						ok[call] = true
					}
				}
				return true
			})
		}
		return true
	})
	return ok
}

// computeBorrowedAliases finds `var y = x` aliases that are pure BORROWED
// VIEWS (#4402 opt 1 — dead-alias dup/drop cancellation): y's transfer inc
// and exit-sweep dec are a guaranteed net-zero pair, so both are elided and
// x is pinned to exit-sweep-only release. Soundness gates (all required):
//
//   - x is an owned rc LOCAL (not a param — borrowed params are the
//     caller's), never reassigned anywhere, not moved (movedLocals covers
//     move-on-alias/return/destructure AND move-on-consume into `own`
//     params), and freeEligible (untainted, proven owner);
//   - y is rc-tracked, never reassigned, not itself moved, freeEligible
//     (untainted — no container escape), declared with the bare-Ident init
//     `var y = x` (its ONLY inc site), name-unique in the function (the
//     slot-sharing hazard), and never referenced under a Return (a returned
//     borrow would outlive x's sweep — conservative subtree check).
//
// x's release stays the exit dec sweep — after every statement, hence after
// every read through y — enforced by the borrowSources exclusions in
// computePreciseDrops and computeReuseSources' donor gate.
// computeBorrowedMapFieldResults finds locals bound to a Map COW-mutator call
// whose receiver is a field access (`var m = s.m.insert(k, v)`) — see the
// borrowedMapFieldResults field doc (issue #4871). Purely syntactic: the walk
// runs on the already-mangled AST (`insert` → `__method_Map_set`), the same
// form isMapMutatorCall matches at the construction site.
func (b *builder) computeBorrowedMapFieldResults() map[string]bool {
	out := map[string]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		v, ok := n.(*ast.Var)
		if !ok || v.Init == nil {
			return true
		}
		if mapMutatorReceiverIsFieldAccess(v.Init) {
			out[v.Name] = true
		}
		return true
	})
	return out
}

// mapMutatorReceiverIsFieldAccess reports whether e is a Map COW-mutator call
// (`__method_Map_set` / `_delete` / `_clear`) whose receiver argument (arg 0)
// is a field access — the "borrowed through a container" shape that makes the
// rc==1 in-place mutation alias the container's buffer (#4871).
func mapMutatorReceiverIsFieldAccess(e ast.Expr) bool {
	call, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "__method_Map_set", "__method_Map_delete", "__method_Map_clear":
	default:
		return false
	}
	if len(call.Args) == 0 {
		return false
	}
	_, isField := call.Args[0].(*ast.FieldAccess)
	return isField
}

func (b *builder) computeBorrowedAliases() {
	b.rc.borrowedAlias = map[string]bool{}
	b.rc.borrowedAliasSites = map[ast.Node]bool{}
	b.rc.borrowSources = map[string]bool{}
	reassigned := map[string]bool{}
	returned := map[string]bool{}
	scrutinee := map[string]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Assign:
			if id, ok := x.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		case *ast.Return:
			if x.Value != nil {
				ast.Walk(x.Value, func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok {
						returned[id.Name] = true
					}
					return true
				})
			}
		case *ast.Match:
			// A matched-on local can be reclaimed mid-function
			// (reclaimableMatchScrutinee frees the box after the match);
			// a borrow through it would dangle — exclude both roles.
			if id, ok := x.Tag.(*ast.Ident); ok {
				scrutinee[id.Name] = true
			}
		case *ast.MatchExpr:
			if id, ok := x.Tag.(*ast.Ident); ok {
				scrutinee[id.Name] = true
			}
		}
		return true
	})
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		v, ok := n.(*ast.Var)
		if !ok || v.Init == nil {
			return true
		}
		src, ok := v.Init.(*ast.Ident)
		if !ok {
			return true
		}
		y, x := v.Name, src.Name
		if y == x || b.rc.borrowedAlias[y] || b.rc.borrowSources[y] || b.rc.borrowedAlias[x] {
			return true
		}
		if !b.isOwnedRcLocal(x) || !b.isOwnedRcLocal(y) {
			return true
		}
		if reassigned[x] || reassigned[y] || returned[y] || scrutinee[x] || scrutinee[y] {
			return true
		}
		if b.rc.movedLocals[x] || b.rc.movedLocals[y] {
			return true
		}
		if !b.rc.freeEligible[x] || !b.rc.freeEligible[y] {
			return true
		}
		if !b.localNameUnique(x) || !b.localNameUnique(y) {
			return true
		}
		if !needsRcIncOnAlias(v.Init, b) {
			return true // untracked shape: no inc to cancel
		}
		b.rc.borrowedAlias[y] = true
		b.rc.borrowedAliasSites[v] = true
		b.rc.borrowSources[x] = true
		return true
	})
}

// -------- #4873: cross-function in-place array growth containment ---------
//
// The rc==1 in-place fast paths on array mutation (`__method_Array_push`'s
// grow, `__method_Array_set`'s cow_inplace, and the `arr[i] = v` desugar)
// are sound intra-function: the #4827 forced copy protects reused idents,
// and the #4838 exemptions (return-position / self-reassign appends) only
// skip it where no LATER INTRA-FUNCTION read can observe the mutation. But
// function parameters are borrowed (no inc at the call site), so the
// caller's binding aliases the same buffer at the same rc — and a callee
// mutation that is unobservable *inside* the callee is fully observable to
// a caller that keeps its argument live (`var c = grow(a, 3); a.len()`),
// silently diverging from the interpreter's copy-on-shared semantics
// (issue #4873; the struct form `b.xs.append(x)` reaches the same hole
// through the functional-update exemption).
//
// Containment is CALLER-side: computeGrowParams summarises, per function,
// which parameters' buffers the callee may grow in place (directly or
// transitively), and callBody brackets each call site whose corresponding
// argument SURVIVES the call with an rc-inc before / rc-dec after — the
// callee's uniqueness gate then sees rc >= 2 and takes the copy path, so
// the caller's buffer is never touched. An argument that dies at the call
// (the strict `x = f(.., x, ..)` self-reassign shape, callArgDeaths) skips
// the bracket, which keeps the #4838 O(n) accumulator chains
// (`acc = walk(acc, …)`) on the in-place fast path. The bracket is
// rc-balanced by construction: the grow/cow copy paths leave the operand's
// rc untouched (the same invariant the #4827 forced-copy inc/dec pair
// relies on), so inc→call→dec restores the incoming count and never frees.
//
// Residual (documented, out of scope here): an argument that aliases a
// LOCAL container field (`s.f = a; g(a)`) is an intra-function aliasing
// question the #4827 machinery also approximates; and `own`-param
// positions are already safe (an aliased arg is inc'd by the
// owned-by-default transfer, and explicit `own` requires the binding to
// die — E051).

// growParamKind bits for computeGrowParams entries.
const (
	growArgBuffer uint8 = 1 // the param IS an array whose buffer may grow in place
	growFieldBufs uint8 = 2 // the param is a struct whose array-field buffers may grow in place
)

// callArgDeaths marks, per call node, the ident arguments whose value can
// no longer be observed through that binding in this function after the
// call, so the #4873 bracket may skip them. Two shapes qualify:
//
//   - the strict self-reassign `x = f(.., x, ..)`: the RHS is exactly the
//     call and x occurs in it exactly once, directly as an argument — the
//     old binding is overwritten by the result (the #5056 move-and-rebind
//     shape, sans the `own` requirement);
//   - the return-position `return f(.., x, ..)` under the same
//     exactly-once rule: a return exits the function (loop or not), so no
//     later read exists. This is what keeps recursive accumulator tails
//     (`return walk(acc, …)`) on the in-place fast path — bracketing them
//     would force one copy per recursion level, the #4838 O(n²) class.
//
// A textually-last occurrence is deliberately NOT sufficient: inside a
// loop the "last" occurrence re-executes, and an unbracketed in-place
// growth would be observed by the next iteration (interp copies).
func callArgDeaths(body ast.Node) map[*ast.Call]map[string]bool {
	out := map[*ast.Call]map[string]bool{}
	markOnce := func(c *ast.Call, name string) {
		direct := 0
		for _, a := range c.Args {
			if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
				direct++
			}
		}
		total := 0
		ast.Walk(c, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				total++
			}
			return true
		})
		if direct == 1 && total == 1 {
			if out[c] == nil {
				out[c] = map[string]bool{}
			}
			out[c][name] = true
		}
	}
	ast.Walk(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.Assign:
			t, ok := st.Target.(*ast.Ident)
			if !ok {
				return true
			}
			c, ok := st.Value.(*ast.Call)
			if !ok {
				return true
			}
			markOnce(c, t.Name)
		case *ast.Return:
			c, ok := st.Value.(*ast.Call)
			if !ok {
				return true
			}
			for _, a := range c.Args {
				if aid, ok := a.(*ast.Ident); ok {
					markOnce(c, aid.Name)
				}
			}
		}
		return true
	})
	return out
}

// fieldChainRoot chases a field-access chain (`s.cur.insts`) to its root
// ident, or reports false for a non-ident-rooted chain.
func fieldChainRoot(fa *ast.FieldAccess) (*ast.Ident, bool) {
	e := fa.Target
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x, true
		case *ast.FieldAccess:
			e = x.Target
		default:
			return nil, false
		}
	}
}

// computeGrowParams returns, per function name, a per-parameter bitmask of
// growArgBuffer / growFieldBufs: the parameter positions whose argument
// buffer(s) the callee may grow or mutate in place through the rc==1 fast
// paths. Seeded from direct mutations (an array push/set whose receiver is
// a param ident or a field of a param ident, and an `arr[i] = v`
// index-assign on a param ident), then closed transitively over calls that
// pass a param onward in a position the CALLER-side bracket would not
// protect (the callArgDeaths self-reassign shape for ident args, and any
// param-field argument).
func computeGrowParams(prog *ast.Program, info *checker.Info) map[string][]uint8 {
	grow := map[string][]uint8{}
	decls := map[string]*ast.FuncDecl{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		decls[fn.Name] = fn
		grow[fn.Name] = make([]uint8, len(fn.Params))
	}
	paramIdx := func(fn *ast.FuncDecl, name string) int {
		for i, p := range fn.Params {
			if p.Name == name {
				return i
			}
		}
		return -1
	}
	isArrayMutator := func(name string) bool {
		return name == "__method_Array_push" || name == "__method_Array_set"
	}
	// Seed: direct in-place-capable mutations on params.
	for name, fn := range decls {
		g := grow[name]
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Call:
				id, ok := x.Callee.(*ast.Ident)
				if !ok || !isArrayMutator(id.Name) || len(x.Args) == 0 {
					return true
				}
				switch r := x.Args[0].(type) {
				case *ast.Ident:
					if pi := paramIdx(fn, r.Name); pi >= 0 {
						g[pi] |= growArgBuffer
					}
				case *ast.FieldAccess:
					// Chase a field CHAIN to its root ident so a nested
					// receiver (`s.cur.insts.append(x)`, the EmitState
					// functional-update shape) seeds too.
					if rid, ok := fieldChainRoot(r); ok {
						if pi := paramIdx(fn, rid.Name); pi >= 0 {
							g[pi] |= growFieldBufs
						}
					}
				}
			}
			return true
		})
	}
	// (`arr[i] = v` index-assign needs no seeding: the checker rejects
	// subscript assignment — `.with` is the API — and both `.with` and
	// `.set` carry their own receiver-live containment, #2832.)
	// Transitive closure over unbracketed pass-throughs.
	changed := true
	for changed {
		changed = false
		for name, fn := range decls {
			g := grow[name]
			deaths := callArgDeaths(fn.Body)
			ast.Walk(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.Call)
				if !ok {
					return true
				}
				cid, ok := c.Callee.(*ast.Ident)
				if !ok {
					return true
				}
				cg, ok := grow[cid.Name]
				if !ok {
					return true
				}
				for ai, a := range c.Args {
					if ai >= len(cg) || cg[ai] == 0 {
						continue
					}
					switch arg := a.(type) {
					case *ast.Ident:
						pi := paramIdx(fn, arg.Name)
						if pi < 0 {
							continue
						}
						// A surviving ident arg gets the caller-side bracket
						// (contained); only the dying self-reassign shape
						// passes the buffer through unprotected.
						if deaths[c][arg.Name] {
							if g[pi]|cg[ai] != g[pi] {
								g[pi] |= cg[ai]
								changed = true
							}
						}
					case *ast.FieldAccess:
						// `g(p.f)` — a param-field argument aliases the
						// param's field buffer and is never bracketed, so a
						// growable position propagates as a field growth of
						// the param (chain root for nested fields).
						if rid, ok := fieldChainRoot(arg); ok && cg[ai] != 0 {
							if pi := paramIdx(fn, rid.Name); pi >= 0 {
								if g[pi]|growFieldBufs != g[pi] {
									g[pi] |= growFieldBufs
									changed = true
								}
							}
						}
					}
				}
				return true
			})
		}
	}
	return grow
}
