package e2eselfhost

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// TestSelfHostRcPlanDiff is the e2e half of the #4482 rcPlan
// differential harness (goal 2): instead of only checking that programs still
// run, it diffs the Perceus decision TABLES between the two compilers, so each
// ported analysis lands with "tables match native" evidence.
//
// Native half: ir.RcPlanHook receives every function's rcPlan dump (format
// pinned by TestRcPlanDumpFormat, internal/ir/rc_dump.go) right after
// lowerFunc finishes it. Self-host half: the irlower_run driver's `-rc-plan`
// mode prints irlower.rc_plan_dump for every function — the same rendering
// from irlower's tables. Both compile the identical source, so per-function
// lines are directly comparable.
//
// The diff covers `diffedTables` and widens line-by-line as ports land:
// preciseDrops landed first, consumedParams second (the consumed_params_of
// port of native computeConsumedParams), freeEligible third (the
// free_eligible_of port of native computeFreeEligible, #4482), movedLocals +
// moveSites fourth/fifth (the rc_ml_compute port of native
// computeMovedLocals, whose site keys render as the native nodePos
// "line:col"). The self-host dump deliberately OMITS tables it has no
// counterpart for (arraySetInc, reuseSources, ...) — a native-only line is a
// documented port gap, not a failure, so the comparison is per-table.
//
// Known divergences are pinned explicitly (both sides' current output) so
// drift on EITHER side is caught; agreement cases assert equality plus, for
// the anchor case, the absolute expected value — agreement alone can't tell
// "both right" from "both wrong the same way".
func TestSelfHostRcPlanDiff(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "rcplan_driver")

	// The tables diffed per function; ALL NINE dumped tables are diffed as
	// of the consumingMatchReuse port (#4482 complete).
	diffedTables := []string{"aliasBindIncs", "arraySetInc", "consumedParams", "consumingMatchReuse", "freeEligible", "lastUses", "movedLocals", "moveSites", "preciseDrops", "reuseConsumed", "reuseSources"}

	type divergence struct {
		native   string // native's line value ("" = no line)
		selfhost string // self-host's value
	}
	// PORT GAP: irlower's rc_fe_escape_owned still taints a source that an
	// INC-ing sink RETAINS; native stopped (#7345), because the sink's dup is
	// released by the container's deep drop and the source keeps a reference
	// of its own. So a local stored into a struct literal or an array element
	// is free-eligible on the native side only. Inert wherever the local also
	// MOVES (a moved name is never swept, and both sides agree on
	// movedLocals); a real reclaim where the move declines. These rows leave
	// with the port.
	// The lastUses table is computed over each side's OWN freeEligible set,
	// so every one of these rows carries the same divergence one projection
	// over: the natively-eligible-only name has a native-only last-use entry.
	countedSinkSource := func(native, selfhost, luNative, luSelf string) map[string]divergence {
		return map[string]divergence{
			"freeEligible": {native: native, selfhost: selfhost},
			"lastUses":     {native: luNative, selfhost: luSelf},
		}
	}
	cases := []struct {
		name string
		src  string
		// anchor: function -> table -> exact value BOTH sides must emit.
		anchor map[string]map[string]string
		// diverge: function -> table -> pinned per-side values. Everything
		// else must simply agree between the compilers.
		diverge map[string]map[string]divergence
	}{
		{
			// The pinned TestRcPlanDumpFormat shape: big's last use is stmt 1,
			// the drop lands right after it.
			name: "literal-drop",
			src: `function dropper(): i32 {
	var big: i32[] = [1, 2, 3, 4];
	var s: i32 = big[0];
	return s + 1;
}
function main(): i32 { return dropper(); }`,
			anchor: map[string]map[string]string{"dropper": {"preciseDrops": "1=big", "freeEligible": "big", "lastUses": "big=1"}},
		},
		{
			// Two disjoint literal locals, dropped at their own last uses.
			name: "two-drops",
			src: `function two(): i32 {
	var a: i32[] = [1, 2];
	var x: i32 = a[0];
	var b: i32[] = [3, 4, 5];
	var y: i32 = b[1];
	return x + y;
}
function main(): i32 { return two(); }`,
			anchor: map[string]map[string]string{"two": {"preciseDrops": "1=a,3=b", "freeEligible": "a,b", "lastUses": "a=1,b=3"}},
		},
		{
			// Both locals last-used in ONE statement: a shared index group,
			// names sorted and joined with "+".
			name: "same-stmt-group",
			src: `function pair(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = [3, 4];
	var s: i32 = a[0] + b[1];
	return s;
}
function main(): i32 { return pair(); }`,
			anchor: map[string]map[string]string{"pair": {"preciseDrops": "2=a+b", "freeEligible": "a,b"}},
		},
		{
			// The local escapes by return: no precise drop on either side in
			// the producer. KNOWN DIVERGENCE in the consumer: the self-host
			// precise-drops a CALL-RETURNED fresh array (`var m = make()`,
			// via its arr-returning-fn registry); native leaves it to the
			// exit sweep. Both are sound — the drop placement differs — and
			// this is precisely the kind of table-level fact the harness
			// exists to surface (cf. #4356's donor-sourcing divergences).
			name: "escape-and-call-returned",
			src: `function make(): i32[] {
	var a: i32[] = [7, 8];
	return a;
}
function main(): i32 { var m: i32[] = make(); return m[0]; }`,
			anchor: map[string]map[string]string{"make": {"preciseDrops": "", "freeEligible": "a"}, "main": {"freeEligible": "m"}},
			diverge: map[string]map[string]divergence{
				"main": {"preciseDrops": {native: "", selfhost: "1=m"}},
			},
		},
		{
			// Last use inside a nested if: the drop still lands after the
			// enclosing TOP-LEVEL statement (index 2).
			name: "nested-last-use",
			src: `function pick(f: i32): i32 {
	var a: i32[] = [5, 6, 7];
	var r: i32 = 0;
	if (f > 0) { r = a[0]; } else { r = a[2]; }
	return r;
}
function main(): i32 { return pick(1); }`,
			anchor: map[string]map[string]string{"pick": {"preciseDrops": "2=a", "freeEligible": "a"}},
		},
		{
			// The pinned TestRcPlanDumpFormat consumed-threading shape: a
			// string-bearing struct param reassigned in the body is promoted
			// consumed-threaded, and both sides mark it freeEligible (the
			// consumed promotion un-taints it; user-struct type eligible).
			name: "consumed-thread",
			src: `struct Ctx { name: string, n: i32 }
function thread(c: Ctx): i32 {
	c = Ctx { name: "x", n: c.n + 1 };
	return c.n;
}
function main(): i32 { return thread(Ctx { name: "a", n: 1 }); }`,
			anchor: map[string]map[string]string{"thread": {"consumedParams": "c", "freeEligible": "c", "lastUses": "c=1"}},
		},
		{
			// A string/array-FREE struct param that is reassigned and does
			// NOT escape: borrow inference demotes it to borrowed, and the
			// consumed promotion must fire so the reassignment's overwrite
			// dec is balanced by the entry inc (the borrowed-param
			// over-release fix — without it the caller's box double-freed).
			name: "consumed-includes-borrow-demoted-scalar-struct",
			src: `struct P { x: i32, y: i32 }
function bump(p: P): i32 {
	p = P { x: p.x + 1, y: p.y };
	return p.x;
}
function main(): i32 { return bump(P { x: 1, y: 2 }); }`,
			anchor: map[string]map[string]string{"bump": {"consumedParams": "p"}},
		},
		{
			// A read-only (never reassigned) param is not consumed-threaded.
			name: "consumed-skips-unassigned",
			src: `struct S { name: string, n: i32 }
function read(s: S): i32 { return s.n; }
function main(): i32 { return read(S { name: "a", n: 3 }); }`,
			anchor: map[string]map[string]string{"read": {"consumedParams": ""}},
		},
		{
			// Tuple params take the same promotion: string-bearing + reassigned.
			// KNOWN freeEligible DIVERGENCE: native synthesizes a destructure
			// temp local (`__destruct_<line>_<col>`) that is itself eligible;
			// the self-host's destructure temps are synthesized at LOWER time
			// (not parse time), so they never appear in its AST-level table.
			// The real bindings agree: s (string, counted destructure owner)
			// and t (consumed tuple param) are eligible on both sides.
			name: "consumed-tuple",
			src: `function tup(t: (string, i32)): i32 {
	t = ("x", 1);
	var (s, k) = t;
	return k + s.len();
}
function main(): i32 { return tup(("a", 2)); }`,
			anchor: map[string]map[string]string{"tup": {"consumedParams": "t"}},
			diverge: map[string]map[string]divergence{
				"tup": {"freeEligible": {native: "__destruct_3_2,s,t", selfhost: "s,t"}},
			},
		},
		{
			// MOVE-ON-ALIAS: `var b = a` at a's last mention — the alias inc
			// and a's exit-sweep dec cancel; b owns the box. movedLocals: a on
			// both sides, and BOTH sides now elide the transfer inc at the
			// move site (the self-host's var-bind array path consults
			// moves_local_at and note_moved_elided's sweep skip pairs with
			// it), so aliasBindIncs is anchored EMPTY — the first pinned
			// divergence of the retain-plan table, burned down.
			name: "move-on-alias",
			src: `function mv(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a;
	return b[0];
}
function main(): i32 { return mv(); }`,
			anchor: map[string]map[string]string{"mv": {"movedLocals": "a", "moveSites": "3:2", "aliasBindIncs": ""}},
		},
		{
			// ALIAS-BIND retain, live source: `a` is read again after
			// `var b = a`, so neither side can move or dead-alias-cancel —
			// the transfer inc fires on BOTH and the site agrees exactly.
			// The retain-plan agreement anchor for the #7282 array limb.
			name: "alias-bind-live-source-array",
			src: `function f(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a;
	return a[0] + b[0];
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "3:2=b"}},
		},
		{
			// ALIAS of a borrowed PARAM: the callee's new binding takes a
			// counted retain (its exit dec balances) on both sides — the
			// binding-origin the leak matrix's alias_param rows probe, at the
			// retain level. Agreement anchor.
			name: "alias-bind-param-source",
			src: `function g(xs: i32[]): i32 {
	var v: i32[] = xs;
	return v[0] + xs[0];
}
function main(): i32 { var a: i32[] = [4, 5]; return g(a); }`,
			anchor: map[string]map[string]string{"g": {"aliasBindIncs": "2:2=v"}},
		},
		{
			// MOVE-ON-ALIAS, string limb: at the source's last top-level
			// mention both sides elide the transfer inc (the self-host's
			// string clause consults moves_local_at, and the string sweep
			// loop skips the elided source) — anchored agreement, the second
			// aliasBindIncs divergence family burned down.
			name: "move-on-alias-string",
			src: `function f(): i32 {
	var s: string = "hi" + "!";
	var v: string = s;
	return v.len();
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"movedLocals": "s", "moveSites": "3:2", "aliasBindIncs": ""}},
		},
		{
			// STRING alias bind (#7282 string limb), LIVE source: a closed
			// divergence — the dead-alias cancellation's string limb elides
			// the borrowed view's inc/dec pair exactly as native does, and
			// the source releases only at the exit sweep. Anchored agreement.
			name: "alias-bind-string",
			src: `function f(): i32 {
	var s: string = "hi" + "!";
	var v: string = s;
	var n: i32 = v.len() + s.len();
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "s,v", "aliasBindIncs": ""}},
		},
		{
			// STRING[] alias bind, LIVE source: a closed divergence, twice
			// over — #7420's buffer-rc-gated element walk made shallow
			// ownership sound for string[], and the dead-alias cancellation
			// (the array clause covers every is_arr slot) then elides the
			// borrowed view's inc/dec pair exactly as native does. Anchored
			// agreement.
			name: "alias-bind-strarr",
			src: `function f(): i32 {
	var xs: string[] = ["a", "bb"];
	var v: string[] = xs;
	var n: i32 = v.len() + xs.len();
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "v,xs", "aliasBindIncs": ""}},
		},
		{
			// DEAD-ALIAS CANCELLATION (#4402 opt 1), array limb: the alias is
			// a pure borrowed view (reads only, never returned), so BOTH
			// sides elide the transfer inc — and neither precise-drops the
			// source, which releases only at the exit sweep (an early free
			// would dangle the borrow). Anchored agreement.
			name: "dead-alias-borrow-only",
			src: `function f(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a;
	var n: i32 = b.len() + a.len();
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "", "preciseDrops": "", "freeEligible": "a,b"}},
		},
		{
			// The exclusion the live-source array anchor already pins from
			// the other side: an alias mentioned in a RETURN expression is
			// not cancelled (native's returned[y] gate) — the retain stays
			// on both sides. Source-reassigned is the same family: pinned
			// preciseDrops divergence only (native drops the retained alias
			// at its last use; the self-host leaves aliases to the sweep).
			name: "dead-alias-source-reassigned-excluded",
			src: `function f(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a;
	var n: i32 = b.len();
	a = [3, 4, 5];
	return n + a.len();
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "3:2=b"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "2=b", selfhost: ""}},
			},
		},
		{
			// CHAIN refusal: b borrows a (cancelled), and c aliasing b is
			// refused on both sides (a cancelled alias sources nothing) —
			// c keeps its retain. Native precise-drops the retained c at its
			// last use; the self-host leaves it to the sweep (the known
			// placement class).
			name: "dead-alias-chain-refused",
			src: `function f(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a;
	var c: i32[] = b;
	var n: i32 = c.len() + b.len() + a.len();
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "4:2=c"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "3=c", selfhost: ""}},
			},
		},
		{
			// The returned-alias exclusion, string limb: v is mentioned in a
			// RETURN expression (native's returned[y] gate), so the retain
			// stays on both sides — the positive control pinning that the
			// string cancellation still increments where it must.
			name: "dead-alias-string-returned-excluded",
			src: `function f(): i32 {
	var s: string = "hi" + "!";
	var v: string = s;
	var n: i32 = s.len();
	return v.len() + n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "3:2=v"}},
		},
		{
			// Source-reassigned exclusion, string limb: both sides refuse the
			// cancellation, but the self-host's retain never fired here in
			// the first place — a reassigned string source earns no "STR:"
			// credit (the single-bind refusal, the str__rebind leak-matrix
			// family), and the retain is co-extensive with the credit. Native
			// retains; the divergence predates the cancellation.
			name: "dead-alias-string-source-reassigned-excluded",
			src: `function f(): i32 {
	var a: string = "hi" + "!";
	var b: string = a;
	var n: i32 = b.len();
	a = "x" + "y";
	return n + a.len();
}
function main(): i32 { return f(); }`,
			diverge: map[string]map[string]divergence{
				"f": {"aliasBindIncs": {native: "3:2=b", selfhost: ""}},
			},
		},
		{
			// CHAIN refusal, string limb: b borrows a (cancelled on both
			// sides), and c aliasing b is refused on both sides (a cancelled
			// alias sources nothing). Native keeps c's retain; the self-host
			// never granted it — a re-aliased alias loses its "STR:" credit
			// (the credit collector's own chain refusal), and the retain is
			// co-extensive with the credit. The divergence is the credit
			// model's, not the cancellation's.
			name: "dead-alias-string-chain-refused",
			src: `function f(): i32 {
	var a: string = "hi" + "!";
	var b: string = a;
	var c: string = b;
	var n: i32 = c.len() + b.len() + a.len();
	return n;
}
function main(): i32 { return f(); }`,
			diverge: map[string]map[string]divergence{
				"f": {"aliasBindIncs": {native: "4:2=c", selfhost: ""}},
			},
		},
		{
			// Conditional (if-arm) alias, string limb: native's walker visits
			// the nested Var and da_scan recurses into if arms, so both sides
			// cancel. Pinned because the alias model's stated reason for
			// duplication was branch-dependence — the cancellation must stay
			// branch-safe: nothing is emitted in the arm at all and the
			// source sweeps unconditionally at exit.
			name: "dead-alias-string-conditional",
			src: `function f(c: i32): i32 {
	var s: string = "hi" + "!";
	var n: i32 = 0;
	if (c > 0) {
		var v: string = s;
		n = v.len();
	}
	return n + s.len();
}
function main(): i32 { return f(1); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "", "freeEligible": "s,v"}},
		},
		{
			// A CALL-producer source — the "STR:" credit family beyond the
			// concat literal (the import-free proxy for the .to_string()/
			// .join() families this harness cannot compile). Both sides
			// cancel.
			name: "dead-alias-string-call-producer",
			src: `function mk(a: string): string { return a + "!"; }
function f(): i32 {
	var s: string = mk("ab");
	var v: string = s;
	var n: i32 = v.len() + s.len();
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": "", "freeEligible": "s,v"}},
		},
		{
			// LOOP-scoped pair: both sides cancel, and the alias bind must
			// then store WITHOUT the dec-on-overwrite — the source's own
			// rebind already freed the prior box at rc 1 (native skips
			// emitVarReinitDropOld at borrowed-alias sites). The runtime
			// half is pinned by the loop_local alias cells in the leak
			// matrix, whose sanitize leg is the instrument that sees the
			// double free.
			name: "dead-alias-string-loop-scoped",
			src: `function f(): i32 {
	var n: i32 = 0;
	var i: i32 = 0;
	while (i < 3) {
		var s: string = "hi" + "!";
		var v: string = s;
		n = n + v.len() + s.len();
		i = i + 1;
	}
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"aliasBindIncs": ""}},
		},
		{
			// MOVE-ON-ALIAS, tuple limb (the rc-tuple flavor — the one whose
			// release-role transfer is load-bearing): at the source's last
			// top-level mention both sides elide the transfer inc; the
			// self-host moves the "TUPRCS:" deep-sweep class to the alias
			// site and copies the slot facts the deep free reads
			// (tuple_elems / tup_elem_kinds), and the tuple sweep loops skip
			// the elided source. Anchored agreement — the last aliasBindIncs
			// move family burned down.
			name: "move-on-alias-tuple",
			src: `function f(): i32 {
	var t: (i32, i32[]) = (7, [1, 2]);
	var v: (i32, i32[]) = t;
	return v.0;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"movedLocals": "t", "moveSites": "3:2", "aliasBindIncs": ""}},
		},
		{
			// MOVE-ON-ALIAS, struct limb: at the source's last top-level
			// mention both sides elide the transfer inc; the self-host drops
			// the alias's box-only NODEEP marker so it inherits the source's
			// deep field walk (the struct-specific release-role transfer),
			// and the struct sweep loop skips the elided source. Anchored
			// agreement — the third aliasBindIncs family burned down.
			name: "move-on-alias-struct",
			src: `struct P { xs: i32[], n: i32 }
function f(): i32 {
	var p: P = P { xs: [1, 2], n: 3 };
	var v: P = p;
	return v.n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"movedLocals": "p", "moveSites": "4:2", "aliasBindIncs": ""}},
		},
		{
			// STRUCT alias bind (#7282 struct limb / the #7349 site-keyed
			// credit), LIVE source: retain fires on the self-host, native
			// elides the pair (dead-alias cancellation — a separate port).
			name: "alias-bind-struct",
			src: `struct P { xs: i32[], n: i32 }
function f(): i32 {
	var p: P = P { xs: [1, 2], n: 3 };
	var v: P = p;
	var n: i32 = v.n + p.n;
	return n;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "p,v"}},
			diverge: map[string]map[string]divergence{
				"f": {"aliasBindIncs": {native: "", selfhost: "4:2=v"}},
			},
		},
		{
			// MOVE-ON-CONSTRUCTION: an owned rc local consumed at last use in
			// a struct-lit rc-tracked (non-string) field — the field-init inc
			// and x's sweep dec cancel; the struct's field-drop frees it once.
			name: "move-on-construction",
			src: `struct Wrap { items: i32[] }
function w(): i32 {
	var x: i32[] = [1, 2];
	var s: Wrap = Wrap { items: x };
	return s.items[0];
}
function main(): i32 { return w(); }`,
			anchor: map[string]map[string]string{"w": {"movedLocals": "x"}}, // moveSites agreement-checked (ident col)
			diverge: map[string]map[string]divergence{
				"w": countedSinkSource("s,x", "s", "s=2,x=1", "s=2"),
			},
		},
		{
			// ARRAY-STORE MOVE (#6535, native #6532): `xs.append(v)` stores the
			// element under the same counted retain an array literal's elements
			// take, and the buffer's deep drop dec's it — so a last-use owned rc
			// local is a MOVE there too. The element type gate is
			// arrElemIsRcTracked, so `Wrap[]` qualifies and an `i32[]` would not.
			name: "move-on-array-store",
			src: `struct Wrap { items: i32[] }
function w(): i32 {
	var v: Wrap = Wrap { items: [1, 2] };
	var xs: Wrap[] = [];
	xs = xs.append(v);
	return xs[0].items[0];
}
function main(): i32 { return w(); }`,
			anchor: map[string]map[string]string{"w": {"movedLocals": "v"}}, // moveSites agreement-checked (ident col)
			diverge: map[string]map[string]divergence{
				"w": countedSinkSource("v,xs", "xs", "v=2,xs=3", "xs=3"),
			},
		},
		{
			// The SAME store one level down, inside a struct literal's field —
			// the shape a walk that only looks one level into the statement
			// misses. A container literal evaluates every operand
			// unconditionally, so the statement's dominance guard already
			// covers it.
			name: "move-on-nested-array-store",
			src: `struct Wrap { items: i32[] }
struct Doc { vals: Wrap[], root: i32 }
function w(): i32 {
	var d: Doc = Doc { vals: [], root: 0 };
	var v: Wrap = Wrap { items: [3, 4] };
	d = Doc { ...d, vals: d.vals.append(v) };
	return d.vals[0].items[1];
}
function main(): i32 { return w(); }`,
			anchor: map[string]map[string]string{"w": {"movedLocals": "v"}},
			diverge: map[string]map[string]divergence{
				"w": countedSinkSource("d,v", "d", "d=3,v=2", "d=3"),
			},
		},
		{
			// LOOP-BODY MOVE, no early exit — the control the three rows below
			// are read against. `v` is declared in the body and consumed by the
			// push, so it moves on both sides.
			name: "loop-body-move-no-early-exit",
			src: `struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
	var vals: Val[] = [];
	var total: i32 = 0;
	for i in 0..n {
		var v = Val { kind: i, kids: [] };
		vals = vals.append(v);
		total = total + vals.len();
	}
	return total;
}
function main(): i32 { return build(3); }`,
			anchor: map[string]map[string]string{"build": {"movedLocals": "v"}},
			diverge: map[string]map[string]divergence{
				"build": countedSinkSource("v,vals", "vals", "v=2,vals=2", "vals=2"),
			},
		},
		{
			// The early exit sits AFTER the push, so it cannot leave the body
			// with `v` built and unstored — the interval that matters is
			// declaration-to-construction, not the whole body (#6533/#6869).
			name: "loop-body-move-continue-after-push",
			src: `struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
	var vals: Val[] = [];
	var total: i32 = 0;
	for i in 0..n {
		var v = Val { kind: i, kids: [] };
		vals = vals.append(v);
		if (i == 9999) { continue; }
		total = total + vals.len();
	}
	return total;
}
function main(): i32 { return build(3); }`,
			anchor: map[string]map[string]string{"build": {"movedLocals": "v"}},
			diverge: map[string]map[string]divergence{
				"build": countedSinkSource("v,vals", "vals", "v=2,vals=2", "vals=2"),
			},
		},
		{
			// The guard clause every parser is built out of: the exit precedes
			// `v`'s declaration, so on the exiting path no box exists yet.
			name: "loop-body-move-guard-return-before-decl",
			src: `struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
	var vals: Val[] = [];
	var total: i32 = 0;
	for i in 0..n {
		if (i == 9999) { return 12345; }
		var v = Val { kind: i, kids: [] };
		vals = vals.append(v);
		total = total + vals.len();
	}
	return total;
}
function main(): i32 { return build(3); }`,
			anchor: map[string]map[string]string{"build": {"movedLocals": "v"}},
			diverge: map[string]map[string]divergence{
				"build": countedSinkSource("v,vals", "vals", "v=2,vals=2", "vals=2"),
			},
		},
		{
			// The direction that is UNSOUND to get wrong: `?` returns the
			// failure variant from the enclosing function, so it exits the body
			// between `v`'s declaration and the push exactly as `return` would,
			// leaving a path that builds `v` and never stores it. It is an
			// EXPRESSION (the unary `try_`), so a scan over statement kinds
			// alone does not see it and moves a value past an exit it cannot
			// observe. Both sides must decline.
			name: "loop-body-move-try-between-decl-and-push",
			src: `struct Val { kind: i32, kids: i32[] }
function step(i: i32): Option[i32] {
	if (i < 0) { return None; }
	return Some(i);
}
function build(n: i32): Option[i32] {
	var vals: Val[] = [];
	var total: i32 = 0;
	for i in 0..n {
		var v = Val { kind: i, kids: [] };
		var w: i32 = step(i)?;
		vals = vals.append(v);
		total = total + vals.len() + w;
	}
	return Some(total);
}
function main(): i32 { match (build(3)) { Some(v) => { return v; }, None => { return 0; } } }`,
			anchor: map[string]map[string]string{"build": {"movedLocals": ""}},
			// The move DECLINES here, so native's untainted `v` is a real
			// per-iteration reclaim the self-host still misses, not an inert
			// bookkeeping difference.
			diverge: map[string]map[string]divergence{
				"build": countedSinkSource("v,vals", "vals", "v=2,vals=2", "vals=2"),
			},
		},
		{
			// DESTRUCTURE MOVE: `var (xs, n) = t` at the tuple LOCAL's last
			// mention — the destructure's alias inc cancels t's sweep dec.
			// (freeEligible carries the same native-only parse-time
			// destructure temp pinned on the consumed-tuple case.)
			name: "move-on-destructure",
			src: `function d(): i32 {
	var t: (i32[], i32) = ([5], 3);
	var (xs, n) = t;
	return xs[0] + n;
}
function main(): i32 { return d(); }`,
			anchor: map[string]map[string]string{"d": {"movedLocals": "t", "moveSites": "3:2"}},
			diverge: map[string]map[string]divergence{
				"d": {"freeEligible": {native: "__destruct_3_2,t,xs", selfhost: "t,xs"}},
			},
		},
		{
			// ARRAY-SET INC, live receiver: `a` is read again after the
			// `.with`, so the CoW must inc (force-copy) — the buffer is not
			// solely the result's.
			// arraySetInc agrees exactly (call-node position convention
			// matches). KNOWN preciseDrops placement divergence, same class
			// as escape-and-call-returned: the self-host precise-drops the
			// receiver at its last-use statement; native leaves it to the
			// exit sweep. Both sound.
			name: "arrayset-inc-live-receiver",
			src: `function f(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a.with(0, 9);
	return a[0] + b[0];
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"arraySetInc": "3:23=true"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "", selfhost: "2=a"}},
			},
		},
		{
			// ARRAY-SET INC, reassign-to-self: `a = a.with(..)` overwrites the
			// receiver with the result — the in-place rc==1 fast path (no inc).
			name: "arrayset-inc-reassign-self",
			src: `function g(): i32 {
	var a: i32[] = [1, 2];
	a = a.with(0, 9);
	return a[0];
}
function main(): i32 { return g(); }`,
			anchor: map[string]map[string]string{"g": {"arraySetInc": "3:12=false"}},
		},
		{
			// ARRAY-SET INC, borrowed param receiver: the caller still owns
			// the buffer, so the CoW must inc even at the last use.
			// (main carries the same known self-host-only precise drop of its
			// argument array at last use; native sweeps.)
			name: "arrayset-inc-borrowed-param",
			src: `function h(xs: i32[]): i32 {
	var b: i32[] = xs.with(0, 9);
	return b[0];
}
function main(): i32 { var a: i32[] = [1, 2]; return h(a); }`,
			anchor: map[string]map[string]string{"h": {"arraySetInc": "2:24=true"}},
			diverge: map[string]map[string]divergence{
				"main": {"preciseDrops": {native: "", selfhost: "1=a"}},
			},
		},
		{
			// REUSE PAIRING, struct: dead donor `a` paired with recipient `b`
			// (same type, same block) — both pairing algorithms agree here.
			// reuseSources/reuseConsumed agree exactly (lit position + donor).
			// The donor's precise drop is suppressed on both sides (the dump
			// mirrors lower_func's donor suppression), and so is the
			// recipient's — this case pins full convergence on every table.
			// It used to carry a preciseDrops divergence for the recipient's
			// last-use-at-return placement; that self-host-only class stays
			// pinned by escape-and-call-returned and arrayset-inc-live-receiver.
			//
			// The recipient multiplies a field by a PARAM so it is not itself a
			// compile-time constant: the self-host excludes a constant recipient
			// from reuse (reuse_recipient_ok) so #6149 can place it statically
			// instead, and native has no such placement — an all-literal
			// recipient here would pin that difference rather than the pairing
			// this case is for. The literal's column is unchanged, so the
			// reuseSources anchor still reads 5:13. A param rather than `s`
			// because `a` is itself a constant now, which makes `3 * s`
			// foldable in a way `3 * one` is not.
			name: "reuse-sources-struct",
			src: `struct P { x: i32, y: i32 }
function r(one: i32): i32 {
	var a: P = P { x: 1, y: 2 };
	var s: i32 = a.x;
	var b: P = P { x: 3 * one, y: 4 };
	return s + b.x;
}
function main(): i32 { return r(1); }`,
			anchor: map[string]map[string]string{"r": {"reuseSources": "5:13<-a", "reuseConsumed": "a"}},
		},
		{
			// REUSE PAIRING, tuple: same shape over tuple literals (the
			// ExprTuple position addition).
			name: "reuse-sources-tuple",
			src: `function q(): i32 {
	var t1: (i32, i32) = (1, 2);
	var s: i32 = t1.0;
	var t2: (i32, i32) = (3, 4);
	return s + t2.1;
}
function main(): i32 { return q(); }`,
			anchor: map[string]map[string]string{"q": {"reuseSources": "4:23<-t1", "reuseConsumed": "t1"}},
			diverge: map[string]map[string]divergence{
				"q": {"preciseDrops": {native: "", selfhost: "3=t2"}},
			},
		},
		{
			// CONSUMING-MATCH REUSE — the two mechanisms differ structurally:
			// the self-host's inarm family fires on a fresh sole-owner LOCAL
			// consumed by a value-position match (entries = the arm ctor
			// positions); native's C2 table covers only `own`-param STATEMENT
			// matches, so it emits nothing here. Pinned per-side — the diff
			// documents the mechanism gap in both directions.
			name: "consuming-match-inarm",
			src: `enum E { V(i32, i32), W(i32, i32) }
function go(): i32 {
	var x = V(3, 4);
	var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) };
	var r = match (y) { V(a, b) => a + b, W(c, d) => c + d };
	return r;
}
function main(): i32 { return go(); }`,
			anchor: map[string]map[string]string{"go": {"freeEligible": "x,y"}},
			diverge: map[string]map[string]divergence{
				"go": {"consumingMatchReuse": {native: "", selfhost: "4:34,4:62"}},
			},
		},
		{
			// NESTED (loop-body) reuse: the canonical FBIP shape — donor `a`
			// and recipient `b` both declared in the while body, paired every
			// iteration. Both compilers walk the nested block; a
			// fn.body-top-level-only dump under-reports it. reuseSources keys
			// on the recipient struct-lit position.
			name: "nested-loop-reuse",
			src: `struct P { x: i32, y: i32 }
