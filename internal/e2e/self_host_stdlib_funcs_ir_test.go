package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real stdlib-USING programs on the self-host IR path. Beyond @derive, with
// stdlib loading + the (default-on) treeshake prune the self-hosted compiler
// now compiles programs that call genuine stdlib functions/methods — std/string
// methods, core/int's qualified `parse_int_radix` free function — through the
// loaded modules, routing IR and matching the native interpreter. This is the
// "self-host can compile real stdlib programs" milestone the importless driver
// (which inlines/avoids stdlib) could never reach. Drives the self-hosted
// x86-64 loader (asm_load_run) with the repo's real stdlib as the root.
var stdlibFuncCases = []struct {
	name string
	src  string
}{
	// std/string method `.to_upper()` (receiver method on string).
	{"str-to-upper", `import "std/string";
function main(): i32 { var s = "hello"; if (s.to_upper() == "HELLO") { return 42; } return 0; }`},
	// several std/string methods in one program: to_lower / starts_with / len.
	{"str-methods", `import "std/string";
function main(): i32 { var s = "Hello, World"; var n = 0; if (s.to_lower() == "hello, world") { n = n + 1; } if (s.starts_with("Hello")) { n = n + 1; } if (s.len() == 12) { n = n + 1; } return n * 14; }`},
	// core/int's qualified module-free function parse_int_radix (decimal).
	{"int-parse-dec", `import "core/int";
function main(): i32 { match (int.parse_int_radix("42", 10)) { Some(n) => { return n; }, None => { return 0; } } }`},
	// parse_int_radix base 16: "2a" → 42.
	{"int-parse-hex", `import "core/int";
function main(): i32 { match (int.parse_int_radix("2a", 16)) { Some(n) => { return n; }, None => { return 0; } } }`},
}

func TestSelfHostStdlibFuncsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
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
			// Oracle: the native interpreter's exit code.
			_, want := runFixtureInterp(t, entry, "")
			// Loading the stdlib auto-applies treeshake → the merged module fits
			// the budget and routes IR.
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			// Emit + assemble + run; must match the oracle.
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
