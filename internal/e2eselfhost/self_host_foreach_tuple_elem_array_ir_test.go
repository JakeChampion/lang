package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `for x in t.N` loop over a tuple ELEMENT that is itself an array now lowers
// on the self-host IR path. The struct-field foreach path (`for c in r.cells`)
// already snapshots an array it doesn't own into a hidden BORROW local (never
// array-marked, so the exit-sweep never decs it) and walks it with explicit
// arr_len / arr_get — the field buffer's lifetime stays with the owning value,
// which leaks it, so the borrow can't double-free. A tuple element `t.N` of array
// type is the same shape: the tuple is leak-only on the IR path (its box + arrays
// are never freed), so its array element is an equally-safe read-only borrow. The
// only difference from the struct-field case is the type SOURCE — the element type
// comes from the tuple (expr_tuple_elem_tag), not a struct field decl — so the
// classification was unified to read from either. Before this, `for x in t.1`
// fell to lower_foreach_snapshot's owning `var $forit = t.1` bind, which can't
// alias a leak-only tuple's array, and the whole module dropped to the legacy AST
// emitter. Found by differential probing; each case is oracle-checked and pinned
// "ir". Scalar-array tuple elements (i32[]/f64[]/i64[]) stay deferred (they need
// the width-typed element read, like the struct-field scalar arrays).
var foreachTupleElemArrayIRCases = []struct {
	name string
	src  string
}{
	// A string-array tuple element, LOCAL tuple: sum the element lengths → 5 + (1
	// + 2 + 3) = 11.
	{"strarr_local", `function main(): i32 {
    var t: (i32, string[]) = (5, ["a", "bb", "ccc"]);
    var s = t.0;
    for x in t.1 { s = s + x.len(); }
    return s;
}`},
	// The SAME shape as a STRUCT FIELD (`r.t.1`): the tuple element access resolves
	// through the struct field's tuple type → 11.
	{"strarr_field", `struct Row { t: (i32, string[]) }
function main(): i32 {
    var r = Row { t: (5, ["a", "bb", "ccc"]) };
    var s = r.t.0;
    for x in r.t.1 { s = s + x.len(); }
    return s;
}`},
	// A STRUCT-array tuple element: `x.a` resolves through the element struct name
	// bound on the loop var → 5 + (1 + 2 + 3) = 11.
	{"structarr", `struct Inner { a: i32 }
function main(): i32 {
    var t: (i32, Inner[]) = (5, [Inner{a:1}, Inner{a:2}, Inner{a:3}]);
    var s = t.0;
    for x in t.1 { s = s + x.a; }
    return s;
}`},
	// An ARRAY-OF-TUPLES tuple element — the loop var is itself a tuple, so its
	// element tags (`p.1`) must resolve too → 5 + (1 + 2) = 8.
	{"arr_of_tuples", `function main(): i32 {
    var t: (i32, (i32, string)[]) = (5, [(1, "a"), (2, "bb")]);
    var s = t.0;
    for p in t.1 { s = s + p.1.len(); }
    return s;
}`},
	// An ENUM-array tuple element matched in the body → 5 + 1 + 100 + 1 = 107.
	{"enumarr_match", `enum Color { Red, Green, Blue }
function main(): i32 {
    var t: (i32, Color[]) = (5, [Color.Red, Color.Blue, Color.Green]);
    var s = t.0;
    for c in t.1 { match (c) { Color.Blue => { s = s + 100; }, _ => { s = s + 1; } } }
    return s;
}`},
	// An Option-array tuple element, payload recovered via match → 5 + 3 + 1 + 7 = 16.
	{"optarr_match", `function main(): i32 {
    var t: (i32, Option[i32][]) = (5, [Some(3), None, Some(7)]);
    var s = t.0;
    for o in t.1 { match (o) { Some(n) => { s = s + n; }, None => { s = s + 1; } } }
    return s;
}`},
	// RC stress: a fresh tuple with a string-array element built + iterated every
	// round — the leak-only borrow must not over-release → (50 * (1+2+3)) % 256 =
	// 300 % 256 = 44.
	{"rc_loop", `function main(): i32 {
    var acc = 0;
    var i = 0;
    while (i < 50) {
        var t: (i32, string[]) = (i, ["a", "bb", "ccc"]);
        var s = 0;
        for x in t.1 { s = s + x.len(); }
        acc = acc + s;
        i = i + 1;
    }
    return acc % 256;
}`},
}

// The x86-64 leg.
func TestSelfHostForeachTupleElemArrayIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "ftea")
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

	for _, tc := range foreachTupleElemArrayIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "ftea_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "ftea_"+tc.name+"_bin", asm)
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

// The wasm leg: the fix lives in shared irlower.fern, so the wasm IR backend walks
// the tuple-element array borrow through the same 4-byte-slot arr_get counted loop.
func TestSelfHostForeachTupleElemArrayWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping foreach-tuple-elem-array wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range foreachTupleElemArrayIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "wftea_"+tc.name+".fern")
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