function loopr(): i32 {
	var sum: i32 = 0;
	var i: i32 = 0;
	while (i < 4) {
		var a: P = P { x: i, y: i + 1 };
		var s: i32 = a.x + a.y;
		var b: P = P { x: i * 2, y: 3 };
		sum = sum + s + b.x + b.y;
		i = i + 1;
	}
	return sum;
}
function main(): i32 { return loopr(); }`,
			anchor: map[string]map[string]string{"loopr": {"reuseSources": "8:14<-a", "reuseConsumed": "a"}},
		},
		{
			// NESTED (if-arm) reuse: donor + recipient inside an if-then block.
			// The recipient multiplies a field by the param (1 at the call site)
			// for the same reason reuse-sources-struct does — a constant
			// recipient is not a reuse shape on the self-host.
			name: "nested-if-reuse",
			src: `struct P { x: i32, y: i32 }
function ifr(f: i32): i32 {
	var r: i32 = 0;
	if (f > 0) {
		var a: P = P { x: 10, y: 20 };
		var s: i32 = a.x + a.y;
		var b: P = P { x: 3 * f, y: 4 };
		r = s + b.x + b.y;
	}
	return r;
}
function main(): i32 { return ifr(1); }`,
			anchor: map[string]map[string]string{"ifr": {"reuseSources": "7:14<-a", "reuseConsumed": "a"}},
		},
		{
			// CROSS-BLOCK reuse: donor `a` declared in the while body, recipient
			// `b` nested in an if inside that body — paired by lower_block's
			// xblock_pairings_for (donor one block above the recipient). Native
			// computeReuseSources' cross-block pass emits the same; the dump's
			// reuse_xblock_pairs pass mirrors it. reuseSources keys on b's lit.
			name: "cross-block-reuse",
			src: `struct P { x: i32, y: i32 }
