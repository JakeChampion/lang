package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var opTraitGenericIRCases = []struct {
	name string
	src  string
}{
	{"sum3", `import "std/num";
function sum3[T: num.Num](a: T, b: T, c: T): T { return a + b + c; }
function main(): i32 { return sum3(10, 20, 12); }`},
	{"diff-of-squares", `import "std/num";
function dsq[T: num.Num](a: T, b: T): T { return (a + b) * (a - b); }
function main(): i32 { return dsq(5, 3); }`},
	{"unary-neg", `import "std/num";
function ng[T: num.Neg](a: T): T { return -a; }
function main(): i32 { return ng(0 - 5); }`},
	{"accumulate", `import "std/num";
function tot[T: num.Num](xs: T[], z: T): T { let acc: T = z; for x in xs { acc = acc + x; } return acc; }
function main(): i32 { let xs: i32[] = [3, 4, 5, 6]; return tot(xs, 0); }`},
}

func TestSelfHostOpTraitGenericIR(t *testing.T) {
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

	for _, tc := range opTraitGenericIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "optgen_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "optgen_"+tc.name+"_bin", asm)
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
