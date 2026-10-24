package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type tryMarkerCase struct {
	name string
	src  string
	want int
}

func tryMarkerCases() []tryMarkerCase {
	return []tryMarkerCase{
		{"build_ok", `
@try
enum Flag { On(i32), Off }
function pick(f: Flag): Flag { let v: i32 = f?; return On(v + 1); }
function main(): i32 { match (pick(On(7))) { On(v) => { return v; }, Off => { return 0; } } }`, 8},
		{"build_fail", `
@try
enum Flag { On(i32), Off }
function pick(f: Flag): Flag { let v: i32 = f?; return On(v + 1); }
function main(): i32 { match (pick(Off)) { On(v) => { return v; }, Off => { return 5; } } }`, 5},

		{"forward_ok", `
@try
enum Outcome { Good(i32), Bad(i32) }
function step(o: Outcome): Outcome { let v: i32 = o?; return Good(v * 2); }
function main(): i32 { match (step(Good(6))) { Good(v) => { return v; }, Bad(e) => { return e + 10; } } }`, 12},
		{"forward_fail", `
@try
enum Outcome { Good(i32), Bad(i32) }
function step(o: Outcome): Outcome { let v: i32 = o?; return Good(v * 2); }
function main(): i32 { match (step(Bad(3))) { Good(v) => { return v; }, Bad(e) => { return e + 10; } } }`, 13},

		{"call_scrutinee", `
@try
enum Outcome { Good(i32), Bad(i32) }
function mk(n: i32): Outcome { if (n > 0) { return Good(n); } return Bad(9); }
function step(n: i32): Outcome { let v: i32 = mk(n)?; return Good(v * 2); }
function main(): i32 { match (step(4)) { Good(v) => { return v; }, Bad(e) => { return e; } } }`, 8},

		{"payload_string", `
@try
enum Str { Got(string), Missing }
function up(s: Str): Str { let v: string = s?; return Got(v + "!"); }
function main(): i32 { match (up(Got("hi"))) { Got(v) => { return v.len(); }, Missing => { return 0; } } }`, 3},
		{"payload_string_inferred", `
@try
enum Str { Got(string), Missing }
function up(s: Str): Str { let v = s?; return Got(v + "!"); }
function main(): i32 { match (up(Got("hi"))) { Got(v) => { return v.len(); }, Missing => { return 0; } } }`, 3},
		{"payload_i64", `
@try
enum Big { Val(i64), Nope }
function dbl(b: Big): Big { let v: i64 = b?; return Val(v * 2); }
function main(): i32 { match (dbl(Val(21))) { Val(v) => { return (v as i32); }, Nope => { return 1; } } }`, 42},
		{"payload_struct", `
struct P { x: i32, y: i32 }
@try
enum Pt { Here(P), Nowhere }
function sx(p: Pt): Pt { let v: P = p?; return Here(P { x: v.x + 1, y: v.y }); }
function main(): i32 { match (sx(Here(P { x: 4, y: 9 }))) { Here(v) => { return v.x + v.y; }, Nowhere => { return 0; } } }`, 14},
		{"payload_tuple", `
@try
enum Tup { Pair((i32, i32)), NoPair }
function sw(t: Tup): Tup { let v: (i32, i32) = t?; return Pair((v.1, v.0)); }
function main(): i32 { match (sw(Pair((3, 9)))) { Pair(p) => { return p.0 * 10 + p.1; }, NoPair => { return 0; } } }`, 93},

		{"inferred_i64", `
@try
enum Big { Val(i64), Nope }
function dbl(b: Big): Big { let v = b?; return Val(v * 2); }
function main(): i32 { match (dbl(Val(4000000000))) { Val(v) => { if (v == 8000000000) { return 7; } return 1; }, Nope => { return 2; } } }`, 7},
		{"inferred_struct", `
struct P { x: i32, y: i32 }
@try
enum Pt { Here(P), Nowhere }
function sx(p: Pt): Pt { let v = p?; return Here(P { x: v.x + 1, y: v.y }); }
function main(): i32 { match (sx(Here(P { x: 4, y: 9 }))) { Here(v) => { return v.x + v.y; }, Nowhere => { return 0; } } }`, 14},
		{"inferred_tuple", `
@try
enum Tup { Pair((i32, i32)), NoPair }
function sw(t: Tup): Tup { let v = t?; return Pair((v.1, v.0)); }
function main(): i32 { match (sw(Pair((3, 9)))) { Pair(p) => { return p.0 * 10 + p.1; }, NoPair => { return 0; } } }`, 93},

		{"unmarked_shape_match", `
enum Tree { Leaf(i32), Node(Tree) }
function depth(t: Tree): i32 { match (t) { Leaf(n) => { return n; }, Node(k) => { return depth(k) + 1; } } }
function main(): i32 { return depth(Node(Node(Leaf(5)))); }`, 7},

		{"method_receiver", `
@try
enum Flag { On(i32), Off }
function (f: Flag) bump(): Flag { let v: i32 = f?; return On(v + 1); }
function main(): i32 { let a: Flag = On(4); match (a.bump()) { On(v) => { return v; }, Off => { return 0; } } }`, 5},
		{"nested", `
@try
enum Flag { On(i32), Off }
function inner(f: Flag): Flag { let v: i32 = f?; return On(v + 1); }
function outer(f: Flag): Flag { let v: i32 = inner(f)?; let w: i32 = inner(On(v))?; return On(w); }
function main(): i32 { match (outer(On(1))) { On(v) => { return v; }, Off => { return 99; } } }`, 3},

		{"generic_two_instantiations", `
@try
enum Maybe[T] { Yes(T), No }
function bumpi(m: Maybe[i32]): Maybe[i32] { let v: i32 = m?; return Yes(v + 1); }
function bumps(m: Maybe[string]): Maybe[string] { let v: string = m?; return Yes(v + "!"); }
function ai(): i32 { match (bumpi(Yes(7))) { Yes(v) => { return v; }, No => { return 0; } } }
function bi(): i32 { match (bumpi(No)) { Yes(v) => { return v; }, No => { return 50; } } }
function cs(): i32 { match (bumps(Yes("ab"))) { Yes(v) => { return v.len(); }, No => { return 0; } } }
function ds(): i32 { match (bumps(No)) { Yes(v) => { return v.len(); }, No => { return 9; } } }
function main(): i32 { return ai() + bi() + cs() + ds(); }`, 70},

		{"option_result_unchanged", `
function f(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; }
function g(n: i32): Option[i32] { let v: i32 = f(n)?; return Some(v * 3); }
function h(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(7); }
function k(n: i32): Result[i32, i32] { let v: i32 = h(n)?; return Ok(v + 1); }
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

const tryMarkerDeferSrc = `
@try
enum Flag { On(i32), Off }
function step(f: Flag): Flag {
    defer { print("d"); }
    errdefer { print("e"); }
    let v: i32 = f?;
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	const want = "d\ne\n|\nd\n"
	if got := string(out); got != want {
		t.Errorf("stdout %q, want %q — an errdefer that fired on the success "+
			"path (or a defer that did not fire on the failure path) means the "+
			"`?` failure edge is not replaying the cleanup markers",
			strings.ReplaceAll(got, "\n", "\\n"), strings.ReplaceAll(want, "\n", "\\n"))
	}
}

func TestSelfHostTryMarkerWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm @try `?` e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

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
