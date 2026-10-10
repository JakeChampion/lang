package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// boolMatchIRCases exercise `match` on a boolean scrutinee through the IR path.
// The scrutinee is already a 0/1
// value in a slot (no tag to extract), so each arm compares it to the wanted
// value, skips on mismatch, runs the body, and exits the match (mirroring the
// Option-tag arm), rather than bailing.
//
// The original programs retain constant Point data and need no reclaim call.
// The driver is IR-or-error, so successful compilation establishes IR admission.
// Paired runtime programs take their Point fields and match inputs from argv;
// their noinline probe must allocate and reclaim the Point. Exit codes pin the
// matched arms for both representations.
var boolMatchIRCases = []struct {
	name     string
	src      string
	expected int
	runtime  string
}{
	// match on a computed boolean (comparison). classify(8)=1, classify(2)=0 -> 10.
	{"bool-match-computed",
		`struct Point { x: i32, y: i32 } function classify(n: i32): i32 { match (n > 5) { true => { return 1; }, false => { return 0; }, _ => { return 9; } } return 9; } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; return classify(8) * 10 + classify(2) + pad; }`,
		10,
		`struct Point { x: i32, y: i32 } function classify(n: i32): i32 { match (n > 5) { true => { return 1; }, false => { return 0; }, _ => { return 9; } } return 9; } @noinline function probe(n: i32): i32 { let t: Point = Point { x: n, y: n }; let pad: i32 = t.x - t.y; return classify(n + 5) * 10 + classify(n - 1) + pad; } function main(): i32 { return probe(args().len()); }`},
	// match on a boolean parameter. pick(true)=7, pick(false)=3 -> 10.
	{"bool-match-param",
		`struct Point { x: i32, y: i32 } function pick(b: boolean): i32 { match (b) { true => { return 7; }, false => { return 3; }, _ => { return 9; } } return 9; } function main(): i32 { let t: Point = Point { x: 2, y: 2 }; let pad: i32 = t.x - t.y; return pick(true) + pick(false) + pad; }`,
		10,
		`struct Point { x: i32, y: i32 } function pick(b: boolean): i32 { match (b) { true => { return 7; }, false => { return 3; }, _ => { return 9; } } return 9; } @noinline function probe(n: i32): i32 { let t: Point = Point { x: n, y: n }; let pad: i32 = t.x - t.y; return pick(n > 0) + pick(n < 0) + pad; } function main(): i32 { return probe(args().len()); }`},
	// false-arm first (ordering independence). h(false)=4, h(true)=8 -> 12.
	{"bool-match-false-first",
		`struct Point { x: i32, y: i32 } function h(b: boolean): i32 { match (b) { false => { return 4; }, true => { return 8; }, _ => { return 0; } } return 0; } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; return h(false) + h(true) + pad; }`,
		12,
		`struct Point { x: i32, y: i32 } function h(b: boolean): i32 { match (b) { false => { return 4; }, true => { return 8; }, _ => { return 0; } } return 0; } @noinline function probe(n: i32): i32 { let t: Point = Point { x: n, y: n }; let pad: i32 = t.x - t.y; return h(n < 0) + h(n > 0) + pad; } function main(): i32 { return probe(args().len()); }`},
	// guard on a boolean arm (bind-before-guard shape, no binding). g(20)=2, g(5)=1,
	// g(0)=0 -> 2*100 + 1*10 + 0 = 210.
	{"bool-match-guard",
		`struct Point { x: i32, y: i32 } function g(n: i32): i32 { match (n > 0) { true when n > 10 => { return 2; }, true => { return 1; }, false => { return 0; }, _ => { return 9; } } return 9; } function main(): i32 { let t: Point = Point { x: 3, y: 3 }; let pad: i32 = t.x - t.y; return g(20) * 100 + g(5) * 10 + g(0) + pad; }`,
		210,
		`struct Point { x: i32, y: i32 } function g(n: i32): i32 { match (n > 0) { true when n > 10 => { return 2; }, true => { return 1; }, false => { return 0; }, _ => { return 9; } } return 9; } @noinline function probe(n: i32): i32 { let t: Point = Point { x: n, y: n }; let pad: i32 = t.x - t.y; return g(n + 17) * 100 + g(n + 2) * 10 + g(n - 3) + pad; } function main(): i32 { return probe(args().len()); }`},
}

// TestSelfHostBoolMatchIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR-or-error), checks static and runtime Point storage, and
// asserts the matched value.
func TestSelfHostBoolMatchIRX86_64(t *testing.T) {
	boxedProbes(t)
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

	for _, tc := range boolMatchIRCases {
		for _, variant := range []struct {
			name string
			src  string
			heap bool
		}{{"static", tc.src, false}, {"runtime", tc.runtime, true}} {
			t.Run(tc.name+"/"+variant.name, func(t *testing.T) {
				asm := runCapture(t, gcc, runner, driverBin, []byte(variant.src))
				if len(asm) == 0 {
					t.Fatal("self-host compiler emitted 0 bytes")
				}
				if variant.heap {
					// Restrict this check to probe, excluding argv's array cleanup in main.
					body := selfHostFnBody(t, asm, "probe")
					for _, call := range []string{"call __fern_arr_box", "call __fn___fern_arr_dec"} {
						if !strings.Contains(body, call) {
							t.Errorf("probe missing %s:\n%s", call, body)
						}
					}
				} else if !bytes.Contains(asm, []byte(".K0:")) || bytes.Contains(asm, []byte("call __fn___fern_arr_dec")) {
					t.Errorf("constant Point should use static storage without a reclaim call:\n%s", asm)
				}
				progBin := buildBin(t, gcc, dir, tc.name, string(asm))
				var cmd *exec.Cmd
				// args includes the program name, giving probe n=3.
				if len(runner) == 0 {
					cmd = exec.Command(progBin, "a", "b")
				} else {
					cmd = exec.Command(runner[0], append(runner[1:], progBin, "a", "b")...)
				}
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != tc.expected {
					t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
				}
			})
		}
	}
}
