package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optArrArrReclaimCases pin the #4365 `Option[<scalar-arr-of-arr>]` reclaim: a
// `var o: Option[i32[][]] = Some([[i, i+1], ...])` consumed by a borrow-only match
// leaked its payload (inner row buffers + outer buffer) + option box per iteration on the
// self-host IR path (native bounds it). The new "OPTARRARR:" class is the arr-of-arr
// sibling of "OPTSTRUCT:": it admits a fresh Some(<arr-of-arr literal>) / None consumed by
// exactly one borrow-only match, and inline-frees the box — tag-check (Some) -> the payload
// freed whole by the backend-complete __fern_arrarr_free (inner buffers + outer) -> option
// box dec, at the loop-rebind and exit sweep. some_opt_type collapses the arr-of-arr payload
// a level, so the slot check matches on the CREDIT (granted only on the authoritative
// Option[<scalar-arr-of-arr>] annotation), not the opt_type shape.
//
// SOUNDNESS: the Some-arm's payload use is checked by optarrarr_payload_escapes — a
// doubly-indexed scalar read (g[i][j]), g.len() and g[i].len() are borrows (reclaim
// proceeds); a BARE outer `g` or a BARE row `g[i]` extraction (store / return / pass /
// alias / slice) escapes and the local is left leak-safe (never over-released).
//
// The escape-via-fn negative that the other reclaim classes test is omitted here: an
// Option[i32[][]] function parameter / return is outside the IR subset (routes to the AST
// fallback, where this class never applies), so it is not compiled-driver-testable — the
// escape-store / escape-call negatives cover the over-release guard on the IR path.
var optArrArrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, doubly-indexed scalar read only.
	{"optarrarr-churn", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(g) => { acc = (acc + g[0][0]) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[i32[][]] = Some([[j, j + 1]]); match (o2) { Some(g) => { acc = (acc + g[0][0]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Full borrow set: g[i][j], g.len() and g[i].len() are all admitted — still reclaims.
	{"optarrarr-borrow-full", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(g) => { acc = (acc + g[0][0] + g[1][1] + g.len() + g[0].len()) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[i32[][]] = Some([[j, j + 1]]); match (o2) { Some(g) => { acc = (acc + g[0][1]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-STORE negative: `Some(g) => keep = g[0]` extracts an inner ROW out of
	// the arm — the local is NOT credited (leak-safe), and MUST NOT be over-released.
	{"optarrarr-escape-store-safe", `function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[i32[][]] = Some([[i, i + 1]]);
        match (o) { Some(g) => { keep = g[0]; }, None => {} }
        i = i + 1;
    }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-CALL negative: `take(g[0])` passes an inner row to a call (a retain)
	// — un-credited, leak-safe, detector zero.
	{"optarrarr-escape-call-safe", `function take(a: i32[]): i32 { return a[0]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[i32[][]] = Some([[i, i + 1]]);
        match (o) { Some(g) => { acc = (acc + take(g[0])) % 251; }, None => {} }
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostOptArrArrReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostOptArrArrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optArrArrReclaimCases {
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
