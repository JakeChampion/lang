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
// it at the one known site). That is what made the #3425 flip diverge.
//
// The failure is self-referential — the IR path miscompiles the code that
// decides IR eligibility — so it is invisible to every single-generation test.
// Pinning it needs two generations, which is what this test builds.
//
// It is env-gated because it is expensive in a way the ordinary suite is not: it
// builds a full mmc1/mmc2 pair (~4 min, a ~220 MB stage-1 binary and a heavy
// emit). The gate keeps a plain `go test ./...` fast; CI runs it in the `gen2`
// job of test-e2e-selfhost.yml, which sets the variable. That lane exists
// because this is the ONLY guard for the hazard, and it was gated behind a
// variable nothing set — unguarded in CI at exactly the moment the 512-function
// budget was removed and generation 2 became IR-BUILT by default (#3457).
// Locally:
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
	copySelfHostDriver(t, dir, "asm_load_run.fern")

	// No source patching any more. This test used to strip asm_ir.fern's
	// 512-function IR budget in the temp-dir copy, because with the budget in
	// place the ~525-function merged bundle was refused and there was no
	// IR-built second generation to disagree with the first. The budget is now
	// GONE from the real source (#3457), so the test exercises the shipped
	// configuration rather than a simulation of it — which is the point: the
	// hazard it guards is the one the removal creates.

	// gen 1: native-built.
	selfSrc := filepath.Join(dir, "asm_load_run.fern")
	mmc1 := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "cfg_mmc1")

	// gen 2: built by gen 1, so its own body came out of the IR emitter.
	stage2Asm, err := exec.Command(mmc1, selfSrc).Output()
	if err != nil {
		t.Fatalf("mmc1 compile self failed: %v", err)
	}
	if !strings.Contains(string(stage2Asm), ".Lir") {
		t.Fatal("stage-2 asm has no .Lir labels — gen 2 is not IR-built, so the merged bundle is being refused again")
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
		{"fn-value", "function work(): void { var i: i32 = 0; }\n" +
			"function run(f: () => void): i32 { f(); return 0; }\n" +
			"function main(): i32 { return run(work); }\n"},
		{"two-fn-values", "function work(): void { var i: i32 = 0; }\n" +
			"function work2(): void { var j: i32 = 1; }\n" +
			"function run(f: () => void): i32 { f(); return 0; }\n" +
			"function main(): i32 { return run(work) + run(work2); }\n"},
		{"control-direct-call", "function work(): void { var i: i32 = 0; }\n" +
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
