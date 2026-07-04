package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ifLetIRCases pin `if let PAT = EXPR { then } else { else }` on the IR path.
// The parser desugars `if let` to `match (EXPR) { PAT => { then }, _ => { else } }`
// (parser.fern, s_match_origin origin "if_let"), so it rides the already-proven
// match IR machinery. The existing TestSelfHostIfLet* assert only exit codes,
// which the legacy AST emitter also satisfies — so a silent regression that
// kicked `if let` off the IR path would pass undetected. These cases close that
// observability gap, mirroring self_host_bool_match_ir_test.go.
//
// Each program declares a fresh, non-escaping struct temp whose IR-only reclaim
// free (`call __fn___fern_arr_dec`) proves the module took the IR path — the AST
// fallback is leak-only and emits none. `t.x - t.y` pads 0 into every result, so
// exit codes still pin the matched arm.
var ifLetIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"some", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"k\") { return v + pad; } else { return 1 + pad; } }", 42},
	{"none-else", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"absent\") { return v + pad; } else { return 7 + pad; } }", 7},
	{"no-else-fallthrough", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 5); if let Some(v) = m.get(\"absent\") { return v + pad; } return 9 + pad; }", 9},
	{"user-variant", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var s: Shape = Circle(42); if let Circle(r) = s { return r + pad; } else { return 0 + pad; } }", 42},
}

// TestSelfHostIfLetIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserts the IR path was taken (the reclaimed-
// temp struct free), and asserts the matched value.
func TestSelfHostIfLetIRX86_64(t *testing.T) {
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

	for _, tc := range ifLetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if bytes.Count(asm, []byte("call __fn___fern_arr_dec")) == 0 {
				t.Fatalf("%s: no struct free emitted — module fell back to AST, the if-let IR path was NOT exercised", tc.name)
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