function cb(): i32 {
	var sum: i32 = 0;
	var i: i32 = 0;
	while (i < 4) {
		var a: P = P { x: i, y: i + 1 };
		var s: i32 = a.x + a.y;
		if (i > 0) {
			var b: P = P { x: i, y: 3 };
			sum = sum + b.x + b.y;
		}
		sum = sum + s;
		i = i + 1;
	}
	return sum;
}
function main(): i32 { return cb(); }`,
			anchor: map[string]map[string]string{"cb": {"reuseSources": "9:15<-a", "reuseConsumed": "a"}},
		},
		{
			// OWN-PARAM donor function (#4356 slice 10): `bump(own d)` where a
			// construction reuses d's box (own_param_reuse_sites). Both sides
			// agree on freeEligible (c,d); NEITHER emits reuseSources — the
			// self-host's own-param reuse rides own_param_reuse_sites (not the
			// reuseSources dump path), native doesn't pair PARAMS as donors —
			// a documented agreement. The recipient c is precise-dropped at its
			// last use by the self-host (the known placement class native
			// leaves to the exit sweep).
			name: "ownparam-scalar-donor",
			src: `struct P { x: i32, y: i32 }
function bump(own d: P): i32 {
	var u: i32 = d.x + d.y;
	var c = P { x: 10, y: 20 };
	return c.x + c.y + u;
}
function main(): i32 { return bump(P { x: 3, y: 4 }); }`,
			anchor: map[string]map[string]string{"bump": {"freeEligible": "c,d"}},
			diverge: map[string]map[string]divergence{
				"bump": {"preciseDrops": {native: "", selfhost: "2=c"}},
			},
		},
		{
			// OWN-PARAM donor with an ARRAY field (#4356 slice 11): same table
			// shape as the scalar own-param donor — the array-field reuse is a
			// codegen concern, not an rcPlan-table one.
			name: "ownparam-array-donor",
			src: `struct H { id: i32, items: i32[] }
