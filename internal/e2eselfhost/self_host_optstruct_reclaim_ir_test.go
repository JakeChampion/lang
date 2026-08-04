package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optStructReclaimCases pin the #4365 `Option[<struct-with-array-field>]` reclaim: a
// `var o: Option[P] = Some(P { xs: [i, i+1] })` (P has an rc-array field) consumed by a
// borrow-only match leaked its payload array buffer + struct box + option box per
// iteration on the self-host IR path (native bounds it). The new "OPTSTRUCT:" class is
// the struct sibling of "OPTTUP:": it admits a fresh Some(<struct literal>) / None
// consumed by exactly one borrow-only match, and inline-frees the box — tag-check (Some)
// -> struct-field deep-drop (emit_struct_field_drops -> __struct_drop_<P>, balanced by the
// construction alias-inc) -> struct box dec -> option box dec, at the loop-rebind and exit
// sweep. some_opt_type records the full Option[P] for a struct payload (no tuple-style
// collapse), so no opt_ty override is needed.
//
// SOUNDNESS: the Some-arm's payload use is checked by optstruct_payload_escapes — a scalar
// field read (p.n), an indexed array-field read (p.xs[i]) and p.xs.len() are borrows
// (reclaim proceeds); a BARE array-field extraction (store / return / pass / alias / slice
// p.xs) escapes and the local is left leak-safe (never over-released).
var optStructReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, scalar/array read only.
	{"optstruct-churn", `struct P { xs: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[P] = Some(P { xs: [i, i + 1] }); match (o) { Some(p) => { acc = (acc + p.xs[0]) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[P] = Some(P { xs: [j, j + 1] }); match (o2) { Some(p) => { acc = (acc + p.xs[0]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Full borrow set: scalar field (p.n), indexed array-field (p.xs[i]) and p.xs.len()
	// are all admitted — still reclaims (bounded).
	{"optstruct-borrow-full", `struct P { n: i32, xs: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[P] = Some(P { n: i, xs: [i, i + 1] }); match (o) { Some(p) => { acc = (acc + p.n + p.xs[0] + p.xs.len()) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[P] = Some(P { n: j, xs: [j, j + 1] }); match (o2) { Some(p) => { acc = (acc + p.xs[1]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-STORE negative: `Some(p) => keep = p.xs` extracts the array out of the
	// arm — the local is NOT credited (leak-safe), and MUST NOT be over-released.
	{"optstruct-escape-store-safe", `struct P { xs: i32[] }
function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[P] = Some(P { xs: [i, i + 1] });
        match (o) { Some(p) => { keep = p.xs; }, None => {} }
        i = i + 1;
    }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-CALL negative: `take(p.xs)` passes the array field to a call (a retain)
	// — un-credited, leak-safe, detector zero.
	{"optstruct-escape-call-safe", `struct P { xs: i32[] }
function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[P] = Some(P { xs: [i, i + 1] });
        match (o) { Some(p) => { acc = (acc + take(p.xs)) % 251; }, None => {} }
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ESCAPE-VIA-FN negative: the option is passed to a function whose match returns the
	// payload array — ownership moves out, nothing freed, value exact (6 + 7 = 13).
	{"optstruct-escape-fn-safe", `struct P { xs: i32[] }
function pick(o: Option[P]): i32[] {
    match (o) { Some(p) => { return p.xs; }, None => {} }
    return [0];
}
function main(): i32 {
    var o: Option[P] = Some(P { xs: [6, 7] });
    var a = pick(o);
    if (__rc_underflow() != 0) { return 99; }
    return a[0] + a[1];
}`, 13},
}

// TestSelfHostOptStructReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostOptStructReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optStructReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
