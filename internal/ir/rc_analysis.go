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
	"sort"
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
	// consumedParams[name] is true for a pointer-shaped struct/tuple/enum/array
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
	// through such params, and without the promotion none can deep-drop the
	// old value.
	consumedParams map[string]bool
	// cowMapParams[name] is true for a Map param on the borrow baseline that
	// the body threads through `m = m.insert(..)` / `m = m.clear()`. Its slot
	// owns nothing on entry and a fresh copy after the first cow copy, so the
	// overwrite dec and the exit release are gated on a runtime ownership bit
	// (ownFlagName), as a consumed-threaded array param's are. Maps stay off
	// consumedParams (typeDeepDropWired), and without the bit the overwrite
	// dec on the copy path released the caller's handle.
	cowMapParams map[string]bool
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
	// matchBindingTypes caches matchBindingTypes()' walk.
	matchBindingTypes map[string]ast.Type
	// moveSites[stmt] is true for the specific *ast.Var / *ast.Assign
	// alias statement that is a move (skips its transfer inc). Keyed
	// per-site so only the local's LAST alias moves — earlier aliases
	// of the same local keep their inc.
	moveSites map[ast.Node]bool
	// aliasBindIncs records each *ast.Var binding whose init took the
	// transfer inc (the alias-retain the Var case of stmt() emits). Recorded
	// only while RcPlanHook is armed and read only by dumpRcPlan — the
	// self-host's bind-site retain plan is diffed against it (#4482).
	aliasBindIncs map[*ast.Var]bool
	// ownCallMoveArgs[argExpr] is true for the specific argument NODE that
	// move-on-call marked as the consuming transfer of an `own` param (the
	// occurrence computeMovedLocals proved to be the param's last use). Every
	// OTHER occurrence of that param in an `own` argument position is a
	// transfer the exit sweep does NOT pay for, so it needs a compensating
	// retain — see ownArgNeedsRetain.
	ownCallMoveArgs map[ast.Node]bool
	// returnOwnMove[ret] names the `own` param that THIS return statement
	// transfers onward, so the sweep it emits skips that one param while
	// every other return keeps its own. movedLocals cannot say this: it is
	// whole-function, so the textually-last occurrence is the only one it
	// can claim, and on a branchy function the transfers that are NOT last
	// pay a compensating retain instead — one full buffer copy per call
	// through the callee (#6125). Computed by computeReturnOwnMoves.
	returnOwnMove map[ast.Node]string
	// ctorAliasInced[name] records a local that received a CONSTRUCTION
	// alias-inc — its reference was retained into a container literal
	// (array / tuple / struct field / enum payload) while the local itself
	// stayed live, so both own the value. Recorded by noteCtorAliasInc at
	// the construction sites, i.e. exactly where the inc is emitted rather
	// than inferred from freeEligible's taint reasons.
	//
	// emitVarReinitDropOld consumes it: a loop-body local in this set is
	// ineligible for the deep free (the container shares the value) but DOES
	// hold a counted reference that must be released once per iteration.
	// Without that release the inc happens n times and the matching dec only
	// once, at the function-exit sweep — so n-1 values leak, linear and
	// unbounded (#5879 cause A). Keyed on the emitted inc, not on
	// !freeEligible, because ineligibility has several causes and only this
	// one comes with a reference to give back.
	ctorAliasInced map[string]bool
	// arraySetInc[call] is true for a `__method_Array_set` (`.with`) call
	// whose receiver is LIVE after the call (read again, and not a
	// reassign-to-self), so emitArraySet must rc-inc the receiver buffer
	// before __fern_arr_cow_inplace to force the copy path — otherwise the
	// rc==1 in-place reuse aliases/mutates the still-live receiver (#2832).
	// A field receiver computeFieldOwnMoves claims is cleared back to false:
	// the move out of the box replaces the inc.
	arraySetInc map[*ast.Call]bool
	// arraySetConsumed holds the names of bare-ident `.with` receivers whose
	// reference __fern_arr_cow_inplace CONSUMES, and arraySetConsumedSites
	// the calls that do it. It is exactly the complement of arraySetInc for
	// a bare-ident receiver that is not a reassign-to-self: no pre-call inc is
	// emitted there, so the helper sees the receiver's own rc and — per its
	// contract, both branches — takes that reference over:
	//
	//   rc == 1 → returns arr unchanged, no rc change; the single reference
	//             now lives in the RESULT, i.e. it moved out of the receiver.
	//   rc  > 1 → copies and decrements arr's rc itself, returning a fresh
	//             rc=1 buffer; the receiver's reference is already released.
	//
	// Either way the frame's obligation is discharged inside the helper, so
	// the call site ZEROES the receiver's slot once the value is on the
	// operand stack (emitArraySet): every later release of that slot — the
	// exit sweep, a loop's re-init drop — meets a null and no-ops, while an
	// exit path that never reached the `.with` (an early return before it)
	// still releases the reference the slot holds. Releasing a consumed slot
	// again was #6013 — an `own` array param returned through `.with` came
	// back freed, and a second call on the recycled block read a corrupted
	// length (3 for a 7-element array), silently, on every compiled backend.
	// A reassign-to-self (`buf = buf.with(…)`) is NOT here: the result flows
	// back into the same slot, which then genuinely owns a reference the
	// sweep must release. The name set is what computeBorrowedAliases
	// consults: a consumed receiver is not borrowable, nor is an alias of one.
	arraySetConsumed      map[string]bool
	arraySetConsumedSites map[*ast.Call]bool
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
	// returnSpreadReuse marks a RETURN-position struct update
	// `return T { ...p, f: v }` whose base p this frame owns and never reads
	// again, so the update writes p's own box in place instead of filling a
	// fresh one and deep-dropping p. Maps the literal to p's name; the names
	// are also in reuseConsumed. See computeReturnSpreadReuse.
	returnSpreadReuse map[*ast.StructLit]string
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
	// single-word pointer (enum / user struct / tuple / array / native
	// single-word string) consistent across occurrences. These
	// names are NOT borrow-tainted in computeFreeEligible (unlike ordinary
	// match bindings), get a pre-allocated zeroed slot in the prologue, and are
	// deep-dropped by the exit sweep exactly like owned locals. Every pointer
	// payload of every releasing arm of a consumingOwnedMatches match is in
	// this table — a match with an untrackable pointer binding is dropped from
	// the plan instead (see the fixpoint in computeConsumingOwnedMatches).
	// Filled by computeConsumingOwnedMatches.
	consumingBindings map[string]ast.Type
	// ownedPayloadMatches marks a `match` whose scrutinee is a direct call to
	// an rcOwnedPayloadBuiltins builtin: the box is immortal and needs no
	// release, but its success payload is a fresh rc=1 value the caller owns
	// and nothing else ever releases. Its qualifying arms' owned-payload
	// bindings are counted owners in consumingBindings; the bind site drops
	// the slot's previous value first so a loop releases every iteration's
	// payload, and a `_` at an owned position drops the payload at once.
	// Unlike a consuming owned-param match this may sit in a loop (each
	// iteration produces a fresh box) and no arm poisons its siblings (an
	// unadmitted binding is today's leak, not a stranded transfer). Filled
	// by computeConsumingOwnedMatches.
	ownedPayloadMatches map[*ast.Match]bool
	// borrowedBindings names the arm bindings (and `@` bindings) of every
	// NON-consuming match: the arm loads the payload straight out of the
	// scrutinee's box with no retain, so a binding holds the box's own
	// reference and its buffer's rc==1 says nothing about the box's owners.
	// The array fast paths (`.with` in computeArraySetIncs, `.append` in
	// appendDecision) force the copy on such a receiver — an in-place write
	// would edit a value a snapshot of the enum still reads, and the box's
	// later drop would release the element the write stored. Bindings of a
	// consuming match (an `own` scrutinee, or an owned-by-default one the
	// analysis promoted) own their reference and keep the in-place path.
	// Filled by computeBorrowedBindings.
	borrowedBindings map[string]bool
	// ownedArgMoves marks the call arguments (bare-ident nodes) this frame
	// hands to an OWNED-BY-DEFAULT parameter without a retain, because the
	// value dies at the call — the counted model's dup elision, the
	// owned-by-default sibling of ownCallMoveArgs. The call site zeroes the
	// argument's slot once the value is on the operand stack. Filled by
	// computeOwnedArgMoves, which also records each in moveSites.
	ownedArgMoves map[*ast.Ident]bool
	// fieldOwnMoves marks the `x.f` nodes this frame hands to an explicit
	// `own` parameter or to `.with` as a MOVE out of x's box: the enclosing
	// statement is `x = S { ...x, f: g(.., x.f, ..) }` / `x = S { ...x, f:
	// x.f.with(i, v) }` or the return of that literal
	// (checker.SupersededFieldMoves), so the store supersedes the field the
	// consumer takes. The site empties the slot when the box is unique at
	// runtime and retains the value otherwise — see emitFieldOwnMove. Filled
	// by computeFieldOwnMoves.
	fieldOwnMoves map[*ast.FieldAccess]bool
	// preciseDrops[stmtIdx] lists the owned locals to deep-drop + zero right
	// after lowering that top-level statement (Perceus garbage-free precise
	// drops — computePreciseDrops).
	preciseDrops map[int][]string
	// nestedDrops[stmt] is the same precise-drop list for a local declared
	// inside a NESTED block, keyed by the block statement to drop after
	// rather than by a top-level index (computeNestedDrops).
	nestedDrops map[ast.Stmt][]string
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
	// mapCowBindSites holds the Map COW-mutator CALL nodes that owe the
	// COW-seam retain (#6227) — the sites where something OTHER than the
	// receiver's binding will release the handle the in-place branch hands
	// back, so two names share one refcount unless the seam adds a second.
	// That is every binding — `var m2 = m.insert(k, v)`,
	// `var (m2, ok) = m.without(k)`, `var t = m.without(k)` binding the tuple
	// whole — and a delete tuple PROJECTED without one (`m.without(k).0`),
	// whose box the field read deep-drops (#8434).
	// A position where the result is a temporary NOTHING drops is excluded,
	// because there the extra count is returned by no one and a retain leaks
	// instead: a chained receiver (`m.insert(a, 1).insert(b, 2)`) and a call
	// argument (`f(m.insert(k, v))`) each measured ~1.8 kB an iteration — a
	// whole copied table — when the retain fired there.
	// freeEligible consults this, so computeRcAnalyses runs it first.
	// Purely syntactic; filled by computeMapCowBindSites.
	mapCowBindSites map[ast.Node]bool
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
	b.rc.cowMapParams = b.computeCowThreadedMapParams()
	// Koka-style consuming matches on owned-by-default enum params (#4400).
	// Computed before freeEligible, which consults consumingBindings (a
	// qualifying binding becomes a counted owner instead of a tainted borrow).
	b.rc.consumingOwnedMatches, b.rc.ownedPayloadMatches, b.rc.consumingBindings = b.computeConsumingOwnedMatches()
	b.rc.borrowedBindings = b.computeBorrowedBindings()
	// The COW-seam retain sites. Purely syntactic, and freeEligible consults
	// it: a delete tuple projected without a binding owns its map element only
	// where the seam retained it, so the two must be computed in this order.
	b.rc.mapCowBindSites = b.computeMapCowBindSites()
	// Borrow-aware free analysis: which array locals are OWNED and
	// thus safe to return to the freelist at rc==0. Borrowed /
	// borrowed-derived locals are excluded (only the owner frees).
	b.rc.freeEligible = b.computeFreeEligible()
	b.rc.moveSites = map[ast.Node]bool{}
	b.rc.aliasBindIncs = map[*ast.Var]bool{}
	b.rc.ownCallMoveArgs = map[ast.Node]bool{}
	b.rc.movedLocals = b.computeMovedLocals()
	// Per-RETURN-SITE own-param transfers, which the whole-function
	// movedLocals above cannot express (#6125).
	b.rc.returnOwnMove = b.computeReturnOwnMoves()
	// `p = f(…, p, …)` re-moves, whose retain was never balanced by an
	// overwrite-dec.
	b.computeSelfReassignOwnMoves()
	b.rc.ctorAliasInced = b.computeCtorAliasInced()
	b.rc.borrowedMapFieldResults = b.computeBorrowedMapFieldResults()
	b.rc.arraySetInc = b.computeArraySetIncs()
	b.computeBorrowedAliases()
	b.rc.dynAliasElemArrays = map[string]bool{}
	b.rc.dynBorrowedViews = b.computeDynBorrowedViews()
	// After the borrow analyses: a borrowed view's source must stay in its
	// slot until the exit sweep, so it is never moved into a call.
	b.rc.ownedArgMoves = b.computeOwnedArgMoves()
	b.rc.fieldOwnMoves = b.computeFieldOwnMoves()
	b.rc.reuseSources, b.rc.reuseConsumed = b.computeReuseSources()
	b.rc.returnSpreadReuse = b.computeReturnSpreadReuse()
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

// summaryTable is a per-function summary under a worklist fixpoint. Every
// read goes through get / at, which records that the function under analysis
// consulted that entry, and fixpoint revisits a function only once an entry
// it consulted has changed. The edges are the reads the analysis actually
// performed — a callee reached through a helper's recursion or treated
// conservatively as unknown is covered the same way — so there is no
// separately maintained call graph to fall out of step with the pass.
//
// The worklist reaches the same tables as a full sweep: an analysis is a
// pure function of the body and the entries it read, so a function none of
// whose consulted entries changed since its last visit would recompute the
// same result. That rests on each pass writing only the entry of the
// function it is analysing.
type summaryTable[V any] struct {
	vals    map[string]V
	cur     *ast.FuncDecl
	readers map[string]map[*ast.FuncDecl]bool
}

func newSummaryTable[V any](n int) *summaryTable[V] {
	return &summaryTable[V]{vals: make(map[string]V, n), readers: map[string]map[*ast.FuncDecl]bool{}}
}

// get reads name's entry and reports whether the table has one (a builtin or
// an unknown callee has none).
func (s *summaryTable[V]) get(name string) (V, bool) {
	if s.cur != nil {
		rs := s.readers[name]
		if rs == nil {
			rs = map[*ast.FuncDecl]bool{}
			s.readers[name] = rs
		}
		rs[s.cur] = true
	}
	v, ok := s.vals[name]
	return v, ok
}

// at is get where the zero value is the answer for an unknown name.
func (s *summaryTable[V]) at(name string) V {
	v, _ := s.get(name)
	return v
}

// fixpointExhaustive makes fixpoint revisit every function every round — the
// full sweep the worklist replaces, kept as the oracle
// TestFixpointWorklistMatchesFullSweep checks the worklist against.
var fixpointExhaustive bool

// fixpointTables, when non-nil, collects every finished fixpoint's table in
// call order, for the same test.
var fixpointTables *[]fixpointResult

type fixpointResult struct {
	vals   any
	visits int
}

// fixpoint runs analyse over funcs until no entry changes. analyse examines
// one function against the current table and reports whether it changed that
// function's entry. Round one visits every function; afterwards a function
// is revisited only once an entry it consulted has changed.
func (s *summaryTable[V]) fixpoint(funcs []*ast.FuncDecl, analyse func(*ast.FuncDecl) bool) {
	dirty := make(map[*ast.FuncDecl]bool, len(funcs))
	for _, fn := range funcs {
		dirty[fn] = true
	}
	visits := 0
	for len(dirty) > 0 {
		changed := false
		for _, fn := range funcs {
			if !dirty[fn] {
				continue
			}
			delete(dirty, fn)
			s.cur = fn
			visits++
			if analyse(fn) {
				changed = true
				for r := range s.readers[fn.Name] {
					dirty[r] = true
				}
			}
		}
		if fixpointExhaustive && changed {
			for _, fn := range funcs {
				dirty[fn] = true
			}
		}
	}
	s.cur = nil
	if fixpointTables != nil {
		*fixpointTables = append(*fixpointTables, fixpointResult{s.vals, visits})
	}
}

// findReturnsFreshBox reports, per function, whether every value return hands
// back a box the function CONSTRUCTED — a struct, tuple or array literal —
// rather than a value derived from what it was passed.
//
// rhsTainted's *ast.Call case taints a result when ANY argument is tainted.
// That rule is right for a callee that might hand an argument back, and wrong
// for one that always builds something new: `__rx_quant(p, pos, gi)` returns
// `RParse { node: …, pos: …, g: … }`, a fresh box, and the borrow taint on the
// pattern string `p` says nothing about that box's provenance. The Call case
// already carves out the same shape one callee at a time — variant
// constructors, map_new, cell_new — and this generalises it to any callee that
// provably does it.
//
// returnsNoParamEscape cannot serve: it asks whether anything REACHABLE FROM
// the result aliases a parameter, which is false for every regex parser
// function (their nodes legitimately carry slices of the pattern). What taint
// needs is only whether the returned POINTER is the callee's own.
//
// A function with no value returns gets false: it returns nothing to be fresh.
func findReturnsFreshBox(prog *ast.Program, info *checker.Info, pairForm, trmcFuncs map[string]bool, ownedParam func(*ast.FuncDecl, int) bool) map[string]bool {
	// Greatest fixpoint: assume every function with a body qualifies, then
	// eliminate the ones a return disproves. A call may be fresh because its
	// callee is, so the answer for one function depends on the answers for
	// others and a single pass would under-approximate mutual recursion.
	q := newSummaryTable[bool](len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			q.vals[fn.Name] = true
		}
	}
	q.fixpoint(prog.Funcs, func(fn *ast.FuncDecl) bool {
		if fn.Body == nil || !q.at(fn.Name) {
			return false
		}
		ctorFresh := variantCtorFreshIn(info, shadowingNames(fn, info))
		fresh := freshLocalsIn(fn, q, ctorFresh)
		retained := returnedAliasIsRetained(fn, pairForm, trmcFuncs)
		refused := map[string]bool{}
		for i, p := range fn.Params {
			refused[p.Name] = !ownedParam(fn, i)
		}
		ok, saw := true, false
		ast.Walk(fn.Body, func(n ast.Node) bool {
			r, isRet := n.(*ast.Return)
			if !isRet || r.Value == nil {
				return true
			}
			saw = true
			if !returnsOwnBox(r.Value, fresh, q, retained, refused, ctorFresh) {
				ok = false
			}
			return true
		})
		if ok && saw {
			return false
		}
		q.vals[fn.Name] = false
		return true
	})
	return q.vals
}

// returnedAliasIsRetained reports whether returning an alias — a bare ident, a
// field read, an index — hands the caller a reference of its OWN rather than a
// borrow of the callee's.
//
// It does, because the Return lowering already pays for it: needsRcIncOnAlias
// is true for every rcTrackedSlotType and does not care whether the aliased
// base is a local or a parameter, so `return acc` and `return s.types[i]` both
// emit the transfer inc, and the one shape that skips it (move-on-return for a
// bare owned local) excludes that local from the exit sweep instead, which is
// the same transfer without the traffic.
//
// Two rewrites reach a return before that inc does, and are refused rather
// than reasoned about: the pair-form ABI pushes (tag, payload) and returns
// early, and TRMC rewrites returns into an accumulator store.
//
// Returning a bare PARAMETER is credited only where the parameter is
// OWNED-BY-DEFAULT (paramVerdictOwned): the caller retained the argument on the
// way in and the callee's exit sweep releases that reference under the same
// is_unique gate it uses for a local, so the transfer inc is the caller's own.
// That is what lets a tree walk's `Tip => return t` arm — `__om_filter`,
// `__om_glue`, `__om_union` — keep every caller's binding of its result
// reclaimable.
//
// A BORROWED parameter is refused, and the refusal now rests on nothing. It
// was recorded as empirical — crediting one was said to lose three of the five
// frees in `url.query_parse("a=1")`, 256 B — and that does not reproduce. Two
// compilers built from the commit that landed the refusal, differing only in
// this arm, leak identically in every spelling of that call: 0 B bound to a
// local, 256 B as `query_parse(..).len()`, both ways. The 256 B is a
// pre-existing leak in the second spelling and was misattributed; the same
// table comes back from current main. Evidence on #7914.
//
// Removing the refusal measures 11,024 B off self-host driver retention with
// 219 more frees, takes `pair_form_payload_borrowing_call` from 144 B to 128 B
// on both backends, and leaves the rc corpus, its leak gates and the
// conformance census otherwise unmoved. It is still not taken, for the reason
// the disproof does not supply: five probe shapes — a bare identity function
// on `string[]` and on `string[][]`, a struct-array return, and a Map with
// scalar and with `string[]` values, each bound and as a temp — read
// IDENTICALLY under both compilers. Nothing smaller than the whole driver
// distinguishes them, so there is no test that would pin the removal, and a
// credit here has leaked once already.
//
// A PROJECTION of a parameter keeps the credit — a different object the callee
// never owned — and no probe has found a shape where that is unsafe.
func returnedAliasIsRetained(fn *ast.FuncDecl, pairForm, trmcFuncs map[string]bool) bool {
	if pairForm[fn.Name] || trmcFuncs[fn.Name] {
		return false
	}
	return fn.ReturnType != nil && rcTrackedSlotType(fn.ReturnType)
}

// returnsOwnBox reports whether `e` evaluates to a box this function owns
// rather than one it was handed. `retained` is returnedAliasIsRetained for the
// enclosing function: with it, a returned alias is owned because the lowering
// inc'd it on the way out. `ctorFresh` is variantCtorFreshIn for the function:
// a variant construction of an rc-payload enum is a fresh rc=1 box whose
// pointer payloads are inc'd in, the carve-out rhsTainted's Call case makes
// for a direct `Ctor(..)`, one call deeper — it is what lets a tree insert's
// `__om_single` / `__om_bin` / `__om_balance` chain prove fresh, so the
// intermediate node a caller binds is reclaimable.
func returnsOwnBox(e ast.Expr, fresh map[string]bool, q *summaryTable[bool], retained bool, refused map[string]bool, ctorFresh func(*ast.Call) bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		// fresh: a local proven fresh below, or an `own` parameter threaded
		// only through owned values (freshLocalsIn). refused: the BORROWED
		// parameters — see returnedAliasIsRetained on the threaded
		// accumulator, whose rebind may decline the dec that balances the
		// return inc.
		return fresh[x.Name] || (retained && !refused[x.Name])
	case *ast.FieldAccess, *ast.Index:
		return retained
	case *ast.Call:
		id, isIdent := x.Callee.(*ast.Ident)
		if !isIdent {
			return false
		}
		switch id.Name {
		case "__method_Array_push", "__method_Array_set":
			// A cow mutator returns the receiver's own buffer (the rc==1
			// in-place path) or a fresh copy of it, and stores the element
			// counted either way — so the result is the function's box
			// exactly when the receiver is. retained=false: the transfer inc
			// covers the returned expression, which is the call, not its
			// receiver, so a returned-alias receiver earns nothing from it.
			return len(x.Args) > 0 && returnsOwnBox(x.Args[0], fresh, q, false, refused, ctorFresh)
		}
		return q.at(id.Name) || ctorFresh(x)
	case *ast.IfExpr:
		return returnsOwnBox(x.Then, fresh, q, retained, refused, ctorFresh) &&
			returnsOwnBox(x.Else, fresh, q, retained, refused, ctorFresh)
	}
	return allocatesFreshBox(e)
}

// freshLocalsIn returns the names in `fn` whose every assigned value is a box
// the function owns: declared locals, and `own` parameters — the caller moved
// its reference in, so the box is this function's to hand back, and it stays
// so across rebinds from owned values (`acc = acc.append(x)`, `acc = walk(t,
// acc)` with walk itself in q). A local that shadows a parameter name is
// excluded outright, and so is an `own` parameter that any binding shadows:
// distinguishing the two costs more than the reclaim is worth.
func freshLocalsIn(fn *ast.FuncDecl, q *summaryTable[bool], ctorFresh func(*ast.Call) bool) map[string]bool {
	isParam := map[string]bool{}
	ownParam := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own && rcTrackedSlotType(p.Type) {
			ownParam[p.Name] = true
		}
	}
	shadowed := shadowingNames(fn, nil)
	assigned := map[string][]ast.Expr{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if x.Init != nil && !isParam[x.Name] {
				assigned[x.Name] = append(assigned[x.Name], x.Init)
			}
		case *ast.Assign:
			if id, ok := x.Target.(*ast.Ident); ok && (!isParam[id.Name] || ownParam[id.Name]) {
				assigned[id.Name] = append(assigned[id.Name], x.Value)
			}
		}
		return true
	})
	fresh := map[string]bool{}
	for name := range assigned {
		if !isParam[name] {
			fresh[name] = true
		}
	}
	for name := range ownParam {
		if !shadowed[name] {
			fresh[name] = true
		}
	}
	// Same shape as the outer fixpoint: a local may be initialised from
	// another, so shrink until stable rather than deciding in one pass.
	for {
		changed := false
		for name := range fresh {
			for _, rhs := range assigned[name] {
				// retained=false: the transfer inc is emitted at RETURN
				// sites only, so an assignment's RHS earns no credit from it.
				if !returnsOwnBox(rhs, fresh, q, false, isParam, ctorFresh) {
					delete(fresh, name)
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	return fresh
}

// findReturnsFreshPairPayload reports, per function, whether every value
// return hands back a variant whose payload box is NEWLY ALLOCATED rather
// than a pointer the function received.
//
// It is deliberately shallower than findReturnsNoParamEscape, and the two
// answer different questions about the same value. That one asks whether
// anything REACHABLE FROM the result aliases a parameter; this one asks only
// whether the returned payload POINTER is the callee's own.
//
// The pair-form payload release frees that box and deep-drops its fields, so
// the property it needs is the shallow one: a counted reference the box holds
// to a parameter's heap is a reference the release is entitled to decrement,
// because the construction that stored it retained it. Demanding the stronger
// property refused every iterator's `next` — which returns
// `Some((elem, Self { xs: self.xs, … }))` — and cost two stranded boxes per
// element across the whole combinator library (sum, count, fold, map, filter,
// take all share that shape).
//
// A payload that is not a literal construction is refused rather than chased:
// a call could be proven fresh by a fixpoint like its sibling's, but nothing
// in the iterator shape needs it and an unproven shape must stay refused —
// releasing a pointer the callee merely passed through is a use-after-free,
// where declining to release it is the leak that already exists.
func findReturnsFreshPairPayload(prog *ast.Program, info *checker.Info) map[string]bool {
	nullaryVariant := map[string]bool{}
	payloadCount := map[string]int{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			payloadCount[v.Name] = len(v.Payloads)
			if len(v.Payloads) == 0 {
				nullaryVariant[v.Name] = true
			}
		}
	}
	out := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		ok := true
		ast.Walk(fn.Body, func(n ast.Node) bool {
			r, isRet := n.(*ast.Return)
			if !isRet || r.Value == nil {
				return true
			}
			if !returnsFreshVariantPayload(r.Value, nullaryVariant, payloadCount) {
				ok = false
			}
			return true
		})
		out[fn.Name] = ok
	}
	return out
}

// returnsFreshVariantPayload reports whether `e` is a variant construction
// whose payload is allocated on the spot — a payloadless variant, or a
// single-payload constructor applied to a literal.
func returnsFreshVariantPayload(e ast.Expr, nullaryVariant map[string]bool, payloadCount map[string]int) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return nullaryVariant[x.Name]
	case *ast.IfExpr:
		return returnsFreshVariantPayload(x.Then, nullaryVariant, payloadCount) &&
			returnsFreshVariantPayload(x.Else, nullaryVariant, payloadCount)
	case *ast.Call:
		id, isIdent := x.Callee.(*ast.Ident)
		if !isIdent {
			return false
		}
		if nullaryVariant[id.Name] {
			return true
		}
		if payloadCount[id.Name] != 1 || len(x.Args) != 1 {
			return false
		}
		return allocatesFreshBox(x.Args[0])
	}
	return false
}

// allocatesFreshBox reports whether evaluating `e` necessarily produces a box
// this function allocated. It does NOT look inside: what the box contains may
// alias anything, which is the whole point of the distinction above.
func allocatesFreshBox(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.TupleLit, *ast.StructLit, *ast.ArrayLit:
		return true
	case *ast.StringLit:
		// A static sentinel, below the heap — releasing it is a no-op
		// under the same guard every rc helper applies.
		return true
	case *ast.IfExpr:
		return allocatesFreshBox(x.Then) && allocatesFreshBox(x.Else)
	}
	return false
}

