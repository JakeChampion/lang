package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `?` on an `@try` enum, through the self-host IR path (#9302 part 3).
//
// An Option / Result box is `[tag@0, payload@8]`, so its discriminant is an
// integer compare and its payload an offset-8 read. A marked enum's box is an
// ordinary variant box carrying its shape at offset 0, so the self-host lowers
// the same operator through `variant_is` + `struct_get` — the ops a `match` arm
// already uses. These cases pin that second lowering against the exit codes
// native produces for the identical sources (internal/e2e/try_marker_test.go
// is the native half), on both register backends.
//
// Every case is compiled with FERN_STRICT_IR=1, so a lowering that declines
// rather than miscompiles fails here too: the driver names its bail site
// instead of emitting.
type tryMarkerCase struct {
	name string
	src  string
	want int
}

func tryMarkerCases() []tryMarkerCase {
	return []tryMarkerCase{
		// Build shape: the failure variant carries no payload, so the `?`
		// failure edge constructs a fresh 0-field variant box.
		{"build_ok", `
@try
enum Flag { On(i32), Off }
function pick(f: Flag): Flag { var v: i32 = f?; return On(v + 1); }
function main(): i32 { match (pick(On(7))) { On(v) => { return v; }, Off => { return 0; } } }`, 8},
		{"build_fail", `
@try
enum Flag { On(i32), Off }
function pick(f: Flag): Flag { var v: i32 = f?; return On(v + 1); }
function main(): i32 { match (pick(Off)) { On(v) => { return v; }, Off => { return 5; } } }`, 5},

		// Forward shape: the failure variant carries a payload, so the failure
		// edge forwards the source box — its tag and payload already satisfy
		// the enclosing return, which the checker pinned to the same enum.
		{"forward_ok", `
@try
enum Outcome { Good(i32), Bad(i32) }
function step(o: Outcome): Outcome { var v: i32 = o?; return Good(v * 2); }
function main(): i32 { match (step(Good(6))) { Good(v) => { return v; }, Bad(e) => { return e + 10; } } }`, 12},
		{"forward_fail", `
@try
enum Outcome { Good(i32), Bad(i32) }
function step(o: Outcome): Outcome { var v: i32 = o?; return Good(v * 2); }
function main(): i32 { match (step(Bad(3))) { Good(v) => { return v; }, Bad(e) => { return e + 10; } } }`, 13},

		// A direct CALL scrutinee (`mk(n)?`), not a local — the scrutinee-type
		// resolver has to answer for the call's return type as well as a slot.
		{"call_scrutinee", `
@try
enum Outcome { Good(i32), Bad(i32) }
function mk(n: i32): Outcome { if (n > 0) { return Good(n); } return Bad(9); }
function step(n: i32): Outcome { var v: i32 = mk(n)?; return Good(v * 2); }
function main(): i32 { match (step(4)) { Good(v) => { return v; }, Bad(e) => { return e; } } }`, 8},

		// Payload shapes. Each reads through struct_get at the field's own
		// width, exactly as the matching `match` arm binds it.
		{"payload_string", `
@try
enum Str { Got(string), Missing }
function up(s: Str): Str { var v: string = s?; return Got(v + "!"); }
function main(): i32 { match (up(Got("hi"))) { Got(v) => { return v.len(); }, Missing => { return 0; } } }`, 3},
		{"payload_string_inferred", `
@try
enum Str { Got(string), Missing }
function up(s: Str): Str { var v = s?; return Got(v + "!"); }
function main(): i32 { match (up(Got("hi"))) { Got(v) => { return v.len(); }, Missing => { return 0; } } }`, 3},
		{"payload_i64", `
@try
enum Big { Val(i64), Nope }
function dbl(b: Big): Big { var v: i64 = b?; return Val(v * 2); }
function main(): i32 { match (dbl(Val(21))) { Val(v) => { return (v as i32); }, Nope => { return 1; } } }`, 42},
		{"payload_struct", `
struct P { x: i32, y: i32 }
@try
enum Pt { Here(P), Nowhere }
function sx(p: Pt): Pt { var v: P = p?; return Here(P { x: v.x + 1, y: v.y }); }
function main(): i32 { match (sx(Here(P { x: 4, y: 9 }))) { Here(v) => { return v.x + v.y; }, Nowhere => { return 0; } } }`, 14},
		{"payload_tuple", `
@try
enum Tup { Pair((i32, i32)), NoPair }
function sw(t: Tup): Tup { var v: (i32, i32) = t?; return Pair((v.1, v.0)); }
function main(): i32 { match (sw(Pair((3, 9)))) { Pair(p) => { return p.0 * 10 + p.1; }, NoPair => { return 0; } } }`, 93},

		// UNANNOTATED bindings. `var x = inner?` types its slot from the
		// operand alone, so the width, str-tracking and slot-typing paths each
		// have to resolve a marked enum's payload the same way the lowering
		// does — an i64 read at width 32 is a miscompile, not a bail.
		{"inferred_i64", `
@try
enum Big { Val(i64), Nope }
function dbl(b: Big): Big { var v = b?; return Val(v * 2); }
function main(): i32 { match (dbl(Val(4000000000))) { Val(v) => { if (v == 8000000000) { return 7; } return 1; }, Nope => { return 2; } } }`, 7},
		{"inferred_struct", `
struct P { x: i32, y: i32 }
@try
enum Pt { Here(P), Nowhere }
function sx(p: Pt): Pt { var v = p?; return Here(P { x: v.x + 1, y: v.y }); }
function main(): i32 { match (sx(Here(P { x: 4, y: 9 }))) { Here(v) => { return v.x + v.y; }, Nowhere => { return 0; } } }`, 14},
		{"inferred_tuple", `
@try
enum Tup { Pair((i32, i32)), NoPair }
function sw(t: Tup): Tup { var v = t?; return Pair((v.1, v.0)); }
function main(): i32 { match (sw(Pair((3, 9)))) { Pair(p) => { return p.0 * 10 + p.1; }, NoPair => { return 0; } } }`, 93},

		// An enum that satisfies the `?` shape by ACCIDENT and is never marked.
		// `?` is not what these programs use it for — they `match` on it — and
		// the scrutinee must keep typing as a user enum rather than as
		// something to unwrap.
		{"unmarked_shape_match", `
enum Tree { Leaf(i32), Node(Tree) }
function depth(t: Tree): i32 { match (t) { Leaf(n) => { return n; }, Node(k) => { return depth(k) + 1; } } }
function main(): i32 { return depth(Node(Node(Leaf(5)))); }`, 7},

		// A marked enum on a method receiver, and `?` nested twice in one body.
		{"method_receiver", `
@try
enum Flag { On(i32), Off }
function (f: Flag) bump(): Flag { var v: i32 = f?; return On(v + 1); }
function main(): i32 { var a: Flag = On(4); match (a.bump()) { On(v) => { return v; }, Off => { return 0; } } }`, 5},
		{"nested", `
@try
enum Flag { On(i32), Off }
function inner(f: Flag): Flag { var v: i32 = f?; return On(v + 1); }
function outer(f: Flag): Flag { var v: i32 = inner(f)?; var w: i32 = inner(On(v))?; return On(w); }
function main(): i32 { match (outer(On(1))) { On(v) => { return v; }, Off => { return 99; } } }`, 3},

		// A GENERIC marked enum, instantiated at two payload types in one
		// module. The self-host erases to uniform 8-byte slots, so both
		// instantiations share one lowering and the payload read has to agree
		// with what the `match` arm does for the same erased field.
		{"generic_two_instantiations", `
@try
enum Maybe[T] { Yes(T), No }
function bumpi(m: Maybe[i32]): Maybe[i32] { var v: i32 = m?; return Yes(v + 1); }
function bumps(m: Maybe[string]): Maybe[string] { var v: string = m?; return Yes(v + "!"); }
function ai(): i32 { match (bumpi(Yes(7))) { Yes(v) => { return v; }, No => { return 0; } } }
function bi(): i32 { match (bumpi(No)) { Yes(v) => { return v; }, No => { return 50; } } }
function cs(): i32 { match (bumps(Yes("ab"))) { Yes(v) => { return v.len(); }, No => { return 0; } } }
function ds(): i32 { match (bumps(No)) { Yes(v) => { return v.len(); }, No => { return 9; } } }
function main(): i32 { return ai() + bi() + cs() + ds(); }`, 70},

		// Option and Result keep their own lowering, so the marked-enum arm
		// must not have disturbed it.
		{"option_result_unchanged", `
function f(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; }
function g(n: i32): Option[i32] { var v: i32 = f(n)?; return Some(v * 3); }
function h(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(7); }
function k(n: i32): Result[i32, i32] { var v: i32 = h(n)?; return Ok(v + 1); }
function ga(): i32 { match (g(2)) { Some(v) => { return v; }, None => { return 0; } } }
function gb(): i32 { match (g(0 - 1)) { Some(v) => { return v; }, None => { return 100; } } }
function kc(): i32 { match (k(5)) { Ok(v) => { return v; }, Err(e) => { return e; } } }
function kd(): i32 { match (k(0)) { Ok(v) => { return v; }, Err(e) => { return e; } } }
function main(): i32 { return ga() + gb() + kc() + kd(); }`, 119},
	}
}

func TestSelfHostTryMarkerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tryMarkerCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "trymarker_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTryMarkerArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tryMarkerCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "trymarker_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// The `?` failure edge on a marked enum is an early return, so it replays the
// defer pass's cleanup markers exactly as an explicit `return` does: every
// registered plain defer, then the errdefers, and only on the failure path.
// Both shapes of the operator share that code with Option / Result, so what
// this pins is that the marked-enum arm reaches it at all.
const tryMarkerDeferSrc = `
@try
enum Flag { On(i32), Off }
function step(f: Flag): Flag {
    defer { print("d"); }
    errdefer { print("e"); }
    var v: i32 = f?;
    return On(v);
}
function main(): i32 {
    match (step(Off)) { On(v) => {}, Off => {} }
    print("|");
    match (step(On(2))) { On(v) => {}, Off => {} }
    return 0;
}`

func TestSelfHostTryMarkerDeferX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tryMarkerDeferSrc))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "trymarker_defer", string(asm))
	cmd := runX86_64Bin(runner, bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Failure path: the defer fires, then the errdefer. Success path: the
	// defer alone.
	const want = "d\ne\n|\nd\n"
	if got := string(out); got != want {
		t.Errorf("stdout %q, want %q — an errdefer that fired on the success "+
			"path (or a defer that did not fire on the failure path) means the "+
			"`?` failure edge is not replaying the cleanup markers",
			strings.ReplaceAll(got, "\n", "\\n"), strings.ReplaceAll(want, "\n", "\\n"))
	}
}

// The wasm backend reads a variant box's discriminant as a struct type id
// rather than an interned shape pointer, and its struct_get offsets differ
// from the register backends', so the marked-enum `?` needs its own end-to-end
// run there rather than inheriting the x86-64 verdict.
func TestSelfHostTryMarkerWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm @try `?` e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range tryMarkerCases() {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatalf("%s: self-host wasm emitter produced 0 bytes", tc.name)
			}
			watPath := filepath.Join(dir, "trymarker_"+tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
