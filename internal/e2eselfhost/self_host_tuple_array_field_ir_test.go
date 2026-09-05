package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tupleArrayFieldCases cover reading a tuple ELEMENT out of a `(tuple)[]`
// struct field — `p.ts[0].1` (#8459).
//
// The self-host checker resolves a struct field's declared type through
// `type_from_name_with_names_and_unions`, and that resolver — alone among the
// five siblings — had no tuple branch at all. A `ts: (i32, f64)[]` field
// therefore resolved to `unknown[]`, the element type never reached
// `expr_tuple_elem_tag`, and the read fell back to a default 4 bytes: an f64
// element came back as its bit pattern (4616752568008180000) on all three
// self-host backends.
//
// The parser was never at fault, which the issue's diagnosis assumed: `-fmt`
// reconstructs `(i32, f64)[]` exactly. It is the same class as the `char` note
// sitting in that very function — "every resolver must know it", one type
// constructor over — and an `unknown` there is silent and widens into every
// consumer downstream.
//
// The `local` and `call` rows were already correct, and the `bind` row was
// correct because a `var f: f64 = …` annotation supplies the width the field
// type could not. They are kept as controls.
var tupleArrayFieldCases = []struct {
	name string
	src  string
}{
	// The gap: an f64 element behind a (tuple)[] FIELD.
	{"tuple_array_field_f64", `struct P { ts: (i32, f64)[] }
function main(): i32 {
    var p: P = P { ts: [(2, 4.5)] };
    return p.ts[0].0 + (p.ts[0].1 * 10.0) as i32;
}`},
	// The same field reached through a call result.
	{"tuple_array_callfield_f64", `struct P { ts: (i32, f64)[] }
function getp(): P { return P { ts: [(2, 4.5)] }; }
function main(): i32 {
    return getp().ts[0].0 + (getp().ts[0].1 * 10.0) as i32;
}`},
	// The 8-byte integer sibling — pins that the WIDTH comes from the
	// resolved element type, not just that an element exists.
	{"tuple_array_field_i64", `struct P { ts: (i32, i64)[] }
function main(): i32 {
    var p: P = P { ts: [(7, 5000000000)] };
    return p.ts[0].0 + (p.ts[0].1 / 1000000000) as i32;
}`},
	// A nested tuple element inside the array element.
	{"tuple_array_field_nested", `struct P { ts: (i32, (f64, i32))[] }
function main(): i32 {
    var p: P = P { ts: [(2, (4.5, 3))] };
    return p.ts[0].0 + (p.ts[0].1.0 * 10.0) as i32 + p.ts[0].1.1;
}`},
	// Negative guard: an all-i32 tuple field must stay 4-byte.
	{"tuple_array_field_i32_narrow", `struct P { ts: (i32, i32)[] }
function main(): i32 {
    var p: P = P { ts: [(30, 12)] };
    return p.ts[0].0 + p.ts[0].1;
}`},
	// Controls that were already correct.
	{"tuple_array_local_f64", `function mk(): (i32, f64)[] { return [(2, 4.5)]; }
function main(): i32 {
    var ps: (i32, f64)[] = mk();
    return ps[0].0 + (ps[0].1 * 10.0) as i32;
}`},
	{"tuple_array_annotated_bind_f64", `struct P { ts: (i32, f64)[] }
function main(): i32 {
    var p: P = P { ts: [(2, 4.5)] };
    var f: f64 = p.ts[0].1;
    return p.ts[0].0 + (f * 10.0) as i32;
}`},
}

// TestSelfHostTupleArrayFieldIR_X86_64 runs each case through the self-host
// x86-64 IR path and compares against the interpreter.
func TestSelfHostTupleArrayFieldIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range tupleArrayFieldCases {
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
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "tuparrfield_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