func findReturnsNoParamEscape(prog *ast.Program, info *checker.Info) map[string]bool {
	// Variant-constructor name -> payload types, for the construction recursion.
	variantPayloads := map[string][]ast.Type{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			variantPayloads[v.Name] = v.Payloads
		}
	}
	q := newSummaryTable[bool](len(prog.Funcs))
	for _, fn := range prog.Funcs {
		q.vals[fn.Name] = true
	}
	q.fixpoint(prog.Funcs, func(fn *ast.FuncDecl) bool {
		if !q.at(fn.Name) || fn.Body == nil {
			return false
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
		if ok {
			return false
		}
		q.vals[fn.Name] = false
		return true
	})
	return q.vals
}

// inferParamEscapes computes, per function and per POINTER parameter, whether
// that parameter's heap value can ESCAPE the function — flow out through the
// return value, or be stored into a caller-visible container (a retain sink such
// as `m.set` / `arr.push`, or an `own` argument the callee itself lets escape).
// Returning a counted PROJECTION of the parameter is not a flow-out: a
// different object leaves, carrying its own unit (returnedCountedProjection).
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
// findReturnsParamProjection reports, per user function, whether any of its
// returns hands back a PROJECTION — a field read or an element — rather than a
// whole value.
//
// It exists because inferParamEscapes deliberately does not count that as an
// escape: returnedCountedProjection excuses it on the grounds that the Return
// lowering inc's the alias on the way out, so a different object leaves
// carrying its own unit. That reasoning is sound for ownership, and wrong for
// aliasing — `peel(s: S): i32[] { return s.xs; }` hands back the very buffer
// `s.xs` names. Anyone asking "can the result name this argument?" needs this
// alongside the escape summary; anyone asking "must the callee free it?" does
// not.
//
// Conservative by shape rather than by taint: any projection return disqualifies
// the function, without tracking which parameter it came from. Taint would be
// more precise, but returnedCountedProjection is itself the only consumer of
// that precision and it reads the return expression directly, so matching its
// two shapes keeps the two definitions legible side by side.
func findReturnsParamProjection(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			r, isRet := n.(*ast.Return)
			if !isRet || r.Value == nil {
				return true
			}
			switch r.Value.(type) {
			case *ast.FieldAccess, *ast.Index:
				out[fn.Name] = true
			}
			return true
		})
	}
	return out
}

func inferParamEscapes(prog *ast.Program, info *checker.Info, pairForm, trmcFuncs map[string]bool) map[string][]bool {
	variantPayloads := map[string][]ast.Type{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			variantPayloads[v.Name] = v.Payloads
		}
	}
	escapes := newSummaryTable[[]bool](len(prog.Funcs))
	for _, fn := range prog.Funcs {
		escapes.vals[fn.Name] = make([]bool, len(fn.Params))
	}
	escapes.fixpoint(prog.Funcs, func(fn *ast.FuncDecl) bool {
		if fn.Body == nil {
			return false
		}
		own := escapes.at(fn.Name)
		changed := false
		for i, p := range fn.Params {
			if own[i] || !ast.IsPointerType(p.Type) {
				continue
			}
			if paramEscapesInFn(fn, p.Name, info, variantPayloads, escapes,
				returnedAliasIsRetained(fn, pairForm, trmcFuncs)) {
				own[i] = true
				changed = true
			}
		}
		return changed
	})
	return escapes.vals
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
// Conservative by construction: a parameter qualifies only when EVERY
// occurrence is positively classified by its tier — a counted store, a
// non-retaining read (a pure-read builtin, a scalar projection, a concat
// operand, an append receiver), a counted-seed binding, or an argument to a
// position the summary has already credited (the fixpoint below). One
// occurrence outside those — a bare `return name`, a reassignment, an
// uncounted sink — and the summary is false and the caller keeps the
// conservative taint.
//
// A variant-constructor payload is a counted store too — emitEnumNew inc's an
// aliased payload under the same predicate a StructLit field takes — so
// `Node(l, k, r)` credits its arguments, but only where that inc is actually
// emitted: variantCtorCountedIn mirrors emitEnumNew's gate. A credit the
// retain does not honour is an over-release (a use-after-free), where a
// missing credit is only the leak this closes: the fresh `ins(l, k)` temp a
// tree insert hands to a node-building helper was stranded at rc 2 on every
// call, one node per insert.
//
// A parameter shadowed by a local / match binding of the same name disqualifies
// it: the occurrence counting below cannot tell the two apart.
// variantCtorCountedIn returns the predicate "this call is a variant
// construction whose aliased payloads emitEnumNew inc's". It mirrors that
// emitter's gate exactly, minus the two terms that can never hold for a
// parameter occurrence — a parameter is not a move site (markConstructionMoves
// only marks owned rc locals), and needsRcIncOnAlias is true for every
// pointer-shaped parameter type the classifiers credit:
//
//   - the callee resolves to a payload-carrying variant, and the name is not
//     shadowed by a local, a binding or a user function (nameShadowsVariant);
//   - the enum's payloads are rc-counted (enumRcPayloadsEligible);
//   - the construction is not a consuming-match reuse site, which stores its
//     payloads without the inc. computeConsumingMatchReuse pairs the sole
//     `return Ctor(..)` of an arm of a `match` on an `own` enum parameter;
//     every such return is refused here without re-checking its other
//     gates (box uniformity, the reuse flag), the conservative direction.
func variantCtorCountedIn(fn *ast.FuncDecl, info *checker.Info, shadowed map[string]bool) func(*ast.Call) bool {
	ctorFresh := variantCtorFreshIn(info, shadowed)
	if !ast.EnumRcPayloads || info == nil || fn.Body == nil {
		return func(*ast.Call) bool { return false }
	}
	ownParam := map[string]bool{}
	for i, flag := range info.OwnFuncs[fn.Name] {
		if flag && i < len(fn.Params) {
			ownParam[fn.Params[i].Name] = true
		}
	}
	reuseSite := map[*ast.Call]bool{}
	if len(ownParam) > 0 {
		ast.Walk(fn.Body, func(n ast.Node) bool {
			m, ok := n.(*ast.Match)
			if !ok {
				return true
			}
			if tag, ok := m.Tag.(*ast.Ident); !ok || !ownParam[tag.Name] {
				return true
			}
			for _, arm := range m.Arms {
				if arm.Body == nil || len(arm.Body.Stmts) != 1 {
					continue
				}
				if ret, ok := arm.Body.Stmts[0].(*ast.Return); ok && ret.Value != nil {
					if call, ok := ret.Value.(*ast.Call); ok {
						reuseSite[call] = true
					}
				}
			}
			return true
		})
	}
	return func(c *ast.Call) bool { return !reuseSite[c] && ctorFresh(c) }
}

// variantCtorFreshIn returns the predicate "this call constructs a fresh rc=1
// box of an rc-payload enum" — the callee resolves to a payload-carrying
// variant (callBody's dispatch, minus a name a local, a binding or a user
// function shadows: nameShadowsVariant) of an enum whose payloads emitEnumNew
// counts (enumRcPayloadsEligible). Under that gate the construction inc's
// every aliased payload, so the box owns what it holds; a Map-carrying enum
// stores its payloads uncounted and is refused, exactly as rhsTainted's
// direct-constructor carve-out refuses it.
func variantCtorFreshIn(info *checker.Info, shadowed map[string]bool) func(*ast.Call) bool {
	if !ast.EnumRcPayloads || info == nil {
		return func(*ast.Call) bool { return false }
	}
	return func(c *ast.Call) bool {
		id, ok := c.Callee.(*ast.Ident)
		if !ok || shadowed[id.Name] {
			return false
		}
		if _, isFunc := info.FuncSigs[id.Name]; isFunc {
			return false
		}
		en, _, payloads, isVariant := lookupVariantIn(info, id.Name, id.EnumName)
		return isVariant && payloads > 0 && enumRcPayloadsEligibleIn(info, en)
	}
}

// shadowingNames lists every name `fn` binds besides its parameters — declared
// locals, match-arm binders (payload, `@` and tuple-pattern binders included)
// and for-each variables. A parameter reusing one of these names cannot be
// told from the binding by an occurrence walk, and a variant constructor by
// that name is not what a call to it reaches.
func shadowingNames(fn *ast.FuncDecl, info *checker.Info) map[string]bool {
	out := map[string]bool{}
	if info != nil {
		for _, v := range info.Locals[fn] {
			out[v.Name] = true
		}
	}
	bind := func(names ...string) {
		for _, nm := range names {
			if nm != "" {
				out[nm] = true
			}
		}
	}
	if fn.Body == nil {
		return out
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			bind(x.Name)
		case *ast.Match:
			for _, arm := range x.Arms {
				bind(arm.Binders()...)
			}
		case *ast.MatchExpr:
			for _, arm := range x.Arms {
				bind(arm.Binders()...)
			}
		case *ast.ForEach:
			bind(x.Var)
		}
		return true
	})
	return out
}

func inferParamCountedRetain(prog *ast.Program, info *checker.Info) map[string][]bool {
	// Precompute the shadowed-name set per function once (match / match-expr
	// bindings that reuse a parameter name).
	type fnCtx struct {
		shadowed    map[string]bool
		ctorCounted func(*ast.Call) bool
	}
	ctxs := make(map[*ast.FuncDecl]fnCtx, len(prog.Funcs))
	out := newSummaryTable[[]bool](len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		sh := shadowingNames(fn, info)
		ctxs[fn] = fnCtx{sh, variantCtorCountedIn(fn, info, sh)}
		out.vals[fn.Name] = make([]bool, len(fn.Params))
	}
	// Least-fixpoint: struct-param crediting consults the summary for the
	// arg-position rule (a `p` passed as argument i to callee C is counted iff
	// C's parameter i is counted), so a param credited this round can credit a
	// caller next round. Start all-false and only ever add credits — the
	// classifier marks a use safe only on positive local evidence or a
	// callee already proven counted — so the iteration is monotone and
	// converges to the grounded fixpoint (a mutual-recursion cycle with no
	// grounding stays uncredited, the conservative direction).
	out.fixpoint(prog.Funcs, func(fn *ast.FuncDecl) bool {
		c, ok := ctxs[fn]
		if !ok {
			return false
		}
		sh, ctorCounted := c.shadowed, c.ctorCounted
		flags := make([]bool, len(fn.Params))
		for i, p := range fn.Params {
			// A parameter carrying no heap (i32 / bool / f64 / …) can never
			// be retained at all — mark it so a scalar argument doesn't
			// disqualify a call the way a pointer one would. Conditioned
			// below on EVERY pointer param being counted too: a tainted
			// scalar may carry a raw pointer, and only a callee whose every
			// pointer position is proven counted is known not to hand an
			// alias of anything back.
			if !rcTrackedSlotType(p.Type) {
				flags[i] = true
				continue
			}
			if p.Own || sh[p.Name] {
				continue
			}
			switch pt := p.Type.(type) {
			case ast.StringType:
				flags[i] = stringParamCounted(fn, p.Name, out, ctorCounted)
			case ast.ArrayType:
				flags[i] = arrayParamCounted(fn, p.Name, pt, info, out, ctorCounted)
			case ast.StructType:
				// Credit `p` when every one of its appearances is a counted
				// store, a non-retaining read, or a counted call argument —
				// so a result built from it holds only counted references.
				// This is what lets the scalar-arg exemption fire for a
				// scanner threaded through field projections and pure-read
				// methods (lexer.tokenize;
				// docs/SELFHOST-AST-RETIREMENT.md).
				flags[i] = paramProjectionsSafe(fn, p.Name, info, out, ctorCounted)
			case ast.EnumType:
				// The same rule, and sound for the same reason:
				// `needsRcIncOnAlias` is true for an enum, so a bare `p` in
				// a construction slot is inc'd into the new box exactly as
				// a struct is. Without this arm the parser's node
				// constructors — `e_binary(op, l, r)` and its siblings,
				// whose payloads are `ast.Expr`, an enum — were refused,
				// and every caller's fresh argument was stranded. Isolated
				// with a control: `mknode(t, n) -> Node { ty: t, n: n }`
				// leaks its argument when `t` is an enum and does not when
				// `t` is a struct, same call shape either way (#7867).
				flags[i] = paramProjectionsSafe(fn, p.Name, info, out, ctorCounted)
			case ast.TupleType:
				// A tuple has no nominal declaration to name, so the
				// projection arms never fire and only the slot / argument /
				// return rules can credit it. `needsRcIncOnAlias` is true
				// for a tuple, so those carry the same argument.
				flags[i] = paramProjectionsSafe(fn, p.Name, info, out, ctorCounted)
			case *ast.FuncType:
				// A function value is retained by the same counted slots
				// (`needsRcIncOnAlias` is true for a closure) and read,
				// without retaining, by being CALLED — the callee-position
				// arm of paramProjectionsSafe. This is what lets a caller
				// release the lambda it passes to `m.update(k, f)`.
				flags[i] = paramProjectionsSafe(fn, p.Name, info, out, ctorCounted)
			case ast.SliceType:
				// A slice header is retained by the same counted slots (a
				// tuple's argument), and read without retaining by `.len()`
				// (pureReadReceiverBuiltin) and by a scalar element read,
				// which copies a byte out of storage the header does not own.
				// That is the whole of a digest's `absorb(h, chunk)`, and
				// without the arm the parameter is refused — which then
				// de-credits the function's OTHER parameters through
				// ptrAllCounted below and strands the caller's header.
				flags[i] = paramProjectionsSafe(fn, p.Name, info, out, ctorCounted)
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
		if boolSliceEqual(out.vals[fn.Name], flags) {
			return false
		}
		out.vals[fn.Name] = flags
		return true
	})
	return out.vals
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
	case "__method_string_len", "__method_Array_len", "__method_slice_len":
		return true
	case "slice_unchecked":
		// Copies bytes OUT of its string receiver into a fresh buffer
		// (the __str_slice contract) — the builtin spelling of the
		// string SliceExpr credit, so migrating `s[a:b]` to it cannot
		// flip a param out of counted-retain (#5634).
		return true
	}
	return false
}

// copyingBuiltinArg reports whether argument i of BUILTIN name is COPIED or
// merely READ rather than retained, with a result that cannot alias it — the
// general, per-argument form of the fact pureReadReceiverBuiltin states for
// receivers (#7867 slice 2).
//
// Membership demands TWO claims, verified against the runtime body
// (rcsigs.go's rule: read the body, not the name): the callee moves no
// count on the argument — every member is in the inert registry, which
// TestCopyingBuiltinArgsAreInertPerTheRegistry pins — AND the call's
// result cannot alias it. The second is why this is a hand-audited
// table and not derived from rcInertBuiltins, whose header says it does
// not model the result axis.
//
// Both claims are per ARGUMENT, which is why a callee lists positions
// rather than appearing wholesale: a Map read is the case that needs the
// distinction, since it satisfies both for its key and neither for the
// values it hands back. `__method_Map_set` / `__method_Array_push` /
// `_set` move counts and are already refused at the call site by
// calleeRetainsAnyArg; `__heap_release_to` invalidates memory wholesale.
// Those stay out entirely.
//
//   - strbuf_append memcpys the string's bytes past the buffer tail
//     and returns void (its runtime doc, all three implementations);
//   - print / write / eprint write the bytes to an fd, void result;
//   - `w.write(s)` (__fern_writer_write) writes the bytes to the
//     Writer's fd and returns an immortal Option[IoError] box built
//     by __build_io_error / the None sentinel, which cannot name the
//     string;
//   - string_from_bytes_unchecked memcpys the u8[] into a fresh string
//     (inline-packed, the empty sentinel, or an rc1 heap copy — never
//     the input buffer);
//   - __memchr / __rmemchr / __ascii_run / __count_byte scan the
//     bytes and return a scalar;
//   - a Map READ's KEY (position 1) is hashed and compared and nothing
//     else — __map_lookup_keyed reaches only __map_hash_str /
//     __map_eq_str, neither of which moves a count — and what the call
//     returns is the stored VALUE, which cannot alias the key. The same
//     callees' RECEIVER (0) and get_or's FALLBACK (2) are precisely the
//     aliasing results the table must keep out, and do not appear here.
//     Without the key, the native single-word taint below treated every
//     `m.get(k)` as a possible retention of k and suppressed k's own
//     scope-exit release: one stranded key buffer per map (#8277).
//
// The checker rejects a user function redeclaring a builtin name, so
// the table can never answer for a defined function.
var copyingBuiltinArgs = map[string][]int{
	"strbuf_append":               {0},
	"print":                       {0},
	"write":                       {0},
	"eprint":                      {0},
	"__method_Writer_write":       {1},
	"string_from_bytes_unchecked": {0},
	"__memchr":                    {0},
	"__rmemchr":                   {0},
	"__ascii_run":                 {0},
	"__count_byte":                {0},
	"__method_Map_get":            {1},
	"__method_Map_get_or":         {1},
	"__method_Map_has":            {1},
	"__method_Map_delete":         {1},
}

func copyingBuiltinArg(name string, i int) bool {
	for _, idx := range copyingBuiltinArgs[name] {
		if idx == i {
			return true
		}
	}
	return false
}

// stringParamCounted reports whether string parameter `pn` of fn is retained
// only through counted constructions or non-retaining reads — every appearance
// is a bare-ident value of a StructLit / TupleLit / ArrayLit slot, the receiver
// of a pure-read builtin (`s.len()`), the source of a byte index / slice, or
// an argument to a callee whose parameter in that position is itself
// counted-retain. Conservative: a param qualifies only when every occurrence
// is credited.
// everyOccurrenceSafe decides the three counted-retain summaries from the
// occurrence tally: the parameter is safe when every appearance of it was
// classified safe.
//
// ZERO occurrences is SAFE, and that is the whole point of naming this.
// The summaries answer "can this callee retain the argument in a way the
// caller's escape analysis cannot see" — and a parameter the body never
// mentions cannot be retained by any means at all, counted or otherwise.
// It is the strongest form of the property, not the absence of evidence
// for it.
//
// Requiring at least one occurrence read the vacuous case as "unknown"
// and left the caller's conservative taint in place, which on the native
// single-word string ABI is a deliberate never-reclaim: computeFreeEligible
// taints a string ident passed to a user function unless
// paramCountedRetain clears it, "a leak at worst, never a use-after-free".
// For an unused parameter there was nothing to be conservative about, and
// the leak was unconditional — #7798, where three lines allocate one
// string and free none on x86-64 while arm64 is clean.
//
// The tally is sound for this: ast.Walk descends into Lambda bodies and
// MakeClosure captures, so a parameter a nested closure touches is
// counted, and a parameter shadowed by a local is excluded by the caller
// before either function runs.
func everyOccurrenceSafe(total, safe int) bool {
	return total == safe
}

// countedSeedOccurrences names the occurrences of a PARAMETER that seed a
// local which is later reassigned — `var cur: Scope = s;` followed by
// `cur = advance(stmt, cur)`. computeFreeEligible already treats exactly
// that binding as a COUNTED alias rather than a borrow (its countedSeed
// map): needsRcIncOnAlias holds for the initialiser and the source is a
// parameter, so no move site or borrowed-alias cancellation can reach it
// and the *ast.Var lowering emits the transfer inc. The local therefore
// owns a reference of its own, and the caller's argument is retained
// COUNTED — which is what the counted-retain summaries ask about.
//
// The two conditions mirror countedSeed's exactly. Reassignment is
// required because the transfer inc is emitted only for a rebindable
// binding: a local that only ever holds the seed keeps the borrow verdict
// and is a genuine uncounted retention. A duplicated declaration name is
// refused because the verdict would then govern a slot two bindings share
// (localNameUnique, spelled here as "declared once" since these summaries
// run before the builder's slot map exists).
//
// This is the grounding case for a threaded walker. `annotate_block(body,
// s)` binds `s` to `cur` and does nothing else with it, so refusing the
// binding refused the parameter, and through it every caller in the
// fixpoint — leaving `annotate_block(stmts, new_scope_full(...))`'s FRESH
// Scope temp with no owner. That one temp exclusively held 87,584 B of
// module tables in the self-host driver: the sig and struct tables the
// whole #7914 census is ranked by.
// consumedArrayParamPositions is computeConsumedParams' whole-program,
// ARRAY-only projection: per callee name, which parameter positions that
// per-function analysis promotes to consumed-threaded. A call site needs the
// CALLEE's verdict, and computeConsumedParams only ever runs for the function
// being lowered.
//
// Arrays let this be a projection rather than a duplicate. Every other
// condition in computeConsumedParams routes through the borrow verdict, and
// ownedByDefaultShape has no ArrayType arm — so for an array parameter
// paramOwnedByDefault is always false, paramVerdict is always
// NotOwnedType (never Borrowed), and the owned-shape skip never fires.
// The three verdict-dependent gates therefore cannot fire, leaving exactly
// the conditions below. TestConsumedArrayPositionsMatchTheLoweringVerdict
// pins the agreement so the two cannot drift apart.
//
// What a caller does with it: the consumed-threaded protocol
// (emitConsumedArrayOverwriteDec) starts its ownership flag at 0, meaning the
// slot still holds the CALLER's borrow, so the callee never releases the
// buffer it was handed. A fresh temp passed there is owned by nobody — the
// callee treats it as borrowed and the caller's stage-(b) reclaim refuses the
// position, because paramCountedRetain reads false for a parameter the callee
// hands out bare. One 16 B array literal leaked per call of the self-host's
// `fold_all([], items)` shape, 119 blocks in a compile (#7914).
func consumedArrayParamPositions(prog *ast.Program, info *checker.Info, trmcFuncs map[string]bool) map[string][]bool {
	out := map[string][]bool{}
	if !ast.RcFreeEnabled {
		return out
	}
	for _, fn := range prog.Funcs {
		if fn.Body == nil || trmcFuncs[fn.Name] {
			continue
		}
		reassigned := map[string]bool{}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			if a, ok := n.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok {
					reassigned[id.Name] = true
				}
			}
			return true
		})
		var pos []bool
		any := false
		for _, p := range fn.Params {
			ok := false
			if _, isArr := p.Type.(ast.ArrayType); isArr && !p.Own && reassigned[p.Name] &&
				deepDropWired(info, p.Type) {
				ok = true
				any = true
			}
			pos = append(pos, ok)
		}
		if any {
			out[fn.Name] = pos
		}
	}
	return out
}

func countedSeedOccurrences(fn *ast.FuncDecl) map[*ast.Ident]bool {
	decls := map[string]int{}
	seeds := map[string][]*ast.Ident{}
	reassigned := map[string]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			decls[x.Name]++
			if id, ok := x.Init.(*ast.Ident); ok {
				seeds[x.Name] = append(seeds[x.Name], id)
			}
		case *ast.Assign:
			if id, ok := x.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		}
		return true
	})
	out := map[*ast.Ident]bool{}
	for name, ids := range seeds {
		if decls[name] != 1 || !reassigned[name] {
			continue
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out
}

// syncByteCopyCall reports whether `call` is `__memcpy` / `__memset`: a byte
// copy through raw addresses that completes inside the call. The buffers
// its arguments address are read or written and nothing about them
// survives it — no reference is retained and no pointer is embedded — so an
// occurrence there is a non-retaining use, unlike the `buf as usize` the
// CastExpr escape taint exists for, whose raw address lives on.
func syncByteCopyCall(call *ast.Call) bool {
	id, ok := call.Callee.(*ast.Ident)
	return ok && (id.Name == "__memcpy" || id.Name == "__memset")
}

// syncByteCopyRoots names the idents whose bytes a __memcpy / __memset
// argument addresses: `buf as usize` and `s.as_bytes() as usize` both
// resolve to the buffer's own ident.
func syncByteCopyRoots(call *ast.Call) []*ast.Ident {
	var out []*ast.Ident
	for _, a := range call.Args {
		c, ok := a.(*ast.CastExpr)
		if !ok {
			continue
		}
		e := c.Inner
		if view, ok := e.(*ast.Call); ok {
			if vid, ok := view.Callee.(*ast.Ident); ok && vid.Name == "__method_string_as_bytes" && len(view.Args) == 1 {
				e = view.Args[0]
			}
		}
		if id, ok := e.(*ast.Ident); ok {
			out = append(out, id)
		}
	}
	return out
}

func stringParamCounted(fn *ast.FuncDecl, pn string, summary *summaryTable[[]bool], ctorCounted func(*ast.Call) bool) bool {
	safe := map[*ast.Ident]bool{}
	seedOK := countedSeedOccurrences(fn)
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
				if seedOK[x] {
					safe[x] = true
				}
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
		case *ast.Index:
			// `p[i]` yields a u8 — a value copy that creates no reference, so
			// the source retains nothing. structParamProjectionsSafe already
			// credits the same read one field deep (`p.strField[i]`); without
			// it here, any scanner that looks at a byte of its string param
			// lost the credit, and the CALLER's binding of that function's
			// result stayed permanently taint-ineligible. The exit sweep then
			// emitted the dec-only __fern_rc_dec instead of the freeing
			// __fern_arr_dec, so a KMP-shaped `var f = table(p)` leaked its
			// whole array once per call.
			mark(x.Array)
		case *ast.SliceExpr:
			// `p[a:b]` on a string copies the bytes out into a fresh buffer
			// and leaves the source alone (__str_slice), so it retains nothing
			// either — the slice half of the same argument.
			mark(x.Source)
		case *ast.Binary:
			// `a + p` copies both operands' bytes into a FRESH buffer on
			// every backend — __fern_strcat allocates, or returns an
			// SSO-inline value or the shared empty sentinel, and never
			// hands back either operand's pointer — so a concat operand
			// is retained by nothing. This is the same fact
			// stashOwnedStringOperand states ("a BORROWING string op —
			// one that reads its operand's bytes and leaves that buffer
			// alone") and rhsTainted's IsStringConcat case relies on. The
			// in-place `s = s + rhs` append is not an exception: it needs
			// freeEligible[s], which a borrowed param never has.
			//
			// A comparison reads the same way and yields a bool, which
			// cannot alias either side.
			//
			// Concat is the commonest non-retaining use a string parameter
			// has, and it was the one occurrence kind with no arm: an
			// encoder helper `put(reg, key, flags) -> reg.with(b, reg[b] +
			// key + "|" + flags)` refused BOTH string params, and through
			// rhsTainted's counted-argument check that taint reached the
			// caller's ARRAY locals — the self-host's interprocedural
			// borrow fixpoint stranded all ten of its 3 KB registries per
			// compile (#7914).
			if x.IsStringConcat || x.IsStringCmp || x.IsStringOrd {
				mark(x.Left)
				mark(x.Right)
			}
		case *ast.Call:
			// `s.len()` — a pure-read builtin reads the receiver and returns a
			// scalar, retaining nothing, so the receiver occurrence is safe.
			if id, ok := x.Callee.(*ast.Ident); ok && pureReadReceiverBuiltin(id.Name) && len(x.Args) > 0 {
				mark(x.Args[0])
			}
			// `__memcpy(dst as usize, p.as_bytes() as usize, n)` reads p's
			// bytes during the call and keeps nothing — the `bytes()` shape.
			if syncByteCopyCall(x) {
				for _, id := range syncByteCopyRoots(x) {
					mark(id)
				}
			}
			// A copying builtin — strbuf_append, print, the byte
			// scanners — memcpys or writes the bytes out and retains
			// nothing, and its scalar/void result cannot alias the
			// argument. (The checker rejects a user function
			// redeclaring a builtin name, so the table cannot answer
			// for a defined function.)
			if id, ok := x.Callee.(*ast.Ident); ok {
				for ai, a := range x.Args {
					if copyingBuiltinArg(id.Name, ai) {
						mark(a)
					}
				}
			}
			// `xs.append(p)` — __method_Array_push's ELEMENT position is a
			// COUNTED store: emitArrayPush emits the alias inc for a
			// pointer element unconditionally (needsRcIncOnAlias, and an
			// Ident read is never a move site), and the buffer's deep drop
			// gives it back. The same argument that credited the position
			// in arrayParamCounted (#7867 slices 1 and 4); the string tier
			// never had it, so a helper storing its string param into an
			// array — the checker's per-block derived Scope, the #7914
			// frontier's dominant shape — refused, and every caller's
			// fresh argument temp stranded uncounted.
			if id, ok := x.Callee.(*ast.Ident); ok &&
				id.Name == "__method_Array_push" && len(x.Args) == 2 {
				mark(x.Args[1])
			}
			// `xs.with(i, p)` — __method_Array_set's ELEMENT position is
			// push's sibling and is counted the same way: for a
			// pointer-shaped element emitArraySet incs an aliased element,
			// drops the one it overwrites, and the copy path retains
			// through __fern_arr_cow_inplace_ptr's element walk, so the
			// buffer owns a reference of its own. computeFreeEligible
			// already reads it that way — its Array_set arm routes the
			// source through escapeOwned under exactly this gate — and the
			// summary tiers were the half that did not.
			if id, ok := x.Callee.(*ast.Ident); ok &&
				id.Name == "__method_Array_set" && len(x.Args) == 3 &&
				len(x.TypeArgs) == 1 && rcTrackedSlotType(x.TypeArgs[0]) {
				mark(x.Args[2])
			}
			// A variant-constructor payload is inc'd like a StructLit field.
			if ctorCounted(x) {
				for _, a := range x.Args {
					mark(a)
				}
			}
			// Passing `s` on to a callee that is ITSELF counted-retain in that
			// position retains nothing new: whatever the callee does with it is
			// already known to be a counted store or a pure read. This is the
			// argument-position rule structParamProjectionsSafe has always had,
			// and its absence here meant one FORWARDING frame disqualified the
			// whole chain — a dispatcher like `check(kind, s) { return
			// iban_valid(s); }` left every caller's freshly built string
			// permanently taint-ineligible, so `var bad = rewrite(x);
			// check(k, bad)` leaked it while the inline spelling was flat.
			//
			// Sound by the same fixpoint argument as the struct case: the
			// summary starts all-false and only ever gains credits, so a
			// mutual-recursion cycle with no grounding stays uncredited.
			if id, ok := x.Callee.(*ast.Ident); ok {
				if cs, known := summary.get(id.Name); known {
					for ai, a := range x.Args {
						if ai < len(cs) && cs[ai] {
							mark(a)
						}
					}
				}
			}
		}
		return true
	})
	return everyOccurrenceSafe(total, len(safe))
}

