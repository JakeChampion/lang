package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

// A read the loop test already made is the same read in the loop body. The
// index comes from a parameter, so no bounds proof removes the check: only
// the merge does, and the body then adds no check of its own.
var dominatedReadCases = []struct{ name, merged, bare string }{
	{"str_index",
		`@noinline function scan(s: string, from: i32): i32 { let i: i32 = from; let v: i32 = 0; while (i < s.len() && s[i] as i32 != 46) { v = v * 10 + (s[i] as i32 - 48); i = i + 1; } return v; } function main(): i32 { return scan("12.5", 0) + scan("7", 0); }`,
		`@noinline function scan(s: string, from: i32): i32 { let i: i32 = from; let v: i32 = 0; while (i < s.len() && s[i] as i32 != 46) { v = v * 10 + 1; i = i + 1; } return v; } function main(): i32 { return scan("12.5", 0) + scan("7", 0); }`},
	{"array_get",
		`@noinline function sum(xs: i32[], from: i32): i32 { let i: i32 = from; let v: i32 = 0; while (i < xs.len() && xs[i] > 0) { v = v + xs[i]; i = i + 1; } return v; } function main(): i32 { return sum([3, 5, 7, 0, 9], 1); }`,
		`@noinline function sum(xs: i32[], from: i32): i32 { let i: i32 = from; let v: i32 = 0; while (i < xs.len() && xs[i] > 0) { v = v + 1; i = i + 1; } return v; } function main(): i32 { return sum([3, 5, 7, 0, 9], 1); }`},
}

// The read under the if does not dominate the read after it, so the later
// read keeps its own check.
const undominatedRead = `@noinline function pick(xs: i32[], i: i32, flag: boolean): i32 { let a: i32 = 0; if (flag) { a = xs[i]; } return a + xs[i]; } `

func TestSelfHostDominatedReadMerged(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	arm64gcc, qemu := arm64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		emit := func(src string) string {
			asm := runCapture(t, gcc, runner, driverBin, []byte(src+"\n"), "-target", target)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", target)
			}
			return string(asm)
		}
		exit := func(name, asm string) int {
			var cmd *exec.Cmd
			if target == "arm64-linux" {
				cmd = runArm64Bin(qemu, buildBin(t, arm64gcc, dir, "domread_arm64_"+name, asm))
			} else {
				cmd = runX86_64Bin(runner, buildBin(t, gcc, dir, "domread_"+name, asm))
			}
			_ = cmd.Run()
			return cmd.ProcessState.ExitCode()
		}
		for _, tc := range dominatedReadCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				asm := emit(tc.merged)
				if got, bare := strings.Count(asm, "oob_abort"), strings.Count(emit(tc.bare), "oob_abort"); got != bare {
					t.Errorf("the body read kept a check: %d oob_abort references, %d with no body read", got, bare)
				}
				if got, want := exit(tc.name, asm), interpExit(t, interpBin, tc.merged+"\n"); got != want {
					t.Errorf("exited %d, want %d (interp oracle)", got, want)
				}
			})
		}
		t.Run(target+"/undominated", func(t *testing.T) {
			in := undominatedRead + `function main(): i32 { return pick([1, 2], 1, true); }`
			if got, want := exit("undominated_in", emit(in)), interpExit(t, interpBin, in+"\n"); got != want {
				t.Errorf("exited %d, want %d (interp oracle)", got, want)
			}
			out := undominatedRead + `function main(): i32 { return pick([1, 2], 5, false); }`
			if got := exit("undominated_out", emit(out)); got != 134 {
				t.Errorf("exited %d, want 134 (the read after the if must stay checked)", got)
			}
		})
	}
}
