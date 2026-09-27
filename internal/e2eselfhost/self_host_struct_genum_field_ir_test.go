package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// structGenEnumFieldIRCase is a self-host program with a (non-generic) struct
// whose FIELD is typed as a user generic-enum instantiation — `struct S { o:
// Opt[i32] }`. The enum monomorphiser rewrote function/param/return types and
// match scrutinees, but never the field types of a passed-through struct, so the
// field kept its reference to the dropped generic `Opt` and the module bailed to
// the AST emitter (which also miscompiled it to 0). The fix collects + rewrites
// struct field types in the enum pass (`o: Opt[i32]` -> `o: Opt__i32`) and
// teaches `me_scrutinee_type` to recover a struct field's type so `match (s.o)`
// rewrites its arms. Native handles these, so this closes a goal-1 IR-subset
// gap. Each exit code is pinned against the native interpreter oracle and kept
// <= 120.
type structGenEnumFieldIRCase struct {
	name     string
	src      string
	expected int
}

var structGenEnumFieldIRCases = []structGenEnumFieldIRCase{
	// the core shape: a struct field holding a user generic enum, matched via
	// `s.o` (field-access scrutinee).
	{"field_match_i32", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function main(): i32 {
    var s = S { o: Sm(5) };
    match (s.o) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 5},
	// string payload, method dispatch on the bound value.
	{"field_match_string", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[string] }
function main(): i32 {
    var s = S { o: Sm("hi") };
    match (s.o) { Sm(n) => { return n.len(); }, Nn => { return 0; } }
}`, 2},
	// the field holds the unit variant.
	{"field_unit", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function main(): i32 {
    var s = S { o: Nn };
    match (s.o) { Sm(n) => { return n; }, Nn => { return 9; } }
}`, 9},
	// two generic-enum-typed fields on one struct.
	{"two_fields", `enum Opt[T] { Sm(T), Nn }
struct S { a: Opt[i32], b: Opt[i32] }
function main(): i32 {
    var s = S { a: Sm(3), b: Sm(4) };
    var x: i32 = 0;
    match (s.a) { Sm(n) => { x = n; }, Nn => { } }
    match (s.b) { Sm(n) => { x = x + n; }, Nn => { } }
    return x;
}`, 7},
	// the struct binding carries an explicit annotation.
	{"annotated_binding", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function main(): i32 {
    var s: S = S { o: Sm(8) };
    match (s.o) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 8},
	// the struct arrives as a function PARAMETER (its type comes from the param
	// env, the field type from the struct table).
	{"struct_param", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function get(s: S): i32 {
    match (s.o) { Sm(n) => { return n; }, Nn => { return 0; } }
}
function main(): i32 { return get(S { o: Sm(6) }); }`, 6},
	// two distinct instantiations as fields of one struct coexist.
	{"mixed_instantiations", `enum Opt[T] { Sm(T), Nn }
struct S { a: Opt[i32], b: Opt[string] }
function main(): i32 {
    var s = S { a: Sm(4), b: Sm("xyz") };
    var x: i32 = 0;
    match (s.a) { Sm(n) => { x = n; }, Nn => { } }
    match (s.b) { Sm(t) => { x = x + t.len(); }, Nn => { } }
    return x;
}`, 7},
}

// TestSelfHostStructGenEnumFieldIRX86_64 runs each case through the self-host
// asm_run driver.
func TestSelfHostStructGenEnumFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range structGenEnumFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "struct_genum_field_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("struct-genum-field %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostStructGenEnumFieldWasmIR is the wasm sibling: the enum pass is a
// target-independent parser pass, so the wasm IR backend gets these for free.
func TestSelfHostStructGenEnumFieldWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range structGenEnumFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