// arrayParamCounted is the array sibling of stringParamCounted: an array
// parameter `pn` qualifies when every appearance is a bare-ident value of a
// StructLit / TupleLit / ArrayLit slot, the receiver of a pure-read builtin
// (`p.len()`), the RECEIVER of `p.append(v)` (the grow never hands p's own
// count out — see the push arm), or an argument to a callee whose parameter
// in that position is itself counted-retain.
//
// `p[i]` is credited only where the read provably retains nothing (#7867
// slice 4, and the runtime shape behind #7914's projection leak):
//   - the element type is SCALAR — the read is a value copy, exactly the
//     u8 argument stringParamCounted has always used;
//   - `p[i].scalarField` on a struct element — the projection copies a
//     scalar out and the element reference never outlives the expression;
//   - `p[i]` in `__method_Array_push`'s ELEMENT position — emitArrayPush
//     emits the alias inc unconditionally for a pointer element
//     (needsRcIncOnAlias, and an Index read is never a move site), so the
//     container's reference is counted; a scalar element is a copy. The
//     same argument that credited the bare-parameter element in slice 1.
//
// A bare `p[i]` of a POINTER element anywhere else still refuses — it
// hands out a live reference nothing counts. Similarly no SliceExpr arm —
// an array slice can share the buffer.
func arrayParamCounted(fn *ast.FuncDecl, pn string, at ast.ArrayType, info *checker.Info, summary *summaryTable[[]bool], ctorCounted func(*ast.Call) bool) bool {
	safe := map[*ast.Ident]bool{}
	seedOK := countedSeedOccurrences(fn)
	mark := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Name == pn {
			safe[id] = true
		}
	}
	// paramIndex unwraps `p[i]` (a real array index, not a string or
	// slice read) to the parameter ident, or nil.
	paramIndex := func(e ast.Expr) *ast.Ident {
		ix, ok := e.(*ast.Index)
		if !ok || ix.IsString || ix.IsSlice {
			return nil
		}
		if id, ok := ix.Array.(*ast.Ident); ok && id.Name == pn {
			return id
		}
		return nil
	}
	elemScalar := !rcTrackedSlotType(at.Elem)
	elemStruct := ""
	if st, ok := at.Elem.(ast.StructType); ok {
		elemStruct = st.Name
	}
	total := 0
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == pn {
				total++
				if seedOK[x] {
					safe[x] = true
				}
			}
		case *ast.Index:
			if id := paramIndex(x); id != nil && elemScalar {
				safe[id] = true
			}
		case *ast.FieldAccess:
			id := paramIndex(x.Target)
			if id == nil || elemStruct == "" || info == nil {
				break
			}
			if sd, ok := info.Structs[elemStruct]; ok {
				for _, f := range sd.Fields {
					if f.Name == x.Field && !rcTrackedSlotType(f.Type) {
						safe[id] = true
						break
					}
				}
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
			if id, ok := x.Callee.(*ast.Ident); ok && pureReadReceiverBuiltin(id.Name) && len(x.Args) > 0 {
				mark(x.Args[0])
			}
			if id, ok := x.Callee.(*ast.Ident); ok && id.Name == "__method_Array_push" && len(x.Args) == 2 {
				// `p.append(v)` retains nothing of p uncounted on either of
				// the grow's outcomes: the copy path leaves p's buffer at its
				// incoming count and hands back a fresh rc 1 buffer, and the
				// in-place path (rc == 1 with spare capacity) sets the
				// buffer's count to 2 before returning it — every backend's
				// __fern_arr_push_grow does — so the result is a counted
				// reference either way and p's own count is untouched. That
				// is exactly the "rc 2 on the escaping path, rc 1 on the
				// non-escaping one" contract the caller's post-call dec
				// relies on, so `return p.append(v)` credits. `.with` is not
				// its sibling here: __fern_arr_cow_inplace returns the
				// receiver at rc 1 unbumped, an uncounted identity.
				mark(x.Args[0])
				if eid := paramIndex(x.Args[1]); eid != nil {
					safe[eid] = true
				}
			}
			// The `.with` sibling of the push arm above, on the same
			// terms: emitArraySet's element inc counts the store for a
			// pointer-shaped element, so a `p[j]` read handed to it is a
			// counted occurrence rather than a live reference handed out.
			if id, ok := x.Callee.(*ast.Ident); ok && id.Name == "__method_Array_set" &&
				len(x.Args) == 3 && len(x.TypeArgs) == 1 && rcTrackedSlotType(x.TypeArgs[0]) {
				mark(x.Args[2])
				if eid := paramIndex(x.Args[2]); eid != nil {
					safe[eid] = true
				}
			}
			// A variant-constructor payload is inc'd like a StructLit field
			// (a pointer element read `p[i]`) or copied (a scalar one).
			if ctorCounted(x) {
				for _, a := range x.Args {
					mark(a)
					if eid := paramIndex(a); eid != nil {
						safe[eid] = true
					}
				}
			}
			// Copying builtins, for tier parity with the string and
			// struct classifiers. Nothing in the table takes an array
			// today, so this arm is inert until one does.
			if id, ok := x.Callee.(*ast.Ident); ok {
				for ai, a := range x.Args {
					if copyingBuiltinArg(id.Name, ai) {
						mark(a)
					}
				}
			}
			if id, ok := x.Callee.(*ast.Ident); ok {
				if cs, known := summary.get(id.Name); known {
					for ai, a := range x.Args {
						if ai < len(cs) && cs[ai] {
							mark(a)
						}
					}
				}
			}
		}
		return true
	})
	return everyOccurrenceSafe(total, len(safe))
}

