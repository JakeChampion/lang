package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A compiler BUILT THROUGH THE IR PATH must agree with the native-built one
// about IR eligibility (#5649).
//
// It did not. Any module that passed a bare named function as a value was
// wrongly routed to the legacy AST emitter by the IR-built generation, while
// the native-built generation emitted it through IR. The bail is
// emit_module_ir_gated's const_func arm: module_has_func could not find the
// lifted wrapper, because make_wrap_named_func — run inside an IR-built
// compiler — declared the wrapper under its BASE name (`main`) rather than
// `main$wrap0` (the lowering defect behind that is #5674; irlower works around
// it at the one known site). That mattered twice over: it made the #3425 flip
// diverge, and per #5622 the AST emitter silently drops i32 wrapping, so
// anything wrongly routed there is compiled by an emitter with a known
// correctness defect.
//
// The failure is self-referential — the IR path miscompiles the code that
// decides IR eligibility — so it is invisible to every single-generation test.
// Pinning it needs two generations, which is what this test builds.
//
// It is env-gated because it is expensive in a way the ordinary suite is not:
// it builds a second full mmc1/mmc2 pair (~4 min, a ~220 MB stage-1 binary and
// a heavy emit) that cannot be shared with TestSelfHostStage2FixedPoint's pair,
// since it needs asm_ir.fern's 512-function budget removed. With the budget in
// place the bootstrap uses the AST emitter for BOTH generations, they agree
// trivially, and the bug cannot manifest. Run it with:
//
//	RUN_CONST_FUNC_GEN2=1 go test ./internal/e2eselfhost/ -run TestSelfHostConstFuncGen2 -timeout 30m
func TestSelfHostConstFuncGen2(t *testing.T) {
	if os.Getenv("RUN_CONST_FUNC_GEN2") == "" {
		t.Skip("set RUN_CONST_FUNC_GEN2=1 to run the two-generation const_func eligibility check")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("two-generation test runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "util.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Drop the 512-function IR budget in the temp-dir copy only. Both budget
	// sites gate on the same predicate; neutering them routes the ~525-function
	// merged bundle through IR, which is what makes generation 2 an IR-BUILT
	// compiler rather than an AST-built one.
	irPath := filepath.Join(dir, "asm_ir.fern")
	irSrc, err := os.ReadFile(irPath)
	if err != nil {
		t.Fatalf("read asm_ir.fern: %v", err)
	}
	patched := strings.NewReplacer(
		"if (mod.funcs.len() > 512) { return false; }", "if (false) { return false; }",
		`if (mod.funcs.len() > 512) { return ""; }`, `if (false) { return ""; }`,
	).Replace(string(irSrc))
	if n := strings.Count(patched, "if (false) { return"); n != 2 {
		t.Fatalf("budget-removal patch matched %d sites, want 2 — asm_ir.fern's budget shape changed", n)
	}
	if err := os.WriteFile(irPath, []byte(patched), 0o644); err != nil {
		t.Fatalf("write patched asm_ir.fern: %v", err)
	}

	// gen 1: native-built.
	selfSrc := filepath.Join(dir, "asm_load_run.fern")
	mmc1 := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "cfg_mmc1")

	// gen 2: built by gen 1, so its own body came out of the IR emitter.
	stage2Asm, err := exec.Command(mmc1, selfSrc).Output()
	if err != nil {
		t.Fatalf("mmc1 compile self failed: %v", err)
	}
	if !strings.Contains(string(stage2Asm), ".Lir") {
		t.Fatal("stage-2 asm has no .Lir labels — the budget removal did not take, so gen 2 is not IR-built")
	}
	mmc2 := buildBin(t, gcc, dir, "cfg_mmc2", string(stage2Asm))

	// The `.Lir` label count is the emitter discriminator: the IR emitter emits
	// them, the AST emitter emits none. Equal counts mean the two generations
	// agree on routing; the control's row is what proves the fn-value is the
	// trigger rather than the harness.
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"fn-value", "function work() { var i: i32 = 0; }\n" +
			"function run(f: () => void): i32 { f(); return 0; }\n" +
			"function main(): i32 { return run(work); }\n"},
		{"two-fn-values", "function work() { var i: i32 = 0; }\n" +
			"function work2() { var j: i32 = 1; }\n" +
			"function run(f: () => void): i32 { f(); return 0; }\n" +
			"function main(): i32 { return run(work) + run(work2); }\n"},
		{"control-direct-call", "function work() { var i: i32 = 0; }\n" +
			"function main(): i32 { work(); return 0; }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prog := filepath.Join(dir, "cfg_"+tc.name+".fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			n1 := lirLabelCount(t, mmc1, prog)
			n2 := lirLabelCount(t, mmc2, prog)
			if n1 == 0 {
				t.Fatalf("gen 1 emitted no .Lir labels — the program did not take the IR path at all")
			}
			if n1 != n2 {
				t.Errorf("gen 1 emitted %d .Lir labels, gen 2 emitted %d: the IR-built compiler disagrees about IR eligibility (#5649)", n1, n2)
			}
		})
	}
}

// lirLabelCount compiles prog with the given self-host compiler binary and
// returns how many `.Lir` labels the emitted asm carries.
func lirLabelCount(t *testing.T, compiler, prog string) int {
	t.Helper()
	asm, err := exec.Command(compiler, prog).Output()
	if err != nil {
		t.Fatalf("%s %s: %v", filepath.Base(compiler), filepath.Base(prog), err)
	}
	if len(asm) == 0 {
		t.Fatalf("%s emitted 0 bytes for %s", filepath.Base(compiler), filepath.Base(prog))
	}
	return strings.Count(string(asm), ".Lir")
}
