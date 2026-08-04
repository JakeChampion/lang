package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrStructReclaimCases pin the #4365 `(<struct-with-array-field>)[]` array-of-structs
// reclaim: a `var ps: P[] = [P { xs: [i, i+1] }, ...]` local — an array whose ELEMENTS
// are struct boxes each carrying a fresh rc-array field — leaked all three levels (the
// per-element field array buffers, the element struct boxes, and the outer buffer) per
// loop iteration on the self-host IR path (native bounds it). The new "ARRSTRUCT:" class
// is the struct sibling of "ARRTUP:": it credits a fresh array of fresh struct literals
// consumed borrow-only, and releases it with the same counted element walk
// (emit_arrstruct_deep_free) — but each element is dropped via the struct-field deep-drop
// (__struct_drop_<P>, balanced by the construction alias-inc) + a struct-box dec, then
// the outer buffer. The element struct type is taken from the slot's struct_type (already
// recorded at the array-of-structs binding).
//
// SOUNDNESS: the element field use is checked by arrstruct_elem_payload_escapes — a
// scalar field read (ps[i].n), an indexed array-field read (ps[i].xs[j]) and
// ps[i].xs.len() are borrows (reclaim proceeds); a BARE array-field extraction (store /
// return / pass / alias / slice ps[i].xs) OR a bound element (var t = ps[i] / for t in ps,
// via arrarr_row_escapes) escapes and the local is left leak-safe (never over-released).
var arrStructReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, scalar/array read only.
	{"arrstruct-churn", `struct P { xs: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var ps: P[] = [P { xs: [i, i + 1] }, P { xs: [i + 2, i + 3] }]; acc = (acc + ps[0].xs[0]) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var qs: P[] = [P { xs: [j, j + 1] }]; acc = (acc + qs[0].xs[0]) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Full borrow set: scalar field (ps[i].n), indexed array-field (ps[i].xs[j]) and
	// ps[i].xs.len() are all admitted — still reclaims (bounded).
	{"arrstruct-borrow-full", `struct P { n: i32, xs: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var ps: P[] = [P { n: i, xs: [i, i + 1] }, P { n: i + 1, xs: [i + 2, i + 3] }];
        acc = (acc + ps[0].n + ps[1].xs[0] + ps[0].xs.len()) % 251;
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var qs: P[] = [P { n: j, xs: [j, j + 1] }]; acc = (acc + qs[0].xs[1]) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-STORE negative: `keep = ps[0].xs` extracts the array field out of an
	// element — the local is NOT credited (leak-safe), and MUST NOT be over-released.
	{"arrstruct-escape-store-safe", `struct P { xs: i32[] }
function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) {
        var ps: P[] = [P { xs: [i, i + 1] }];
        keep = ps[0].xs;
        i = i + 1;
    }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-CALL negative: `take(ps[0].xs)` passes the array field to a call
	// (a retain) — un-credited, leak-safe, detector zero.
	{"arrstruct-escape-call-safe", `struct P { xs: i32[] }
function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var ps: P[] = [P { xs: [i, i + 1] }];
        acc = (acc + take(ps[0].xs)) % 251;
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ESCAPE-VIA-FN negative: the array-of-structs is returned — ownership moves out,
	// nothing freed, value exact (a[0].xs[0] + a[1].xs[1] = 5 + 8 = 13).
	{"arrstruct-escape-fn-safe", `struct P { xs: i32[] }
function mk(n: i32): P[] {
    var ps: P[] = [P { xs: [n, n + 1] }, P { xs: [n + 2, n + 3] }];
    return ps;
}
function main(): i32 {
    var a = mk(5);
    var v: i32 = a[0].xs[0] + a[1].xs[1];
    if (__rc_underflow() != 0) { return 99; }
    return v;
}`, 13},
}

// TestSelfHostArrStructReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostArrStructReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrStructReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = array-of-structs leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