// paramProjectionsSafe reports whether every occurrence of pointer
// parameter `pn` in fn is a COUNTED store, a NON-RETAINING read, or a
// COUNTED call argument — the generalisation of the string
// counted-retain summary to a whole-value parameter, closed over the
// interprocedural `summary` for the arg-position rule. Conservative by
// construction: a p-occurrence is credited only when the walk positively
// proves it safe, and the param qualifies only when EVERY occurrence is
// credited (`total == len(safe)`), so any unhandled or escaping use
// disqualifies the whole param.
//
// The rules apply to `p` and to every binding a `match (p)` introduces — a
// payload binding is an uncounted alias of p's interior, so it is held to the
// same rules, and the match itself is then a non-retaining read. That is what
// credits the tree walks (`match (t) { Tip => …, Bin(l, k, v, r) => … }`) that
// every persistent-collection function is built out of.
//
// Credited (safe) occurrences:
//   - a bare `p` or `p.field` stored as a StructLit / TupleLit / ArrayLit slot
//     value — the construction inc's a pointer field / copies a scalar;
//   - a SCALAR field read `p.scalarField` anywhere — a value copy; an enum or
//     a tuple has no field to read and the projection arms never fire for one;
//   - a SCALAR element read `p.arrField[i]` / `xs[i]` on a tracked array —
//     a value copy;
//   - the SOURCE of a string slice `p.strField[a:b]` / string index
//     `p.strField[i]` — a copying read;
//   - the scrutinee of a `match`, whose bindings are then tracked;
//   - a function-typed `p` in CALLEE position — the call loads and dispatches;
//   - a bare `p` / `p.field` passed as argument i to a call whose callee
//     parameter i is itself counted-retain (`summary[C][i]`) — the method
//     receiver `l.at_end()` and the self-reassign source `l.advance()`;
//   - a bare `p` / `p.field` RETURNED — the return-transfer inc counts it;
//   - `p` as the TARGET of an assignment (`p = …`) — a rebind, not a retention;
//     the old value's fate is decided by the RHS classification and the
//     reassigned-param overwrite dec (computeConsumedParams).
//
// Everything else (a bare `p` outside a slot, a pointer field or element read
// that escapes, `p` passed to an UNCOUNTED / builtin argument, `x = p`, an
// array-slice source) is left uncredited and disqualifies — which is what
// keeps `grow(m, k): Map { m = m.insert(k, …); return m; }` out: `m` reaches a
// builtin `__method_Map_set` argument (never in `summary`), so it is never
// credited and its scalar `k` is never exempted.
func paramProjectionsSafe(fn *ast.FuncDecl, pn string, info *checker.Info, summary *summaryTable[[]bool], ctorCounted func(*ast.Call) bool) bool {
	// tracked holds `pn` and every binding a match over a tracked name
	// introduces: a payload binding is an uncounted alias of the value's
	// interior, so it is held to exactly the rules the parameter is, and the
	// match itself then reads without retaining. typeOf carries each tracked
	// name's static type for the scalar-read arms; a binding whose type is
	// unresolved (or a type variable) stays tracked with no usable type, so
	// none of its reads is credited.
	tracked := map[string]bool{pn: true}
	typeOf := map[string]ast.Type{}
	for _, p := range fn.Params {
		if p.Name == pn {
			typeOf[pn] = p.Type
		}
	}
	for {
		grew := false
		track := func(tag ast.Expr, names []string, types []ast.Type) {
			id, ok := tag.(*ast.Ident)
			if !ok || !tracked[id.Name] {
				return
			}
			for i, nm := range names {
				var bt ast.Type
				if i < len(types) {
					bt = types[i]
				}
				if _, isVar := bt.(ast.ParamType); bt != nil && !isVar && !ast.IsPointerType(bt) {
					continue
				}
				if !tracked[nm] {
					tracked[nm] = true
					typeOf[nm] = bt
					grew = true
				}
			}
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch m := n.(type) {
			case *ast.Match:
				for _, arm := range m.Arms {
					track(m.Tag, arm.Bindings, arm.BindingTypes)
				}
			case *ast.MatchExpr:
				for _, arm := range m.Arms {
					track(m.Tag, arm.Bindings, arm.BindingTypes)
				}
			}
			return true
		})
		if !grew {
			break
		}
	}
	// fieldTypeOf resolves `owner.field` for a tracked struct-typed name. An
	// enum or a tuple has no declaration to consult, so the answer is "no
	// such field" and the projection arms simply never credit it.
	fieldTypeOf := func(owner, field string) (ast.Type, bool) {
		st, ok := typeOf[owner].(ast.StructType)
		if !ok {
			return nil, false
		}
		sd, ok := info.Structs[st.Name]
		if !ok {
			return nil, false
		}
		for _, f := range sd.Fields {
			if f.Name == field {
				return f.Type, true
			}
		}
		return nil, false
	}
	safe := map[*ast.Ident]bool{}
	// markSlotValue credits a p-use that sits directly in a counted position —
	// a construction slot or a counted call argument: a bare `p` (the whole
	// struct is inc'd in), a `p.field` (a pointer field is inc'd, a scalar is
	// copied), or an element `xs[i]` / `p.field[i]` read straight into the
	// position (inc'd or copied the same way).
	markSlotValue := func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.Ident:
			if tracked[v.Name] {
				safe[v] = true
			}
		case *ast.FieldAccess:
			if id, ok := v.Target.(*ast.Ident); ok && tracked[id.Name] {
				safe[id] = true
			}
		case *ast.Index:
			if v.IsString || v.IsSlice {
				return
			}
			switch arr := v.Array.(type) {
			case *ast.Ident:
				if tracked[arr.Name] {
					safe[arr] = true
				}
			case *ast.FieldAccess:
				if id, ok := arr.Target.(*ast.Ident); ok && tracked[id.Name] {
					safe[id] = true
				}
			}
		}
	}
	// scalarElemRead reports the tracked root of an index read whose element
	// is a plain scalar — `p.tail[i]`, `xs[i]` on a tracked array binding, or
	// `b[i]` through a tracked `[T]` view — which copies a value out and
	// retains nothing. A slice reads through its header into storage the
	// header does not own, so the copy aliases neither.
	scalarElemRead := func(x *ast.Index) (*ast.Ident, bool) {
		var root *ast.Ident
		var at ast.Type
		switch arr := x.Array.(type) {
		case *ast.Ident:
			root, at = arr, typeOf[arr.Name]
		case *ast.FieldAccess:
			id, ok := arr.Target.(*ast.Ident)
			if !ok {
				return nil, false
			}
			root = id
			at, _ = fieldTypeOf(id.Name, arr.Field)
		default:
			return nil, false
		}
		var elem ast.Type
		switch c := at.(type) {
		case ast.ArrayType:
			elem = c.Elem
		case ast.SliceType:
			elem = c.Elem
		default:
			return nil, false
		}
		if !tracked[root.Name] {
			return nil, false
		}
		switch elem.(type) {
		case ast.NumberType, ast.BoolType, ast.FloatType:
			return root, true
		}
		return nil, false
	}
	total := 0
	seedOK := countedSeedOccurrences(fn)
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if tracked[x.Name] {
				total++
				if seedOK[x] {
					safe[x] = true
				}
			}
		case *ast.Match:
			// The bindings are tracked (above), so the match reads the box
			// without anything escaping uncounted.
			if id, ok := x.Tag.(*ast.Ident); ok && tracked[id.Name] {
				safe[id] = true
			}
		case *ast.MatchExpr:
			if id, ok := x.Tag.(*ast.Ident); ok && tracked[id.Name] {
				safe[id] = true
			}
		case *ast.StructLit:
			for _, f := range x.Fields {
				markSlotValue(f.Value)
			}
			// A struct-update SPREAD base is a counted retention like a field
			// slot: the literal copies each un-overridden field out of the base
			// and inc's every pointer one, so the new box owns references of its
			// own. Without this the functional-update threading method
			// `S { ...s, f: … }` — the shape the self-host lowering is written
			// in — could never be credited, and its caller kept the
			// conservative taint on every intermediate.
			markSlotValue(x.Base)
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
			if id, ok := x.Target.(*ast.Ident); ok && tracked[id.Name] {
				if ft, ok := fieldTypeOf(id.Name, x.Field); ok && !ast.IsPointerType(ft) {
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
			// nothing; neither does a scalar element read.
			if x.IsString {
				markSlotValue(x.Array)
			} else if root, ok := scalarElemRead(x); ok {
				safe[root] = true
			}
		case *ast.Call:
			// A `p` / `p.field` passed as argument i to a call whose callee
			// parameter i is counted-retain is inc'd (or read) there, not
			// aliased out — so it is safe, exactly like a construction slot.
			// A builtin / external callee is absent from `summary`, so its
			// arguments stay uncredited (the map-mutator receiver guard).
			if id, ok := x.Callee.(*ast.Ident); ok {
				// Calling THROUGH a function-typed `p` loads the pair and
				// dispatches; the closure itself is not retained.
				if tracked[id.Name] {
					safe[id] = true
				}
				if pureReadReceiverBuiltin(id.Name) && len(x.Args) > 0 {
					markSlotValue(x.Args[0])
				}
				// A copying builtin memcpys or writes the bytes out and
				// its result cannot alias the argument — the per-argument
				// generalisation of the receiver rule above.
				for ai, a := range x.Args {
					if copyingBuiltinArg(id.Name, ai) {
						markSlotValue(a)
					}
				}
				// `p.f.append(v)` retains the field buffer COUNTED whichever
				// path the grow helper takes: in place it sets the buffer's rc
				// to 2 so the result co-owns it alongside `p.f`, and on the copy
				// path it allocates a fresh buffer and leaves the receiver's
				// count untouched.
				//
				// The ELEMENT is counted too, and for a plainer reason:
				// `emitArrayPush` emits `emitAliasInc(Args[1])` whenever
				// `needsRcIncOnAlias` holds and the value is not a move site.
				// A bare parameter satisfies both — the predicate is true for
				// every pointer type, and `isOwnedRcLocal` (which gates every
				// move site) walks `info.Locals` only, so a parameter is never
				// one. So the buffer co-owns a counted reference and the
				// caller's is_unique-gated drop nets the temp to a single
				// owner. Without this the caller's fresh argument was
				// stranded: 2 blocks a round for an element carrying a heap
				// field, linear and unbounded (#7867).
				if id.Name == "__method_Array_push" && len(x.Args) == 2 {
					markSlotValue(x.Args[0])
					markSlotValue(x.Args[1])
				}
				// `.with`'s element position is push's element position:
				// emitArraySet incs an aliased pointer-shaped element and
				// the buffer's deep drop gives it back. The RECEIVER is
				// deliberately absent — `.with` hands the receiver's own
				// buffer back at rc 1, which is not a retention the caller
				// can discount.
				if id.Name == "__method_Array_set" && len(x.Args) == 3 &&
					len(x.TypeArgs) == 1 && rcTrackedSlotType(x.TypeArgs[0]) {
					markSlotValue(x.Args[2])
				}
				if cs, ok := summary.get(id.Name); ok {
					for i, a := range x.Args {
						if i < len(cs) && cs[i] {
							markSlotValue(a)
						}
					}
				}
			}
			// A variant-constructor payload is a counted slot exactly like a
			// StructLit field: a bare `p` is inc'd in, a `p.field` is inc'd
			// (pointer) or copied (scalar).
			if ctorCounted(x) {
				for _, a := range x.Args {
					markSlotValue(a)
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
	return everyOccurrenceSafe(total, len(safe))
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
		// Structs / tuples / enums (incl. unions) / ARRAYS. Whatever the shape,
		// a reassignment of a param slot emits the overwrite dec — so leaving a
		// reassigned param on the borrow baseline releases a reference the
		// caller never handed over. Enums were excluded here until the
		// parse_postfix under-count (`base = e_unary_at(op, base, …)` on a
		// borrowed `Expr` param, whose new node KEEPS the old value) showed the
		// exclusion is exactly the escape hatch the paragraph below closes for
		// scalar-only structs: same one-reference undercount, same early free
		// through a live alias. See TestX86_64UnionThreadedParam.
		//
		// ARRAYS were the last shape left out, and the sentence above already
		// said why they should not be: #6021 is the same undercount reached
		// through `acc = f(.., acc)` on a borrowed `string[]` param. It sat
		// latent on main because the Assign catch-all's plain __fern_rc_dec
		// does not FREE — it only corrupts the count, and the early free then
		// happens at whichever site legitimately owns the buffer. In the
		// self-host compiler that was astwalk.collect_idents_stmt's StmtIf arm
		// stealing a count from irlower.precise_drop_names' `none`, which
		// surfaced as a ~50% segfault in __fern_alloc's freelist pop on any
		// change that shifted allocation sizes. See the
		// array_param_threaded_by_reassignment rc-corpus case.
		//
		// Arrays do NOT pay for this with an entry retain, though — they carry
		// a hidden ownership flag instead (isConsumedArrayParam). An entry
		// retain would make the incoming rc 2, and rc==1 is exactly the
		// uniqueness test __fern_arr_push_grow's in-place fast path gates on,
		// so every append in the function — and in everything it threads the
		// buffer through — would copy the whole buffer. See
		// emitConsumedArrayOverwriteDec.
		switch p.Type.(type) {
		case ast.StructType, ast.TupleType, ast.EnumType, ast.ArrayType:
		default:
			continue
		}
		// Only types owned-by-default EXCLUDES (ownedByDefaultShape, read
		// without the flag): a shape the owned model covers is already
		// handled by it when the flag is on and must stay on the borrow
		// baseline when it is off; promoting it here would diverge the
		// OwnedByDefault-vs-borrow differential gate. typeDeepDropWired keeps
		// Map / slice / unwired shapes out (their deep drop is incomplete).
		//
		// EXCEPT when borrow inference DEMOTED the param (verdict Borrowed):
		// the caller then passes without an inc, and the reassignment's
		// overwrite dec releases a reference the callee never owned — the
		// caller's box rc undercounts, its own later is_unique-gated drop
		// frees early, and the still-live aliases double-free (the
		// `c = c2;` cursor-threading loop in a recursive `(T, Cur)`-tuple
		// reader was the repro: freelist link clobbered by a reused block's
		// rc header). The OwnedByDefault-vs-borrow differential gate is
		// unaffected: with the flag off the verdict is NotOwnedType, not
		// Borrowed, and the skip below still fires.
		if b.ownedByDefaultShape(p.Type) &&
			b.paramVerdict(b.fn.Name, p.Type, i) != paramVerdictBorrowed {
			continue
		}
		if !deepDropWired(b.info, p.Type) {
			continue
		}
		res[p.Name] = true
	}
	return res
}

// computeCowThreadedMapParams finds the borrow-baseline Map params the body
// self-mutates (`m = m.insert(..)`); see rcState.cowMapParams.
func (b *builder) computeCowThreadedMapParams() map[string]bool {
	res := map[string]bool{}
	if !ast.RcFreeEnabled || b.fn.Body == nil {
		return res
	}
	for i, p := range b.fn.Params {
		st, isStruct := p.Type.(ast.StructType)
		if !isStruct || st.Name != "Map" || p.Own || b.paramOwnedByDefault(p.Type, i) || b.rc.consumedParams[p.Name] {
			continue
		}
		name := p.Name
		ast.Walk(b.fn.Body, func(n ast.Node) bool {
			if a, ok := n.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok && id.Name == name && isSelfMapMutation(a.Value, name) {
					res[name] = true
				}
			}
			return true
		})
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
// countedBindingAlias reports whether `target = e` emits a transfer inc, making
// `target` an owner rather than an inheritor of `e`'s borrow taint.
//
// It holds when `e` is a BINDING — a match / if-let / let-else / for-in name,
// which is neither a declared local nor a parameter. Such a name is
// borrow-tainted at its own site (it aliases an enum payload with no projection
// dup), but `keep = g` is not that alias: needsRcIncOnAlias fires on the
// assignment, so `keep` takes a reference of its own and reclaims through
// __fern_arr_dec — whose is_unique gate can never free out from under the
// binding.
//
// The two cancellations that would remove that inc cannot reach here, which is
// what makes the untaint sound rather than optimistic: a move site needs an
// owned rc LOCAL source (computeMovedLocals' isOwnedRcLocal), and so does a
// borrowed-alias site. A binding is neither, so the inc the Assign lowering
// emits is unconditional. That matters because moveSites is not populated yet
// when this analysis runs — it is built from computeFreeEligible's result.
//
// The countedness is read off the TARGET's declared type, not the binding's. A
// binding has no resolvable type this early — needsRcIncOnAlias answers false
// for one here and true at the lowering site, which is the trap this predicate
// exists to avoid — while the checker guarantees the two types match, and
// isOwnedRcLocal tests exactly the type set needsRcIncOnAlias does.
//
// Parameters are deliberately excluded: `var L = <param>` has its own, narrower
// untaint (countedSeed, gated on L being reassigned and uniquely named) with a
// separate history (#6403, #4174), and widening it is not this rule's business.
//
// Without this, a match arm handing its binding to an outer local left that
// local borrow-tainted, so its overwrite-dec fell back to the flat
// __fern_rc_dec that decrements without reclaiming — one leaked buffer per
// assignment (#7163).
func (b *builder) countedBindingAlias(target string, e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	if b.isOwnedRcLocal(id.Name) || b.paramNamed(id.Name) != nil {
		return false
	}
	return b.isOwnedRcLocal(target)
}

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
// Taint sources: parameters; for-in / match / if-let / let-else
// bindings; locals that ESCAPE into an UNCOUNTED sink (a capture cell,
// a raw-pointer cast, a container store its lowering does not retain —
// retained without an inc, so the owner must not free out from under
// them). The container stores that DO retain — array push / .with,
// struct / tuple / rc-enum construction, if- / match-expr yields, map
// insert — are counted and taint nothing (#4399).
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
	// escaped is the subset of `tainted` that reached an UNCOUNTED sink
	// (`escape` below, propagated backward through aliases like the rest of
	// the taint): the value may be held somewhere no count records. The
	// remaining taint only says a slot may alias something it did not
	// retain, which forbids a precise drop but not the release of a count
	// the frame genuinely owns — see the owned-param loop at the end.
	escaped := map[string]bool{}
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
	// seedParamInit[L] is the `var L = <param>` initialiser of a local seeded
	// from a borrow-tainted PARAMETER, and reassignedIdent records the locals an
	// *ast.Assign later overwrites. countedSeed below combines them.
	seedParamInit := map[string]ast.Expr{}
	reassignedIdent := map[string]bool{}
	// countedAssign[rhs] marks an `L = <binding>` whose lowering emits the
	// transfer inc, so L owns a reference of its own and does not inherit the
	// binding's borrow taint. Populated in the *ast.Assign case below.
	countedAssign := map[ast.Expr]bool{}
	markBindings := func(names []string) {
		for _, n := range names {
			tainted[n] = true
		}
	}
	// escape taints a local that flows into an UNCOUNTED retain sink: a
	// value the sink holds without an inc (a capture cell, a struct /
	// enum map key, a kind-1 map value, a non-rc enum payload). Freeing
	// the local at scope exit would then use-after-free the alias the
	// sink still holds.
	//
	// A pointer-shaped value read OUT of a container and retained into
	// such a sink — `outer.insert(k, inner_maps[i])`, `Arr(grid[j])` —
	// copies the pointer without an inc too, so the SOURCE container
	// (`inner_maps` / `grid`) must not free it out from under the sink
	// either. escape unwraps such projection chains (index / field /
	// array-slice) to the root local and taints that.
	// The unwrap is gated on the projected value being pointer-shaped:
	// a scalar element (`i32[]`) can't alias, so its source stays
	// reclaimable. A string slice copies into a fresh owned buffer
	// (not a view), so it isn't unwrapped.
	//
	// syncCast: the `buf as usize` arguments of a __memcpy / __memset.
	// The raw address dies with the call, so these are exempt from the
	// CastExpr escape taint below (#8403).
	syncCast := map[*ast.CastExpr]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.Call); ok && syncByteCopyCall(call) {
			for _, a := range call.Args {
				if c, ok := a.(*ast.CastExpr); ok {
					syncCast[c] = true
				}
			}
		}
		return true
	})
	var escape func(e ast.Expr)
	escape = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.Ident:
			tainted[x.Name] = true
			escaped[x.Name] = true
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
	// escapeOwned is the variant for the INC-ing sinks (StructLit / TupleLit
	// construction, rc-eligible enum payloads, the array element stores).
	// Each of those dups a `needsRcIncOnAlias` source into the container
	// under exactly that predicate, and the container's deep drop releases
	// the dup, so a counted source keeps a reference of its OWN and stays
	// reclaimable — the same rule ArrayLit elements have always had (no
	// taint arm at all) and escapeCountedYield applies to arm yields.
	// Tainting a counted source strands precisely that reference, because
	// the sweep then falls back to the non-freeing flat `__fern_rc_dec`
	// (#7345); it also needlessly defeats constructor reuse
	// (TestStructReuseFiresForPointerField). A last-use source takes the
	// move instead (markConstructionMoves), which skips the dup and drops
	// the name from the sweep, so that path is balanced too.
	//
	// What remains is the source a sink stores UNCOUNTED: `dyn Trait`, which
	// needsRcIncOnAlias declines (dyn cells carry no rc header, so
	// construction must not inc them). Freeing such a local at scope exit
	// would reclaim the cell the container still holds.
	escapeOwned := func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok {
			return
		}
		// A consuming-match binding (#4400) is a counted owner the same way,
		// but by NAME rather than by type: it is not a declared local, so
		// `exprType` has nothing to classify and needsRcIncOnAlias reads
		// false for it. Tainting it would skip the sweep and leak the dup —
		// `Cons(h, t) => return Cons(h + 1, t)` would leak the whole tail per
		// call. The UNCOUNTED sinks (escape) still taint these names.
		if _, owned := b.rc.consumingBindings[id.Name]; owned {
			return
		}
		if !needsRcIncOnAlias(id, b) {
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
	// escapeMapEntry is the map-store variant (#4399 sink 5): a key or
	// value the store retains (mapSetKeyCounted / mapSetValueCounted —
	// the predicate emitMapSetRetains itself runs) is co-owned by the
	// column and released by the map's drop, so its source keeps a
	// reference of its own; any other entry side keeps the escape walk.
	escapeMapEntry := func(key, val ast.Expr, kType, vType ast.Type) {
		if !b.mapSetKeyCounted(key, kType) {
			escape(key)
		}
		if !b.mapSetValueCounted(val, vType) {
			escape(val)
		}
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.Var:
			if s.Init != nil {
				assigns[s.Name] = append(assigns[s.Name], s.Init)
				if id, ok := s.Init.(*ast.Ident); ok && b.paramNamed(id.Name) != nil &&
					needsRcIncOnAlias(id, b) {
					seedParamInit[s.Name] = s.Init
				}
			}
		case *ast.Assign:
			if id, ok := s.Target.(*ast.Ident); ok {
				assigns[id.Name] = append(assigns[id.Name], s.Value)
				reassignedIdent[id.Name] = true
				if b.countedBindingAlias(id.Name, s.Value) {
					countedAssign[s.Value] = true
				}
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
		case *ast.Call:
			// Retain sinks the checker lowers to Calls. Each is a
			// COUNTED store for the shapes its lowering retains and
			// its container's drop releases; only the uncounted
			// remainder of each keeps the escape taint.
			if id, ok := s.Callee.(*ast.Ident); ok {
				switch {
				case id.Name == "__method_Map_set":
					// `m.insert(k, v)` — Args[0] is the map (threaded /
					// reassigned), not retained. The key and value
					// columns are COUNTED stores (#4399 sink 5) exactly
					// where emitMapSetRetains retains — mapSetKeyCounted /
					// mapSetValueCounted are the lowering's own predicate
					// — and appendMapDropChain walks those columns at the
					// map's drop, so the source is co-owned and stays
					// reclaimable. The uncounted remainder (a struct /
					// enum key, a kind-1 value such as a nested Map)
					// keeps the escape walk.
					if len(s.Args) == 3 {
						var kType, vType ast.Type
						if len(s.TypeArgs) >= 1 {
							kType = s.TypeArgs[0]
						}
						if len(s.TypeArgs) >= 2 {
							vType = s.TypeArgs[1]
						}
						escapeMapEntry(s.Args[1], s.Args[2], kType, vType)
					} else {
						for _, a := range s.Args[1:] {
							escape(a)
						}
					}
				case id.Name == "__method_Array_push":
					// Args[0] is the receiver array (threaded /
					// reassigned), not retained. The element is a COUNTED
					// store (#4399 sink 1): emitArrayPush emits the
					// needsRcIncOnAlias element inc (the same Ident /
					// FieldAccess / Index shapes `escape` walks), and the
					// buffer's deep drop decs elements — so the source is
					// co-owned by the buffer and stays reclaimable
					// (escapeOwned, the same rule StructLit / TupleLit /
					// rc-eligible enum payloads take).
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
					// __fern_arr_cow_inplace_ptr — so the source stays
					// reclaimable (escapeOwned, the Array_push rule).
					//
					// A scalar element can't alias but also can't strand
					// anything, so the escape walk's pointer gate already
					// no-ops it.
					if len(s.Args) == 3 {
						if len(s.TypeArgs) == 1 && rcTrackedSlotType(s.TypeArgs[0]) {
							escapeOwned(s.Args[2])
						} else {
							escape(s.Args[2])
						}
					}
				default:
					// Variant constructor (`Arr(xs)`): under the move model
					// emitEnumNew stores the payload without an inc, so a local
					// passed as a payload escapes into the box (full escape). Under
					// EnumRcPayloads it inc's like StructLit, so the payload is
					// co-owned and its source stays reclaimable — escapeOwned.
					if _, isLocal := b.locals[id.Name]; !isLocal {
						if en, _, _, isVariant := b.lookupVariantOn(id.Name, id.EnumName); isVariant {
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
							// A builtin that COPIES its source rather than
							// retaining it cannot hold the caller's buffer,
							// so the taint is protecting against nothing.
							// `paramCountedRetain` is keyed by user
							// declaration, so a builtin has no entry and the
							// exemption below never reaches one — which cost
							// `slice_unchecked(line, a, b)` its source's
							// scope-exit drop entirely, one leaked buffer per
							// source, on top of the unreleased slice itself
							// (#7876). pureReadReceiverBuiltin is the same
							// fact this file already states three other
							// places: "copies bytes OUT of its string
							// receiver into a fresh buffer".
							pureRead := pureReadReceiverBuiltin(id.Name)
							counted := b.paramCountedRetain[id.Name]
							for ai, a := range s.Args[argStart:] {
								if aid, ok := a.(*ast.Ident); ok {
									if _, isStr := b.exprType(aid).(ast.StringType); isStr {
										// The receiver position of a
										// copying builtin. Only argument 0
										// can be one: the `__method_` forms
										// have argStart == 1, so their
										// receiver never reaches this loop.
										if pureRead && ai == 0 {
											continue
										}
										// A copying-builtin argument: the
										// bytes are copied or written out
										// and nothing retains the string,
										// so the binding must keep its
										// scope-exit drop. Without this,
										// `var msg = pfx + body;
										// strbuf_append(msg);` stranded
										// msg's buffer once per call — the
										// bound-local half of #7867's
										// slice 2, distinct from the
										// argument-temp half countedArgTemp
										// fixes.
										if copyingBuiltinArg(id.Name, ai+argStart) {
											continue
										}
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
			// `Map { k: v }` stores each entry through the same
			// emitMapSetRetains the insert form takes, so it is the
			// same counted store.
			for _, ent := range s.Entries {
				escapeMapEntry(ent.Key, ent.Value, s.KeyType, s.ValueType)
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
			if syncCast[s] {
				break
			}
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
	// `var L = <param>` where L is REASSIGNED later: the binding is a COUNTED
	// alias, not a borrow. needsRcIncOnAlias holds for the initialiser and the
	// source is a parameter, so no move site or borrowed-alias cancellation can
	// reach it (both require an owned rc LOCAL source) and the *ast.Var lowering
	// therefore emits the transfer inc — L owns a reference of its own, and every
	// drop of it is is_unique-gated, so it can never release the caller's value.
	// Only the SEED is skipped: every other taint source still reaches L, and a
	// local that is never reassigned holds nothing but the seed and keeps the
	// borrow verdict. One verdict per local governed every later value the slot
	// held, and each of those leaked (#6403).
	countedSeed := map[ast.Expr]bool{}
	for name, init := range seedParamInit {
		if reassignedIdent[name] && b.localNameUnique(name) {
			countedSeed[init] = true
		}
	}
	for {
		changed := false
		for name, rhss := range assigns {
			for _, rhs := range rhss {
				if !tainted[name] && !countedSeed[rhs] && !countedAssign[rhs] && b.rhsTainted(rhs, tainted) {
					tainted[name] = true
					changed = true
				}
				// Backward alias propagation: a tainted local
				// assigned a bare Ident shares that source's
				// buffer, so the source must not be freed either
				// (`tmp = arr; m.set(k, tmp)` taints arr too) — and
				// an escaped one carries the escape back with it.
				if tainted[name] {
					if src, ok := rhs.(*ast.Ident); ok && !tainted[src.Name] {
						tainted[src.Name] = true
						changed = true
					}
					if src, ok := rhs.(*ast.Ident); ok && escaped[name] && !escaped[src.Name] {
						escaped[src.Name] = true
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
		case ast.SliceType:
			// An owned slice frees its header at the last reference
			// (emitDec → __fern_closure_drop); the viewed bytes belong to
			// the source and are never touched.
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
		// promoted to callee-owned the same way.
		//
		// An `own` param that was tainted at all is skipped (a safe leak). An
		// owned-by-default / consumed param is skipped only when its value
		// ESCAPED into an uncounted sink: the caller retained it (or the
		// entry inc did), so the frame holds a count it must spend at exit,
		// and every reference the frame can leave behind is counted — a
		// returned payload takes the transfer inc, a stored one the
		// construction inc — so the is_unique-gated deep drop cannot free
		// under anything live. Alias taint alone (`var cur = t; cur = l`, the
		// walk every tree lookup is written as) used to keep the count
		// forever: one whole tree per call.
		if !p.Own && !b.paramOwnedByDefault(p.Type, i) && !b.rc.consumedParams[p.Name] {
			continue
		}
		if p.Own && tainted[p.Name] {
			continue
		}
		if !p.Own && escaped[p.Name] {
			continue
		}
		switch t := p.Type.(type) {
		case ast.ArrayType, ast.EnumType, *ast.FuncType, ast.TupleType, ast.SliceType:
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
	case *ast.MapLit:
		// `Map { … }` lowers to a fresh `map_new` plus one `__method_Map_set`
		// per entry — the same construction `var m = map_new(n)` followed by
		// inserts produces — so the local owns the handle and aliases nothing.
		// The entries' own sources take computeFreeEligible's MapLit arm,
		// exactly as the insert form's `__method_Map_set` args do.
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
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit:
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
		// every iteration. Gated to a struct source this function holds a
		// slot for — a local, a parameter or a match binding — reached
		// directly or through a chain of struct / tuple fields (`r.a.b`):
		// every container on the chain deep-drops the next one, so the
		// counted-alias argument holds at each level, and the two-level
		// read taints nothing the one-level read does not (#8179, the
		// self-host LowerState's nested per-function box). A non-struct
		// source, or a Map anywhere on the chain, keeps the conservative
		// taint. The escape sink walk is unchanged, so a projection flowing
		// into an UNCOUNTED sink (`m.set(k, r.field)`) still taints its
		// source there.
		// A TUPLE local is the same argument, and was one type short of it.
		// `var q: P = p.1` incs at the binding site exactly as the struct read
		// does, and the tuple deep-drops its elements at scope exit
		// (__drop_tuple_<…>), so the extracted element owns its own reference.
		// Falling through to the conservative taint left `q` un-reclaimable
		// and the inc unbalanced: the element leaked once per extraction,
		// unbounded, and grew with the element's width rather than the
		// tuple's. `(value, state)` threading is where this shows up, and
		// reading the field straight through — `p.1.a` — was flat all along.
		//
		// A MAP element is credited too, but only because the tuple now holds
		// a reference of its own. It did not: the delete tuple stored the
		// receiver's handle uncounted, so crediting the projection gave the
		// destination a deep drop of a map the tuple still referenced, and
		// segfaulted map_delete_tuple_churn_free on both natives. The
		// COW-seam retain above is what supplies that count (#8276), and the
		// two are a pair — crediting here without it is the segfault, and
		// retaining there without this is a count nobody returns. The rest of
		// the counted-alias argument carries over unchanged.
		if id, ok := b.projectionChainRoot(x.Target).(*ast.Ident); ok {
			_, isLocal := b.locals[id.Name]
			// A MATCH-ARM BINDING is the same case and was invisible to
			// it. `b.locals` is the lowering's slot map, which
			// bindingSlotScoped fills as it enters an arm — this
			// analysis runs before that, so `Some(t) => cur = t.1` read
			// as a projection out of nothing and took the conservative
			// taint. `cur` then missed freeEligible, its overwrite dec
			// routed to the generic __fern_rc_dec rather than
			// __fern_arr_dec, and that helper decrements to zero without
			// reclaiming — so the element leaked once per arm execution
			// (docs/rc-log/2026-08-30-match-binding-rebind-overretain.md).
			//
			// The counted-alias argument carries over unchanged: the
			// bound tuple deep-drops its elements when the arm ends
			// (__drop_tuple_<…> is emitted for the binding), so the
			// extracted element owns its own reference exactly as it
			// does out of a declared local. Maps stay excluded for the
			// reason the comment above gives.
			bt, isBinding := b.matchBindingTypes()[id.Name]
			if isLocal || isBinding {
				t := b.exprType(id)
				if isBinding && t == nil {
					t = bt
				}
				switch t.(type) {
				case ast.StructType:
					return false
				case ast.TupleType:
					return false
				}
			}
		}
		// A FRESH owned container is the same counted alias, one step
		// further: `mk_box().items` has no local to reclaim the container at
		// all, so the FieldAccess lowering retains the loaded field and then
		// deep-drops the container, which nets the field to this
		// expression's own single reference (#6401). That leaves the
		// destination genuinely owning it — the conservative taint would
		// strand the retain instead, which is what turned a 32 B/round
		// container leak into a 64 B/round field leak when the lowering
		// landed on its own.
		//
		// Gated on exactly the predicate that lowering uses, so the two
		// cannot disagree: a struct/tuple temp the borrow analysis proved
		// fresh, or a delete tuple the COW seam retained. A MAP element out
		// of one is credited on the same terms as out of a bound tuple — the
		// seam supplies the count the container's drop spends — so it is the
		// seam, not the element type, that decides.
		if b.freshOwnedFieldContainer(x.Target) {
			return false
		}
		return true
	case *ast.Index:
		// The fresh-owned-container argument again, one expression form
		// over: `mk_strs()[0]` has no local to reclaim the container, so the
		// index lowering retains the loaded element and deep-drops the
		// container, netting the element to this expression's own single
		// reference. The conservative taint would strand that retain — the
		// same 304 B/round → 64 B/round half-fix the field read went through.
		// Gated on exactly the predicate that lowering uses. A string index
		// yields a scalar byte and a slice index a borrowed view, so neither
		// is this shape; maps stay out for the reason the field case gives.
		if !x.IsString && !x.IsSlice && b.freshOwnedIndexContainer(x.Array) &&
			!isMapType(b.exprType(x)) {
			return false
		}
		// An element read out of an array-typed LOCAL is the counted alias the
		// struct/tuple field read above describes, one container over:
		// needsRcIncOnAlias fires for a pointer-shaped element, so the binding
		// site inc's it and the destination owns a reference of its own. The
		// conservative taint instead pinned the destination for the whole
		// function, so `var line = words[0]` followed by `line = line + …`
		// stranded every intermediate concat (#6567) — a seed of `""` was flat
		// all along, which is what made the shape easy to miss.
		if !x.IsString && !x.IsSlice && !isMapType(b.exprType(x)) && needsRcIncOnAlias(x, b) {
			if id, isIdent := x.Array.(*ast.Ident); isIdent {
				if _, isLocal := b.locals[id.Name]; isLocal {
					if _, isArr := b.exprType(id).(ast.ArrayType); isArr {
						return false
					}
				}
			}
		}
		return true
	case *ast.SliceExpr:
		// A STRING slice copies its bytes into a fresh owned heap buffer
		// (the wasm runtime always allocates) and boxes it in a fresh
		// `Option`, so both are reclaimable — not views. Keyed on
		// IsString, NOT on exprType: exprType now reports the Option box,
		// so a type test would taint the fresh box and downgrade the
		// consumer's reclaim to the non-freeing __fern_rc_dec.
		//
		// An ARRAY slice is a fresh rc=1 header over the source's buffer.
		// The header is what the local owns and what its drop releases;
		// the shared buffer is never touched by that release, so the
		// source's taint says nothing about it.
		return false
	case *ast.Binary:
		// String concat (`a + b`) copies both operands into a fresh owned
		// heap buffer regardless of operand provenance, so the result is
		// always reclaimable.
		//
		// Any other binary yields a scalar, which can alias a borrowed value
		// only by carrying a raw pointer (`(buf as usize) + 8`), and that
		// provenance is already on the operand: the pointer→integer cast and
		// a parameter both read as tainted. The result inherits exactly its
		// operands' taint, so a literal-seeded counter stays untainted
		// through `i = i + 1` and a call taking it as an argument keeps a
		// reclaimable result — the blanket taint stranded `t = bump(t, i)`
		// at exit while `t = bump(t, 1)` reclaimed it.
		if x.IsStringConcat {
			return false
		}
		return b.rhsTainted(x.Left, tainted) || b.rhsTainted(x.Right, tainted)
	case *ast.Unary:
		// `-x` on a composite is a method call (NegCall); its result takes
		// the conservative default.
		if x.NegCall != nil {
			return true
		}
		return b.rhsTainted(x.Operand, tainted)
	case *ast.Call:
		// A slice-typed result always hands the caller exactly one unit on
		// the HEADER: a fresh __slice_make, a moved local, or a returned
		// param / field / element alias carrying the return-transfer inc (a
		// param is never an owned local, so move-on-return cannot cancel
		// it). Whether the viewed bytes alias an argument is immaterial —
		// the binding's release frees the header alone.
		if isSliceType(b.exprType(x)) {
			return false
		}
		// Slice 1b: under EnumRcPayloads a variant constructor is a FRESH
		// rc=1 box that inc's its pointer payloads (like StructLit), so the
		// constructed value is reclaimable regardless of payload taint — return
		// false, mirroring the StructLit/TupleLit cases. Without this the generic
		// any-arg-tainted recursion below propagates a tainted nullary-variant
		// arg (`Nil`) up to the enum local, leaving it permanently ineligible.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isLocal := b.locals[id.Name]; !isLocal {
				if en, _, _, isVar := b.lookupVariantOn(id.Name, id.EnumName); isVar && b.enumRcPayloadsEligible(en) {
					return false
				}
			}
		}
		// A callee whose every value return CONSTRUCTS its result hands
		// back a box of its own, so an argument's borrow taint says
		// nothing about it. This is the general form of the carve-outs
		// around it — the variant constructor above, map_new and cell_new
		// below — and it is what keeps a recursive-descent parser's
		// `RParse { node: …, pos: … }` result reclaimable even though the
		// pattern string it was parsed from is a borrowed parameter.
		// findReturnsFreshBox says why returnsNoParamEscape cannot serve.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isLocal := b.locals[id.Name]; !isLocal && b.returnsFreshBox[id.Name] {
				return false
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
			case "__method_Map_get_or":
				// A counted-read VALUE (array / struct / enum) comes back
				// retained on both outcomes (__map_get_or_impl), so the
				// binding owns a reference of its own and reclaims it —
				// ownedCallResultType's admission, from the taint side. A
				// string value keeps the conservative default: its inline
				// retain is per-ABI (the get_or lowering).
				if len(x.TypeArgs) >= 2 {
					if _, isStr := x.TypeArgs[1].(ast.StringType); !isStr && b.mapGetHandsCountedValue(x.TypeArgs[1]) {
						return false
					}
				}
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
			case "__alloc_u8":
				// A fresh zero-filled rc=1 buffer straight from the runtime
				// allocator (or the static empty sentinel for n==0, which
				// every dec no-ops on). Its only argument is a SCALAR byte
				// count, so the result cannot alias it however tainted that
				// count is — the generic any-arg-tainted rule below said
				// otherwise and left every dynamically-sized buffer
				// permanently ineligible: `__alloc_u8(16)` (literal, untainted)
				// reclaimed but `__alloc_u8(n_bytes)` did not, leaking one
				// buffer per call out of int_to_string / __int_to_string_u64 /
				// int_to_string_radix — i.e. out of every `n.to_string()`
				// (#5931). Untainting the ALLOCATOR is narrower than untainting
				// the scalar-binary size expression itself (see the *ast.Binary
				// case below): the buffer stays protected by the CastExpr
				// escape taint whenever the function threads it as a raw
				// `buf as usize` pointer, and by the escape / move analysis
				// whenever it flows into a container or out through a return.
				return false
			case "random_bytes", "tcp_recv":
				// A fresh rc=1 u8[] in the __alloc_u8 box shape on every
				// backend (CSPRNG bytes / one socket read). Same argument
				// as __alloc_u8 above: every argument is a scalar (byte
				// count, fd), so the result cannot alias one.
				return false
			case "slice_unchecked":
				// Copies bytes OUT of its source into a fresh owned
				// buffer (the __str_slice contract) — the string
				// SliceExpr arm above, spelled as a builtin, so the
				// source's taint says nothing about the result.
				return false
			case "__method_string_as_bytes":
				// A fresh rc=1 slice header viewing the receiver's bytes —
				// the array SliceExpr arm above, spelled as a method. Only
				// the header is owned and released.
				return false
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

// projectionChainRoot walks a field-read chain down to the expression it
// projects out of, stepping only through struct / tuple fields that are not
// maps: for `s.frame.alias` (Target `s.frame`) it returns `s`; for a direct
// read (Target `s`) it returns `s` unchanged. A step through any other type
// stops the walk there, so the caller's Ident test fails for it.
func (b *builder) projectionChainRoot(e ast.Expr) ast.Expr {
	for {
		fa, ok := e.(*ast.FieldAccess)
		if !ok {
			return e
		}
		switch t := b.exprType(fa).(type) {
		case ast.StructType:
			if isMapType(t) {
				return e
			}
		case ast.TupleType:
		default:
			return e
		}
		e = fa.Target
	}
}

func (b *builder) computeMovedLocals() map[string]bool {
	moved := map[string]bool{}
	if b.fn.Body == nil {
		return moved
	}
	order := b.curIdentOrder()
	sawExit := false
	for _, st := range b.fn.Body.Stmts {
		if !sawExit {
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
			if rhs != nil && b.movableAliasSource(rhs.Name) && order.isLast(rhs) {
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
		if stmtCanLeaveFunction(st) {
			sawExit = true
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
	//
	// The claim is whole-function, so the site must DOMINATE every exit —
	// walkDominatingExprs is what establishes that. Textually-last is not
	// enough: on
	//
	//	if (…) { a = S { …a, code: f(a.code) }; return a; }
	//	return g(a);                       // last occurrence, but not on that path
	//
	// the claim silences the sweep on BOTH paths while only the second one
	// hands the reference away, so the first leaks one box per call — and with
	// it one reference to every rc-tracked field, which is what made the next
	// append see the buffer at rc 2 and copy the whole thing (#8146). A
	// transfer that does not dominate keeps ownArgNeedsRetain's compensating
	// retain, and a return-position one is re-claimed per-site by
	// computeReturnOwnMoves.
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
			walkDominatingExprs(b.fn.Body, func(n ast.Node) bool {
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
							b.rc.ownCallMoveArgs[arg] = true
						}
					}
				}
				return true
			})
		}
	}
	return moved
}

// walkDominatingExprs visits the nodes of `body` that are evaluated exactly
// once on every path from entry to every exit, so a whole-function claim made
// at one of them holds on all of them.
//
// Scanning stops at the first statement that can return: everything after it is
// skipped on the path leaving through that return. Within a statement only the
// always-evaluated positions are offered — an `if` gives its condition, not its
// arms; a `match` its scrutinee, not its bodies — and a LOOP gives nothing,
// since a transfer under one runs once per iteration rather than once.
func walkDominatingExprs(body *ast.Block, f func(ast.Node) bool) {
	if body == nil {
		return
	}
	for _, st := range body.Stmts {
		switch s := st.(type) {
		case *ast.Var:
			walkAlwaysEvaluated(s.Init, f)
		case *ast.ExprStmt:
			walkAlwaysEvaluated(s.Expr, f)
		case *ast.Return:
			walkAlwaysEvaluated(s.Value, f)
		case *ast.If:
			walkAlwaysEvaluated(s.Cond, f)
		case *ast.Match:
			if f(s) {
				walkAlwaysEvaluated(s.Tag, f)
			}
		}
		if stmtCanLeaveFunction(st) {
			return
		}
	}
}

// walkAlwaysEvaluated visits `e` and the sub-expressions evaluated whenever `e`
// is. It stops at the operands that are not: a lambda body, the right operand
// of `&&` / `||`, and the arms of an if- or match-expression.
func walkAlwaysEvaluated(e ast.Expr, f func(ast.Node) bool) {
	if e == nil {
		return
	}
	ast.Walk(e, func(n ast.Node) bool {
		if !f(n) {
			return false
		}
		switch x := n.(type) {
		case *ast.Lambda:
			return false
		case *ast.Binary:
			if x.Op == "&&" || x.Op == "||" {
				walkAlwaysEvaluated(x.Left, f)
				return false
			}
		case *ast.IfExpr:
			walkAlwaysEvaluated(x.Cond, f)
			return false
		case *ast.MatchExpr:
			walkAlwaysEvaluated(x.Tag, f)
			return false
		}
		return true
	})
}

// markConstructionMoves implements the move-on-construction slice of
// Phase 4 pair-cancellation: when a struct literal built at a
// dominating top-level statement consumes an OWNED rc local in an
// rc-tracked field at the local's LAST use
// (`var s = Wrap{ inner: x }`, `x` dead after), the field-init inc and
// x's exit-sweep dec cancel — x's single reference is moved into the
// struct's field. Skipping the inc (gated on b.rc.moveSites[fieldIdent] at
// the StructLit lowering) and x's dec (moved[x] excludes it from the
// exit sweep) leaves the struct owning x; the struct's own field-drop
// (emitDec) releases it exactly once, so the net rc is unchanged.
//
// The eligibility mirrors the inc/drop sides exactly: the field must be
// `rcTrackedSlotType` (string / array / struct / enum / closure / tuple —
// the fields the StructLit inc's AND emitDec dec's), and the value
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
	// A container literal evaluates every operand unconditionally, so the
	// caller's dominance guard for this statement covers a construction
	// NESTED in one of those operands too — `d = Doc { ...d, vals:
	// d.vals.append(v) }` reaches the push, and with it v's move. Only the
	// operands the switch already visits are descended into; nothing else
	// about the statement is walked.
	var visit func(ast.Expr)
	visit = func(val ast.Expr) {
		switch lit := val.(type) {
		case *ast.StructLit:
			sd, ok := b.info.Structs[lit.TypeName]
			if !ok {
				return
			}
			for _, f := range lit.Fields {
				// Only fields the StructLit inc's AND emitDec dec's on drop.
				if rcTrackedSlotType(fieldType(sd.Fields, f.Name)) {
					mark(f.Value)
				}
				visit(f.Value)
			}
		case *ast.ArrayLit:
			// An array of rc-tracked elements: each element is inc'd on
			// construction and dec'd by __fern_drop_arr_ptr at the array's
			// drop, so a moved element balances. Plain-scalar arrays never
			// reach the element inc — mark is a no-op there (isOwnedRcLocal
			// is false for scalars).
			for _, el := range lit.Elems {
				mark(el)
				visit(el)
			}
		case *ast.TupleLit:
			// A tuple with rc-tracked elements: each is inc'd on
			// construction and dec'd by __drop_tuple_<...> at the tuple's
			// drop (dropFnNameFor), so a moved element
			// balances — same shape as the struct/array cases. Only mark
			// owned rc locals; mark self-filters non-pointer elements via
			// isOwnedRcLocal.
			for _, el := range lit.Elems {
				mark(el)
				visit(el)
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
				if en, _, _, isVar := b.lookupVariantOn(id.Name, id.EnumName); isVar && b.enumRcPayloadsEligible(en) {
					for _, a := range lit.Args {
						mark(a)
					}
				}
				// `xs.append(v)` / `xs.with(i, v)` store the element into the
				// buffer under the same `needsRcIncOnAlias && !moveSites` gate as
				// an array literal's elements, and the buffer's deep drop dec's it
				// — so a moved last-use owned local balances there too, and
				// saves the inc/dec pair the unmoved form pays.
				if el, ok := b.storedArrayElemOperand(lit, id.Name); ok {
					mark(el)
					visit(el)
				}
			}
		}
	}
	visit(val)
}

// storedArrayElemOperand returns the element operand of an array-store call
// that retains it with the construction alias-inc — `xs.append(v)`'s value and
// `xs.with(i, v)`'s value — together with whether `callee` is such a call.
//
// Only rc-tracked element types qualify (arrElemIsRcTracked): those are exactly
// the elements the buffer's deep drop walks and dec's, which is what balances a
// skipped inc. Strings are excluded for the same reason the StructLit arm
// excludes them — their two-word retain/release diverges per backend.
func (b *builder) storedArrayElemOperand(call *ast.Call, callee string) (ast.Expr, bool) {
	var el ast.Expr
	switch {
	case callee == "__method_Array_push" && len(call.Args) == 2:
		el = call.Args[1]
	case callee == "__method_Array_set" && len(call.Args) == 3:
		el = call.Args[2]
	default:
		return nil, false
	}
	if len(call.TypeArgs) != 1 || !arrElemIsRcTracked(call.TypeArgs[0]) {
		return nil, false
	}
	return el, true
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
	b.rc.arraySetConsumed = map[string]bool{}
	b.rc.arraySetConsumedSites = map[*ast.Call]bool{}
	if b.fn.Body == nil {
		return incs
	}
	order := b.curIdentOrder()
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
	//
	// A consumed ARRAY param counts as borrowed here even though it is
	// promoted. The other promoted shapes take an entry retain, which is what
	// let this predicate treat "consumed" as "rc >= 2, cow will copy anyway".
	// Arrays deliberately do not (isConsumedArrayParam — the retain costs them
	// the in-place append), so a promoted array param sits at rc==1 holding the
	// CALLER's buffer until its first reassignment replaces it, and an in-place
	// cow there mutates the caller's array. `bump(xs) { xs = xs.with(0, 99) }`
	// left the caller's `a[0]` at 99 — the with_reassign_self_borrowed_param
	// corpus case, whose comment already says a reassign-to-self does not make
	// the buffer ours. Forcing the inc costs a copy on the paths where the slot
	// HAS been replaced, which is the pre-#6021 behaviour and rare: `.with`
	// threading is not the append accumulator this promotion exists for.
	borrowedParam := map[string]bool{}
	for i, p := range b.fn.Params {
		if !p.Own && !b.paramOwnedByDefault(p.Type, i) &&
			(!b.rc.consumedParams[p.Name] || b.isConsumedArrayParam(p.Name)) {
			borrowedParam[p.Name] = true
		}
	}
	// `.with` calls whose receiver outlives an enclosing loop's back edge.
	// The last-occurrence test below is TEXTUAL, and a name declared OUTSIDE
	// the loop is read again by the next iteration, so its textually-last
	// occurrence is not its last dynamic use: an in-place store there is
	// observed on the following pass, where interp copies. callArgDeaths
	// states the same invariant for its own last-use test (see its doc
	// comment) and gets it structurally, by admitting only the self-reassign
	// and `return` shapes.
	//
	// A receiver DECLARED in the body ahead of the call is exempt: the next
	// iteration re-declares it, so nothing on the back edge can observe the
	// store, and forcing the inc there would cost the loop a copy per pass
	// and re-break #6013 — the consuming receiver's slot is empty at the
	// re-init drop precisely because cow_inplace took its reference.
	//
	// The accumulator is unaffected either way: `a = a.with(i, v)` takes the
	// reassignSelf early return above and never reaches here, so the #4838
	// in-place threading keeps its rc==1 branch.
	liveAcrossBackEdge := map[*ast.Call]bool{}
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
		}
		if body == nil {
			return true
		}
		// Names declared as direct statements of the body, accumulated in
		// statement order so a declaration that follows the call — where the
		// receiver still names the OUTER binding — does not exempt it. A
		// nested loop's own bodies are visited by this same walk, so a call
		// is marked if ANY enclosing loop declares its receiver elsewhere,
		// which is exactly "declared outside the innermost enclosing loop".
		blk, _ := body.(*ast.Block)
		if blk == nil {
			markArraySetReceivers(body, nil, liveAcrossBackEdge)
			return true
		}
		declared := map[string]bool{}
		for _, st := range blk.Stmts {
			markArraySetReceivers(st, declared, liveAcrossBackEdge)
			if v, ok := st.(*ast.Var); ok {
				declared[v.Name] = true
			}
		}
		return true
	})
	// Match-arm bindings of a NON-consuming match are borrows too
	// (rc.borrowedBindings): `B(n, kids) => B(n, kids.with(i, v))` must
	// not rewrite the box's payload.
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || !isArraySetCall(c) {
			return true
		}
		if rid, rok := c.Args[0].(*ast.Ident); rok && (borrowedParam[rid.Name] || b.rc.borrowedBindings[rid.Name]) {
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
			//
			// A read out of a FRESH owned container is the exception, and the
			// same predicate the binding site uses says so: that lowering
			// retains the value and deep-drops the container, so the
			// expression holds the only reference and cow_inplace may take
			// it. Forcing the inc there bought a copy AND leaked the
			// original, since no slot holds it for anything to release —
			// `mk_box().items.with(…)` cost 64 B a round, unbounded.
			switch c.Args[0].(type) {
			case *ast.Index, *ast.FieldAccess, *ast.SliceExpr:
				incs[c] = !b.isOwnedContainerRead(c.Args[0])
			default:
				incs[c] = false
			}
			return true
		}
		// Live after the call iff this occurrence is NOT the receiver
		// name's last use — or the receiver survives a loop's back edge and
		// "last" re-executes (liveAcrossBackEdge above).
		incs[c] = !order.isLast(rid) || liveAcrossBackEdge[c]
		// No inc means cow_inplace consumes this receiver's reference (see
		// arraySetConsumed) — record the site so emitArraySet zeroes the
		// slot. Every role the sweep releases qualifies: a declared owned
		// local, an OWNED param (which the sweep reclaims in its own extra
		// pass, `own` / owned-by-default / consumed — the same predicate used
		// there), and a consuming-match binding (swept as a counted owner). A
		// borrowed param or binding already took the incs=true path above, so
		// it cannot reach here; an untracked name has nothing swept either way.
		_, consumingBinding := b.rc.consumingBindings[rid.Name]
		if !incs[c] && (b.isOwnedRcLocal(rid.Name) || b.isOwnedRcParam(rid.Name) || consumingBinding) {
			b.rc.arraySetConsumed[rid.Name] = true
			b.rc.arraySetConsumedSites[c] = true
		}
		return true
	})
	return incs
}

// computeBorrowedBindings collects the arm bindings of every non-consuming
// match in the body; see rcState.borrowedBindings for what that means.
func (b *builder) computeBorrowedBindings() map[string]bool {
	out := map[string]bool{}
	if b.fn.Body == nil {
		return out
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		m, ok := n.(*ast.Match)
		if !ok {
			return true
		}
		if _, consuming := b.rc.consumingOwnedMatches[m]; consuming {
			return true
		}
		if _, ownScrut := b.ownParamEnumScrutinee(m.Tag); ownScrut {
			return true
		}
		// An owned-payload match's admitted bindings own their reference;
		// its other bindings are borrows like any non-consuming arm's.
		ownedPayload := b.rc.ownedPayloadMatches[m]
		for _, arm := range m.Arms {
			for _, name := range arm.Bindings {
				if name == "" || name == "_" {
					continue
				}
				if _, owned := b.rc.consumingBindings[name]; owned && ownedPayload {
					continue
				}
				out[name] = true
			}
			if arm.AtBinding != "" {
				out[arm.AtBinding] = true
			}
		}
		return true
	})
	return out
}

// markArraySetReceivers records every `.with` call under n whose ident
// receiver is not in declared — the names a loop body re-declares each
// iteration, which are the only receivers a back edge cannot observe.
func markArraySetReceivers(n ast.Node, declared map[string]bool, out map[*ast.Call]bool) {
	ast.Walk(n, func(m ast.Node) bool {
		c, ok := m.(*ast.Call)
		if !ok || !isArraySetCall(c) {
			return true
		}
		if rid, rok := c.Args[0].(*ast.Ident); !rok || !declared[rid.Name] {
			out[c] = true
		}
		return true
	})
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
//   - No `return` / `break` / `continue` / `?` may sit BETWEEN the var's
//     declaration and the construction. That interval is what makes the
//     construction unconditional. `moved` is function-wide, so a path that
//     creates the box and then leaves without reaching the construction leaks
//     it — nothing ever releases what the container was supposed to own.
//   - The name must be declared exactly once in the function (localNameUnique).
//     `moved` is name-keyed, so a shadowed name would suppress the exit-sweep
//     dec of an unrelated same-name local.
//
// An early exit outside that interval is harmless, which is what makes the
// guard-clause shape every parser is built out of eligible (#6533). Before the
// var's own declaration no box exists yet on the exiting path, so the
// suppressed drop had nothing to release — the slot is either null from the
// prologue zero-init or holds an earlier iteration's already-moved pointer.
// After the construction the value belongs to the container, whose drop
// releases it once.
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
		if !ok {
			return true
		}
		// exitsBefore[i] counts the body's top-level statements in [0, i) that
		// carry an early exit, so "nothing exits between statements d and i" is
		// exitsBefore[i] == exitsBefore[d].
		exitsBefore := make([]int, len(blk.Stmts)+1)
		for i, st := range blk.Stmts {
			exitsBefore[i+1] = exitsBefore[i]
			if stmtHasEarlyExit(st) {
				exitsBefore[i+1]++
			}
		}
		// Vars declared so far in THIS body — the only names a construction
		// here may move, and only from a declaration that reaches it.
		declAt := map[string]int{}
		at := 0
		allow := func(name string) bool {
			d, ok := declAt[name]
			return ok && b.localNameUnique(name) && exitsBefore[at] == exitsBefore[d]
		}
		for i, st := range blk.Stmts {
			at = i
			switch s := st.(type) {
			case *ast.Var:
				if s.Init != nil {
					b.markConstructionMoves(s.Init, order, moved, allow)
				}
				declAt[s.Name] = i
			case *ast.ExprStmt:
				if a, ok := s.Expr.(*ast.Assign); ok {
					b.markConstructionMoves(a.Value, order, moved, allow)
				}
			}
		}
		return true
	})
}

// stmtHasEarlyExit reports whether `st` can leave the enclosing loop body early:
// a `return`, `break`, `continue`, or a `?` (TryOp), which returns the failure
// variant from the function.
//
// Coarse in two directions, both of which only forgo a move: a `break` belonging
// to a nested loop counts, and so does a `return` inside a nested lambda.
func stmtHasEarlyExit(st ast.Stmt) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.Return, *ast.Break, *ast.Continue, *ast.TryOp:
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
		// A type that reaches a `core/mem.Drop` impl — its own, or one on
		// anything it transitively holds — never participates in reuse.
		// Reuse hands the dying value's box shell straight to the next
		// constructor rather than freeing it, so the drop glue for the box
		// itself never runs: a destructor silently skipped on a value that
		// really did die, which is worse than leaking it. A finalizer on a
		// FIELD is not skipped but MOVED: the reuse branch releases the old
		// fields where the box is taken over rather than where the donor
		// would otherwise have died. That point is target-dependent — box
		// classes are computed from ptrW, so two types pair on wasm and not
		// on native — and a user-visible finalizer must not fire in a
		// different order per backend. Declining is the conservative form
		// either way, and it costs the optimisation only on the types that
		// reach a `drop`.
		if b.typeReachesUserDrop(t, map[string]bool{}) {
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
						if _, _, pc, isVar := b.lookupVariantOn(callee.Name, callee.EnumName); isVar && pc > 0 {
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

	// pairedTo lists, per donor, the constructions already paired with it —
	// normally at most one (`consumed` is 1:1), more only when `share` admits
	// a mutually-exclusive second claimant (cross-branch sharing, below).
	pairedTo := map[string][]ast.Expr{}

	// attemptPair tries to pair construction C (cName / cNode) with a dead,
	// owned source D drawn from `declIdx` (name → declaration index in some
	// statement list), where D must be declared before `k` and dead from `k`
	// onward per `deadFrom`. Used by BOTH the same-block pass (declIdx/k/deadFrom
	// scoped to C's own block) and the cross-block pass (scoped to the function
	// body, with k the top-level statement that ENCLOSES a nested C). Records the
	// pairing in `sources`/`consumed` and returns true on success.
	//
	// `share`, when non-nil, re-admits an ALREADY-consumed donor: it reports
	// whether C can never run in the same pass as the constructions already
	// holding D's token. Only the cross-branch pass passes it.
	attemptPair := func(cName string, cNode ast.Expr, declIdx map[string]int, k int, deadFrom func(string, int) bool, share func(prev []ast.Expr) bool) bool {
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
			if di >= k || dName == cName || reassigned[dName] {
				continue
			}
			if consumed[dName] && (share == nil || !share(pairedTo[dName])) {
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
			// D and C pair whenever their boxes fall in the SAME freelist
			// class, whatever KIND each is (struct / tuple / enum): C's box
			// then fits D's block exactly and __alloc_reuse's runtime class
			// check matches. Neither layout is imposed on the other — D's old
			// pointer fields are released through D's own layout
			// (reuseSourceLayout) before C stores its own — so a tuple can
			// hand its box to a struct and an enum to either. Same-type pairs
			// are the degenerate case: equal layouts, equal class.
			_, _, dClass, ok := reuseClassOf(dName)
			if !ok || dClass != cClass {
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
			pairedTo[bestD] = append(pairedTo[bestD], cNode)
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
						attemptPair(cName, cNode, declIdx, k, deadFrom, nil)
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
				// CROSS-BRANCH sharing: a donor already claimed by an earlier
				// construction under this SAME enclosing statement may be
				// claimed again when the two sit in different arms of an
				// `if` / `match` nested in it — no single pass through st
				// reaches both, so at most one of them consumes the token.
				// (The claim is safe even if the exclusivity were wrong: the
				// first consumer zeroes D's slot, so a second claim reads a
				// null token and __alloc_reuse degrades to a fresh alloc.
				// Exclusivity is what makes the second token sequence worth
				// its code, not what makes it sound.)
				share := func(prev []ast.Expr) bool {
					for _, p := range prev {
						if !mutuallyExclusive(st, p, cNode) {
							return false
						}
					}
					return true
				}
				attemptPair(cName, cNode, declIdx, k, deadFrom, share)
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

// computeReturnSpreadReuse admits the RETURN-position struct update
// `return T { ...p, f: v }` for in-place reuse of p's box, and records each
// admitted p in reuseConsumed. It is the state-threading shape — every
// `s = s.emit(op)` in the self-host emitters is one call of a method built
// this way — where the general reuse pairing (computeReuseSources) cannot
// help: there D must be dead AT C, and here C reads D's every field.
//
// What makes it sound is that the frame owns p and cannot read it again:
//
//   - freeEligible[p] and frameOwnsIdent(p) (via structUpdateReuseDecl at the
//     lowering site, plus the runtime is_unique gate there): p's reference
//     belongs to this frame. For an owned-by-default PARAMETER that is the
//     whole point — the caller retained an argument it keeps
//     (calleeParamOwnedByDefault) and moved one it does not
//     (computeOwnedArgMoves), so a surviving caller-side alias shows up as
//     rc>1 and the reuse declines to a fresh box.
//   - Nothing runs between the construction and the return, so p is dead: the
//     reuse zeroes p's slot and the exit sweep meets a null. A `defer` DOES
//     run there (and can name p), and a lambda can hold p past the frame, so
//     both refuse — deferOrLambdaNames, and a body-wide defer check because a
//     defer anywhere reaches this return.
//   - A moved p (computeMovedLocals) has already handed its reference on, and
//     a shadowed name (bindingNameUnique) cannot be tracked by name at all.
//   - A superseded-field own-move (computeFieldOwnMoves) claims this same
//     literal to empty one of p's fields into an `own` argument; it nulls the
//     field under its own is_unique test, so the two claims must not overlap.
func (b *builder) computeReturnSpreadReuse() map[*ast.StructLit]string {
	out := map[*ast.StructLit]string{}
	if !ast.RcFreeEnabled || !ast.RcReuseEnabled || b.fn.Body == nil {
		return out
	}
	var defers []*ast.Defer
	collectDefers(b.fn.Body, &defers)
	if len(defers) > 0 {
		return out
	}
	esc := deferOrLambdaNames(b.fn.Body)
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl, *ast.Lambda:
			return false // a nested body's `return` is not this frame's
		case *ast.Return:
			sl, isLit := x.Value.(*ast.StructLit)
			if !isLit || sl.Base == nil {
				return true
			}
			id, isID := sl.Base.(*ast.Ident)
			if !isID || esc[id.Name] || b.rc.movedLocals[id.Name] || !b.bindingNameUnique(id.Name) {
				return true
			}
			if !b.frameOwnsIdent(id.Name) {
				return true
			}
			if _, ok := b.structUpdateReuseDecl(sl, id); !ok {
				return true
			}
			if len(checker.SupersededFieldOwnMoveArgs(sl, id.Name, b.info.OwnFuncs)) > 0 {
				return true
			}
			out[sl] = id.Name
			b.rc.reuseConsumed[id.Name] = true
		}
		return true
	})
	return out
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
	reassigned := b.reassignedAnywhere()
	declIdx := blockDeclIndices(stmts, reassigned)
	out := map[int][]string{}
	// Iterate in DECLARATION order, not Go map order. Two locals whose last
	// use falls on the same statement both append to out[last], and ranging
	// the map appended them in a random order — so the same source compiled
	// twice emitted their drops in either order, and the binaries differed.
	// Declaration order is the program's own order and is stable.
	for _, name := range sortedByDeclIdx(declIdx) {
		last, ok := b.preciseDropTarget(stmts, declIdx[name], name, reassigned)
		if !ok {
			continue
		}
		out[last] = append(out[last], name)
	}
	return out
}

// blockDeclIndices maps each name a `var` declares at the TOP level of one
// block's statement list to that statement's index, and marks a shadowed
// redeclaration in `reassigned` so it bails out of precise-drop candidacy.
func blockDeclIndices(stmts []ast.Stmt, reassigned map[string]bool) map[string]int {
	declIdx := map[string]int{}
	for i, st := range stmts {
		v, ok := st.(*ast.Var)
		if !ok {
			continue
		}
		if _, dup := declIdx[v.Name]; dup {
			reassigned[v.Name] = true // shadowed redeclaration — bail
		} else {
			declIdx[v.Name] = i
		}
	}
	return declIdx
}

// reassignedAnywhere collects every local the function body assigns to, at ANY
// depth. A precise drop allows the last use to sit inside a nested block, so a
// `name = ...` buried in an `if`/`while` rebinds the slot and the straight-line
// `last` index can't see it — bail on any such local.
func (b *builder) reassignedAnywhere() map[string]bool {
	out := map[string]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// computeNestedDrops extends precise-drop placement into NESTED block scopes:
// a local declared inside an if / while / for / match body drops right after
// its last use WITHIN that block, instead of surviving to the block's next
// entry (a loop body's re-init drop) or the function-exit sweep. Returns the
// statement to drop AFTER → the names to drop there.
//
// computePreciseDrops only ever considered TOP-LEVEL declarations, so a
// binding made inside a loop held its reference for the rest of the iteration
// no matter how early it died. That is what made a dead ALIAS expensive
// (#6024): `var keep = acc` in a loop body leaves `acc` at rc 2, and
// __fern_arr_push_grow mutates in place only at rc 1, so all 200 appends
// behind the alias copied the whole buffer — 199 wasted copies bought by a
// binding nothing reads again. Releasing `keep` at its last read restores
// rc 1 and the appends run in place.
//
// Soundness is the top-level pass's argument (deep drop + zeroed slot, so
// every re-init drop and exit sweep that follows null-guards to a no-op) plus
// one gate the top-level pass gets for free: the drop and the declaration sit
// in the SAME block, so they run the same number of times. A local declared
// OUTSIDE a loop whose last use is inside it keeps its top-level placement —
// after the whole loop — because only names `blk` itself declares are
// candidates here; dropping such a local at the inner use would free it on
// the first iteration and leave the second reading a zeroed slot.
func (b *builder) computeNestedDrops() map[ast.Stmt][]string {
	if !ast.RcFreeEnabled || b.fn.Body == nil {
		return nil
	}
	var blocks []*ast.Block
	for _, st := range b.fn.Body.Stmts {
		collectNestedBlocks(st, &blocks)
	}
	if len(blocks) == 0 {
		return nil
	}
	reassigned := b.reassignedAnywhere()
	bodyRefs := identCounts(b.fn.Body)
	out := map[ast.Stmt][]string{}
	for _, blk := range blocks {
		declIdx := blockDeclIndices(blk.Stmts, reassigned)
		if len(declIdx) == 0 {
			continue
		}
		blkRefs := identCounts(blk)
		// Declaration order, not Go map order — same determinism requirement
		// as the top-level pass (two locals dying on one statement must drop
		// in a stable order or the same source emits two different binaries).
		for _, name := range sortedByDeclIdx(declIdx) {
			// Every reference must be inside this block. Scoping and the
			// localNameUnique gate already imply it, but the last-use scan
			// only reads blk.Stmts, so make the pass answerable for that on
			// its own rather than on a property of another layer.
			if bodyRefs[name] != blkRefs[name] {
				continue
			}
			last, ok := b.preciseDropTarget(blk.Stmts, declIdx[name], name, reassigned)
			if !ok {
				continue
			}
			out[blk.Stmts[last]] = append(out[blk.Stmts[last]], name)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectNestedBlocks appends every statement `*ast.Block` reachable from `s`
// — the bodies and arms of the control-flow statements, plus bare blocks — in
// source order. Nested FuncDecl bodies are NOT traversed: they lower through
// their own lowerFunc with their own tables. Mirrors collectDefers' recursion.
func collectNestedBlocks(s ast.Stmt, out *[]*ast.Block) {
	if s == nil {
		return
	}
	switch x := s.(type) {
	case *ast.Block:
		*out = append(*out, x)
		for _, st := range x.Stmts {
			collectNestedBlocks(st, out)
		}
	case *ast.If:
		collectNestedBlocks(x.Then, out)
		collectNestedBlocks(x.Else, out)
	case *ast.While:
		collectNestedBlocks(x.Body, out)
	case *ast.Loop:
		collectNestedBlocks(x.Body, out)
	case *ast.For:
		collectNestedBlocks(x.Body, out)
	case *ast.Match:
		for _, arm := range x.Arms {
			collectNestedBlocks(arm.Body, out)
		}
	}
}

// identCounts tallies how many times each name occurs as an *ast.Ident under
// `n`. Comparing a block's tally against the whole body's is how
// computeNestedDrops proves a candidate is referenced nowhere else.
func identCounts(n ast.Node) map[string]int {
	out := map[string]int{}
	ast.Walk(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok {
			out[id.Name]++
		}
		return true
	})
	return out
}

// preciseDropTarget runs the precise-drop candidacy gates for `name`, declared
// by `stmts[di]` in ONE block's statement list, and returns the index of the
// statement after which its drop belongs. ok=false leaves the local on the
// function-exit sweep. The two callers differ only in which statement list
// they hand it: computePreciseDrops passes the function body's top-level
// statements, computeNestedDrops passes a nested block's.
func (b *builder) preciseDropTarget(stmts []ast.Stmt, di int, name string, reassigned map[string]bool) (int, bool) {
	if reassigned[name] || b.rc.movedLocals[name] || !b.rc.freeEligible[name] || !b.localNameUnique(name) {
		return 0, false
	}
	// #4402 opt 1: a borrow source releases ONLY at the exit sweep (a
	// live borrowed view reads through its buffer until then); a
	// borrowed alias is a view and is never dropped at all.
	if b.rc.borrowSources[name] || b.rc.borrowedAlias[name] {
		return 0, false
	}
	// A local whose box is handed off to a general-FBIP reuse site
	// (computeReuseSources) is already consumed there (its box taken, or
	// dec'd on the shared path, and its slot zeroed) — dropping it again
	// here would double-release. The reuse site subsumes its drop.
	if b.rc.reuseConsumed[name] {
		return 0, false
	}
	if !b.preciseDroppableType(name) {
		return 0, false
	}
	// The local's INIT may itself produce an uncounted alias of a still-
	// live value — a slice (view), a pointer-typed if/match expr, or a
	// call whose result could BE a pointer argument (`var v3 = id(v2)`).
	// Precise-dropping such a local would free a buffer the source still
	// holds. A scalar-arg call (`fill(100)`) returns a fresh value and
	// stays eligible (the common builder-call win). The OTHER end — the
	// source flowing INTO the call — is handled by flowsIntoUncountedAlias
	// below.
	//
	// A COUNTED-ALIAS init (`var y = x` / `x.field` / `x[i]` —
	// needsRcIncOnAlias) is NOT excluded: the precise drop gives back
	// exactly the inc the init took, and giving it back at the last read
	// rather than at scope exit is what restores the source to rc 1 — the
	// uniqueness `__fern_arr_push_grow` gates its in-place append on
	// (#6024: 200 appends behind a dead alias cost 199 full buffer copies).
	if v, ok := stmts[di].(*ast.Var); ok {
		if b.initMayAliasLive(v.Init) {
			return 0, false
		}
		if b.retainsCtorAliasedSource(v.Init) {
			return 0, false
		}
	}
	last := -1
	for i := di + 1; i < len(stmts); i++ {
		if !stmtReferencesName(stmts[i], name) {
			continue
		}
		// Control-flow-aware placement (slice 5): the last use may now sit
		// INSIDE a nested if / while / for / match. We still drop the local
		// right after the whole statement that contains its last use — by
		// then the local is dead on EVERY path through that statement, so a
		// single drop + zero-slot is sound, and any early `return` on a path
		// keeps the value live to its own exit sweep (the zeroed slot makes
		// the post-statement drop a no-op on the paths that already
		// returned). Slightly less precise than a per-branch drop, but it
		// reclaims before the (often long) tail after an `if`, which is
		// where the win is.
		//
		// A reference inside a pointer-producing call / slice / if-expr /
		// match-expr can create an UNCOUNTED alias of `name` that outlives
		// the drop point (e.g. `var v3 = id(v2)` — a generic identity
		// returns its borrowed arg with no inc). The inc'd-alias sites
		// (`var y = x` / `x.field` / `x[i]`, container literals) are SAFE —
		// the precise drop only decs there. Bail on the uncounted-alias
		// shapes (flowsIntoUncountedAlias walks the whole nested statement).
		if b.flowsIntoUncountedAlias(stmts[i], name) {
			return 0, false
		}
		last = i
	}
	if last < 0 {
		// Declared but never used after — drop right after the decl
		// (a dead owned alloc reclaims immediately).
		last = di
	}
	// Control-flow placement guard: when the last use sits INSIDE a
	// nested control-flow statement (if / while / for / match / block),
	// the precise drop fires after that whole statement — the slice-5
	// extension over the straight-line slices 1-3, which only placed
	// drops after a simple top-level use. That extension is
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
		return 0, false
	}
	// A `return` whose value is this local is handled by the Return
	// lowering's own move-on-return / sweep; dropping after it is dead
	// code. Skip — the value reclaims at the return instead.
	if _, isRet := stmts[last].(*ast.Return); isRet {
		return 0, false
	}
	return last, true
}

// isControlFlowStmt reports whether `st` is a control-flow statement whose
// body holds a nested block — an if / while / for / match / bare block. A
// reference to a local inside one of these is a "nested" use, so a precise
// drop placed after the whole statement is the slice-5 control-flow
// extension (vs a simple top-level Var / ExprStmt / Return use).
func (b *builder) isControlFlowStmt(st ast.Node) bool {
	switch st.(type) {
	case *ast.If, *ast.While, *ast.Loop, *ast.For, *ast.Match, *ast.Block:
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
		//
		// An argument in a COUNTED-RETAIN position (paramCountedRetain) is not
		// that risk: whatever of it the result carries, it carries under a
		// reference of its own, so the precise drop decs rather than freeing
		// through to a live source — the same admission the counted-alias
		// inits (`var y = x` / `x.field`) already get, and what lets a
		// functional-update threading chain release each dead intermediate at
		// its last read instead of at scope exit.
		var counted []bool
		if id, isID := x.Callee.(*ast.Ident); isID {
			if _, isLocal := b.locals[id.Name]; !isLocal {
				counted = b.paramCountedRetain[id.Name]
			}
		}
		for i, a := range x.Args {
			if !b.mayAliasResult(a) {
				continue
			}
			if i < len(counted) && counted[i] {
				continue
			}
			return true
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

// paramNamed returns the current function's parameter called `name`, or nil.
func (b *builder) paramNamed(name string) *ast.Param {
	for i := range b.fn.Params {
		if b.fn.Params[i].Name == name {
			return &b.fn.Params[i]
		}
	}
	return nil
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
		case ast.ArrayType, ast.StructType, ast.EnumType, *ast.FuncType, ast.TupleType, ast.SliceType:
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

// movableAliasSource reports whether `name` holds a reference this frame will
// release at exit, so an alias taking its LAST occurrence can move it: the
// alias skips its transfer inc and the sweep skips the dec, a net-zero pair.
//
// Both roles qualify, and the parameter half is the one that matters for a
// cursor threaded through a function. `var c: C = c0;` on an owned parameter
// used to inc, leaving c0's reference alive to the exit sweep for the whole
// body — so the container's array field sat at rc 2 and every field append
// after it copied the buffer instead of growing in place. Three appends around
// one call went quadratic on that alone (#8498): 2990 ms against 2 ms for the
// same work written without the alias.
//
// A BORROWED parameter is excluded, and must be: moving it would hand away a
// reference the caller still owns. isOwnedRcParam is the same predicate the
// sweep's owned-param pass releases on, so "will this frame dec it?" has one
// answer for both.
func (b *builder) movableAliasSource(name string) bool {
	return b.isOwnedRcLocal(name) || b.isOwnedRcParam(name)
}

// isOwnedRcParam reports whether `name` is a parameter the CALLEE owns and so
// must release at exit — `own`-annotated, owned by default for its type, or
// proven consumed. It is the param-side sibling of isOwnedRcLocal (which only
// walks declared `var` locals), and the same predicate
// emitRcDecLocalsAtExitExcept's owned-param pass releases on, so a caller that
// wants to know "will the sweep dec this name?" gets one answer for both roles.
func (b *builder) isOwnedRcParam(name string) bool {
	for i, p := range b.fn.Params {
		if p.Name != name {
			continue
		}
		return rcTrackedSlotType(p.Type) &&
			(p.Own || b.paramOwnedByDefault(p.Type, i) || b.rc.consumedParams[p.Name])
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
	case *ast.Ident, *ast.FieldAccess, *ast.Index, *ast.CaptureRef:
		// A CaptureRef reads the closure env's own reference (inc'd at
		// MakeClosure, dec'd by the env drop thunk), so it aliases like a
		// local.
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
	// A slice header (rc=1 from __slice_make) is retained like a tuple box:
	// the inc balances the header release the exit sweep emits for slice
	// locals and keeps a header stored into a container alive past the
	// source local. The viewed bytes are not counted either way.
	if isSliceType(t) {
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
// instead of freeing it (true zero-alloc FBIP). Deciding it here rather than
// mid-lowering is what keeps the rcPlan immutable while lowering runs.
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
//     paramOwnedByDefault (which implies isOwnedByDefaultType: an
//     rc-eligible enum whose deep drop is wired) and no same-named declared
//     local;
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
//
// The same binding role serves a second scrutinee shape, returned as the
// second map: a direct call to an rcOwnedPayloadBuiltins builtin
// (ownedPayloadMatches). There the box is immortal and released by nobody,
// so the only question is who owns the fresh payload; its qualifying arms'
// string / array bindings become counted owners under the same name gates,
// with no loop restriction (each iteration reads a fresh box — the bind site
// drops the previous value) and no sibling poisoning (an unadmitted binding
// is a leak, not a stranded transfer). A `_` there is released at the bind
// site rather than disqualifying the match.
func (b *builder) computeConsumingOwnedMatches() (map[*ast.Match]string, map[*ast.Match]bool, map[string]ast.Type) {
	matches := map[*ast.Match]string{}
	payload := map[*ast.Match]bool{}
	bindings := map[string]ast.Type{}
	if !ast.RcFreeEnabled || b.fn.Body == nil {
		return matches, payload, bindings
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
	// BY NAME (bindingSlotScoped reuses b.locals[name] — including a param's slot),
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
		}
		return true
	})
	if hasDefer {
		return matches, payload, bindings
	}
	order := b.curIdentOrder()
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		m, ok := n.(*ast.Match)
		if !ok {
			return true
		}
		if b.ownedPayloadScrutinee(m.Tag) {
			payload[m] = true
			return true
		}
		if inLoop[m] {
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
		// Every payload-carrying arm frees the box at ITS variant's size
		// (the layout need not be uniform), so each must be sizable — a
		// generic-erased payload is not. A payloadless arm matches a
		// static sentinel and releases nothing.
		for _, arm := range m.Arms {
			if arm.IsWildcard || arm.Literal != nil || arm.VariantName == "" {
				continue
			}
			for _, v := range ed.Variants {
				if v.Name != arm.VariantName || len(v.Payloads) == 0 {
					continue
				}
				if _, sized := enumVariantBoxSize(ed, arm.VariantName, b.ptrW); !sized {
					return true
				}
			}
		}
		matches[m] = id.Name
		return true
	})
	if len(matches) == 0 && len(payload) == 0 {
		return matches, payload, bindings
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
	// An array binding is a single-word buffer pointer on every backend and
	// the sweep's array arm deep-drops it; a string binding qualifies only
	// where the shared-branch dup can retain it with a flat rc_inc
	// (ownMatchDupPayload — single-word native strings), since a two-word
	// string's length is not a pointer.
	sweepable := func(t ast.Type) bool {
		switch tt := t.(type) {
		case ast.EnumType, ast.TupleType, ast.ArrayType:
			return true
		case ast.StructType:
			_, isUser := b.info.Structs[tt.Name]
			return isUser
		case ast.StringType:
			return ownMatchDupPayload(t, b.ptrW)
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
		admit := func(arm *ast.MatchArm, sweepable func(ast.Type) bool) {
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
				if payload[x] {
					for _, arm := range x.Arms {
						if !arm.IsWildcard && arm.Guard == nil && arm.Literal == nil && !armHasSubPatterns(arm) {
							admit(arm, ownedPayloadType)
						} else {
							disqualify(arm.Bindings)
						}
					}
					break
				}
				// A non-qualifying arm that BINDS anything poisons the whole
				// match, not just its own names. The canonical shape is a
				// guarded arm followed by an unguarded one over the same
				// variant (`Cons(h, t) when h > 3 => …, Cons(h, t) => …`): a
				// failed guard falls through to a sibling that re-reads the
				// same payloads, so consuming the box in that sibling is only
				// safe if the guarded arm never ran. This used to fall out of
				// the name-keyed disqualify — both arms wrote `t`, so
				// poisoning the guarded arm's `t` poisoned the other's too.
				// Shadowrename now gives sibling-scope redeclarations distinct
				// names (it must: colliding names collapse onto one IR slot),
				// which removed that accidental coupling. Stating it
				// structurally keeps the guard independent of what the
				// bindings happen to be called. Arms that bind nothing (a bare
				// `_ =>` / `Nil =>`) poison nothing, as before.
				armBindsNames := func(arm *ast.MatchArm) bool {
					for _, nm := range arm.Bindings {
						if nm != "" && nm != "_" {
							return true
						}
					}
					return false
				}
				poisoned := false
				for _, arm := range x.Arms {
					qualifying := consuming && !arm.IsWildcard && arm.Guard == nil && arm.Literal == nil
					if !qualifying && armBindsNames(arm) {
						poisoned = true
					}
				}
				for _, arm := range x.Arms {
					if !poisoned && consuming && !arm.IsWildcard && arm.Guard == nil && arm.Literal == nil {
						admit(arm, sweepable)
					} else {
						disqualify(arm.Bindings)
					}
				}
			case *ast.MatchExpr:
				for _, arm := range x.Arms {
					disqualify(arm.Bindings)
				}
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
			markUsed := func(m *ast.Match) {
				for _, arm := range m.Arms {
					if arm.IsWildcard || arm.Guard != nil || arm.Literal != nil {
						continue
					}
					for _, nm := range arm.Bindings {
						used[nm] = true
					}
				}
			}
			for m := range matches {
				markUsed(m)
			}
			for m := range payload {
				markUsed(m)
			}
			for nm, bt := range cand {
				if used[nm] {
					bindings[nm] = bt
				}
			}
			return matches, payload, bindings
		}
	}
}

// ownedPayloadScrutinee reports whether `tag` is a direct call to an
// rcOwnedPayloadBuiltins builtin, so a match over it owns the string / array
// payload it binds. A user-declared function of the same name (the oracle
// maps key every decl in prog.Funcs) is not the builtin.
func (b *builder) ownedPayloadScrutinee(tag ast.Expr) bool {
	call, ok := tag.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok || !rcOwnedPayloadBuiltins[id.Name] {
		return false
	}
	_, isUserFn := b.returnsNoParamEscape[id.Name]
	return !isUserFn
}

// armHasSubPatterns reports whether any payload position of `arm` carries a
// sub-pattern (`Some((a, b))`, `Ok("x")`) rather than a plain binding.
func armHasSubPatterns(arm *ast.MatchArm) bool {
	for _, p := range arm.Payloads {
		if p != nil {
			return true
		}
	}
	return false
}

// stmtReferencesName reports whether any *ast.Ident named `name` appears
// anywhere in the subtree `st` — the shared occurrence predicate behind the
// last-use / deadness scans (#4480). Shared by computeReuseSources,
// computePreciseDrops, and flowsIntoUncountedAlias.
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
	esc := deferOrLambdaNames(body)
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

// deferOrLambdaNames returns the names still readable once the statement that
// mentions them completes: anything referenced under a defer action or inside a
// lambda body, since a closure can run later and read a captured binding.
// Conservative — any occurrence of the name is enough. Both return-position
// exemptions below rest on it.
func deferOrLambdaNames(body ast.Node) map[string]bool {
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
		// Descend anyway: nested statements hold names of their own.
		return true
	})
	return esc
}

// place is one syntactic read of a container root: either a BARE occurrence of
// the root ident, which hands the whole container over and so reaches every
// field, or the longest ident-rooted field chain (`o.a.b`), which reaches only
// what sits under that path.
type place struct {
	root string
	path []string // nil for a bare occurrence
	node ast.Node
}

// fieldPlace decomposes an ident-rooted field chain into its root name and the
// field path read from the root outwards.
func fieldPlace(fa *ast.FieldAccess) (string, []string, bool) {
	var rev []string
	for e := ast.Expr(fa); ; {
		switch x := e.(type) {
		case *ast.FieldAccess:
			rev = append(rev, x.Field)
			e = x.Target
		case *ast.Ident:
			path := make([]string, len(rev))
			for i, f := range rev {
				path[len(rev)-1-i] = f
			}
			return x.Name, path, true
		default:
			return "", nil, false
		}
	}
}

// overlaps reports whether reading `q` can reach the buffer `p` names: a bare
// root reaches every field, and two chains collide when one path is a prefix of
// the other (`o.a` contains `o.a.b`).
func (p place) overlaps(q place) bool {
	if p.root != q.root {
		return false
	}
	n := min(len(p.path), len(q.path))
	for i := 0; i < n; i++ {
		if p.path[i] != q.path[i] {
			return false
		}
	}
	return true
}

// placesIn collects every place under `n`. A field chain is recorded once, at
// its outermost FieldAccess; the idents inside it are NOT also recorded as bare
// occurrences, which is what keeps `o.ys` from reading as a whole-container use.
func placesIn(n ast.Node) []place {
	var out []place
	ast.Walk(n, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.FieldAccess:
			root, path, ok := fieldPlace(x)
			if !ok {
				return true
			}
			out = append(out, place{root: root, path: path, node: x})
			return false
		case *ast.Ident:
			out = append(out, place{root: x.Name, node: x})
		}
		return true
	})
	return out
}

func isArrayPushCall(c *ast.Call) bool {
	id, ok := c.Callee.(*ast.Ident)
	return ok && id.Name == "__method_Array_push" && len(c.Args) == 2
}

// fieldPlaceAppendCopies returns the appends whose receiver is a field PLACE
// (`o.xs`, `o.a.b`) and whose rc==1 in-place grow would still be observable
// through the container, so emitArrayPush must force the copy path (#6665).
//
// A field receiver carries none of the uniqueness information a bare ident
// does: rc==1 says the container holds the only reference to the buffer, not
// that nobody reads it again — the container is still named, and reading its
// field again yields the same pointer. So an overlapping read of the place —
// the same field chain, a prefix of it, or a bare read of the root, which
// hands the whole container over — forces the copy unless one of three things
// puts every such read out of the grow's reach:
//
//   - REBIND. The append feeds the literal that REPLACES the container
//     (`o = S { xs: o.xs.append(v), ys: o.ys }`), so later reads of `o.xs`
//     resolve through the new container and should see the appended value.
//     This is the self-host EmitState threading shape and it must stay O(1).
//     It excuses only reads through the NAME: an overlapping read inside the
//     replacing expression is evaluated against the pre-append container
//     (`inner: I { data: o.xs }`, the #6665 repro), and a bare read that binds
//     the container elsewhere (`var old = o`) keeps the old one reachable
//     under another name. A binding only counts when it CAN hold the
//     container: `var n: i32 = len_of(o)` hands `o` to a call that gives back
//     a scalar, so nothing reachable afterwards names it (bindingHoldsContainer).
//   - RETURN. The append sits in a return expression with no overlapping read
//     later in that expression, so the function exits before anything here can
//     look again. What the CALLER can still see through a parameter is the
//     #4873 caller-side grow bracket's job; a name captured by a defer or a
//     lambda is excluded because those run after the return expression.
//   - NO OVERLAP at all in the body.
//
// A struct-update SPREAD base does not count as an overlapping read of the
// field the same literal overrides — `A { ...a, code: a.code.append(v) }`
// copies every field of `a` except `code`, so its copy never names the grown
// buffer. This is the assembler's own emit shape; treating `...a` as a
// whole-container read cost 75% of the self-host driver's compile time.
// argNoEscape answers, for one call and one argument position, whether the
// callee provably lets nothing of that argument outlive the call — neither
// through the return value nor into a caller-visible container. nil means
// "assume it can", which is what every caller but the lowering builder passes.
type argNoEscape func(c *ast.Call, argIdx int) bool

// shieldedPlaces collects the place nodes that sit inside a call argument
// argNoEscape vouches for. Reading a container into such an argument hands it
// to nobody: the callee cannot store it and cannot return it, so the binding
// the call result flows into does not name it.
//
// Whole subtrees, because an argument the callee cannot leak shields whatever
// is nested inside it too.
func shieldedPlaces(root ast.Expr, noEsc argNoEscape) map[ast.Node]bool {
	shielded := map[ast.Node]bool{}
	if noEsc == nil {
		return shielded
	}
	ast.Walk(root, func(n ast.Node) bool {
		c, isCall := n.(*ast.Call)
		if !isCall {
			return true
		}
		for i, a := range c.Args {
			if !noEsc(c, i) {
				continue
			}
			for _, q := range placesIn(a) {
				shielded[q.node] = true
			}
		}
		return true
	})
	return shielded
}

func fieldPlaceAppendCopies(body ast.Node, noEsc argNoEscape) map[*ast.Call]bool {
	// The container-replacing assignment, and the return expression, each
	// field append sits under.
	rebind := map[*ast.Call]*ast.Assign{}
	retExpr := map[*ast.Call]ast.Expr{}
	// Bare reads of a root that BIND the container somewhere it outlives the
	// expression: a `var` initialiser or an assignment's value WHOSE BINDING
	// can hold the container. A scalar binding cannot, however the container
	// reaches it — `var t: i32 = lookup(o, k)` passes `o` to a call that
	// returns an i32, and an i32 names nothing.
	capturing := map[ast.Node]bool{}
	declared := map[string]ast.Type{}
	ast.Walk(body, func(n ast.Node) bool {
		if v, isVar := n.(*ast.Var); isVar {
			declared[v.Name] = v.Type
		}
		return true
	})
	markPushes := func(scope ast.Expr, want string, mark func(*ast.Call)) {
		ast.Walk(scope, func(m ast.Node) bool {
			if _, isLambda := m.(*ast.Lambda); isLambda {
				return false // runs when the closure is called, not here
			}
			c, isCall := m.(*ast.Call)
			if !isCall || !isArrayPushCall(c) {
				return true
			}
			if fa, isFA := c.Args[0].(*ast.FieldAccess); isFA {
				if root, _, ok := fieldPlace(fa); ok && (want == "" || root == want) {
					mark(c)
				}
			}
			return true
		})
	}
	// Struct-update spread bases, keyed by the base expression's place node.
	spreadOf := map[ast.Node]*ast.StructLit{}
	ast.Walk(body, func(n ast.Node) bool {
		lit, isLit := n.(*ast.StructLit)
		if !isLit || lit.Base == nil {
			return true
		}
		switch bx := lit.Base.(type) {
		case *ast.Ident:
			spreadOf[bx] = lit
		case *ast.FieldAccess:
			if _, _, ok := fieldPlace(bx); ok {
				spreadOf[bx] = lit
			}
		}
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		var bound ast.Expr
		var boundTo string
		var boundType ast.Type
		var boundTypeKnown bool
		switch st := n.(type) {
		case *ast.Assign:
			bound = st.Value
			if t, isID := st.Target.(*ast.Ident); isID && st.Value != nil {
				boundTo = t.Name
				boundType, boundTypeKnown = declared[t.Name]
				markPushes(st.Value, t.Name, func(c *ast.Call) { rebind[c] = st })
			}
		case *ast.Var:
			bound, boundTo = st.Init, st.Name
			boundType, boundTypeKnown = st.Type, true
		case *ast.Return:
			if st.Value != nil {
				markPushes(st.Value, "", func(c *ast.Call) { retExpr[c] = st.Value })
			}
		}
		if bound != nil && !(boundTypeKnown && !bindingHoldsContainer(boundType)) {
			shielded := shieldedPlaces(bound, noEsc)
			for _, q := range placesIn(bound) {
				// Reading a container to rebuild ITSELF (`a = A { ...a, … }`)
				// hands the old value to its own replacement, not to a name
				// that outlives it.
				if len(q.path) == 0 && q.root != boundTo && !shielded[q.node] {
					capturing[q.node] = true
				}
			}
		}
		return true
	})

	byRoot := map[string][]place{}
	for _, q := range placesIn(body) {
		byRoot[q.root] = append(byRoot[q.root], q)
	}
	esc := deferOrLambdaNames(body)
	out := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		c, isCall := n.(*ast.Call)
		if !isCall || !isArrayPushCall(c) {
			return true
		}
		fa, isFA := c.Args[0].(*ast.FieldAccess)
		if !isFA {
			return true
		}
		root, path, ok := fieldPlace(fa)
		if !ok {
			return true
		}
		p := place{root: root, path: path, node: fa}
		// The expression the append's own statement evaluates, if that
		// statement puts the reads after it out of reach.
		var scope ast.Expr
		if asn := rebind[c]; asn != nil {
			scope = asn.Value
		} else if re, isRet := retExpr[c]; isRet && !esc[root] {
			scope = re
		}
		inScope := map[ast.Node]bool{}
		if scope != nil {
			for _, q := range placesIn(scope) {
				inScope[q.node] = true
			}
		}
		for _, q := range byRoot[root] {
			if q.node == fa || !p.overlaps(q) || overriddenSpread(spreadOf[q.node], q, p) {
				continue
			}
			if scope == nil || inScope[q.node] || (len(q.path) == 0 && capturing[q.node]) {
				out[c] = true
				break
			}
		}
		return true
	})
	return out
}

// bindingHoldsContainer reports whether a binding of type `t` could hold a
// reference to a container read in its initialiser. Only the scalars are
// ruled out, by whitelist: every other type either is a pointer or is a
// runtime handle (Map, Reader, …) that can carry one, and an unknown type
// is not a licence to assume otherwise.
func bindingHoldsContainer(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.FloatType, ast.BoolType, ast.CharType, ast.VoidType:
		return false
	}
	return true
}

// overriddenSpread reports whether `q` is the spread base of a struct-update
// literal that explicitly overrides the field `p` continues into, so the
// literal's copy of `q` never carries p's buffer.
func overriddenSpread(lit *ast.StructLit, q, p place) bool {
	if lit == nil || len(q.path) >= len(p.path) {
		return false
	}
	next := p.path[len(q.path)]
	for _, fl := range lit.Fields {
		if fl.Name == next {
			return true
		}
	}
	return false
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
//
// A second accepted shape, with its own gate set (documented at the walk):
// the for-in element binding `var y = __foreach_iter_N[idx]`, whose
// container is the desugar's synthetic iterand local.
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

// computeMapCowBindSites finds the Map COW-mutator calls bound directly to a
// new local — see the mapCowBindSites field doc (#6227). `insert` / `cleared`
// return the map and bind through `var`; `without` returns a (Map, boolean)
// tuple, which destructuring and a plain `var t = m.without(k)` both bind, and
// BOTH owe the retain. Reading the tuple whole and projecting `t.0` / `t.1` is
// the spelling the rc corpus itself uses, and excluding it left the tuple's
// map element aliasing the receiver's handle at rc 1: two names, one count,
// both releasing — an rc underflow everywhere and a SIGSEGV on arm64 / a trap
// on wasm32 once the freed key cell was recycled (#8276).
//
// The FieldAccess arm is the same call with no binding at all —
// `m = m.without(k).0`, `f(m.without(k).0)`. The tuple is a temporary, but the
// field read deep-drops its box (freshOwnedFieldContainerType admits a
// seam-retained delete tuple), and that drop releases the map element, so this
// owes the retain for the same reason a binding does. Without the pair the box
// was never freed AND the receiver stayed tainted out of freeEligible: 128000 /
// 144000 / 104000 B over the corpus fixture (#8434).
//
// Purely syntactic: the walk runs on the already-mangled AST, the same form
// isMapMutatorCall matches at the call site.
func (b *builder) computeMapCowBindSites() map[ast.Node]bool {
	out := map[ast.Node]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.Var:
			if c, ok := s.Init.(*ast.Call); ok && isMapMutatorCall(c) {
				out[c] = true
			}
		case *ast.Destructure:
			if c, ok := s.Init.(*ast.Call); ok && isMapDeleteCall(c) {
				out[c] = true
			}
		case *ast.FieldAccess:
			if c, ok := s.Target.(*ast.Call); ok && isMapDeleteCall(c) {
				out[c] = true
			}
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
		// A `.with` receiver whose reference __fern_arr_cow_inplace CONSUMES
		// is not borrowable, and neither is an alias of one. The helper takes
		// the receiver's own reference over: at rc == 1 it mutates the buffer
		// IN PLACE and the element it overwrites is deep-dropped. The alias
		// inc is the only thing holding that buffer at rc 2, i.e. the only
		// reason the helper copies instead — cancelling it makes the mutation
		// visible through the alias, which is a use-after-free once the
		// overwritten element is freed and a silent value-semantics break
		// when it is not.
		//
		// arraySetConsumedReinit needs no test of its own: it is populated
		// only from names already in arraySetConsumed, so the loop-body shape
		// is covered by the line below rather than by a second clause.
		//
		// The self-host compiler does NOT have this bug today, because its rc
		// port has not taken the dead-alias cancellation yet — measured: it
		// answers 19 and 30 where both natives answered 99 and 54. Whoever
		// ports #4402 across (roadmap goal 2) has to port this guard with it,
		// or the collision arrives with it.
		if b.rc.arraySetConsumed[x] || b.rc.arraySetConsumed[y] {
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
	// For-in element borrow (#6888): the desugar's per-iteration element
	// binding `var y = __foreach_iter_N[__foreach_idx_N]`
	// (ast.DesugarForEachArray) reads an element the iterand array owns.
	// When every use of y is a read THROUGH the value (bindingConfinedToArm
	// over the whole body — a bind, store, capture, or unproven call all
	// refuse; a return is admitted only in the shapes forinElemReturnsConfined
	// names), y needs no reference of its own: the element stays alive
	// because the container's buffer does.
	//
	// The container here is the synthetic iter local, which user code cannot
	// name (ast.ForEachIterPrefix), so its release sites are exactly the ones
	// this marking pins: precise/nested drops refused via borrowSources, FBIP
	// donorship refused, leaving the exit sweeps — each on a function exit,
	// after the loop body's reads on that path. In-place element overwrite
	// cannot reach it either: `arr[i] = v` does not exist (checker E056), a
	// consumed `.with` receiver is refused below, and an unconsumed one incs
	// first, so __fern_arr_cow_inplace sees rc >= 2 and copies.
	//
	// Two deliberate departures from the bare-Ident shape above:
	//   - no freeEligible gate on x. That gate stands in for "x's buffer
	//     cannot be released before the borrow's last read", which holds
	//     structurally here — a borrowed-param-backed iter is freeEligible-
	//     tainted precisely BECAUSE the caller owns it, which makes it a
	//     safer borrow source, not a worse one.
	//   - x may itself be a borrowed alias (the walk above marks
	//     `var __foreach_iter_N = xs` when xs qualifies): xs is then already
	//     pinned to exit-sweep-only release, the same guarantee one level up.
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		f, ok := n.(*ast.For)
		if !ok {
			return true
		}
		body, ok := f.Body.(*ast.Block)
		if !ok || len(body.Stmts) == 0 {
			return true
		}
		v, ok := body.Stmts[0].(*ast.Var)
		if !ok || v.Init == nil {
			return true
		}
		ix, ok := v.Init.(*ast.Index)
		if !ok || !ix.Unchecked || ix.IsString || ix.IsSlice {
			return true
		}
		src, ok := ix.Array.(*ast.Ident)
		if !ok || !strings.HasPrefix(src.Name, ast.ForEachIterPrefix) {
			return true
		}
		y, x := v.Name, src.Name
		if b.rc.borrowedAlias[y] || b.rc.borrowSources[y] || b.rc.borrowSources[x] {
			return true
		}
		if !b.isOwnedRcLocal(x) || !b.isOwnedRcLocal(y) {
			return true
		}
		if reassigned[x] || reassigned[y] || scrutinee[x] || scrutinee[y] {
			return true
		}
		if b.rc.movedLocals[x] || b.rc.movedLocals[y] {
			return true
		}
		if b.rc.arraySetConsumed[x] || b.rc.arraySetConsumed[y] {
			return true
		}
		if !b.localNameUnique(x) || !b.localNameUnique(y) {
			return true
		}
		if !needsRcIncOnAlias(v.Init, b) || b.isOwnedContainerRead(v.Init) {
			return true
		}
		if !b.bindingConfinedToArm(b.fn.Body, y, v.Type) || !b.forinElemReturnsConfined(y) {
			return true
		}
		b.rc.borrowedAlias[y] = true
		b.rc.borrowedAliasSites[v] = true
		b.rc.borrowSources[x] = true
		return true
	})
}

// forinElemReturnsConfined reports whether every `return` that mentions the
// for-in element y hands out nothing that outlives the iterand (#8178).
// bindingConfinedToArm reads a field or element projection of y as a borrow
// wherever it stands; a return needs this second look because its value
// leaves the frame, and the exit sweep on that path releases the iterand.
//
// y may appear only projected (the target of a field access or the base of
// an index), and the returned value must then take one of two shapes:
//
//   - a plain projection chain rooted at y (`sd.f`, `sd.f.g`, `sd.xs[i]`) of
//     an rc-tracked type, which the Return lowering retains on its own
//     (needsRcIncOnAlias) — the credit returnedCountedProjection gives a
//     borrowed parameter, under the same returnedAliasIsRetained refusals
//     (pair-form and TRMC rewrite the return before that inc is reached);
//   - a value of a non-pointer type (`sd.fields.len()`, `sd.a == k`): nothing
//     pointer-shaped leaves, and each sub-expression is the transient read it
//     would be in statement position.
//
// Everything else stays refused: y itself (move-on-return would hand the
// caller an uncounted element) and any other pointer-typed value built around
// a projection of y — a fresh aggregate, a variant construction, a call
// result, a slice view — whose counting this rule has not shown.
func (b *builder) forinElemReturnsConfined(y string) bool {
	retained := returnedAliasIsRetained(b.fn, b.pairForm, b.trmcFuncs)
	ok := true
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		r, isRet := n.(*ast.Return)
		if !isRet || r.Value == nil || !ok || !exprMentionsIdent(r.Value, y) {
			return ok
		}
		switch {
		case !identOnlyProjected(r.Value, y):
			ok = false
		case needsRcIncOnAlias(r.Value, b):
			ok = retained && projectionRoot(r.Value) == y
		default:
			t := b.exprType(r.Value)
			ok = t != nil && !ast.IsPointerType(t)
		}
		return ok
	})
	return ok
}

// identOnlyProjected reports whether every mention of `name` in e is the
// target of a field access or the base of an index — read through, never
// handed on whole.
func identOnlyProjected(e ast.Expr, name string) bool {
	projected := map[*ast.Ident]bool{}
	ast.Walk(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FieldAccess:
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == name {
				projected[id] = true
			}
		case *ast.Index:
			if id, ok := x.Array.(*ast.Ident); ok && id.Name == name {
				projected[id] = true
			}
		}
		return true
	})
	ok := true
	ast.Walk(e, func(n ast.Node) bool {
		if id, isID := n.(*ast.Ident); isID && id.Name == name && !projected[id] {
			ok = false
		}
		return ok
	})
	return ok
}

// projectionRoot returns the ident a chain of field accesses and index reads
// is rooted at, or "" when e is anything else.
func projectionRoot(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.FieldAccess:
			e = x.Target
		case *ast.Index:
			e = x.Array
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
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

// growParam records what a callee may mutate in place through one parameter:
// the parameter's own buffer when it IS an array, and the named DIRECT array
// fields when it is a struct. `growAnyField` stands in for a nested or
// unresolvable field path, and makes every field of the struct growable.
//
// The field set is what keeps the caller-side bracket proportionate: a
// `LowerState` has twenty-odd array fields, and bracketing all of them because
// the callee appends to ONE forces a copy on each of the others as well.
type growParam struct {
	buffer bool
	fields map[string]bool
}

// growAnyField is the field-set entry for a path this analysis cannot name.
const growAnyField = "*"

func (g growParam) any() bool { return g.buffer || len(g.fields) > 0 }

func (g growParam) growsField(name string) bool {
	return g.fields[growAnyField] || g.fields[name]
}

// addField records one growable field, reporting whether it was new. The
// set has one form: growAnyField alone, or named fields — the unnamed path
// covers every name, and a name beside it would be read twice, by
// growsField as redundant and by the propagation over dying fields as the
// only entry that propagates.
func (g *growParam) addField(name string) bool {
	if g.fields[growAnyField] || g.fields[name] {
		return false
	}
	if g.fields == nil || name == growAnyField {
		g.fields = map[string]bool{}
	}
	g.fields[name] = true
	return true
}

// merge folds another parameter's growth into this one.
func (g *growParam) merge(o growParam) bool {
	changed := false
	if o.buffer && !g.buffer {
		g.buffer = true
		changed = true
	}
	for f := range o.fields {
		if g.addField(f) {
			changed = true
		}
	}
	return changed
}

// renameRoots maps a local that RENAMES another binding — `var c: C = c0;`,
// where c0 is named nowhere else in the body — to the name it renames, chased
// through chained renames to the root.
//
// A rename is one binding spelled twice: nothing after it can read the source,
// so the two names share every buffer with no second live reader. Every
// analysis that asks "which binding is this name?" has to agree on that, or
// the answer splits — callArgDeaths admitting the rename on its source's
// footing while computeGrowParams stopped propagating at it withdrew the
// caller-side bracket from a buffer the caller still read (#8498).
//
// A name declared twice is excluded: the occurrence census cannot tell its two
// bindings apart.
func renameRoots(body ast.Node) map[string]string {
	occurrences := map[string]int{}
	declCount := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			occurrences[x.Name]++
		case *ast.Var:
			declCount[x.Name]++
		}
		return true
	})
	direct := map[string]string{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar || declCount[v.Name] != 1 {
			return true
		}
		if src, isID := v.Init.(*ast.Ident); isID && occurrences[src.Name] == 1 {
			direct[v.Name] = src.Name
		}
		return true
	})
	out := make(map[string]string, len(direct))
	for name := range direct {
		root := direct[name]
		seen := map[string]bool{name: true, root: true}
		for {
			next, chained := direct[root]
			if !chained || seen[next] {
				break
			}
			root, seen[next] = next, true
		}
		out[name] = root
	}
	return out
}

// callArgDeaths marks, per call node, the ident arguments whose value can
// no longer be observed through that binding in this function after the
// call, so the #4873 bracket may skip them. Four shapes qualify:
//
//   - the strict self-reassign `x = f(.., x, ..)`: the RHS is exactly the
//     call and x occurs in it exactly once, directly as an argument — the
//     old binding is overwritten by the result (the #5056 move-and-rebind
//     shape, sans the `own` requirement);
//   - the return-position `return f(.., x, ..)` under the same
//     exactly-once rule: a return exits the function (loop or not), so no
//     later read exists. This is what keeps recursive accumulator tails
//     (`return walk(acc, …)`) on the in-place fast path — bracketing them
//     would force one copy per recursion level, the #4838 O(n²) class;
//   - the SOLE-OCCURRENCE shape (#6036): a PARAMETER read exactly once in
//     the whole body, at a straight-line position — no later read of the
//     binding exists at all, whatever the syntax around the call. This is
//     what covers `var t = f(b, v); return t;` and the inner call of
//     `return f(f(b, v), v + 1)`, neither of which is a reassign or a
//     direct return argument, yet both of which were paying one
//     full-buffer copy per call;
//   - the LAST-OCCURRENCE shape: the read at this call is the binding's
//     textually last (identOrder.isLast) and the call is enclosed by no
//     loop or lambda body, so control passes it once and nothing reads the
//     binding again. Admitted for a param and for a `var` local whose
//     initialiser is a direct call to a named function — the state-
//     threading chain `var a = s.emit(o); var b = a.emit(o); return b;`,
//     where every receiver is at its last use. Each of those was paying a
//     full-buffer copy per link, which is O(n²) bytes over a chain: the
//     self-host lowering threads its LowerState this way and one 400-arm
//     `else if` chain bumped 40 MB in `emit` alone. A local that RENAMES
//     an admitted name at that name's only occurrence — `var c: C = c0;`
//     on a parameter — is the same binding spelled twice, so it is
//     admitted on the source's footing.
//
// The last-occurrence test needs the no-loop gate to be sound at all:
// inside a loop the "last" occurrence re-executes, and an unbracketed
// in-place growth would be observed by the next iteration (interp
// copies). A name read inside a defer or a lambda is excluded outright —
// those run after the syntactic position that looks final.
//
// For a LOCAL the death verdict also needs the binding not to be an alias
// of something else still live: `var t = holder; f(t)` makes `t`'s last
// use unbracketed while `holder` still reads the same field buffers, and
// binding a struct incs the BOX, not the buffers inside it. A direct-call
// initialiser cannot be that: its result is either freshly allocated or
// shares a buffer with an argument, and an argument shares only when the
// callee grew it in place — which required that argument to have died at
// ITS call, so nothing observes the sharing. That is the same induction
// #4873's caller-side containment already rests on, and the transitive
// closure in computeGrowParams consults this map, so a buffer passed on
// unbracketed propagates as a growable position of the enclosing
// function's own parameter.
//
// A rename is not that alias either, for the plainer reason that its source is
// never named again — and computeGrowParams resolves through it (renameRoots)
// so the closure does not stop at the new name.
func callArgDeaths(fn *ast.FuncDecl, info *checker.Info, obs map[string][]fieldObs) map[*ast.Call]map[string]bool {
	body := fn.Body
	out := map[*ast.Call]map[string]bool{}
	// Occurrence census over the whole body, for the sole-occurrence shape.
	// A shadowing inner declaration of the same name inflates the count,
	// which only ever withholds the death verdict — the safe direction.
	occurrences := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			occurrences[id.Name]++
		}
		return true
	})
	isParam := map[string]bool{}
	// frameOwns is the set of names whose value this frame reclaims: an
	// `own` parameter and every local. A borrowed parameter is excluded —
	// its box belongs to the caller.
	frameOwns := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
			frameOwns[p.Name] = true
		}
	}
	ast.Walk(body, func(n ast.Node) bool {
		if v, isVar := n.(*ast.Var); isVar && !isParam[v.Name] {
			frameOwns[v.Name] = true
		}
		return true
	})
	// Locals bound from a direct call to a named function — the only local
	// binding form the last-occurrence shape admits (see above). A name
	// declared more than once is dropped: the occurrence order cannot tell
	// the two bindings apart.
	callInitLocal := map[string]bool{}
	declCount := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar {
			return true
		}
		declCount[v.Name]++
		if c, isCall := v.Init.(*ast.Call); isCall {
			if _, named := c.Callee.(*ast.Ident); named {
				callInitLocal[v.Name] = true
			}
		}
		return true
	})
	for name, n := range declCount {
		if n > 1 {
			delete(callInitLocal, name)
		}
	}
	// A local UNPACKED from a call-init local's field — `var eqL = park(…);
	// var sl = eqL.state;` — is admitted on the same footing. The alias
	// exclusion asks whether another live name in this frame reads the same
	// buffers, and the unpack is the only reader of that field: `h.f` occurs
	// once in the body and every other mention of `h` selects a different
	// field, so nothing survives the last use of `q` that names `h.f`. The
	// self-host's `lower_expr_binary` threads its state through exactly this
	// shape twice per string comparison, and each link was paying one copy of
	// the whole op list.
	unpackInitLocal := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar || declCount[v.Name] != 1 {
			return true
		}
		fa, isField := v.Init.(*ast.FieldAccess)
		if !isField {
			return true
		}
		hid, isID := fa.Target.(*ast.Ident)
		if !isID || !callInitLocal[hid.Name] {
			return true
		}
		reads, selections, mentions := 0, 0, 0
		ast.Walk(body, func(m ast.Node) bool {
			switch x := m.(type) {
			case *ast.FieldAccess:
				if id, ok := x.Target.(*ast.Ident); ok && id.Name == hid.Name {
					selections++
					if x.Field == fa.Field {
						reads++
					}
				}
			case *ast.Ident:
				if x.Name == hid.Name {
					mentions++
				}
			}
			return true
		})
		// Every field selection also walks its target Ident, so h is named
		// only by selections exactly when the two counts agree.
		if reads == 1 && selections == mentions {
			unpackInitLocal[v.Name] = true
		}
		return true
	})
	// A local RENAMED from an already-admitted name — `var c: C = c0;` on a
	// parameter, the line every state-threading function in the self-host
	// lowering opens with — is admitted on that name's footing. The alias
	// exclusion asks whether another live name in this frame reads the same
	// buffers, and a rename taking the source's ONLY occurrence leaves none.
	// Without this the rename withheld the death from every call in the chain
	// below it, so the container reaching each field append was at rc 2 and
	// copied the whole buffer (#8498).
	aliasInitLocal := map[string]bool{}
	for name, root := range renameRoots(body) {
		if isParam[root] || callInitLocal[root] || unpackInitLocal[root] {
			aliasInitLocal[name] = true
		}
	}
	admitted := func(name string) bool {
		return isParam[name] || callInitLocal[name] || unpackInitLocal[name] || aliasInitLocal[name]
	}
	// markOnce marks `name` dead at the call inside `scope` that takes it,
	// when scope names it exactly once and that occurrence is a direct
	// argument. Every shape below hands it the expression whose evaluation is
	// the name's last chance to be read — an assignment's value, a returned
	// expression, or the call itself.
	//
	// The call taking the name need not be scope's OUTERMOST:
	// `c = emit(emit(c, v), v)` hands c to the inner one, and the store
	// supersedes c either way, so the death belongs where the argument is.
	// Stopping at the top level left that spelling — and the method chain
	// `c = c.emit(v).emit(v)` that desugars to it — paying a full-buffer copy
	// per link inside a loop, where the last-occurrence shapes cannot help
	// (#8696).
	//
	// Naming it exactly once is the whole guard, and it is also what makes the
	// site unambiguous: a second read anywhere in scope would see the buffer
	// the callee grew, so `c = emit(emit(c, v), c.insts.len())` declines.
	markOnce := func(scope ast.Expr, name string) {
		total := 0
		ast.Walk(scope, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				total++
			}
			return true
		})
		if total != 1 {
			return
		}
		var site *ast.Call
		ast.Walk(scope, func(m ast.Node) bool {
			c, isCall := m.(*ast.Call)
			if !isCall {
				return true
			}
			for _, a := range c.Args {
				if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
					site = c
				}
			}
			return true
		})
		if site == nil {
			return
		}
		if out[site] == nil {
			out[site] = map[string]bool{}
		}
		out[site][name] = true
	}
	ast.Walk(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.Assign:
			t, ok := st.Target.(*ast.Ident)
			if !ok {
				return true
			}
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				markSupersededFields(out, sl, t.Name)
				return true
			}
			c, ok := st.Value.(*ast.Call)
			if !ok {
				return true
			}
			markOnce(c, t.Name)
		case *ast.Return:
			// `return S { ...x, f: g(.., x.f, ..) }` is the same superseded
			// field as the assignment form: nothing runs after the return,
			// so the old field value cannot be read back through x. It needs
			// one condition the assignment does not, because there is no
			// store to x here — the FRAME must own x. A borrowed parameter's
			// box outlives the call, and its caller can still read the field
			// the callee grew in place.
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				if sl.Base != nil {
					if bid, ok := sl.Base.(*ast.Ident); ok && frameOwns[bid.Name] {
						markSupersededFields(out, sl, bid.Name)
					}
				}
				return true
			}
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
	// The TWO-STATEMENT spelling of the self-reassign shape. `x = f(…, x, …)`
	// is one statement and matched above; a step that hands something back
	// beside the cursor — a label id, an offset, a slot number — or that just
	// names the result before storing it, spells the same thing as two:
	//
	//	let (c2, p) = emit(c, op);   var c2 = emit(c, op);
	//	c = c2;                      c = c2;
	//
	// The store still supersedes x before any other statement runs, so no
	// later read can reach the old buffer through it, exactly as in the
	// one-statement form. Neither half matched on its own: the binding
	// statement stores to a new name, and `c = c2` names no call. Inside a
	// loop the last-occurrence shapes are out too (`repeating`), so nothing
	// marked the argument dead and every call paid a full-buffer copy — 920 ms
	// against 0 ms for the same emit written as one statement, over 20000
	// appends (#8633).
	//
	// The store's value must not READ x: `var y = f(x); x = g(x);` would hand
	// g the buffer the callee just grew.
	ast.Walk(body, func(n ast.Node) bool {
		blk, isBlk := n.(*ast.Block)
		if !isBlk {
			return true
		}
		for i := 0; i+1 < len(blk.Stmts); i++ {
			var init ast.Expr
			switch st := blk.Stmts[i].(type) {
			case *ast.Var:
				init = st.Init
			case *ast.Destructure:
				init = st.Init
			default:
				continue
			}
			c, isCall := init.(*ast.Call)
			if !isCall {
				continue
			}
			es, isExpr := blk.Stmts[i+1].(*ast.ExprStmt)
			if !isExpr {
				continue
			}
			asn, isAsn := es.Expr.(*ast.Assign)
			if !isAsn || asn.Value == nil {
				continue
			}
			t, isID := asn.Target.(*ast.Ident)
			if !isID || stmtReferencesName(asn.Value, t.Name) {
				continue
			}
			markOnce(c, t.Name)
		}
		return true
	})
	// Sole-occurrence shape. `repeating` is every call reachable from a
	// loop or lambda body — a single textual read there is still many
	// dynamic reads, so those calls are excluded.
	repeating := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach, *ast.Lambda:
		default:
			return true
		}
		ast.Walk(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.Call); ok {
				repeating[c] = true
			}
			return true
		})
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || !isParam[aid.Name] || occurrences[aid.Name] != 1 {
				continue
			}
			if out[c] == nil {
				out[c] = map[string]bool{}
			}
			out[c][aid.Name] = true
		}
		return true
	})
	// Last-occurrence shape. Same no-loop / no-lambda gate as above, plus the
	// defer-and-lambda exclusion (a capture is read when the closure runs, not
	// where it is written) and markOnce's exactly-once-in-this-call rule, so a
	// second read inside the same call cannot observe the first's growth.
	escaping := deferOrLambdaNames(body)
	order := identOrderOf(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || !order.isLast(aid) {
				continue
			}
			if !admitted(aid.Name) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	// Path-last-occurrence shape. `order.isLast` is a TEXTUAL test, and the
	// self-host lowering is written as a chain of `if (…) { … return …; }`
	// branches that each thread the state once: every one of them has a
	// textually later read, on a path that cannot also have run. So a read is
	// equally final when the statement list it sits in RETURNS before
	// mentioning the name again — control leaves the function from inside this
	// block, so no later statement of the body is reachable. The same no-loop
	// / no-lambda / exactly-once gates as the textual shape apply; a `break` or
	// `continue` that could leave the block before the return withdraws it,
	// since control would then reach the code after it.
	stmtIdx := callBlockPositions(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || order.isLast(aid) {
				continue
			}
			if !admitted(aid.Name) {
				continue
			}
			// An ARRAY position is excluded. The death withdraws the bracket
			// around the argument's OWN buffer there, so the callee grows the
			// caller's buffer in place and the superseded generation is left to
			// a bare __fern_rc_dec — which decrements to zero without freeing
			// (the typed drop half of reclaim is not built), so that buffer and
			// every element it holds stay live. The conformance leak census
			// reads it as 115 extra unpaired allocations over five regex
			// fixtures. The textual shape reaches the same gap where it already
			// applies; this one is new, and the cliff gate puts the array half
			// of it at 0.02% of the bytes, so it is not taken.
			if arrayArgPosition(info, c, aid) {
				continue
			}
			if !returnsBeforeReading(stmtIdx, c, aid.Name) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	markUnobservedParamFields(out, fn, info, obs, repeating, escaping, occurrences)
	return out
}

// arrayArgPosition reports whether `aid` is an argument of `c` at a parameter
// position of ARRAY type. A call whose callee has no known signature answers
// true: an unresolvable position is treated as the array case.
func arrayArgPosition(info *checker.Info, c *ast.Call, aid *ast.Ident) bool {
	if info == nil {
		return true
	}
	callee, isID := c.Callee.(*ast.Ident)
	if !isID {
		return true
	}
	sig := info.FuncSigs[callee.Name]
	if sig == nil {
		return true
	}
	for i, a := range c.Args {
		if id, ok := a.(*ast.Ident); !ok || id != aid {
			continue
		}
		if i >= len(sig.Params) {
			return true
		}
		_, isArr := sig.Params[i].(ast.ArrayType)
		return isArr
	}
	return true
}

// blockPos locates a call in the innermost statement list holding it.
type blockPos struct {
	blk *ast.Block
	idx int
}

// callBlockPositions maps every call in `body` to its innermost enclosing
// statement list and the index of the statement it appears in.
func callBlockPositions(body ast.Node) map[*ast.Call]blockPos {
	var blocks []*ast.Block
	ast.Walk(body, func(n ast.Node) bool {
		if b, ok := n.(*ast.Block); ok {
			blocks = append(blocks, b)
		}
		return true
	})
	out := map[*ast.Call]blockPos{}
	// Pre-order, so an inner block overwrites the outer one's verdict.
	for _, b := range blocks {
		for i, st := range b.Stmts {
			ast.Walk(st, func(n ast.Node) bool {
				if c, ok := n.(*ast.Call); ok {
					out[c] = blockPos{blk: b, idx: i}
				}
				return true
			})
		}
	}
	return out
}

// returnsBeforeReading reports whether the statement list holding `c` returns
// out of the function before mentioning `name` again — so this read is the
// last one on every path that reaches it, whatever comes later in the body.
func returnsBeforeReading(pos map[*ast.Call]blockPos, c *ast.Call, name string) bool {
	bp, ok := pos[c]
	if !ok {
		return false
	}
	// Once in the whole statement, so nothing else in it reads the name after
	// the call — the statement-level twin of markOnce's rule.
	if mentions(bp.blk.Stmts[bp.idx], name) != 1 {
		return false
	}
	for i := bp.idx + 1; i < len(bp.blk.Stmts); i++ {
		if mentions(bp.blk.Stmts[i], name) != 0 || jumpEscapes(bp.blk.Stmts[i]) {
			return false
		}
		if _, isRet := bp.blk.Stmts[i].(*ast.Return); isRet {
			return true
		}
	}
	return false
}

// mentions counts the occurrences of `name` in a statement subtree.
func mentions(n ast.Node, name string) int {
	k := 0
	ast.Walk(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && id.Name == name {
			k++
		}
		return true
	})
	return k
}