function bump(own d: H): i32 {
	var u: i32 = d.id + d.items[0];
	var c = H { id: 5, items: [7, 8, 9] };
	return c.id + c.items[0] + u;
}
function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`,
			anchor: map[string]map[string]string{"bump": {"freeEligible": "c,d"}},
			diverge: map[string]map[string]divergence{
				"bump": {"preciseDrops": {native: "", selfhost: "2=c"}},
			},
		},
		{
			// OWN-PARAM self-overwrite base (#4356 slice 12): `var c = P{...own_d, x}`
			// reuses d's box IN PLACE, so there is no separate recipient to
			// precise-drop — the tables agree exactly (freeEligible c,d only).
			name: "ownparam-selfoverwrite",
			src: `struct P { x: i32, y: i32 }
function bump(own d: P): i32 {
	var c = P { ...d, x: 10 };
	return c.x + c.y;
}
function main(): i32 { return bump(P { x: 3, y: 4 }); }`,
			anchor: map[string]map[string]string{"bump": {"freeEligible": "c,d"}},
		},
		{
			// FREE-ELIGIBLE for UNANNOTATED locals (found by the differential
			// bug-hunt): a bare-lambda-bound closure. Native marks `add`
			// freeEligible (owned closure frees its env); the self-host used to
			// emit nothing — its free_eligible_of both mis-inferred the type
			// (no lambda arm) AND tainted the closure (no ExprLambda owned-rhs
			// arm). Both fixed to match native.
			name: "fe-unannotated-closure",
			src: `function f(): i32 {
	var add = (a: i32) => a + 1;
	return add(5);
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "add"}},
		},
		{
			// Unannotated string literal + concat: both `a` ("hi") and `b`
			// (a + "!") are owned heap strings, freeEligible on both sides.
			name: "fe-unannotated-string-concat",
			src: `function f(): i32 {
	var a = "hi";
	var b = a + "!";
	return b.len();
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "a,b"}},
		},
		{
			// Chained .with: `b` is bound from an array-preserving method call,
			// so its type is inferred (i32[]) and it reaches the eligibility
			// gate — freeEligible a,b. arraySetInc records both calls (the inner
			// with on a live receiver incs; the outer with on the fresh temp
			// does not).
			name: "fe-unannotated-with-chain",
			src: `function f(): i32 { var a: i32[] = [1, 2, 3]; var b = a.with(0, 9).with(1, 8); return b[0] + b[1] + a[0]; }
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "a,b", "arraySetInc": "1:61=true,1:72=false"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "", selfhost: "2=a"}},
			},
		},
		{
			// Nested-if .with: `b` bound from a method call inside an if-arm —
			// the collect-types fallback resolves it from the receiver `a` even
			// across the block boundary.
			name: "fe-unannotated-with-nested-if",
			src: `function f(c: i32): i32 { var a: i32[] = [1, 2]; if (c > 0) { var b = a.with(0, 9); return b[0] + a[0]; } return a[1]; }
function main(): i32 { return f(1); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "a,b", "arraySetInc": "1:77=true"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "", selfhost: "2=a"}},
			},
		},
		{
			// FREE-ELIGIBLE for a builtin-enum local (found by the widened
			// bug-hunt): `var r: Result[i32[], i32] = Ok([5,6])` matched below.
			// An eligibility type-switch with no arm for the builtin
			// Option/Result enums lets the annotated local fall through
			// unrecognised and emits nothing. rc_fe_is_builtin_enum makes
			// it eligible, matching native's `r`.
			name: "fe-builtin-enum-result",
			src: `function f(): i32 {
	var r: Result[i32[], i32] = Ok([5, 6]);
	var v = 0;
	match (r) { Ok(xs) => { v = xs[0] + xs[1]; }, Err(e) => { v = e; } }
	return v;
}
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "r"}},
		},
		{
			// FREE-ELIGIBLE for a variant-constructed enum local bound from a
			// BORROWED param arg (found by the widened bug-hunt): `var x = A(n)`
			// where n is a borrowed i32 param. The self-host's rc_fe_rhs_tainted
			// used to fall through the variant-ctor call to its args and, seeing
			// the taint-seeded `n`, taint x — dropping it from freeEligible. It
			// now returns false for a variant constructor (a fresh owned rc=1
			// box, like native's rhsTainted), so x is eligible on both sides.
			// KNOWN preciseDrops divergence, reversed from the usual class:
			// native precise-drops the matched enum at the match statement
			// (2=x); the self-host leaves x to its exit sweep. Both sound.
			name: "fe-variant-ctor-borrowed-arg",
			src: `enum E { A(i32), B(i32) }
function f(n: i32): i32 {
	var x = A(n);
	var r = 0;
	match (x) { A(v) when v > 0 => { r = v; }, A(v) => { r = 0; }, B(v) => { r = v; } }
	return r;
}
function main(): i32 { return f(5); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "x"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "2=x", selfhost: ""}},
			},
		},
		{
			// MAP LOCAL divergence: `var m: Map[..] = Map{..}` desugars at PARSE
			// time to a map_new().insert()... chain. Both compilers now admit
			// the map local (freeEligible agrees, so it is not pinned); they
			// still differ on WHERE it dies — native leaves it to the exit
			// sweep (empty) where the self-host precise-drops at last use
			// (1=m). Both sound, the self-host simply more aggressive, so the
			// remaining row is pinned rather than "fixed": matching native
			// would REMOVE a valid self-host optimisation.
			name: "fe-map-strkey-local",
			src: `function f(): i32 { var m: Map[string, i32] = Map { "a": 1, "b": 2 }; return m.get_or("a", 0) + m.get_or("b", 0); }
function main(): i32 { return f(); }`,
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "", selfhost: "1=m"}},
			},
		},
		{
			// FOR-IN over an array (widened bug-hunt PIN — the synthesized-temp
			// class): native lowers `for x in xs` through a parse-time
			// synthesized iterator temp `__foreach_iter_1` that is itself
			// freeEligible, so its table reads `__foreach_iter_1,xs`. The
			// self-host synthesizes the foreach iterator at LOWER time, so the
			// temp never appears in its AST-level table (`xs` only) — the same
			// mechanism as the `__destruct_<line>_<col>` pins above. It also
			// precise-drops xs at last use (2=xs) where native sweeps.
			name: "fe-for-in-array",
			src: `function f(): i32 { var xs = [1, 2, 3]; var s = 0; for x in xs { s = s + x; } return s; }
function main(): i32 { return f(); }`,
			diverge: map[string]map[string]divergence{
				"f": {"freeEligible": {native: "__foreach_iter_1,xs", selfhost: "xs"}, "lastUses": {native: "__foreach_iter_1=2,xs=2", selfhost: "xs=2"}, "preciseDrops": {native: "", selfhost: "2=xs"}},
			},
		},
		{
			// CALL-RETURN type inference (found by the round-3 bug-hunt): an
			// UNANNOTATED local `var b = mk()` bound from a non-generic function
			// call. Native reads the checker's resolved call type (i32[]) and
			// marks b freeEligible; the self-host used to leave b untyped — its
			// rc_fe_collect_types ExprCall arm only recognised variant ctors, so
			// b never reached the eligibility gate. rc_fe_call_ret_type now looks
			// up mk's declared return type, matching native's `b`. (Self-host
			// precise-drops the fresh call-returned array at last use — the known
			// placement class native leaves to its exit sweep.)
			name: "fe-call-return-array",
			src: `function mk(): i32[] { return [1, 2, 3]; }
function f(): i32 { var b = mk(); return b[0] + b[1]; }
function main(): i32 { return f(); }`,
			anchor: map[string]map[string]string{"f": {"freeEligible": "b"}},
			diverge: map[string]map[string]divergence{
				"f": {"preciseDrops": {native: "", selfhost: "1=b"}},
			},
		},
		{
			// GENERIC call-return boundary (round-3 bug-hunt PIN — the flip side
			// of the fix above): `var b = id(a)` where `id[T](x: T): T` is
			// generic. Native instantiates T=i32[] via the checker and marks b
			// freeEligible (`a,b`); the syntactic self-host mirror deliberately
			// SKIPS generic functions in rc_fe_call_ret_type (it can't
			// instantiate the type parameter), so b stays untyped (`a` only).
			// Pinned to document the boundary — closing it needs monomorphised
			// return-type resolution, a separate slice.
			name: "fe-generic-call-return",
			src: `function id[T](x: T): T { return x; }
function f(): i32 { var a = [1, 2, 3]; var b = id(a); return b[0]; }
function main(): i32 { return f(); }`,
			diverge: map[string]map[string]divergence{
				"f": {"freeEligible": {native: "a,b", selfhost: "a"}, "lastUses": {native: "a=1,b=2", selfhost: "a=1"}},
			},
		},
		{
			// #5935 half one: the ALLOCATOR untaint. `__alloc_u8(n)` hands back a
			// fresh block that cannot alias its scalar size argument, however
			// tainted that argument is — so the buffer is freeEligible even when
			// the count derives from a borrowed param. Before the port the
			// self-host's generic any-arg-tainted rule left every dynamically
			// sized buffer permanently ineligible, so `__alloc_u8(16)` was
			// eligible and `__alloc_u8(n)` was not — the divergence that made
			// #5931's native-only fix invisible to this harness.
			name: "fe-alloc-u8-computed-size",
			src: `function fill(n: i32): i32 {
	var buf: u8[] = __alloc_u8(n + 1);
	buf = buf.with(0, 65 as u8);
	return buf[0] as i32;
}
function main(): i32 { return fill(3); }`,
			anchor: map[string]map[string]string{"fill": {"freeEligible": "buf"}},
		},
		{
			// #5935 half two: the guard that makes half one sound. Casting a
			// pointer-shaped value to a non-pointer target hands out a raw
			// address rc cannot follow (`std/string`'s `s.bytes()` passes
			// `buf as usize` to __memcpy), so the operand escapes and the buffer
			// must NOT be eligible. Without this the untaint above would reclaim
			// a buffer still being written through the raw pointer.
			//
			// Asserted as the EMPTY table rather than by omission: "no line" and
			// "line naming something else" are different failures, and only the
			// explicit value distinguishes a working guard from a table that
			// simply stopped being emitted.
			name: "fe-alloc-u8-address-escapes",
			src: `function raw(n: i32): i32 {
	var buf: u8[] = __alloc_u8(n + 1);
	var addr: usize = buf as usize;
	return (addr as i32) & 1;
}
function main(): i32 { return raw(3); }`,
			anchor: map[string]map[string]string{"raw": {"freeEligible": ""}},
			// KNOWN aliasBindIncs DIVERGENCE: the self-host classifies the
			// `buf as usize` bind array-shaped and retains it (addr's sweep
			// dec balances; buf stays unswept, the guard above); native emits
			// no retain — buf is tainted on both sides either way. Both leak
			// the buffer by design, one count apart.
			diverge: map[string]map[string]divergence{
				"raw": {"aliasBindIncs": {native: "", selfhost: "3:2=addr"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			native := nativeRcPlanDumps(t, tc.src)
			shOut := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-rc-plan")
			selfhost := parseRcPlanDumps(string(shOut))

			fns := map[string]bool{}
			for fn := range native {
				fns[fn] = true
			}
			for fn := range selfhost {
				fns[fn] = true
			}
			for fn := range fns {
				for _, table := range diffedTables {
					nl := rcPlanLine(native[fn], table)
					sl := rcPlanLine(selfhost[fn], table)
					if d, ok := tc.diverge[fn][table]; ok {
						if nl != d.native {
							t.Errorf("%s: native %s = %q, pinned divergence expects %q", fn, table, nl, d.native)
						}
						if sl != d.selfhost {
							t.Errorf("%s: self-host %s = %q, pinned divergence expects %q", fn, table, sl, d.selfhost)
						}
						continue
					}
					if nl != sl {
						t.Errorf("%s: %s diverge — native %q vs self-host %q\n--- native dump ---\n%s--- self-host dump ---\n%s",
							fn, table, nl, sl, native[fn], selfhost[fn])
					}
					if want, ok := tc.anchor[fn][table]; ok && nl != want {
						t.Errorf("%s: %s = %q on both sides, but the anchor expects %q", fn, table, nl, want)
					}
				}
			}
		})
	}
}

// nativeRcPlanDumps lowers src with the native pipeline (parse -> constfold ->
// check -> ir.LowerWith, RcFreeEnabled like the ir-package tests) with
// ir.RcPlanHook armed, returning function name -> rcPlan dump.
func nativeRcPlanDumps(t *testing.T, src string) map[string]string {
	t.Helper()
	dumps := map[string]string{}
	ir.RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { ir.RcPlanHook = nil }()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("native parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("native constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("native check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	_, err = ir.LowerWith(prog, info, 8)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("native lower: %v", err)
	}
	return dumps
}

// parseRcPlanDumps splits the self-host driver's `-rc-plan` output
// (`== <name>` headers) into function name -> dump body.
func parseRcPlanDumps(out string) map[string]string {
	dumps := map[string]string{}
	var cur string
	var body strings.Builder
	flush := func() {
		if cur != "" {
			dumps[cur] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.Split(out, "\n") {
		if name, ok := strings.CutPrefix(line, "== "); ok {
			flush()
			cur = name
			continue
		}
		if line != "" {
			body.WriteString(line + "\n")
		}
	}
	flush()
	return dumps
}

// rcPlanLine extracts one table's value from a dump ("" when the line is
// absent — i.e. the table is empty or not computed on that side).
func rcPlanLine(dump, key string) string {
	for _, line := range strings.Split(dump, "\n") {
		if v, ok := strings.CutPrefix(line, key+": "); ok {
			return v
		}
	}
	return ""
}
