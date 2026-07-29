package e2eselfhost

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
	// camel_case / pascal_case (the inverse of snake_case): method calls,
	// oracle-checked against the interpreter. Probe result CONTENT via
	// byte-indexing so a self-host miscompile of the case fold — not just
	// a length change — is caught. "foo_bar".camel_case() == "fooBar", so
	// [3] is 'B' (66); "foo_bar".pascal_case() == "FooBar", [0] is 'F'
	// (70); "Foo_bar".camel_case() lower-cases the first initial to 'f'
	// (102); "__foo__bar__".camel_case() == "fooBar" (len 6).
	{"camel_case-boundary", `import "std/string";
function main(): i32 { return "foo_bar".camel_case()[3] as i32; }`},
	{"pascal_case-initial", `import "std/string";
function main(): i32 { return "foo_bar".pascal_case()[0] as i32; }`},
	{"camel_case-first-lower", `import "std/string";
function main(): i32 { return "Foo_bar".camel_case()[0] as i32; }`},
	{"camel_case-collapse-len", `import "std/string";
function main(): i32 { return "__foo__bar__".camel_case().len(); }`},
	// is_camel_case / is_pascal_case predicates, oracle-checked. Return
	// the boolean as an exit code (true=1) so a self-host miscompile of
	// the classifier diverges from the interpreter. "fooBar" is camel (1);
	// "FooBar" is not camel (0); "FooBar" is pascal (1); "foo_bar" (with a
	// separator) is neither (0).
	{"is_camel_case-true", `import "std/string";
function main(): i32 { if ("fooBar".is_camel_case()) { return 1; } return 0; }`},
	{"is_camel_case-false", `import "std/string";
function main(): i32 { if ("FooBar".is_camel_case()) { return 1; } return 0; }`},
	{"is_pascal_case-true", `import "std/string";
function main(): i32 { if ("FooBar".is_pascal_case()) { return 1; } return 0; }`},
	{"is_pascal_case-sep", `import "std/string";
function main(): i32 { if ("foo_bar".is_pascal_case()) { return 1; } return 0; }`},
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
