package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostMatchPayloadCaptureIRX86_64(t *testing.T) {
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

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"option-i32-payload",
			`struct H { f: (i32) => i32, id: i32 } function g(o: Option[i32]): i32 { let r: i32 = 0; match (o) { Some(v) => { let h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; r = h.f(10) + h.id; }, None => { r = 0; } } return r; } function main(): i32 { return g(Some(5)); }`,
			20},
		{"result-ok-payload",
			`struct H { f: (i32) => i32, id: i32 } function g(r: Result[i32, i32]): i32 { let acc: i32 = 0; match (r) { Ok(v) => { let h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; acc = h.f(10) + h.id; }, Err(e) => { acc = e; } } return acc; } function main(): i32 { return g(Ok(6)); }`,
			22},
		{"option-string-payload",
			`struct H { f: (i32) => i32, id: i32 } function g(o: Option[string]): i32 { let r: i32 = 0; match (o) { Some(s) => { let h: H = H { f: (x: i32): i32 => { return x + s.len(); }, id: 2 }; r = h.f(10) + h.id; }, None => { r = 0; } } return r; } function main(): i32 { return g(Some("abc")); }`,
			15},
		{"user-enum-single-payload",
			`enum E { One(i32), Two(string) } struct H { f: (i32) => i32, id: i32 } function g(e: E): i32 { let r: i32 = 0; match (e) { One(v) => { let h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; r = h.f(10) + h.id; }, Two(s) => { let h: H = H { f: (x: i32): i32 => { return x + s.len(); }, id: 2 }; r = h.f(10) + h.id; } } return r; } function main(): i32 { return g(One(5)) + g(Two("abcd")); }`,
			36},
		{"user-enum-multi-payload",
			`enum E { Tag(i32, i32), Empty } struct H { f: (i32) => i32, id: i32 } function g(e: E): i32 { let r: i32 = 0; match (e) { Tag(a, b) => { let h: H = H { f: (x: i32): i32 => { return x + a + b; }, id: a }; r = h.f(1) + h.id; }, Empty => { r = 0; } } return r; } function main(): i32 { return g(Tag(3, 4)); }`,
			11},
		{"user-enum-three-payload",
			`enum E { P(i32, i32, i32), Q } struct H { f: (i32) => i32, id: i32 } function g(e: E): i32 { let r: i32 = 0; match (e) { P(a, b, c) => { let h: H = H { f: (x: i32): i32 => { return x + a + b + c; }, id: b }; r = h.f(1) + h.id; }, Q => { r = 0; } } return r; } function main(): i32 { return g(P(3, 4, 5)); }`,
			17},
		{"tuple-destructure-i32",
			`struct H { f: (i32) => i32, id: i32 } function g(): i32 { let t: (i32, i32) = (3, 4); let (a, b) = t; let h: H = H { f: (x: i32): i32 => { return x + a + b; }, id: a }; return h.f(1) + h.id; } function main(): i32 { return g(); }`,
			11},
		{"tuple-destructure-string",
			`struct H { f: (i32) => i32, id: i32 } function g(): i32 { let t: (string, i32) = ("abc", 5); let (s, n) = t; let h: H = H { f: (x: i32): i32 => { return x + s.len() + n; }, id: n }; return h.f(1) + h.id; } function main(): i32 { return g(); }`,
			14},
		{"tuple-destructure-three",
			`struct H { f: (i32) => i32, id: i32 } function g(): i32 { let t: (i32, i32, i32) = (2, 3, 4); let (a, b, c) = t; let h: H = H { f: (x: i32): i32 => { return x + a + b + c; }, id: b }; return h.f(1) + h.id; } function main(): i32 { return g(); }`,
			13},
		{"call-scrutinee-option-free-fn",
			`struct H { f: (i32) => i32, id: i32 } function pop(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; } function g(i: i32): i32 { let r: i32 = 0; match (pop(i)) { Some(v) => { let h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; r = h.f(10) + h.id; }, None => { r = 0; } } return r; } function main(): i32 { return g(5); }`,
			20},
		{"call-scrutinee-result-method",
			`struct B { v: i32 } function (b: B) take(): Result[i32, i32] { if (b.v > 0) { return Ok(b.v); } return Err(0 - 1); } struct H { f: (i32) => i32, id: i32 } function g(b: B): i32 { let acc: i32 = 0; match (b.take()) { Ok(v) => { let h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; acc = h.f(10) + h.id; }, Err(e) => { acc = e; } } return acc; } function main(): i32 { return g(B { v: 6 }); }`,
			22},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the payload capture did not lower through the IR", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
