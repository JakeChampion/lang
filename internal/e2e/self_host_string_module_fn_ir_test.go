package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/string free-FUNCTION calls (`string.repeat_char(…)`) on the self-host IR
// path. The std/string module's basename alias is `string`, which collides with
// the `string` primitive TYPE keyword: the self-host lexer emits a keyword token
// for it, so `string.repeat_char(…)` did not parse as a module-qualified
// reference (the native -interp parser accepts it). It was therefore never
// rewritten to `string__repeat_char` by flatten_qualified and the call bailed to
// the AST emitter (the function itself lowered fine — only the call site at the
// type-keyword base was unparseable). parse_primary now treats a type keyword
// immediately followed by `.` as a module-qualified reference base, so these
// calls route the self-host IR path and match the interpreter. (String METHODS
// like `"x".trim()` already worked — this is specifically the module-qualified
// free-function form.)
var stringModuleFnIRCases = []struct {
	name string
	src  string
}{
	// repeat_char(ch, n) -> string; "AAA".len() == 3.
	{"repeat_char-len", `import "std/string";
function main(): i32 { return string.repeat_char(65, 3).len(); }`},
	// Bound to a var first, then .len() — exercises the call in a var-init.
	{"repeat_char-var", `import "std/string";
function main(): i32 { var s: string = string.repeat_char(66, 5); return s.len(); }`},
	// n <= 0 -> "" (the early-return branch); len 0.
	{"repeat_char-empty", `import "std/string";
function main(): i32 { return string.repeat_char(67, 0).len(); }`},
	// A string METHOD still lowers (regression guard for the parser change).
	{"trim-method", `import "std/string";
function main(): i32 { return "  hi  ".trim().len(); }`},
}

func TestSelfHostStringModuleFnIR(t *testing.T) {
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

	for _, tc := range stringModuleFnIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "strmod_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "strmod_"+tc.name+"_bin", asm)
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
