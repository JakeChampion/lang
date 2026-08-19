package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optStructPayloadIRCases exercise `match` on an Option/Result whose payload is a
// leaf-safe (scalar-field) struct — `Some(p)` / `Ok(p)` / `Err(p)` binding `p` as
// a struct so `p.field` resolves. The payload is stored by pointer at offset 8 of
// the enum box (identical to the string case), so it reads via op_opt_payload and
// the bound slot is struct-typed; it is BORROWED from the box (never a fresh
// `var p = P{...}`), so it is correctly left to leak (no double-free).
//
// The match lives in a helper whose scrutinee is a call to an Option/Result-
// returning function (the opt-type is recovered via opt_ret_fns). main also
// declares a fresh, non-escaping struct temp `t` (field-read only) which the IR
// path reclaims with a shallow box free; `t.<a> - t.<b>` pads 0 into the result.
// A module that bails emits nothing at all, and no case uses an
// array — so the presence of `call __fn___fern_arr_dec` proves the module took
// the IR path with the struct-payload lowering exercised, rather than
// some narrower route. Exit codes pin the matched value.
var optStructPayloadIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Some(struct): bind p, read two fields. p.x+p.y = 7; pad = t.x-t.y = 0.
	{"option-some-struct",
		`struct Point { x: i32, y: i32 } function pick(n: i32): Option[Point] { if (n > 0) { return Some(Point { x: 3, y: 4 }); } return None; } function useit(n: i32): i32 { match (pick(n)) { Some(p) => { return p.x + p.y; }, None => { return 99; } } return 0; } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return useit(1) + pad; }`,
		7},
	// None arm of a struct-payload Option: payload type still resolves, None taken -> 99.
	{"option-none-with-struct-payload",
		`struct Point { x: i32, y: i32 } function pick(n: i32): Option[Point] { if (n > 0) { return Some(Point { x: 3, y: 4 }); } return None; } function useit(n: i32): i32 { match (pick(n)) { Some(p) => { return p.x + p.y; }, None => { return 99; } } return 0; } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return useit(0) + pad; }`,
		99},
	// Ok(struct): p.a+p.b = 15.
	{"result-ok-struct",
		`struct Pair { a: i32, b: i32 } function get(n: i32): Result[Pair, i32] { if (n > 0) { return Ok(Pair { a: 10, b: 5 }); } return Err(7); } function useit(n: i32): i32 { match (get(n)) { Ok(p) => { return p.a + p.b; }, Err(e) => { return e; } } return 0; } function main(): i32 { var t: Pair = Pair { a: 1, b: 1 }; var pad: i32 = t.a - t.b; return useit(1) + pad; }`,
		15},
	// Err(struct): the error payload is a struct; read its field -> 42.
	{"result-err-struct",
		`struct Efield { code: i32 } function run(n: i32): Result[i32, Efield] { if (n > 0) { return Ok(n); } return Err(Efield { code: 42 }); } function useit(n: i32): i32 { match (run(n)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } return 0; } function main(): i32 { var t: Efield = Efield { code: 0 }; var pad: i32 = t.code; return useit(0) + pad; }`,
		42},
	// Guard referencing a struct-payload field (bind-before-guard): p.x > 3 holds
	// for pick(5) -> Some(Point{5,0}); first arm returns p.x = 5.
	{"option-struct-payload-guard",
		`struct Point { x: i32, y: i32 } function pick(n: i32): Option[Point] { if (n > 0) { return Some(Point { x: n, y: 0 }); } return None; } function useit(n: i32): i32 { match (pick(n)) { Some(p) when p.x > 3 => { return p.x; }, Some(p) => { return 1; }, None => { return 2; } } return 0; } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return useit(5) + pad; }`,
		5},
}

// TestSelfHostOptStructPayloadIRX86_64 compiles each case through the self-hosted
// x86-64 driver (asm_run, IR default-on), asserts the IR path was taken (the
// reclaimed-temp struct free), and asserts the matched value.
func TestSelfHostOptStructPayloadIRX86_64(t *testing.T) {
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

	for _, tc := range optStructPayloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// No array appears, so a
			// struct free proves the IR path lowered this module (incl. the
			// Option/Result struct-payload match).
			if bytes.Count(asm, []byte("call __fn___fern_arr_dec")) == 0 {
				t.Fatalf("%s: no struct free emitted — the Option/Result struct-payload IR path was NOT exercised", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
