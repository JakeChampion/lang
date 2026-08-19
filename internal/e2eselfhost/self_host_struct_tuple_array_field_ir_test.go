package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A struct field whose type is a tuple ARRAY (`pairs: (i32, string)[]`) now
// lowers on the IR path. Three gaps had to close, all in shared irlower.fern:
//
//  1. STRUCT ADMISSION (decl_is_leaksafe_d): the array-element branch admitted a
//     struct/enum/nested-array/Option element but not a leak-safe TUPLE element,
//     so ANY struct with a tuple-array field — even one that never read it —
//     bailed the whole module to the ~35 KB AST emitter (and miscompiled). Now an
//     `is_leaksafe_tuple_field` element is admitted in leak mode, like the sibling
//     array-of-struct / array-of-enum fields.
//  2. INDEX-READ BIND (lower_stmt_var): `var p = r.pairs[0]` read the element
//     tuple tags off the array LOCAL's arrarr_elem slot — but a struct-field array
//     access has no slot, so p got no tuple tags and `p.1.len()` mis-read a
//     pointer element (a silent miscompile: p.0=1, p.1.len()=0 → 1 not 2). The
//     new ExprFieldAccess arm resolves the element tuple type from the field's
//     declared type and marks p.
//  3. FOR-LOOP BIND (lower_stmt_for): the struct-field foreach path bound the loop
//     var for string[]/struct[]/enum[]/Option[] element fields but not a tuple[]
//     element field, so `for p in r.pairs` bailed the module to AST. A new tuple
//     element kind marks the loop var's tuple tags (the field-foreach sibling of
//     the #5305 local-array foreach fix).
//
// The tuple-array field leaks with the struct (like the string[]/struct[] fields),
// matching the AST path's exit codes. Found by differential probing (interp exit
// vs the self-host-IR binary); each case is oracle-checked and routing-pinned "ir".
var structTupleArrayFieldIRCases = []struct {
	name string
	src  string
}{
	// FOR-LOOP over the field reading a pointer (string) element: 1+2+3 = 6.
	{"foreach_field", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    var r = Row { pairs: [(1, "a"), (2, "bb"), (3, "ccc")] };
    var s = 0;
    for p in r.pairs { s = s + p.1.len(); }
    return s;
}`},
	// FOR-LOOP reading BOTH the i32 and the string element: 1*10+1 + 2*10+2 +
	// 3*10+3 = 66.
	{"foreach_both", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    var r = Row { pairs: [(1, "a"), (2, "bb"), (3, "ccc")] };
    var s = 0;
    for p in r.pairs { s = s + p.0 * 10 + p.1.len(); }
    return s;
}`},
	// INDEX-READ into a local: `var p = r.pairs[0]` then p.0 + p.1.len() = 1 + 1
	// = 2 (the silent-miscompile case: was 1 before the ExprFieldAccess arm).
	{"index_bind", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    var r = Row { pairs: [(1, "a"), (2, "bb")] };
    var p = r.pairs[0];
    return p.0 + p.1.len();
}`},
	// DIRECT index without a binding: r.pairs[2].0 + r.pairs[2].1.len() = 3 + 3 = 6.
	{"direct_index", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    var r = Row { pairs: [(1, "a"), (2, "bb"), (3, "ccc")] };
    return r.pairs[2].0 + r.pairs[2].1.len();
}`},
	// A (string, string) element array — read the SECOND string element: 2 + 3 = 5.
	{"string_string", `struct Row { pairs: (string, string)[] }
function main(): i32 {
    var r = Row { pairs: [("a", "xx"), ("bb", "yyy")] };
    var s = 0;
    for p in r.pairs { s = s + p.1.len(); }
    return s;
}`},
	// A scalar field alongside the tuple-array field + a NESTED tuple element
	// `(i32, (i32, string))`: n + sum of p.0 + p.1.0 + p.1.1.len() = 5 + (1+2+2)
	// + (3+4+3) = 20.
	{"mixed_nested", `struct Row { n: i32, pairs: (i32, (i32, string))[] }
function main(): i32 {
    var r = Row { n: 5, pairs: [(1, (2, "ab")), (3, (4, "cde"))] };
    var s = r.n;
    for p in r.pairs { s = s + p.0 + p.1.0 + p.1.1.len(); }
    return s;
}`},
	// RC stress: build 100 such structs in a loop, each passed by value to a
	// function that iterates the field — the leak-only field must not over-release
	// / underflow. total % 256 = (100 * (1+2)) % 256 = 300 % 256 = 44.
	{"rc_loop", `struct Row { pairs: (i32, string)[] }
function sumrow(r: Row): i32 { var s = 0; for p in r.pairs { s = s + p.1.len(); } return s; }
function main(): i32 {
    var total = 0;
    var i = 0;
    while (i < 100) {
        var r = Row { pairs: [(i, "x"), (i, "yy")] };
        total = total + sumrow(r);
        i = i + 1;
    }
    return total % 256;
}`},
}

// The x86-64 leg: emit + assemble + run through the self-hosted loader driver.
func TestSelfHostStructTupleArrayFieldIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "staf")
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

	for _, tc := range structTupleArrayFieldIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "staf_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "staf_"+tc.name+"_bin", asm)
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

// The wasm leg: the fixes live in shared irlower.fern, so the wasm IR backend
// admits the same struct and reads the tuple-box pointer elements through the
// 4-byte-slot arr_get walk. Drives wasm_ir_run (stdin → wat) and runs under
// wasmtime. Case table shared with the x86-64 leg.
func TestSelfHostStructTupleArrayFieldWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct-tuple-array-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structTupleArrayFieldIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Oracle: the native interpreter's exit code.
			entry := filepath.Join(dir, "wstaf_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s wasm = %d, want %d (native oracle)", tc.name, got, want)
			}
		})
	}
}
