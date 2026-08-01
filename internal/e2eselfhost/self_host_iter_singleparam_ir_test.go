package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// iterSingleParamIRCases are real `import "core/iter"` programs that drive
// the single-type-parameter trait-bound combinators over a concrete
// iterator (iter.range -> iter.Range). These combinators —
// sum/product/min/max/contains/count_value/position (all
// `[I: Iterator[i32]]`) plus count (`[T, I: Iterator[T]]`) — were routed
// to the legacy AST emitter because the monomorphiser's clone_bg split the
// instantiation key on `__`, and a module-mangled type argument
// (`iter__Range`, from the bundling loader) itself contains `__`, so
// `split_dunder("iter__Range")` shattered into ["iter","Range"] and
// subst_ty bound the type parameter to the bogus "iter". The clone's
// `it: I` then became `it: iter`, an unknown type, and the IR lowerer
// bailed (BAIL lower). Guarding the single-type-param case (use the key
// verbatim, never split — the same guard the generic-struct / enum
// monomorphisers already use) lets these clones lower, so the whole
// program reaches `module: IR`.
//
// Each case asserts the modload -ir-probe verdict is `module: IR` AND
// that the compiled binary matches the interpreter oracle. x86-64 only
// (the loader driver takes argv file paths, like the other modload tests).
var iterSingleParamIRCases = []struct {
	name string
	main string
}{
	// sum 1..5 (exclusive) = 1+2+3+4 = 10.
	{"sum", "import \"core/iter\";\nfunction main(): i32 { return iter.sum(iter.range(1, 5)); }\n"},
	// product 1..5 = 1*2*3*4 = 24.
	{"product", "import \"core/iter\";\nfunction main(): i32 { return iter.product(iter.range(1, 5)); }\n"},
	// min over 3..9 = 3.
	{"min", "import \"core/iter\";\nfunction main(): i32 { match (iter.min(iter.range(3, 9))) { Some(v) => { return v; }, None => { return 0; }, } }\n"},
	// max over 3..9 = 8 (range is half-open).
	{"max", "import \"core/iter\";\nfunction main(): i32 { match (iter.max(iter.range(3, 9))) { Some(v) => { return v; }, None => { return 0; }, } }\n"},
	// contains 5 in 1..9 -> true -> 7.
	{"contains", "import \"core/iter\";\nfunction main(): i32 { if (iter.contains(iter.range(1, 9), 5)) { return 7; } return 0; }\n"},
	// count_value: how many 3s in 0..5 -> 1.
	{"count-value", "import \"core/iter\";\nfunction main(): i32 { return iter.count_value(iter.range(0, 5), 3); }\n"},
	// position of 13 in 10..20 -> index 3.
	{"position", "import \"core/iter\";\nfunction main(): i32 { match (iter.position(iter.range(10, 20), 13)) { Some(p) => { return p; }, None => { return 99; }, } }\n"},
	// count of 0..7 (two type params [T, I]) = 7.
	{"count", "import \"core/iter\";\nfunction main(): i32 { return iter.count(iter.range(0, 7)); }\n"},
}

// TestSelfHostIterSingleParamIRX86_64 compiles real `import "core/iter"`
// programs that use the single-type-parameter trait-bound combinators
// over a concrete iterator through the multi-module bundling loader, and
// asserts the whole program routes the IR path (`module: IR`) — the win
// from fixing clone_bg's instantiation-key over-split for module-mangled
// type arguments. Each binary is oracle-checked against the interpreter.
func TestSelfHostIterSingleParamIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range iterSingleParamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the whole program must reach the IR path.
			// Use -decide (the real codegen route decision, taken AFTER
			// monomorphisation) rather than -ir-probe: the combinators are
			// generic templates, so a pre-monomorphise per-function probe
			// always shows the un-instantiable template (`it: I`) bailing —
			// the route that matters is the one the monomorphised clones take.
			decide, err := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if got := string(bytes.TrimSpace(decide)); got != "ir" {
				t.Fatalf("%s routed %q, want ir", tc.name, got)
			}
			// Compile + run, oracle-checked.
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
