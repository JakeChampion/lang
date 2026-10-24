package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var stringModuleFnIRCases = []struct {
	name string
	src  string
}{
	{"repeat_char-len", `import "std/string";
function main(): i32 { return string.repeat_char(65 as char, 3).len(); }`},
	{"repeat_char-var", `import "std/string";
function main(): i32 { let s: string = string.repeat_char(66 as char, 5); return s.len(); }`},
	{"repeat_char-empty", `import "std/string";
function main(): i32 { return string.repeat_char(67 as char, 0).len(); }`},
	{"trim-method", `import "std/string";
function main(): i32 { return "  hi  ".trim().len(); }`},
	{"camel_case-boundary", `import "std/string";
function main(): i32 { return "foo_bar".camel_case()[3] as i32; }`},
	{"pascal_case-initial", `import "std/string";
function main(): i32 { return "foo_bar".pascal_case()[0] as i32; }`},
	{"camel_case-first-lower", `import "std/string";
function main(): i32 { return "Foo_bar".camel_case()[0] as i32; }`},
	{"camel_case-collapse-len", `import "std/string";
function main(): i32 { return "__foo__bar__".camel_case().len(); }`},
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
