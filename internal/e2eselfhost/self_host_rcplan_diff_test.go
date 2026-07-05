package e2eselfhost

import (
	"os"
	"path/filepath"
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
// free_eligible_of port of native computeFreeEligible, #4482), movedLocals
// fourth (the moved_locals_of port of native computeMovedLocals). The
// self-host dump deliberately OMITS tables it has no counterpart for
// (moveSites, ...) — a native-only line is a documented port gap, not a
// failure, so the comparison here is per-table, not whole-dump.
//
// Known divergences are pinned explicitly (both sides' current output) so
// drift on EITHER side is caught; agreement cases assert equality plus, for
// the anchor case, the absolute expected value — agreement alone can't tell
// "both right" from "both wrong the same way".
func TestSelfHostRcPlanDiff(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irlower_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "rcplan_driver")

	// The tables diffed per function; widened line-by-line as ports land
	// (#4482). moveSites etc. are NOT diffed yet — the self-host has no
	// counterpart tables (moveSites is node-position-keyed and waits on
	// #2857), and its dump omits the lines.
	diffedTables := []string{"consumedParams", "freeEligible", "movedLocals", "preciseDrops"}

	type divergence struct {
		native   string // native's line value ("" = no line)
		selfhost string // self-host's value
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
			anchor: map[string]map[string]string{"dropper": {"preciseDrops": "1=big", "freeEligible": "big"}},
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
			anchor: map[string]map[string]string{"two": {"preciseDrops": "1=a,3=b", "freeEligible": "a,b"}},
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
			anchor: map[string]map[string]string{"thread": {"consumedParams": "c", "freeEligible": "c"}},
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
			// both sides.
			name: "move-on-alias",
			src: `function mv(): i32 {
	var a: i32[] = [1, 2];
	var b: i32[] = a;
	return b[0];
}
function main(): i32 { return mv(); }`,
			anchor: map[string]map[string]string{"mv": {"movedLocals": "a"}},
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
			anchor: map[string]map[string]string{"w": {"movedLocals": "x"}},
		},
		{
			// DESTRUCTURE MOVE: `var (xs, n) = t` at the tuple LOCAL's last
			// mention — the destructure's alias inc cancels t's sweep dec.
			name: "move-on-destructure",
			src: `function d(): i32 {
	var t: (i32[], i32) = ([5], 3);
	var (xs, n) = t;
	return xs[0] + n;
}
function main(): i32 { return d(); }`,
			anchor: map[string]map[string]string{"d": {"movedLocals": "t"}},
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
	if err := constfold.Fold(prog); err != nil {
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
