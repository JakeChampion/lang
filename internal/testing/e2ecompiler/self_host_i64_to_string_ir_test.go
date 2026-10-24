package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var i64ToStringIRCases = []struct {
	name string
	src  string
}{
	{"i64-neg-large", `import "std/i64";
function main(): i32 { let n: i64 = 0 - (9876543210 as i64); if (n.to_string() == "-9876543210") { return 42; } return 0; }`},
	{"i64-pos-large", `import "std/i64";
function main(): i32 { let n: i64 = 9876543210 as i64; if (n.to_string() == "9876543210") { return 42; } return 0; }`},
	{"u64-highbit-direct", `import "core/int";
function main(): i32 { let n: u64 = 18000000000000000007 as u64; if (int.__int_to_string_u64(n as i64, 0) == "18000000000000000007") { return 42; } return 0; }`},
	{"i64-as-u64-mod", `import "core/int";
function main(): i32 { let m: i64 = 9876543210 as i64; return (m as u64 % (7 as u64)) as i32; }`},
}

func TestSelfHostI64ToStringIR(t *testing.T) {
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

	for _, tc := range i64ToStringIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "i64s_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "i64s_"+tc.name+"_bin", asm)
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
