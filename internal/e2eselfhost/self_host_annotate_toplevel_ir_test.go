package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateTopLevelCases close the last producer gap in the typed-IR annotate
// pass (#5520 / #5531): annotate_module stamped every function body but left
// mod.top_stmts alone, so a SCRIPT-shaped module — top-level statements, no
// `main`, which asmcore.synth_script_main desugars into `function main(): i32`
// inside the emit (#5657) — lowered with every top-level call unannotated.
// annotate_module now walks top_stmts from the bare module scope, threading
// statement-to-statement exactly as check_module's own top_stmts loop does.
//
// Each case makes a top-level call's RESULT TYPE observable in the exit code:
// f64 arithmetic, an unsigned shift, a string length, a struct field, and a
// for-loop over a call-returned array (which also exercises the loop-variable
// binding). Expected values are computed by hand rather than from the `-interp`
// oracle the sibling annotate tests use: cmd/fern's parser accepts only
// function-shaped modules, so a script has no native oracle — script support is
// a self-host driver feature.
//
// The route assertion is load-bearing twice over. It pins that these programs
// take the IR path at all, and it pins the `-decide` fix that shipped with them:
// the gate judged the RAW module, whose `main` the emit had not synthesised yet,
// so `-decide` printed "ast" for a script that emit_module_ir_gated then lowered
// through IR. Both now normalise through asm_ir.script_normalized.
var annotateTopLevelCases = []struct {
	name string
	src  string
	want int
}{
	// f64-returning call at the top level: an integer add on the doubles' bits
	// would not sum to 3.5.
	{"top_f64_call", `function half(): f64 { return 0.5; }
var t: f64 = half() + half() + 2.5;
return t as i32;
`, 3},
	// u64-returning call at the top level: a SIGNED >> on a bit-63-set value
	// gives a different answer.
	{"top_u64_call", `function bigu(): u64 { return 18000000000000000000 as u64; }
var t: u64 = bigu() >> 40;
return t as i32;
`, 216},
	// string-returning call bound to a top-level local, then measured.
	{"top_str_call", `function label(): string { return "abc"; }
var s: string = label();
return s.len();
`, 3},
	// struct-returning call at the top level, read by field.
	{"top_struct_call", `struct Pt { x: i32, y: i32 }
function origin(): Pt { return Pt { x: 3, y: 4 }; }
var p: Pt = origin();
return p.x * p.y;
`, 12},
	// A top-level for-loop over a call-returned array, summing an f64 method on
	// the loop variable — top_stmts annotation and the loop-variable binding in
	// the same program.
	{"top_for_f64_method", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 2.0; }
function mk(): V[] { return [V { x: 1.5 }, V { x: 0.375 }]; }
var vs: V[] = mk();
var t: f64 = 0.0;
for v in vs { t = t + v.scaled(); }
return t as i32;
`, 3}, // 3.0 + 0.75 = 3.75 -> 3
}

// TestSelfHostAnnotateTopLevelIR_X86_64 pins the checker-stamped result type on
// TOP-LEVEL calls through the self-host x86-64 IR path (#5520 / #5531).
func TestSelfHostAnnotateTopLevelIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, _ := annotateF64ProjDir(t)
	for _, tc := range annotateTopLevelCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (a script must normalise through script_normalized before the gate)", tc.name, got)
			}
			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			// The IR path's whole-program emit writes `_start` -> `call __fn_main`;
			// the AST no-main path inlines the statements into `_start` instead, so
			// this is what distinguishes a genuinely IR-lowered script.
			if !strings.Contains(string(asm), "call __fn_main") {
				t.Fatalf("%s: emitted asm has no `call __fn_main` — the script did not lower through the IR", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "anntop_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s (IR annotate path) exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
