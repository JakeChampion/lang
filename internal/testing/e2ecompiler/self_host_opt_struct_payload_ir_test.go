package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var optStructPayloadIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"option-some-struct",
		`struct Point { x: i32, y: i32 } function pick(n: i32): Option[Point] { if (n > 0) { return Some(Point { x: 3, y: 4 }); } return None; } function useit(n: i32): i32 { match (pick(n)) { Some(p) => { return p.x + p.y; }, None => { return 99; } } return 0; } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; return useit(1) + pad; }`,
		7},
	{"option-none-with-struct-payload",
		`struct Point { x: i32, y: i32 } function pick(n: i32): Option[Point] { if (n > 0) { return Some(Point { x: 3, y: 4 }); } return None; } function useit(n: i32): i32 { match (pick(n)) { Some(p) => { return p.x + p.y; }, None => { return 99; } } return 0; } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; return useit(0) + pad; }`,
		99},
	{"result-ok-struct",
		`struct Pair { a: i32, b: i32 } function get(n: i32): Result[Pair, i32] { if (n > 0) { return Ok(Pair { a: 10, b: 5 }); } return Err(7); } function useit(n: i32): i32 { match (get(n)) { Ok(p) => { return p.a + p.b; }, Err(e) => { return e; } } return 0; } function main(): i32 { let t: Pair = Pair { a: 1, b: 1 }; let pad: i32 = t.a - t.b; return useit(1) + pad; }`,
		15},
	{"result-err-struct",
		`struct Efield { code: i32 } function run(n: i32): Result[i32, Efield] { if (n > 0) { return Ok(n); } return Err(Efield { code: 42 }); } function useit(n: i32): i32 { match (run(n)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } return 0; } function main(): i32 { let t: Efield = Efield { code: 0 }; let pad: i32 = t.code; return useit(0) + pad; }`,
		42},
	{"option-struct-payload-guard",
		`struct Point { x: i32, y: i32 } function pick(n: i32): Option[Point] { if (n > 0) { return Some(Point { x: n, y: 0 }); } return None; } function useit(n: i32): i32 { match (pick(n)) { Some(p) when p.x > 3 => { return p.x; }, Some(p) => { return 1; }, None => { return 2; } } return 0; } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; return useit(5) + pad; }`,
		5},
}

func TestSelfHostOptStructPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range optStructPayloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
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
