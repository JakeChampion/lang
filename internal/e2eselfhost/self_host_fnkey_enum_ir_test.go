package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFnKeyEnumIRX86_64 pins a USER generic enum instantiated at a fn
// type argument (`Opt[(i32) => i32]`): a closure stored in the enum payload,
// matched out through an annotated scrutinee, and CALLED. Before #5298 the
// monomorphiser refused a composite (fn) type-arg key (ge_targ_mangle returned
// "" for a `(...) => R` spelling), so `Opt` stayed generic with an erased `T`
// payload; the payload call then dispatched the closure BOX pointer as code and
// SIGSEGV'd on both the IR and AST paths. Now ge_targ_mangle sanitises the fn
// arg to a symbol-safe key, `Insts.iargs` threads the ORIGINAL spelling so the
// clone's field gets the real fn type (coarsened to "fn" + fn_ret, the parser's
// shape), and the IR match-arm binding marks it a closure local — so the chain
// computes the native value on the IR path (`.Lir_` asserted).
//
// The INLINE construct-and-match shape (`match (Has(f))`, no annotation) is
// covered too (#5298 follow-up): the StmtVar lambda encoding carries the full
// fn spelling, me_infer_variant_key sanitises an inferred fn key to the same
// token the annotated path uses (shared clone), and me_scrutinee_type types
// the variant construction so the arms rewrite and the payload binds as a
// closure.
func TestSelfHostFnKeyEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Annotated var: build Opt[(i32)=>i32], match it, call the payload.
		{"fnkey-enum-var-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var f: (i32) => i32 = function (x: i32): i32 { return x + 40; }; var o: Opt[(i32) => i32] = Has(f); match (o) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		// Capture-free closure payload.
		{"fnkey-enum-capturefree",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var o: Opt[(i32) => i32] = Has(function (x: i32): i32 { return x + 40; }); match (o) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		// Capturing closure payload (captures a local through the enum box).
		{"fnkey-enum-capturing",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var base: i32 = 40; var f: (i32) => i32 = function (x: i32): i32 { return x + base; }; var o: Opt[(i32) => i32] = Has(f); match (o) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		// Fn-value param carrying the enum, matched inside the callee.
		{"fnkey-enum-param",
			`enum Opt[T] { Non, Has(T) } function run(o: Opt[(i32) => i32]): i32 { match (o) { Has(g) => { return g(2); }, Non => { return 0; } } } function main(): i32 { var f: (i32) => i32 = function (x: i32): i32 { return x + 40; }; return run(Has(f)); }`,
			42},
		// Non branch taken (unit variant of the fn-keyed enum still resolves).
		{"fnkey-enum-non-branch",
			`enum Opt[T] { Non, Has(T) } function pick(b: boolean): Opt[(i32) => i32] { if (b) { return Has(function (x: i32): i32 { return x + 40; }); } return Non; } function main(): i32 { var o: Opt[(i32) => i32] = pick(false); match (o) { Has(g) => { return g(2); }, Non => { return 42; } } }`,
			42},
		// INLINE construct-and-match, no annotation anywhere on the enum value
		// (#5298 follow-up): the instantiation is inferred from the payload
		// arg's full fn spelling and the match arms rewrite to the same clone
		// the annotated path uses. Was a SIGSEGV.
		{"fnkey-enum-inline-match",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var f: (i32) => i32 = function (x: i32): i32 { return x + 40; }; match (Has(f)) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		// Inline shape with a CAPTURING closure payload.
		{"fnkey-enum-inline-capturing",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var base: i32 = 40; var f: (i32) => i32 = function (x: i32): i32 { return x + base; }; match (Has(f)) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		// The i32 inline sibling stays correct (the inferred-key path is
		// shared; a nominal key must not regress).
		{"fnkey-enum-inline-i32-regress",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { match (Has(42)) { Has(n) => { return n; }, Non => { return 0; } } }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the fn-keyed enum did not lower through the IR", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
