package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// closurePtrCaptureIRCases exercise a nested fn (a var-bound lambda used as a
// value — returned as an enum payload) that captures a POINTER-shaped value:
// a string, a struct, an array, or a mix of i32 + string.
//
// Before this slice, make_clo_func declined any capture whose type was not i32
// (`cap_type(..) != "i32"`) and hardcoded each capture-read var as i32, so such
// a closure bailed the module. In the self-host's
// `-no-pie -static` binary every heap / code address fits in 32 bits, so a
// pointer-shaped capture rides the SAME 32-bit env-box slot as an i32 (exactly
// like a `string[]` element, which also uses the 32-bit arr_get path). The fix:
// cap_slot_ok admits any capture except an 8-byte scalar (i64 / u64 / f64), and
// the capture-read var is declared with its REAL type so a captured string's
// `.len()` (etc.) dispatches correctly. The env box stays an i32[]; only the
// downstream slot type changes.
var closurePtrCaptureIRCases = []struct {
	name string
	src  string
}{
	// A captured string; resume reads acc.len() -> 37 + 5.
	{"string_capture", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, acc: string): Fut {
    function resume(woken: i32): Fut { return Rdy(woken + acc.len()); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0, "hello");
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { var r: Fut = k(37); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
	// A captured struct; resume reads p.x + p.y -> 1 + 30 + 11.
	{"struct_capture", `struct P { x: i32, y: i32 }
enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, p: P): Fut {
    function resume(w: i32): Fut { return Rdy(w + p.x + p.y); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0, P { x: 30, y: 11 });
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { var r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
	// A captured i32 array; resume reads xs.len() + xs[0] -> 0 + 3 + 40.
	{"array_capture", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, xs: i32[]): Fut {
    function resume(w: i32): Fut { return Rdy(w + xs.len() + xs[0]); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0, [40, 7, 8]);
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { var r: Fut = k(0); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
	// A MIX of an i32 and a string capture in the same closure -> 17 + 20 + 5.
	{"mixed_i32_string", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, n: i32, s: string): Fut {
    function resume(w: i32): Fut { return Rdy(w + n + s.len()); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0, 20, "abcde");
    match (f) { Rdy(v) => { return v; }, Pend(fd, k) => { var r: Fut = k(17); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } } }
    return 99;
}`},
}

// TestSelfHostClosurePtrCaptureIRX86_64 builds the self-host asm_run + path-probe
// drivers and, for each program, asserts it routes the IR path (probe == "ir")
// and runs to the interpreter oracle. x86-64.
func TestSelfHostClosurePtrCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range closurePtrCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))

			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%q routed through %q path, want \"ir\" (pointer-capture closure bailed make_clo_func)", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "ptr_capture_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%q exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