// jumpEscapes reports whether a break / continue inside this statement can
// transfer control out of the statement list holding it — an unlabelled jump
// outside any loop the statement itself contains, or any labelled one.
func jumpEscapes(st ast.Stmt) bool {
	inLoop := map[ast.Node]bool{}
	ast.Walk(st, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			ast.Walk(n, func(m ast.Node) bool { inLoop[m] = true; return true })
		}
		return true
	})
	out := false
	ast.Walk(st, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Break:
			if !inLoop[n] || x.Label != "" {
				out = true
			}
		case *ast.Continue:
			if !inLoop[n] || x.Label != "" {
				out = true
			}
		}
		return true
	})
	return out
}

// fieldObs summarises which of a parameter's own fields a function can read
// the value of, or hand to something that can. `all` is the give-up state:
// some occurrence of the parameter is a shape this summary does not model —
// bound to another name, spread into a literal, returned, captured, or passed
// through an indirect call — so any field may be reached through it.
type fieldObs struct {
	all    bool
	fields map[string]bool
}

func (o fieldObs) reads(field string) bool { return o.all || o.fields[field] }

// paramOccKind classifies one occurrence of a parameter name.
type paramOccKind struct {
	// id is the occurrence itself, for the reachability comparison.
	id *ast.Ident
	// field is the field selected when the occurrence is `p.field`.
	field string
	// callee / argIdx name the direct-call position when the occurrence is
	// a bare-ident argument.
	callee string
	argIdx int
	// call is the call node an argument occurrence belongs to.
	call *ast.Call
	// modelled is false for every other shape.
	modelled bool
}

