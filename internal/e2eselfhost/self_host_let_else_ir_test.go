package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// letElseIRCases pin `let PAT = EXPR else { divergent };` on the IR path. The
// parser desugars it by folding the rest of the enclosing block into the success
// arm of a statement-match (parser.fern), so it rides the already-proven match IR
// machinery. As with the if-let pin, the existing TestSelfHostLetElse* assert
// only exit codes (which the AST emitter also satisfies); these cases add the
// missing IR-path gate, mirroring self_host_bool_match_ir_test.go.
//
// Each program declares a fresh, non-escaping struct temp whose IR-only reclaim
// free (`call __fn___fern_arr_dec`) proves the module took the IR path. `t.x -
// t.y` pads 0 into every result, so exit codes still pin the matched arm.
var letElseIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"matched", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var s: Shape = Circle(42); let Circle(r) = s else { return 0 + pad; } return r + pad; }", 42},
	{"else-path", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var s: Shape = Empty; let Circle(r) = s else { return 7 + pad; } return r + pad; }", 7},
	{"opt-some", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); let Some(v) = m.get(\"k\") else { return 1 + pad; } return v + pad; }", 42},
	{"opt-none", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); let Some(v) = m.get(\"absent\") else { return 9 + pad; } return v + pad; }", 9},
	{"rest-multi", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 40); let Some(v) = m.get(\"k\") else { return 1 + pad; } var w: i32 = v + 2 + pad; return w; }", 42},
	// The head now goes through the shared parse_pattern rather than a
	// hand-rolled binding list, so `@` and or-patterns work here as they do in
	// a match arm and in `if let`.
	{"at-binding", "enum E { A(i32), B(i32) } struct Point { x: i32, y: i32 } function whole(e: E): i32 { match (e) { A(v) => { return v; }, B(v) => { return 0; } } } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var e: E = A(6); let w @ A(x) = e else { return 1 + pad; } return whole(w) + x + pad; }", 12},
	{"or-first-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var e: E = A(4); let A(x) | B(x) = e else { return 21 + pad; } return x + pad; }", 4},
	{"or-second-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var e: E = B(4); let A(x) | B(x) = e else { return 21 + pad; } return x + pad; }", 4},
	// A variant outside the alternatives still reaches the else, so the
	// wildcard arm survives the per-alternative expansion.
	{"or-no-match-else", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var e: E = C(9); let A(x) | B(x) = e else { return 21 + pad; } return x + pad; }", 21},
}

// TestSelfHostLetElseIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserts the IR path was taken (the reclaimed-
// temp struct free), and asserts the matched value.
func TestSelfHostLetElseIRX86_64(t *testing.T) {
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

	for _, tc := range letElseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if bytes.Count(asm, []byte("call __fn___fern_arr_dec")) == 0 {
				t.Fatalf("%s: no struct free emitted — the let-else IR path was NOT exercised", tc.name)
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
