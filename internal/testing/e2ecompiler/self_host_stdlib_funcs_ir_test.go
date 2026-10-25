package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var stdlibFuncCases = []struct {
	name string
	src  string
}{
	{"str-to-upper", `import "std/unicode";
import "std/string";
function main(): i32 { let s = "hello"; if (s.to_upper() == "HELLO") { return 42; } return 0; }`},
	{"str-methods", `import "std/unicode";
import "std/string";
function main(): i32 { let s = "Hello, World"; let n = 0; if (s.to_lower() == "hello, world") { n = n + 1; } if (s.starts_with("Hello")) { n = n + 1; } if (s.len() == 12) { n = n + 1; } return n * 14; }`},
	// core/int's qualified module-free function parse_int_radix (decimal).
	{"int-parse-dec", `import "std/i32";
import "core/int";
function main(): i32 { match (i32.parse_i32_radix("42", 10)) { Ok(n) => { return n; }, Err(_) => { return 0; } } }`},
	// parse_int_radix base 16: "2a" → 42.
	{"int-parse-hex", `import "std/i32";
import "core/int";
function main(): i32 { match (i32.parse_i32_radix("2a", 16)) { Ok(n) => { return n; }, Err(_) => { return 0; } } }`},
}

func TestSelfHostStdlibFuncsIR(t *testing.T) {
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

	for _, tc := range stdlibFuncCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "sf_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "sf_"+tc.name+"_bin", asm)
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