// classifyParamOccurrences classifies every occurrence of `name` in `body`.
// An occurrence that is neither a field selection on the name nor a bare-ident
// argument of a direct call gets modelled=false, which forces the fieldObs
// summary — and every caller-side use of it — to the conservative answer.
func classifyParamOccurrences(body ast.Node, name string) []paramOccKind {
	byIdent := map[*ast.Ident]paramOccKind{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FieldAccess:
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == name {
				byIdent[id] = paramOccKind{field: x.Field, modelled: true}
			}
		case *ast.Call:
			cid, isID := x.Callee.(*ast.Ident)
			if !isID {
				return true
			}
			for i, a := range x.Args {
				if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
					byIdent[aid] = paramOccKind{callee: cid.Name, argIdx: i, call: x, modelled: true}
				}
			}
		}
		return true
	})
	var out []paramOccKind
	ast.Walk(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		k := byIdent[id]
		k.id = id
		out = append(out, k)
		return true
	})
	return out
}

// blockChain is a node's position in the nested statement lists above it:
// one step per enclosing block, outermost first.
type chainStep struct {
	blk *ast.Block
	idx int
}

// reachability answers "can control reach B after passing A" for two
// occurrences in one function body, structurally: a block every path out of
// which RETURNS cannot be followed by a later statement of an enclosing list.
type reachability struct {
	chains    map[ast.Node][]chainStep
	divergent map[*ast.Block]bool
}

