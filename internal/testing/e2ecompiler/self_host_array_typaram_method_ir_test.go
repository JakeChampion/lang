package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrayTyparamMethodIRCases = []struct {
	name string
	src  string
}{
	{"map", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = xs.map((n: i32): i32 => { return n * 10; }); return ys[2]; }`},
	{"map-widen", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 22, 333]; let ys: string[] = xs.map((n: i32): string => { return n.to_string(); }); return ys[2].len() * 10 + ys.len(); }`},
	{"flat_map", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = xs.flat_map((x: i32): i32[] => { return [x, x * 10]; }); return ys.len() * 100 + ys[1]; }`},
	{"fold-widen", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 2, 3]; let s: string = xs.fold("", (a: string, n: i32): string => { return a + n.to_string(); }); return s.len(); }`},
	{"zip", `import "std/array";
function main(): i32 { let a: i32[] = [1, 2, 3]; let b: i32[] = [9, 8]; let z: (i32, i32)[] = a.zip(b); return z.len() * 10 + z[0].0; }`},
}

func TestSelfHostArrayTyparamMethodIR(t *testing.T) {
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

	for _, tc := range arrayTyparamMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "atm_"+tc.name+".fern")
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
			if !strings.Contains(asm, "__arrm_") {
				t.Errorf("%s: no monomorphised __arrm_ clone in asm (method did not ride the IR array-method path)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "atm_"+tc.name+"_bin", asm)
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
