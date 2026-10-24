package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrayConcatMethodIRCases = []struct {
	name   string
	src    string
	kernel bool
}{
	{"i32", `import "std/array";
function main(): i32 { let a: i32[] = [1, 2, 3]; let b: i32[] = [4, 5]; let c: i32[] = a.concat(b); return c.len() * 10 + c[4]; }`, true},
	{"string", `import "std/array";
function main(): i32 { let a: string[] = ["ab", "c"]; let b: string[] = ["de", "f"]; let c: string[] = a.concat(b); return c.len(); }`, false},
	{"literal-arg", `import "std/array";
function main(): i32 { let a: i32[] = [7, 8]; let c: i32[] = a.concat([9]); return c.len() * 10 + c[2]; }`, true},
	{"chained", `import "std/array";
function main(): i32 { let a: i32[] = [1]; let b: i32[] = [2, 3]; let c: i32[] = a.concat(b).concat(a); return c.len(); }`, true},
}

func TestSelfHostArrayConcatMethodIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range arrayConcatMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "arrcat_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			if tc.kernel && !strings.Contains(asm, "__fern_arr_concat") {
				t.Errorf("%s: no call to __fern_arr_concat in asm (a scalar concat did not take the runtime join)", tc.name)
			}
			if !tc.kernel && !strings.Contains(asm, "__arrm_concat__") {
				t.Errorf("%s: no monomorphised __arrm_concat__ clone in asm (method did not ride the IR array-method path)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "arrcat_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