func newReachability(body ast.Node) reachability {
	var blocks []*ast.Block
	ast.Walk(body, func(n ast.Node) bool {
		if b, ok := n.(*ast.Block); ok {
			blocks = append(blocks, b)
		}
		return true
	})
	r := reachability{chains: map[ast.Node][]chainStep{}, divergent: map[*ast.Block]bool{}}
	// Pre-order, so each node's chain accumulates outermost-first.
	for _, b := range blocks {
		r.divergent[b] = blockDiverges(b)
		for i, st := range b.Stmts {
			ast.Walk(st, func(n ast.Node) bool {
				r.chains[n] = append(r.chains[n], chainStep{blk: b, idx: i})
				return true
			})
		}
	}
	return r
}

// precedesUnreachably reports whether `earlier` sits on a path that cannot
// also reach `later`: they share an enclosing statement list, `earlier` is in
// a statement before `later`'s, and some block between that statement and
// `earlier` returns out of the function.
func (r reachability) precedesUnreachably(earlier, later ast.Node) bool {
	ce, cl := r.chains[earlier], r.chains[later]
	k := 0
	for k < len(ce) && k < len(cl) && ce[k].blk == cl[k].blk && ce[k].idx == cl[k].idx {
		k++
	}
	if k >= len(ce) || k >= len(cl) || ce[k].blk != cl[k].blk || ce[k].idx >= cl[k].idx {
		return false
	}
	for j := k + 1; j < len(ce); j++ {
		if r.divergent[ce[j].blk] {
			return true
		}
	}
	return false
}

// blockDiverges reports whether reaching this statement list always leaves the
// function. A `break` or `continue` that can escape the list withdraws the
// verdict: control would resume after the enclosing loop instead.
func blockDiverges(b *ast.Block) bool {
	for _, st := range b.Stmts {
		if jumpEscapes(st) {
			return false
		}
		if stmtDiverges(st) {
			return true
		}
	}
	return false
}

// stmtDiverges is the MUST twin of stmtCanLeaveFunction: it asks whether
// reaching this statement ALWAYS leaves the function. `?` is deliberately
// absent — it leaves only on Err.
func stmtDiverges(st ast.Stmt) bool {
	switch x := st.(type) {
	case *ast.Return:
		return true
	case *ast.Block:
		return blockDiverges(x)
	case *ast.If:
		return x.Else != nil && stmtDiverges(x.Then) && stmtDiverges(x.Else)
	}
	return false
}

// computeParamFieldObs returns, per function name, one fieldObs per parameter:
// the fields that function can observe through that parameter.
//
// It exists for the #4873 containment bracket. The bracket protects the value
// a field buffer holds AT the call from a callee that grows it in place, and a
// caller whose binding survives the call pays one full-buffer copy for it —
// which is what puts `irlower.LowerState.emit` at the top of the append-cliff
// baseline even though the append itself lowers in place. When every surviving
// use of the argument is a call that provably cannot reach the field, there is
// nothing to protect and the bracket is pure cost.
//
// A callee that is reachable through a vtable is `all` on every parameter: the
// static name at the call site is not the body that runs.
func computeParamFieldObs(prog *ast.Program, vtableDispatched map[string]bool) map[string][]fieldObs {
	obs := map[string][]fieldObs{}
	sites := map[string][][]paramOccKind{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		o := make([]fieldObs, len(fn.Params))
		s := make([][]paramOccKind, len(fn.Params))
		dyn := vtableDispatched[fn.Name]
		for pi, p := range fn.Params {
			o[pi].fields = map[string]bool{}
			if dyn {
				o[pi].all = true
				continue
			}
			for _, k := range classifyParamOccurrences(fn.Body, p.Name) {
				switch {
				case !k.modelled:
					o[pi].all = true
				case k.field != "":
					o[pi].fields[k.field] = true
				default:
					s[pi] = append(s[pi], k)
				}
			}
		}
		obs[fn.Name] = o
		sites[fn.Name] = s
	}
	for changed := true; changed; {
		changed = false
		for name, ps := range sites {
			for pi, ks := range ps {
				o := obs[name][pi]
				if o.all {
					continue
				}
				for _, k := range ks {
					co, has := obs[k.callee]
					if !has || k.argIdx >= len(co) || vtableDispatched[k.callee] {
						o.all = true
						break
					}
					c := co[k.argIdx]
					if c.all {
						o.all = true
						break
					}
					for f := range c.fields {
						if !o.fields[f] {
							o.fields[f] = true
							changed = true
						}
					}
				}
				if o.all && !obs[name][pi].all {
					changed = true
				}
				obs[name][pi] = o
			}
		}
	}
	return obs
}

