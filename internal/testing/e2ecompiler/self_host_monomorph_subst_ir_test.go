package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

var monomorphSubstCases = []struct {
	name string
	src  string
}{
	{"plain", "import \"std/num\" as num;\n" +
		"function (a: T[]) total[T: num.Num + num.Zero](): T { let s: T = T.zero(); let i: i32 = 0; while (i < a.len()) { s = s.add(a[i]); i = i + 1; } return s; }\n" +
		"function main(): i32 { let xs: i32[] = [40, 2]; return xs.total(); }\n"},
	{"in-array-literal", "import \"std/num\" as num;\n" +
		"function (a: T[]) total[T: num.Num + num.Zero](): T { let seed: T[] = [T.zero()]; let s: T = seed[0]; let i: i32 = 0; while (i < a.len()) { s = s.add(a[i]); i = i + 1; } return s; }\n" +
		"function main(): i32 { let xs: i32[] = [40, 2]; return xs.total(); }\n"},
	{"in-slice", "import \"std/num\" as num;\n" +
		"function (a: T[]) total[T: num.Num + num.Zero](): T { let seed: T[] = [T.zero(), T.zero()]; let s: T = seed[0:1][0]; let i: i32 = 0; while (i < a.len()) { s = s.add(a[i]); i = i + 1; } return s; }\n" +
		"function main(): i32 { let xs: i32[] = [40, 2]; return xs.total(); }\n"},
}

func TestSelfHostMonomorphSubstIRX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range monomorphSubstCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm, progDir := compileSourceModload(t, runner, driverBin, tc.src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, progDir, "msub_"+tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Stdin = bytes.NewReader(nil)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (native interp oracle)", tc.name, code, want)
			}
		})
	}
}
