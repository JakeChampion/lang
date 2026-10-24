package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var u64MethodCases = []struct {
	name string
	src  string
}{
	{"min", `import "std/u64";
function main(): i32 { let a: u64 = 7 as u64; let b: u64 = 3 as u64; return a.min(b) as i32; }`},
	{"max", `import "std/u64";
function main(): i32 { let a: u64 = 7 as u64; let b: u64 = 3 as u64; return a.max(b) as i32; }`},
	{"max-highbit", `import "std/u64";
function main(): i32 { let a: u64 = 18000000000000000007 as u64; let b: u64 = 9 as u64; return (a.max(b) % (100 as u64)) as i32; }`},
	{"clamp-highbit-hi", `import "std/u64";
function main(): i32 { let hi: u64 = 18000000000000000000 as u64; return (50 as u64).clamp(10 as u64, hi) as i32; }`},
	{"to_string-2p32", `import "std/u64";
function main(): i32 { let n: u64 = 4294967296 as u64; if (n.to_string() == "4294967296") { return 42; } return 0; }`},
	{"to_string-highbit", `import "std/u64";
function main(): i32 { let n: u64 = 18000000000000000007 as u64; if (n.to_string() == "18000000000000000007") { return 42; } return n.to_string().len(); }`},
	{"user-method-u64-shift", `struct P { n: u64 }
function (self: P) big(): u64 { return self.n; }
function main(): i32 { let p: P = P { n: 18000000000000000000 as u64 }; return (p.big() >> 57) as i32; }`},
}

func TestSelfHostU64MethodsIR(t *testing.T) {
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

	for _, tc := range u64MethodCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "u64m_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "u64m_"+tc.name+"_bin", asm)
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
