package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// selfHostProgCases are full self-contained programs compiled through
// the self-hosted compiler. They cover two pure-collection / self-host
// capabilities:
//
//   - the value-returning map API (docs/PURE-COLLECTION-API-PLAN.md
//     §3a): `insert` / `without`. The mutable-looking spellings
//     (`set` / `delete` / `clear`) were removed; these are the only
//     names the front-end + backends now recognise.
//   - tuple-destructure type inference: `var (a, b) = recv.method()`
//     now types each binding from the method's tuple return, so a
//     destructured struct/map element dispatches its own methods/fields
//     instead of mis-mangling as `__fn_i32__<m>`. Previously only
//     free-function tuple returns were typed; method-call returns fell
//     back to ("i32","i32").
//
// Exit codes cross-checked against the Go backend semantics.
var selfHostProgCases = []struct {
	name string
	src  string
	exit int
}{
	// Array.with — value-returning element set (replaces arr[i]=v).
	// [1,2,3] with(1,20) → [1,20,3]; 1+20+3 = 24.
	{"array-with", `function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }`, 24},
	// Cell[i32] — single-slot mutable box. (0+5)*2 = 10.
	{"cell-get-set", `function main(): i32 { var c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }`, 10},
	// Cell shared mutation through a function param: 10 bumped 3× = 13.
	{"cell-shared", `function bump(c: Cell[i32]) { c.set(c.get() + 1); } function main(): i32 { var c: Cell[i32] = cell_new(10); bump(c); bump(c); bump(c); return c.get(); }`, 13},
	// Cell[string] — single-pointer string slot (self-host strings are
	// single-pointer, heap is leak-everything), so it reuses the i32 cell
	// machinery. "A" then overwritten to "Z"; first byte 'Z' = 90.
	{"cell-string", `function main(): i32 { var c: Cell[string] = cell_new("A"); c.set("Z"); return c.get()[0]; }`, 90},
	// Cell[string] stored in a STRUCT FIELD — the lamdefs/Ctx shape. "ab"
	// overwritten to "xyz" through the field; len 3.
	{"cell-string-field", `struct Box { c: Cell[string] } function main(): i32 { var b: Box = Box { c: cell_new("ab") }; b.c.set("xyz"); return b.c.get().len(); }`, 3},
	// Cell[string] field mutated through a function PARAM (shared mutation) —
	// exactly how lam_ctr/lamdefs thread through the lambda emitter. "hi" →
	// "hi!" → "hi!!", len 4.
	{"cell-string-shared", `struct Box { c: Cell[string] } function bump(b: Box) { b.c.set(b.c.get() + "!"); } function main(): i32 { var b: Box = Box { c: cell_new("hi") }; bump(b); bump(b); return b.c.get().len(); }`, 4},
	// Cell[i64] — 8-byte element, exercises lower_i64's cell-get case + the
	// width-64 store: 5e9 + 1e9 = 6e9, /1e9 = 6 (#5510).
	{"cell-i64", `function main(): i32 { var c: Cell[i64] = cell_new(5000000000i64); c.set(c.get() + 1000000000i64); return (c.get() / 1000000000i64) as i32; }`, 6},
	// Cell[f64] — 8-byte float element, arr_set(64) store: 3.5 + 2.5 = 6.0.
	{"cell-f64", `function main(): i32 { var c: Cell[f64] = cell_new(3.5); c.set(c.get() + 2.5); return c.get() as i32; }`, 6},
	// Array.build (parser.fern desugar): for-in builds [1,4,9]; sum 14.
	{"array-build", `function main(): i32 { var xs: i32[] = [1,2,3]; var out: i32[] = Array.build(function(b: ArrayBuilder[i32]): void { for x in xs { b.append(x * x); } }); return out[0] + out[1] + out[2]; }`, 14},
	// Repeated with: [0,0,0] → with(0,5) → with(2,7) → [5,0,7]; 5*10+7 = 57.
	{"array-with-chain", `function main(): i32 { var a: i32[] = [0, 0, 0]; a = a.with(0, 5); a = a.with(2, 7); return a[0] * 10 + a[2]; }`, 57},
	// Map.insert (value-returning) with overwrite: {1:99, 2:20}.
	{"map-insert", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); m = m.insert(1,99); return m.get_or(1,-1) + m.get_or(2,-1); }`, 119},
	{"map-insert-fresh", `function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(5,7); return m.get_or(5,0); }`, 7},

	// Tuple-destructure inference from a user method returning a tuple:
	// `q` must be typed Pair so `q.hi`/`q.lo` resolve (without the fix
	// `q` defaults to i32 and the struct-field access mis-compiles).
	// (3 + 7) + 7 = 17.
	{"destructure-method-tuple", `struct Pair { hi: i32, lo: i32 }
function (p: Pair) swapped(): (Pair, i32) { return (Pair { hi: p.lo, lo: p.hi }, p.hi); }
function main(): i32 { var p: Pair = Pair { hi: 7, lo: 3 }; var (q, old) = p.swapped(); return q.hi + q.lo + old; }`, 17},

	// Map.without returns (map, existed); destructured and
	// re-queried, exercising both the runtime helper and the
	// tuple-destructure inference. m={1:10,2:20}; without(1) → existed,
	// m2={2:20}; m2.get_or(2,-1)=20.
	{"map-delete", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); var (m2, ex) = m.without(1); if (!ex) { return 70; } return m2.get_or(2,-1); }`, 20},
	// without: {1:10}; existed; m2.get_or(1,-1)=10 and the
	// removed key reads the default (-1+1=0): total 10.
	{"map-without", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); var (m2, ex) = m.without(2); if (!ex) { return 70; } return m2.get_or(1,-1) + (m2.get_or(2,-1) + 1); }`, 10},
	// Deleting an absent key reports existed=false; the map is unchanged.
	{"map-without-absent", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); var (m2, ex) = m.without(9); if (ex) { return 1; } return m2.get_or(1,-1); }`, 10},
	// String-keyed without: shift over the string-compare search path.
	{"smap-delete", `function main(): i32 { var m: Map[string,i32] = map_new(8); m = m.insert("a",10); m = m.insert("b",20); var (m2, ex) = m.without("a"); if (!ex) { return 70; } return m2.get_or("b",-1) + (m2.get_or("a",-1) + 1); }`, 20},
}

// TestSelfHostProgsX86_64 compiles each program with the self-hosted
// x86-64 compiler and checks the exit code.
func TestSelfHostProgsX86_64(t *testing.T) {
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

	for _, tc := range selfHostProgCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostProgsArm64 — CI-gated arm64 counterpart.
func TestSelfHostProgsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostProgCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