// markUnobservedParamFields is the field-granular death the #4873 bracket
// needs for a STRUCT argument whose binding survives the call: the buffer at
// `p.f` cannot be read back through this frame, so the callee may grow it in
// place even though `p` itself is still live.
//
// The bracket protects the value the field holds at the call. Every other
// occurrence of `p` in the body therefore has to be unable to reach that
// value — before the call as well as after it, since an occurrence before it
// could bind an alias that outlives the call. Two shapes qualify: selecting a
// DIFFERENT field, and passing `p` to a direct call whose fieldObs summary
// excludes `f`. A summary that excludes `f` also excludes leaking it, because
// the only ways out of the callee (returning it, storing it, spreading it into
// a literal) are unmodelled occurrences, which the summary answers `all` for.
//
// The result of THIS call is not such an alias: it is the next generation of
// the threaded value, and reading it is reading what the grow produced rather
// than what the field held before.
//
// An occurrence that RETURNS out of the function before this call is reached
// is not "before" it at all — the self-host lowering is a chain of
// `if (…) { … return …; }` branches that each thread the state once, so every
// one of them has textual company it can never run with.
func markUnobservedParamFields(out map[*ast.Call]map[string]bool, fn *ast.FuncDecl, info *checker.Info, obs map[string][]fieldObs, repeating map[*ast.Call]bool, escaping map[string]bool, occurrences map[string]int) {
	if obs == nil || info == nil {
		return
	}
	paramNames := map[string]bool{}
	for _, p := range fn.Params {
		paramNames[p.Name] = true
	}
	occs := map[string][]paramOccKind{}
	for name := range paramNames {
		occs[name] = classifyParamOccurrences(fn.Body, name)
	}
	reach := newReachability(fn.Body)
	ast.Walk(fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		callee, isID := c.Callee.(*ast.Ident)
		if !isID {
			return true
		}
		sig := info.FuncSigs[callee.Name]
		if sig == nil {
			return true
		}
		for ai, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || !paramNames[aid.Name] || escaping[aid.Name] || ai >= len(sig.Params) {
				continue
			}
			st, isStruct := sig.Params[ai].(ast.StructType)
			if !isStruct || isMapType(st) {
				continue
			}
			sd, has := info.Structs[st.Name]
			if !has {
				continue
			}
			// Exactly once in this call, so a second read here cannot see
			// the first's growth.
			inThisCall := 0
			ast.Walk(c, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && id.Name == aid.Name {
					inThisCall++
				}
				return true
			})
			if inThisCall != 1 || occurrences[aid.Name] < 1 {
				continue
			}
			for _, fld := range sd.Fields {
				if _, isArr := fld.Type.(ast.ArrayType); !isArr {
					continue
				}
				if !paramFieldUnreachable(occs[aid.Name], c, fld.Name, obs, reach) {
					continue
				}
				if out[c] == nil {
					out[c] = map[string]bool{}
				}
				out[c][aid.Name+"."+fld.Name] = true
			}
		}
		return true
	})
}

// paramFieldUnreachable reports whether every occurrence of the parameter
// OTHER than the one at `c` is unable to reach the buffer at `p.field`.
func paramFieldUnreachable(occs []paramOccKind, c *ast.Call, field string, obs map[string][]fieldObs, reach reachability) bool {
	for _, k := range occs {
		if k.call == c {
			continue
		}
		if k.id != nil && reach.precedesUnreachably(k.id, c) {
			continue
		}
		switch {
		case !k.modelled:
			return false
		case k.field != "":
			if k.field == field {
				return false
			}
		default:
			co, has := obs[k.callee]
			if !has || k.argIdx >= len(co) || co[k.argIdx].reads(field) {
				return false
			}
		}
	}
	return true
}

// markSupersededFields handles the struct self-update `x = S { ...x, f:
// g(.., x.f, ..) }`: the field value's call receives `x.f` exactly once in
// the whole statement, and the statement's own store overwrites that field
// of x, so the old buffer cannot be observed through x afterwards — the
// field-level twin of the `x = f(x)` shape. The key is "x.f"; the bracket
// looks a single-hop field argument up under it. The callee's own in-place
// push retains what it grew, so the update's release of the old field value
// only decs — which is what kept this shape correct before field chains
// were bracketed at all, and what made every byte the x86 assembler emits a
// copy of the whole code buffer once they were.
func markSupersededFields(out map[*ast.Call]map[string]bool, sl *ast.StructLit, target string) {
	if sl.Base == nil {
		return
	}
	if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != target {
		return
	}
	for _, f := range sl.Fields {
		c, ok := f.Value.(*ast.Call)
		if !ok {
			continue
		}
		if _, named := c.Callee.(*ast.Ident); !named {
			continue
		}
		direct := 0
		for _, a := range c.Args {
			if fa, ok := a.(*ast.FieldAccess); ok && fa.Field == f.Name {
				if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
					direct++
				}
			}
		}
		if direct != 1 {
			continue
		}
		total := 0
		ast.Walk(sl, func(m ast.Node) bool {
			if fa, ok := m.(*ast.FieldAccess); ok && fa.Field == f.Name {
				if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
					total++
				}
			}
			return true
		})
		if total != 1 {
			continue
		}
		if out[c] == nil {
			out[c] = map[string]bool{}
		}
		out[c][target+"."+f.Name] = true
	}
}

// fieldNameOfDirect names the field a ONE-HOP access selects on `root`, or
// growAnyField for a longer chain, whose intermediate hops are not tracked.
func fieldNameOfDirect(fa *ast.FieldAccess, root *ast.Ident) string {
	if id, ok := fa.Target.(*ast.Ident); ok && id == root {
		return fa.Field
	}
	return growAnyField
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
func computeGrowParams(prog *ast.Program, info *checker.Info, obs map[string][]fieldObs) map[string][]growParam {
	grow := newSummaryTable[[]growParam](len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			grow.vals[fn.Name] = make([]growParam, len(fn.Params))
		}
	}
	// A rename resolves to the binding it renames, so a parameter threaded
	// through `var c: C = c0;` still propagates as a growable position of c0.
	renames := map[*ast.FuncDecl]map[string]string{}
	paramIdx := func(fn *ast.FuncDecl, name string) int {
		if fn.Body != nil {
			r, ok := renames[fn]
			if !ok {
				r = renameRoots(fn.Body)
				renames[fn] = r
			}
			if root, isRename := r[name]; isRename {
				name = root
			}
		}
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
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		g := grow.vals[fn.Name]
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
						g[pi].buffer = true
					}
				case *ast.FieldAccess:
					// Chase a field CHAIN to its root ident so a nested
					// receiver (`s.cur.insts.append(x)`, the EmitState
					// functional-update shape) seeds too — under growAnyField,
					// since only a one-hop receiver names a field of the param.
					if rid, ok := fieldChainRoot(r); ok {
						if pi := paramIdx(fn, rid.Name); pi >= 0 {
							g[pi].addField(fieldNameOfDirect(r, rid))
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
	deathsOf := map[*ast.FuncDecl]map[*ast.Call]map[string]bool{}
	grow.fixpoint(prog.Funcs, func(fn *ast.FuncDecl) bool {
		if fn.Body == nil {
			return false
		}
		g := grow.at(fn.Name)
		deaths, ok := deathsOf[fn]
		if !ok {
			deaths = callArgDeaths(fn, info, obs)
			deathsOf[fn] = deaths
		}
		changed := false
		ast.Walk(fn.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.Call)
			if !ok {
				return true
			}
			cid, ok := c.Callee.(*ast.Ident)
			if !ok {
				return true
			}
			cg, ok := grow.get(cid.Name)
			if !ok {
				return true
			}
			for ai, a := range c.Args {
				if ai >= len(cg) || !cg[ai].any() {
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
						if g[pi].merge(cg[ai]) {
							changed = true
						}
						continue
					}
					// A field the bracket skips (markUnobservedParamFields)
					// is equally unprotected, so it propagates the same
					// way: the enclosing param grows at that field, and
					// ITS caller brackets a surviving argument there.
					// growsField reads the callee's set the way the
					// bracket does, so an unnamed growth covers every
					// dying field.
					for key := range deaths[c] {
						f, isField := strings.CutPrefix(key, arg.Name+".")
						if isField && cg[ai].growsField(f) && g[pi].addField(f) {
							changed = true
						}
					}
				case *ast.FieldAccess:
					// `g(p.f)` — a param-field argument aliases the
					// param's field buffer and is never bracketed, so a
					// growable position propagates as a field growth of
					// the param (chain root for nested fields).
					if rid, ok := fieldChainRoot(arg); ok {
						if pi := paramIdx(fn, rid.Name); pi >= 0 {
							if g[pi].addField(fieldNameOfDirect(arg, rid)) {
								changed = true
							}
						}
					}
				}
			}
			return true
		})
		return changed
	})
	return grow.vals
}

// computeReturnOwnMoves finds the `return f(…, p, …)` sites that hand THIS
// function's `own` param p straight on to another `own` parameter, and claims
// each as a transfer: the argument stops paying the compensating retain, and
// the sweep emitted at that one return skips p.
//
// The whole-function movedLocals cannot express this. Move-on-call claims an
// own param only at its textually-LAST occurrence, so on a function shaped
//
//	if (…) { return inner(p, k); }   // a transfer, but not the last occurrence
//	…
//	return p;                        // the last occurrence
//
// every early return pays `ownArgNeedsRetain`'s retain instead. The retain is
// sound but expensive: the callee then sees the value at rc>1, so its first
// append copies the whole buffer — one copy per call. That is 63 MB on a
// single arm64 compile, all of it in the .data emitters
// arm64_gas_data_ascii / _data_le, which are exactly this shape (#6125).
//
// The claim is sound WITHOUT a liveness analysis because the sweep is emitted
// per return site (emitRcDecLocalsAtExitExcept): the function leaves through
// this return, so excluding p here says nothing about the paths that leave
// through a different one, and each of those still sweeps p normally. What the
// site must guarantee is that p is transferred exactly ONCE along it, hence:
//
//   - exactly one occurrence of p anywhere in the return statement, so a
//     `return f(p, g(p))` — two transfers, one sweep — cannot qualify;
//   - the occurrence is a bare ident at an `own` position of a direct call;
//   - the function has no defers and is not pair-form, because those returns
//     lower through paths that emit their own sweep and would not consult
//     this map while the retain had already been dropped.
//
// The retain it removes was only ever balancing the sweep dec that the
// exclusion now removes too, so the net rc is unchanged — one release, at the
// callee, exactly as for the last-occurrence case move-on-call already claims.
//
// A p that move-on-call already claimed WHOLE-FUNCTION (movedLocals, via a
// different, textually-later occurrence) is still claimed here: the sweep
// then skips p on EVERY path, so an unclaimed early transfer would pay the
// retain with nothing left to balance it — one leaked box per call, and the
// callee growing p's buffers at rc>1 (the self-host x86 assembler leaked one
// X86Asm per instruction exactly this way). No path can release twice: every
// site this pass claims is a return, so control cannot reach a second
// claimed transfer after it. Only the same-node guard remains.
func (b *builder) computeReturnOwnMoves() map[ast.Node]string {
	out := map[ast.Node]string{}
	if b.fn.Body == nil || b.thisIsPair || len(b.info.OwnFuncs) == 0 {
		return out
	}
	var defers []*ast.Defer
	collectDefers(b.fn.Body, &defers)
	if len(defers) > 0 {
		return out
	}
	ownParam := map[string]bool{}
	for _, p := range b.fn.Params {
		// Only a param the sweep actually decs can be excluded from it; for
		// any other the retain is not emitted either (ownArgNeedsRetain), so
		// dropping it would release a reference this frame does not own.
		if p.Own && rcTrackedSlotType(p.Type) && b.rc.freeEligible[p.Name] {
			ownParam[p.Name] = true
		}
	}
	if len(ownParam) == 0 {
		return out
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.Return)
		if !ok || ret.Value == nil {
			return true
		}
		counts := map[string]int{}
		ast.Walk(ret.Value, func(inner ast.Node) bool {
			if id, isID := inner.(*ast.Ident); isID {
				counts[id.Name]++
			}
			return true
		})
		ast.Walk(ret.Value, func(inner ast.Node) bool {
			if _, claimed := out[ret]; claimed {
				return false
			}
			call, isCall := inner.(*ast.Call)
			if !isCall {
				return true
			}
			callee, isID := call.Callee.(*ast.Ident)
			if !isID {
				return true
			}
			if _, isLocal := b.locals[callee.Name]; isLocal {
				return true // shadowed by a local — not a direct call
			}
			flags, isOwn := b.info.OwnFuncs[callee.Name]
			if !isOwn {
				return true
			}
			for i := 0; i < len(call.Args) && i < len(flags); i++ {
				if !flags[i] {
					continue
				}
				arg, isArgID := call.Args[i].(*ast.Ident)
				if !isArgID || !ownParam[arg.Name] || counts[arg.Name] != 1 {
					continue
				}
				if b.rc.ownCallMoveArgs[arg] {
					continue // move-on-call already claimed this very node
				}
				out[ret] = arg.Name
				b.rc.ownCallMoveArgs[arg] = true
				return false
			}
			return true
		})
		return true
	})
	return out
}

// computeSelfReassignOwnMoves claims the `p = f(…, p, …)` sites that hand THIS
// function's `own` param p straight on to another `own` parameter and rebind p
// to the result. The argument stops paying `ownArgNeedsRetain`'s compensating
// retain.
//
// The retain was never balanced on this shape. A self-reassign emits NO
// overwrite-dec — callConsumesIdent suppresses it, because the callee owns and
// drops the old binding — so the extra reference the retain added had nothing
// left to spend it: one leaked box per call, and the callee growing p's buffers
// at rc>1, so its first append copied the whole thing. On the self-host x86
// assembler that came to 130 MB of copies on a single compile, all of it from
// two `a = f(a); return g(a);` chains in the branch emitters.
//
// The claim needs no liveness analysis. The site rebinds p, so the reference
// the callee consumed is gone from this frame and every later mention of p —
// the exit sweep included — reads the fresh result the call returned. Two such
// sites in a row are each sound for the same reason: each consumes the binding
// current at that point. The guards mirror computeReturnOwnMoves:
//
//   - the assignment target is p itself, so the consumed reference is the one
//     being overwritten (`q = f(p)` would leave p dangling for the sweep);
//   - exactly one occurrence of p in the whole right-hand side, so
//     `p = f(p, g(p))` — two transfers, one binding — cannot qualify;
//   - the occurrence is a bare ident at an `own` position of a direct call.
func (b *builder) computeSelfReassignOwnMoves() {
	if b.fn.Body == nil || len(b.info.OwnFuncs) == 0 {
		return
	}
	ownParam := map[string]bool{}
	for _, p := range b.fn.Params {
		// Only a param the sweep actually decs can reach ownArgNeedsRetain;
		// for any other no retain is emitted and there is nothing to claim.
		if p.Own && rcTrackedSlotType(p.Type) && b.rc.freeEligible[p.Name] {
			ownParam[p.Name] = true
		}
	}
	if len(ownParam) == 0 {
		return
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		asg, ok := n.(*ast.Assign)
		if !ok {
			return true
		}
		target, ok := asg.Target.(*ast.Ident)
		if !ok || !ownParam[target.Name] {
			return true
		}
		uses := 0
		ast.Walk(asg.Value, func(inner ast.Node) bool {
			if id, isID := inner.(*ast.Ident); isID && id.Name == target.Name {
				uses++
			}
			return true
		})
		if uses != 1 {
			return true
		}
		call, isCall := asg.Value.(*ast.Call)
		if !isCall {
			return true
		}
		callee, isID := call.Callee.(*ast.Ident)
		if !isID {
			return true
		}
		if _, isLocal := b.locals[callee.Name]; isLocal {
			return true // shadowed by a local — not a direct call
		}
		flags, isOwn := b.info.OwnFuncs[callee.Name]
		if !isOwn {
			return true
		}
		for i := 0; i < len(call.Args) && i < len(flags); i++ {
			if !flags[i] {
				continue
			}
			if arg, isArgID := call.Args[i].(*ast.Ident); isArgID && arg.Name == target.Name {
				b.rc.ownCallMoveArgs[arg] = true
				return true
			}
		}
		return true
	})
}

// computeOwnedArgMoves claims the call arguments this frame hands to an
// OWNED-BY-DEFAULT parameter without a retain: a bare ident naming a value
// the frame holds one reference to (frameOwnsIdent) that DIES at the call
// (callArgDeaths — the self-reassign `x = f(.., x, ..)` and `return f(..,
// x, ..)` shapes, and a sole-occurrence param outside any loop). The
// reference the callee would otherwise be retained into is transferred
// instead, so the callee sees the value at the caller's own count — a
// uniquely held tree reaches its consuming match on the unique branch and
// rewrites the path in place, where a retained argument always copied it.
//
// The call site zeroes the argument's slot once the value is on the operand
// stack, so every later release of that slot — the exit sweep, a
// self-reassign's overwrite dec, a loop re-init drop — meets a null and
// no-ops under the helpers' guards, exactly as the consuming owned match
// zeroes its scrutinee. A name a defer or a lambda can still read after the
// statement (deferOrLambdaNames) is never moved: it would read the null.
func (b *builder) computeOwnedArgMoves() map[*ast.Ident]bool {
	out := map[*ast.Ident]bool{}
	if b.fn.Body == nil {
		return out
	}
	deaths := b.curCallArgDies()
	esc := deferOrLambdaNames(b.fn.Body)
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.Call)
		if !ok {
			return true
		}
		callee, isID := call.Callee.(*ast.Ident)
		if !isID {
			return true
		}
		if _, isLocal := b.locals[callee.Name]; isLocal {
			return true // shadowed by a local — not a direct call
		}
		sig, isFunc := b.info.FuncSigs[callee.Name]
		if !isFunc {
			return true
		}
		for i, a := range call.Args {
			arg, isArgID := a.(*ast.Ident)
			if !isArgID || i >= len(sig.Params) || !deaths[call][arg.Name] || esc[arg.Name] {
				continue
			}
			if !b.calleeParamOwnedByDefault(callee.Name, sig.Params[i], i) || !b.frameOwnsIdent(arg.Name) {
				continue
			}
			out[arg] = true
			b.rc.moveSites[arg] = true
		}
		return true
	})
	return out
}

// computeFieldOwnMoves claims the `x.f` reads of the superseded-field shape —
// `x = S { ...x, f: g(.., x.f, ..) }` and `return S { ...x, f: g(.., x.f,
// ..) }` — as moves out of x's box into a position that consumes them
// (#8186). Two consumers qualify: g's `own` parameter, and the receiver of
// `.with` (`x = S { ...x, f: x.f.with(i, v) }`), whose helper takes the
// reference over on both of its branches (see rc.arraySetConsumed). For the
// `own` half the recognition is the checker's (SupersededFieldOwnMoveArgs):
// E051 admits exactly these nodes, so an admitted argument the analysis does
// NOT claim still reaches the call and must be retained there instead (the
// call site's fallback); the two verdicts are each sound on their own. The
// `.with` half needs no admission — the receiver is a projection the
// computeArraySetIncs rule would otherwise inc into the copy path, and a
// claim here clears that inc so __fern_arr_cow_inplace sees the box's own
// count. Without it every `.with` on a field of a struct being rebuilt copied
// the whole buffer: a streaming hasher's pending block, a writer's buffer.
//
// A claim needs the frame to hold x's reference (frameOwnsIdent — a borrowed
// param's box belongs to the caller, who reads the field back), a field the
// flat helpers can null and retain (arrElemIsRcTracked: one word, not the
// two-word string), for the `own` half a callee that is a direct function,
// and no defer or lambda that could read x after the slot is emptied. The
// return form also needs a function without defers: a defer runs after the
// return value is built, while x is still in its slot.
//
// Uniqueness is decided at RUNTIME, not here. The box may be shared — `var
// b = x` earlier, a capture, a global — and every such route would read the
// emptied slot; so the call site tests is_unique(x) and only then nulls the
// slot, retaining the value for the callee otherwise. That keeps the claim
// free of an alias analysis the way tryStructReuseOverwrite's reuse is.
func (b *builder) computeFieldOwnMoves() map[*ast.FieldAccess]bool {
	out := map[*ast.FieldAccess]bool{}
	if b.fn.Body == nil || !ast.RcFreeEnabled {
		return out
	}
	var defers []*ast.Defer
	collectDefers(b.fn.Body, &defers)
	hasDefer := len(defers) > 0
	esc := deferOrLambdaNames(b.fn.Body)
	ownArg := map[*ast.FieldAccess]bool{}
	claim := func(sl *ast.StructLit, base *ast.Ident, isReturn bool) {
		if esc[base.Name] || (isReturn && hasDefer) || !b.frameOwnsIdent(base.Name) {
			return
		}
		st, ok := b.exprStaticType(base).(ast.StructType)
		if !ok || st.Name != sl.TypeName {
			return
		}
		sd, ok := b.info.Structs[st.Name]
		if !ok {
			return
		}
		for _, fa := range checker.SupersededFieldOwnMoveArgs(sl, base.Name, b.info.OwnFuncs) {
			if !arrElemIsRcTracked(fieldType(sd.Fields, fa.Field)) {
				continue
			}
			out[fa] = true
			ownArg[fa] = true
		}
		for _, fa := range supersededFieldSetReceivers(sl, base.Name) {
			if !arrElemIsRcTracked(fieldType(sd.Fields, fa.Field)) {
				continue
			}
			out[fa] = true
		}
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.Assign:
			t, ok := st.Target.(*ast.Ident)
			if !ok {
				return true
			}
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				claim(sl, t, false)
			}
		case *ast.Return:
			sl, isLit := st.Value.(*ast.StructLit)
			if !isLit || sl.Base == nil {
				return true
			}
			if bid, ok := sl.Base.(*ast.Ident); ok {
				claim(sl, bid, true)
			}
		}
		return true
	})
	// The `own` recognizer needs the callee to be a direct function; a local
	// of the same name shadows it, and the checker's own-flag table is keyed
	// by bare name.
	for fa := range ownArg {
		ast.Walk(b.fn.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.Call)
			if !ok {
				return true
			}
			for _, a := range c.Args {
				if a != fa {
					continue
				}
				cid := c.Callee.(*ast.Ident)
				if _, isLocal := b.locals[cid.Name]; isLocal {
					delete(out, fa)
				} else if _, isFunc := b.info.FuncSigs[cid.Name]; !isFunc {
					delete(out, fa)
				}
			}
			return true
		})
	}
	// A claimed `.with` receiver is handed to __fern_arr_cow_inplace at the
	// box's own count, so the projection inc computeArraySetIncs recorded for
	// it (a field read is a borrow of its container) must not fire.
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || !isArraySetCall(c) {
			return true
		}
		if fa, isField := c.Args[0].(*ast.FieldAccess); isField && out[fa] {
			b.rc.arraySetInc[c] = false
		}
		return true
	})
	return out
}

// supersededFieldSetReceivers is the `.with` half of the superseded-field
// recognition: every `target.f` that is the innermost receiver of the `.with`
// chain initialising field f of a `S { ...target, f: target.f.with(i, v) }`
// literal. The chain's outer calls take the inner result, a fresh owned
// buffer, so only the innermost receiver reads the box.
func supersededFieldSetReceivers(sl *ast.StructLit, target string) []*ast.FieldAccess {
	return checker.SupersededFieldMoves(sl, target, func(field string, value ast.Expr) *ast.FieldAccess {
		recv := value
		for {
			c, ok := recv.(*ast.Call)
			if !ok || !isArraySetCall(c) {
				return nil
			}
			recv = c.Args[0]
			if fa, isField := recv.(*ast.FieldAccess); isField {
				return fa
			}
		}
	})
}

// frameOwnsIdent reports whether `name` holds a reference this frame may hand
// away: a declared rc-tracked local — every binding form leaves it holding
// exactly one (an alias inc, a fresh result, a transferred return) — that is
// neither a borrowed view nor the source one reads through (borrowedAlias /
// borrowSources / dynBorrowedViews), or a parameter the frame owns by a
// count (own / owned-by-default / consumed, but not the flag-carrying
// consumed array). Deliberately not freeEligible: that asks whether the frame
// may FREE the value, and its taint excludes anything that might alias a
// live one; moving needs only that the frame holds a reference.
func (b *builder) frameOwnsIdent(name string) bool {
	if b.rc.borrowedAlias[name] || b.rc.borrowSources[name] || b.rc.dynBorrowedViews[name] {
		return false
	}
	if b.isOwnedRcLocal(name) {
		return true
	}
	return b.isOwnedRcParam(name) && !b.isConsumedArrayParam(name)
}

// computeCtorAliasInced collects every local that a container construction
// RETAINS while the local itself stays live — an array / tuple / struct-field /
// enum-payload element that is a bare ident, is not a move site, and passes
// needsRcIncOnAlias. Those are exactly the sites that emit a construction
// alias-inc, so the local ends up holding a counted reference of its own on top
// of the container's.
//
// Computed here rather than recorded during lowering because the consumer runs
// EARLIER in the same pass: emitVarReinitDropOld fires when the loop body's
// `var xs = …` is lowered, while the construction that retains xs is lowered
// after it. A lowering-time set is therefore always empty at the point of use —
// measured, not assumed: the first version of this recorded at the four
// construction sites and moved no measurement at all.
//
// Mirrors the inc side's gating exactly (needsRcIncOnAlias + !moveSites), so
// analysis and lowering cannot disagree about which locals took an inc; the
// move-on-construction cases are excluded because they never emit one.
func (b *builder) computeCtorAliasInced() map[string]bool {
	out := map[string]bool{}
	if b.fn.Body == nil {
		return out
	}
	mark := func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok || b.rc.moveSites[e] || !needsRcIncOnAlias(e, b) {
			return
		}
		out[id.Name] = true
	}
	b.ctorRetainedOperands(b.fn.Body, mark)
	return out
}

// ctorRetainedOperands calls `f` for every operand of a container construction
// under `n`: array / tuple / struct-literal elements, and the payloads of an
// rc-payload enum-variant call (`Some(xs)` / `Ok(xs)` — inc'd under
// EnumRcPayloads exactly like a struct field). These are the four sites that
// emit a construction alias-inc.
func (b *builder) ctorRetainedOperands(n ast.Node, f func(ast.Expr)) {
	ast.Walk(n, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				f(el)
			}
		case *ast.TupleLit:
			for _, el := range x.Elems {
				f(el)
			}
		case *ast.StructLit:
			for _, fl := range x.Fields {
				f(fl.Value)
			}
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				if en, _, _, isVar := b.lookupVariantOn(id.Name, id.EnumName); isVar && b.enumRcPayloadsEligible(en) {
					for _, a := range x.Args {
						f(a)
					}
				}
			}
		}
		return true
	})
}

// retainsCtorAliasedSource reports whether `e` is a container construction that
// RETAINS a ctorAliasInced local — the shape whose drop order is load-bearing.
//
// Such a source cannot free itself. Being ineligible for the deep free, its own
// release is the flat `__fern_rc_dec` in emitVarReinitDropOld and in the exit
// sweep's ineligible fall-through, and a flat dec only decrements: the buffer
// comes back solely through the type-aware `__fern_arr_dec` / `__drop_*` inside
// THIS container's deep drop, which frees at the last reference. So the
// container must never be released before its source. Declaration order gives
// that for free on the exit sweep (the source is declared first, so it is
// dec'd first) — a precise drop is the one thing that can invert it, and then
// the flat dec runs last, takes the rc to zero, and the buffer is unreachable.
// Keep such containers on the exit sweep.
func (b *builder) retainsCtorAliasedSource(e ast.Expr) bool {
	if e == nil {
		return false
	}
	found := false
	b.ctorRetainedOperands(e, func(op ast.Expr) {
		if id, ok := op.(*ast.Ident); ok && b.rc.ctorAliasInced[id.Name] {
			found = true
		}
	})
	return found
}

// sortedByDeclIdx returns declIdx's names ordered by their declaration
// statement index — the program's own order — so passes that consume it
// produce identical output on every run. Ranging the map directly made
// compilation nondeterministic wherever two names shared an output bucket.
func sortedByDeclIdx(declIdx map[string]int) []string {
	names := make([]string, 0, len(declIdx))
	for n := range declIdx {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if declIdx[names[i]] != declIdx[names[j]] {
			return declIdx[names[i]] < declIdx[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// matchBindingTypes maps every match-arm binding name in this function
// to the type the checker resolved for it, computed once per function.
//
// The bindings are not in `b.info.Locals` and not yet in `b.locals` when
// the rc analyses run, so a predicate that asks either of those about a
// binding gets "no such thing" rather than a type.
func (b *builder) matchBindingTypes() map[string]ast.Type {
	if b.rc.matchBindingTypes != nil {
		return b.rc.matchBindingTypes
	}
	out := map[string]ast.Type{}
	b.rc.matchBindingTypes = out
	if b.fn == nil || b.fn.Body == nil {
		return out
	}
	add := func(names []string, types []ast.Type) {
		for i, nm := range names {
			if i < len(types) && types[i] != nil {
				out[nm] = types[i]
			}
		}
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch m := n.(type) {
		case *ast.Match:
			for _, arm := range m.Arms {
				add(arm.Bindings, arm.BindingTypes)
			}
		case *ast.MatchExpr:
			for _, arm := range m.Arms {
				add(arm.Bindings, arm.BindingTypes)
			}
		}
		return true
	})
	return out
}
