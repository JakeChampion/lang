package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestInterpNegativeExitCodeMatchesNative pins the AST interpreter's exit-code
// conversion for a NEGATIVE main() return to the POSIX low-byte semantics every
// compiled backend uses: the process exit status is the low 8 bits of the value
// passed to exit() (two's complement for negatives), so `return -3` exits 253
// (0xFD) and `return -1` exits 255.
//
// runInterp must not abs a negative return (`code = -code`) before masking:
// that interprets `-3` to exit 3 while the same program compiled to native /
// IR exits 253 — a reference-oracle divergence (the interpreter is the
// documented oracle for the differential self-host tests, which only stay sound
// because they avoid negative returns; cf. the <= 120/125 exit clamps). Each
// case asserts the interpreter exit == the native x86-64 exit == the expected
// POSIX byte.
func TestInterpNegativeExitCodeMatchesNative(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"ret-neg-3", `function main(): i32 { return -3; }`, 253},
		{"ret-neg-1", `function main(): i32 { return -1; }`, 255},
		{"ret-neg-128", `function main(): i32 { return -128; }`, 128},
		// -7 / 2 truncates toward zero to -3.
		{"neg-div-trunc", `function main(): i32 { var a = -7; var b = 2; return a / b; }`, 253},
		// -256 wraps to 0; -257 to 255 (low byte of the two's complement).
		{"ret-neg-256", `function main(): i32 { return -256; }`, 0},
		{"ret-neg-257", `function main(): i32 { return -257; }`, 255},
		// A positive return is unchanged (regression guard for the > 255 mask).
		{"ret-257", `function main(): i32 { return 257; }`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Interpreter (the reference oracle).
			f := filepath.Join(t.TempDir(), "neg.fern")
			if err := os.WriteFile(f, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			cmd := exec.Command(interpBin, "-interp", f)
			_ = cmd.Run()
			interpCode := cmd.ProcessState.ExitCode()
			if interpCode != tc.want {
				t.Errorf("%s: interp exit %d, want %d", tc.name, interpCode, tc.want)
			}
			// Native x86-64 compiled — the authoritative POSIX-exit oracle.
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
				t.Errorf("%s: native x86 exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
