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
	// Float/int type-classification edge cases (#5520) — each exercises a
	// distinct arm of the expr_is_f64 / expr_is_f32 predicates that had NO
	// dedicated coverage, and is the fast safety net for the typed-IR numeric
	// merge (a mis-classified arm changes the emitted float ops -> wrong exit).
	{"float-const-accessor", `const HALF: f64 = 2.5; function main(): i32 { return (HALF + 1.5) as i32; }`, 4},
	{"f32-cast-arith", `function main(): i32 { var x: i32 = 7; var y: f32 = x as f32; return (y * 2.0) as i32; }`, 14},
	{"float-struct-field", `struct P { x: f64 } function main(): i32 { var p: P = P { x: 3.5 }; return (p.x + 0.5) as i32; }`, 4},
	{"f32-struct-field", `struct Q { v: f32 } function main(): i32 { var q: Q = Q { v: 4.5 }; return (q.v + 0.5) as i32; }`, 5},
	{"float-tuple-elem", `function main(): i32 { var t: (f64, i32) = (3.5, 1); return (t.0 + 0.5) as i32; }`, 4},
	{"float-array-index", `function main(): i32 { var a: f64[] = [1.5, 2.5]; return (a[1] + 0.5) as i32; }`, 3},
	// Unsigned-64 classification edge cases (#5520) — the u64 siblings of the
	// float cases above, guarding the field/tuple resolver (#5523) and the
	// method-receiver key resolver (#5524) on the unsigned-64 side (the corpus had
	// zero u64 coverage). Each shifts a u64 with bit 63 set by 60: unsigned shr_u
	// yields 15, a mis-classified SIGNED shr_s yields -1 -> exit 255, so the arm's
	// is_u64 verdict is what the exit code turns on (the -1>>60 control confirms the
	// signed reading is 255). These run on both the legacy-asm x86 leg and the
	// IR-path arm64 leg (asm_ir_run -> irlower), so they guard the refactored
	// predicates directly. (u32 is deliberately NOT covered: x86/arm zero-extension
	// hides its signedness divergence, so a u32 shift test would pass regardless of
	// classification — only wasm exposes it. The u64-IIFE arm is likewise omitted:
	// the legacy asm x86 backend does not classify an IIFE result u64 — a gap that
	// per policy is not fixed, the backend being retired — so it would fail this
	// leg while passing the IR one.)
	{"u64-struct-field", `struct S { v: u64 } function main(): i32 { var s: S = S { v: 18446744073709551615 as u64 }; return (s.v >> 60) as i32; }`, 15},
	{"u64-tuple-elem", `function main(): i32 { var t: (u64, i32) = (18446744073709551615 as u64, 0); return (t.0 >> 60) as i32; }`, 15},
	{"u64-method-ret", `struct S {} function (s: S) w(): u64 { return 18446744073709551615 as u64; } function main(): i32 { var s: S = S {}; return (s.w() >> 60) as i32; }`, 15},
	// A DIRECT closure-call Option scrutinee `match (f(x))` — the find_map-shaped
	// combinator that bailed the whole function to AST before closure_opt_rets let
	// lower_stmt_match recover the closure param's Option return type (#3457 IR-gap).
	// pick(2)=Some(20), so apply(pick) matches Some(v)=20.
	{"closure-opt-match", `function pick(x: i32): Option[i32] { if (x == 2) { return Some(x * 10); } return None; } function apply(f: (i32) => Option[i32]): i32 { match (f(2)) { Some(v) => { return v; }, None => { return 0; } } } function main(): i32 { return apply(pick); }`, 20},
	// A direct `match (recv.method(...))` on a NUMERIC-primitive receiver whose
	// method returns Option — the i64 sibling of closure-opt-match. Bailed before
	// the resolver keyed "<prim>.<method>" for non-struct/enum/string receivers
	// (#3457 IR-gap: std/i64.checked_add et al.). 3.tryadd(4)=Some(7).
	{"prim-recv-opt-match", `function (n: i64) tryadd(x: i64): Option[i64] { if (x > 0 as i64) { return Some(n + x); } return None; } function main(): i32 { var a: i64 = 3 as i64; match (a.tryadd(4 as i64)) { Some(v) => { return v as i32; }, None => { return 0; } } }`, 7},
	// A direct `match (r.caps.get(k))` where `caps` is a Map-typed struct FIELD —
	// the map.get resolver only handled a map IDENT receiver, so a struct-field map
	// bailed the function to AST (#3457 IR-gap: std/peg's PegResult.caps and any
	// map-in-struct). expr_map_type_tag now recovers the field's Map[K,V].
	{"map-field-get-opt-match", `struct R { caps: Map[string, i32] } function mk(): R { var m: Map[string, i32] = map_new(4); m = m.insert("a", 7); return R { caps: m }; } function main(): i32 { var r: R = mk(); match (r.caps.get("a")) { Some(v) => { return v; }, None => { return 0; } } }`, 7},
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
