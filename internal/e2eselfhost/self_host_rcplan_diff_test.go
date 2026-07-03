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

// TestSelfHostRcPlanDiffPreciseDrops is the e2e half of the #4482 rcPlan
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
// This first slice diffs `preciseDrops` only (the one table both sides
// compute); the diff widens line-by-line as ports land. The self-host dump
// deliberately OMITS tables it has no counterpart for (movedLocals,
// moveSites, ...) — a native-only line is a documented port gap, not a
// failure, so the comparison here is per-table, not whole-dump.
//
// Known divergences are pinned explicitly (both sides' current output) so
// drift on EITHER side is caught; agreement cases assert equality plus, for
// the anchor case, the absolute expected value — agreement alone can't tell
// "both right" from "both wrong the same way".
func TestSelfHostRcPlanDiffPreciseDrops(t *testing.T) {
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

	type divergence struct {
		native   string // native's preciseDrops line value ("" = no line)
		selfhost string // self-host's value
	}
	cases := []struct {
		name string
		src  string
		// anchor: function -> exact preciseDrops value BOTH sides must emit.
		anchor map[string]string
		// diverge: function -> pinned per-side values. Everything else must
		// simply agree between the compilers.
		diverge map[string]divergence
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
			anchor: map[string]string{"dropper": "1=big"},
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
			anchor: map[string]string{"two": "1=a,3=b"},
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
			anchor: map[string]string{"pair": "2=a+b"},
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
			anchor: map[string]string{"make": ""},
			diverge: map[string]divergence{
				"main": {native: "", selfhost: "1=m"},
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
			anchor: map[string]string{"pick": "2=a"},
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
				nl := rcPlanLine(native[fn], "preciseDrops")
				sl := rcPlanLine(selfhost[fn], "preciseDrops")
				if d, ok := tc.diverge[fn]; ok {
					if nl != d.native {
						t.Errorf("%s: native preciseDrops = %q, pinned divergence expects %q", fn, nl, d.native)
					}
					if sl != d.selfhost {
						t.Errorf("%s: self-host preciseDrops = %q, pinned divergence expects %q", fn, sl, d.selfhost)
					}
					continue
				}
				if nl != sl {
					t.Errorf("%s: preciseDrops diverge — native %q vs self-host %q\n--- native dump ---\n%s--- self-host dump ---\n%s",
						fn, nl, sl, native[fn], selfhost[fn])
				}
				if want, ok := tc.anchor[fn]; ok && nl != want {
					t.Errorf("%s: preciseDrops = %q on both sides, but the anchor expects %q", fn, nl, want)
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
