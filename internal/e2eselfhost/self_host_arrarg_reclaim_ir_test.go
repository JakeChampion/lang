package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrArgReclaimCases pin the #4365 stage-(b) borrowed-call-arg temp reclaim for
// ARRAYS: a fresh scalar-element array literal passed directly to a borrowing
// free function (`take([i, i+1])`) allocated a buffer per evaluation that
// nothing freed on the self-host IR path (native bounds the shape). The
// call lowering now stashes such an arg (discardable_scalar_arr_lit at a
// call_arg_borrowable position) and __fern_rc_dec's it right after the call —
// the array sibling of the #4355 string literal-arg box reclaim.
//
// The consuming-callee case additionally pins the borrow-verdict soundness fix
// this slice required: a param that is REASSIGNED (`xs = xs.append(9)`) or used
// as an `.append` receiver (`var ys = xs.append(7)`) is never borrowable —
// append reuses/frees a unique receiver buffer on growth, so a caller-side
// release after such a callee double-freed (rc underflow; pre-existing for the
// Level-2 named-local precise drop, which shares the verdict).
var arrArgReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The core churn shape: a fresh scalar array literal arg, rebuilt per call.
	{"arrarg-churn-flat", `function take(xs: i32[]): i32 {
    return xs[0] + xs[1];
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { acc = (acc + take([w, w + 1])) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { acc = (acc + take([i, i + 1])) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Multi-arg frees, a forwarding callee (borrow via the interproc registry),
	// f64 elements, and a string-element literal (excluded from the reclaim —
	// leak-mode but CORRECT at detector zero, values exact).
	{"arrarg-multi-fwd-flat", `function take2(xs: i32[], ys: i32[]): i32 {
    return xs[0] + ys[1];
}
function fwd(xs: i32[]): i32 {
    return take2(xs, xs);
}
function fsum(xs: f64[]): i32 {
    if (xs[0] + xs[1] > 2.9) { return 3; }
    return 2;
}
function slen(xs: string[]): i32 {
    return xs[0].len() + xs.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) {
        acc = (acc + take2([w, w], [w, w + 1])) % 251;
        acc = (acc + fwd([w, w + 2])) % 251;
        acc = (acc + fsum([1.5, 1.5])) % 251;
        acc = (acc + slen(["ab", "c"])) % 251;
        w = w + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) {
        acc = (acc + take2([i, i], [i, i + 1])) % 251;
        acc = (acc + fwd([i, i + 2])) % 251;
        i = i + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    var f3: i32 = fsum([2.0, 1.0]);
    if (f3 != 3) { return 96; }
    var s3: i32 = slen(["xy", "z"]);
    if (s3 != 4) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 94; }
    return 0;
}`, 0},
	// ESCAPE negative: the callee returns its arg — ownership moves out, the
	// param is non-borrowable, no post-call free (values exact, no dangle).
	{"arrarg-escape-safe", `function keep(xs: i32[]): i32[] {
    return xs;
}
function main(): i32 {
    var k: i32[] = keep([7, 8]);
    var acc: i32 = k[0] + k[1];
    if (__rc_underflow() != 0) { return 99; }
    return acc;
}`, 15},
	// CONSUMING-callee negative (the soundness fix): `xs = xs.append(9)` and the
	// unbound `var ys = xs.append(7)` both free a unique receiver buffer on
	// growth — the param must be non-borrowable so neither the call-arg temp
	// reclaim nor the Level-2 named-local precise drop releases the buffer a
	// second time. Was a pre-existing rc underflow for the named-local shape.
	{"arrarg-consuming-callee-safe", `function mut(xs: i32[]): i32 {
    xs = xs.append(9);
    return xs[2] + xs.len();
}
function mut2(xs: i32[]): i32 {
    var ys: i32[] = xs.append(7);
    return ys[2] + ys.len();
}
function main(): i32 {
    var r: i32 = mut([4, 5]);
    if (r != 12) { return 97; }
    var v: i32[] = [1, 2];
    var r2: i32 = mut2(v);
    if (r2 != 10) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostArrArgReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostArrArgReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrArgReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = arg temp leaked; 99 = over-release/underflow; 94-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
