package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Element-type typing of a `for p in <(tuple)[]>` loop variable. The element
// tuple's tags are recorded on the array slot's arrarr_elem (#4365) — the
// direct-index `ps[i].N` reads them there — but the FOR-LOOP var `p` needs its
// own mark_tuple_elems, which the loop lowering omitted. So `p.N` had no element
// tag: an INFERRED `var s = p.1` (a string element) typed it wrong and `s.len()`
// mis-read the length — a SILENT MISCOMPILE (p.1 itself printed fine; only its
// derived length was wrong), on the IR path. The loop var carries the tuple
// tags, so `p.N` resolves — pointer elements (string / struct) included. Found by
// differential probing; each case is oracle-checked vs the interpreter and
// routing-pinned to "ir".
var foreachTupleElemIRCases = []struct {
	name string
	src  string
}{
	// The original miscompile: `p.1.len()` over an (i32, string) tuple array —
	// 1 + 2 + 3 = 6. (`p.1` is a pointer element; the i32 element p.0 was fine.)
	{"i32_string_p1_len", `function main(): i32 {
    var ps: (i32, string)[] = [(1, "a"), (2, "bb"), (3, "ccc")];
    var s = 0;
    for p in ps { s = s + p.1.len(); }
    return s;
}`},
	// Both elements read: p.0 (i32) + p.1.len() → (1+1)+(2+2)+(3+3) = 12.
	{"i32_string_both", `function main(): i32 {
    var ps: (i32, string)[] = [(1, "a"), (2, "bb"), (3, "ccc")];
    var s = 0;
    for p in ps { s = s + p.0 + p.1.len(); }
    return s;
}`},
	// (string, string): reading the SECOND string element specifically → 2 + 3 = 5.
	{"string_string_p1", `function main(): i32 {
    var ps: (string, string)[] = [("a", "xx"), ("bb", "yyy")];
    var s = 0;
    for p in ps { s = s + p.1.len(); }
    return s;
}`},
	// Inferred local off the element: `var str = p.1` (no annotation) — the exact
	// path the miscompile took (an annotated `var str: string = p.1` masked it) → 6.
	{"inferred_local", `function main(): i32 {
    var ps: (i32, string)[] = [(1, "a"), (2, "bb"), (3, "ccc")];
    var s = 0;
    for p in ps { var str = p.1; s = s + str.len(); }
    return s;
}`},
	// A STRUCT tuple element: `p.1.name.len()` + `p.1.age` resolve through the
	// element tuple tag → (1 + 2 + 30) + (2 + 3 + 40) = 78.
	{"struct_elem", `struct Rec { name: string, age: i32 }
function main(): i32 {
    var ps: (i32, Rec)[] = [(1, Rec { name: "ab", age: 30 }), (2, Rec { name: "cde", age: 40 })];
    var s = 0;
    for p in ps { s = s + p.0 + p.1.name.len() + p.1.age; }
    return s;
}`},
}

func TestSelfHostForeachTupleElemIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "fte")
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
