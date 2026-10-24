package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrayClosureMethodIRCases = []struct {
	name string
	src  string
}{
	{"reduce", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; match (xs.reduce((a: i32, b: i32): i32 => { return a + b; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"filter", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4, 5, 6]; let e: i32[] = xs.filter((n: i32): boolean => { return n % 2 == 0; }); return e.len() * 100 + e[0] * 10 + e[2]; }`},
	{"find", `import "std/array";
function main(): i32 { let xs: i32[] = [5, 8, 3, 9]; match (xs.find((n: i32): boolean => { return n > 7; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"find_last", `import "std/array";
function main(): i32 { let xs: i32[] = [5, 8, 3, 9, 2]; match (xs.find_last((n: i32): boolean => { return n > 4; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"rposition", `import "std/array";
function main(): i32 { let xs: i32[] = [1, 2, 3, 2, 1]; match (xs.rposition((n: i32): boolean => { return n == 2; })) { Some(i) => { return i; }, None => { return 9; } } }`},
}

func TestSelfHostArrayClosureMethodIR(t *testing.T) {
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

	for _, tc := range arrayClosureMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "acm_"+tc.name+".fern")
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
			if !strings.Contains(asm, "__arrm_"+tc.name+"__") {
				t.Errorf("%s: no monomorphised __arrm_%s__ clone in asm (method did not ride the IR array-method path)", tc.name, tc.name)
			}
			bin := buildBin(t, gcc, dir, "acm_"+tc.name+"_bin", asm)
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
