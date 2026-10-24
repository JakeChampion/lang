package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
		{"neg-div-trunc", `function main(): i32 { let a = -7; let b = 2; return a / b; }`, 253},
		{"ret-neg-256", `function main(): i32 { return -256; }`, 0},
		{"ret-neg-257", `function main(): i32 { return -257; }`, 255},
		{"ret-257", `function main(): i32 { return 257; }`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
				t.Errorf("%s: native x86 exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
