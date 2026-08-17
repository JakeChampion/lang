package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A bare reference to a top-level `const` had no type in the self-host checker,
// so `-check` rejected every program that reads one — as un-inferable under
// #4346, the catch-all for expressions the partial `Type` union cannot model.
// It was not that kind of gap: a const reaches the parser as a zero-parameter
// FuncDecl carrying `is_const`, so it lands in the SIG table while an ident is
// resolved against the VALUE scope, and the two never met.
//
// The fixpoint is structurally blind to this — it compiles the compiler, it
// does not `-check` it, and lowering was always right (a program the checker
// refused still built and ran correctly). Native is the oracle: each case
// asserts the two compilers AGREE on whether the program checks, so a case that
// native rejects for its own reasons cannot pass by being rejected here for the
// wrong one.
func TestSelfHostConstRefCheckX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("check differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, c := range []struct {
		name string
		src  string
	}{
		// The reference alone, in the three positions a value reaches.
		{"const-i32-returned", "const N: i32 = 41;\nfunction main(): i32 { return N - 41; }\n"},
		{"const-i32-in-arith", "const N: i32 = 41;\nfunction main(): i32 { var x: i32 = N * 2 - 82; return x; }\n"},
		{"const-i32-as-argument", "const N: i32 = 41;\nfunction take(x: i32): i32 { return x - 41; }\nfunction main(): i32 { return take(N); }\n"},
		// Each const type resolves to its OWN declared type, not to a default:
		// a boolean read as i32 would fail the condition, and an f64 read as
		// i32 would fail the arithmetic.
		{"const-boolean-in-condition", "const B: boolean = true;\nfunction main(): i32 { if (B) { return 0; } return 1; }\n"},
		{"const-f64-in-arith", "const F: f64 = 1.5;\nfunction main(): i32 { var g: f64 = F + 0.5; if (g > 1.9) { return 0; } return 1; }\n"},
		// One const reading an earlier one — the const's own body goes through
		// the same resolution as a function body's reference.
		{"const-reads-earlier-const", "const A: i32 = 2;\nconst B: i32 = A * 3;\nfunction main(): i32 { return B - 6; }\n"},
		// The flag is what distinguishes a const from a zero-parameter FUNCTION
		// of the same shape. This case shares the sig table with the ones above
		// and must keep typing as a call rather than as a bare value.
		{"zero-arg-function-unaffected", "function z(): i32 { return 1; }\nfunction main(): i32 { return z() - 1; }\n"},
		// A const naming a type the checker genuinely cannot model must still be
		// refused, and by BOTH — reading the sig's return type unconditionally
		// would have made this one pass here while native rejects it.
		{"const-outside-the-grammar", "struct Cfg { n: i32 }\nconst C: Cfg = Cfg { n: 41 };\nfunction main(): i32 { return C.n; }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, "cref_"+c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			nativeCmd := exec.Command(nativeBin, "-check", src)
			nativeOut, _ := nativeCmd.CombinedOutput()
			shCmd := exec.Command(driverBin, "-check", src, stdlib)
			shOut, _ := shCmd.CombinedOutput()

			nativeOK := nativeCmd.ProcessState.ExitCode() == 0
			shOK := shCmd.ProcessState.ExitCode() == 0
			if nativeOK != shOK {
				t.Errorf("native accepts = %v, self-host accepts = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
		})
	}
}
