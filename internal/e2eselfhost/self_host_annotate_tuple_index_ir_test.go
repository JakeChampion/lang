package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateTupleIndexCases cover a tuple-element read whose tuple comes from an
// INDEX of a call result — `mk()[i].N`.
//
// `expr_tuple_elem_tag`'s ExprIndex arm resolves the element tuple type from the
// slot's recorded `arrarr_elem`, which only exists for a NAMED `(tuple)[]` local.
// When the array is a call result there is no slot to read, it returned "", and
// the tuple-element read lowered as a 4-byte i32 — so an f64 element failed the
// wasm validator ("type mismatch: expected f64, found i32"), exiting 1 against
// an oracle of 33.
//
// The fix needed no new carrier: `ExprIndex.ty` (#6165) already holds the
// checker's type for that exact node, and a tuple stamps its canonical
// "(f64, i32)" spelling, which the existing `tuple_type_elem_tag` decoder reads.
// Walk first, tag fills the hole — the same ordering every other consumer uses.
//
// This is the cheaper half of the migration worth noting: several remaining
// holes are not missing carriers but consumers that never learned to read a
// carrier already in place.
var annotateTupleIndexCases = []struct {
	name string
	src  string
}{
	// The gap: an f64 tuple element behind an index of a call result.
	{"tuple_elem_call_array_f64", `function mk(): (f64, i32)[] { return [(1.5, 7), (2.5, 8)]; }
function main(): i32 { return (mk()[1].0 * 10.0) as i32 + mk()[1].1; }`}, // 33
	// The 8-byte integer sibling — pins that the element WIDTH comes from the
	// tag too, not just the element's existence.
	{"tuple_elem_call_array_i64", `function mk(): (i64, i32)[] { return [(7000000000, 7), (9000000000, 8)]; }
function main(): i32 { return (mk()[1].0 / 1000000000) as i32 + mk()[1].1; }`}, // 17
	// Negative guard: an all-i32 tuple must stay 4-byte. A consumer that widened
	// on any non-empty tag would break this.
	{"tuple_elem_call_array_i32_narrow", `function mk(): (i32, i32)[] { return [(1, 2), (30, 12)]; }
function main(): i32 { return mk()[1].0 + mk()[1].1; }`}, // 42
	// Structural path, unchanged: the same read off a NAMED (tuple)[] local,
	// which the slot walk resolves without ever consulting the tag.
	{"tuple_elem_local_array_f64", `function main(): i32 {
    var a: (f64, i32)[] = [(1.5, 7), (2.5, 8)];
    return (a[1].0 * 10.0) as i32 + a[1].1;
}`}, // 33
}

// TestSelfHostAnnotateTupleIndexIR_X86_64 pins the ExprIndex.ty carrier feeding
// expr_tuple_elem_tag through the self-host x86-64 IR path. asm_load_run.fern is
// the driver because it runs checker.annotate_module; a driver that skips
// annotation leaves every ty empty and cannot regress.
func TestSelfHostAnnotateTupleIndexIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateTupleIndexCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "anntupidx_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
