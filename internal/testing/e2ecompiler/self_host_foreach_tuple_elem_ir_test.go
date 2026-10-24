package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var foreachTupleElemIRCases = []struct {
	name string
	src  string
}{
	{"i32_string_p1_len", `function main(): i32 {
    let ps: (i32, string)[] = [(1, "a"), (2, "bb"), (3, "ccc")];
    let s = 0;
    for p in ps { s = s + p.1.len(); }
    return s;
}`},
	{"i32_string_both", `function main(): i32 {
    let ps: (i32, string)[] = [(1, "a"), (2, "bb"), (3, "ccc")];
    let s = 0;
    for p in ps { s = s + p.0 + p.1.len(); }
    return s;
}`},
	{"string_string_p1", `function main(): i32 {
    let ps: (string, string)[] = [("a", "xx"), ("bb", "yyy")];
    let s = 0;
    for p in ps { s = s + p.1.len(); }
    return s;
}`},
	{"inferred_local", `function main(): i32 {
    let ps: (i32, string)[] = [(1, "a"), (2, "bb"), (3, "ccc")];
    let s = 0;
    for p in ps { let str = p.1; s = s + str.len(); }
    return s;
}`},
	{"struct_elem", `struct Rec { name: string, age: i32 }
function main(): i32 {
    let ps: (i32, Rec)[] = [(1, Rec { name: "ab", age: 30 }), (2, Rec { name: "cde", age: 40 })];
    let s = 0;
    for p in ps { s = s + p.0 + p.1.name.len() + p.1.age; }
    return s;
}`},
}

func TestSelfHostForeachTupleElemIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "fte")
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

	for _, tc := range foreachTupleElemIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "fte_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "fte_"+tc.name+"_bin", asm)
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
