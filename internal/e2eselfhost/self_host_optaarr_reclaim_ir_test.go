package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optAarrReclaimCases pin the Option[<scalar-arr>][] whole-structure reclaim
// (#4365's generic-enum-array heap-bump gap): `var xs: Option[i32[]][] =
// [Some([..]), None]` rebuilt per loop iteration leaked all three levels —
// payload buffers, option boxes, and the outer buffer — on the self-host IR
// path (native bounds the shape). The "OPTAARR:" credit (fresh literal of
// None / Some(<array literal>) elements, non-escaping, not reassigned, no
// element alias, no Some-arm payload escape) routes the exit sweep and the
// loop-rebind through __fern_optarrarr_free: per element a UNIQUELY-owned
// option box first decs its Some-payload buffer, then the box is arr_dec'd
// (rc-guarded), then the outer buffer is freed. The alias / escape negatives
// stay un-credited (leak-mode) and must remain CORRECT at detector zero.
var optAarrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The core churn shape: rebuilt per iteration, len-read only.
	{"optaarr-churn-flat", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var xs: Option[i32[]][] = [Some([w, w + 1]), None]; acc = (acc + xs.len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var xs2: Option[i32[]][] = [Some([i, i + 1]), None]; acc = (acc + xs2.len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Payload BORROW inside a match on an element — reads precede the rebind
	// free, values exact, still bounded.
	{"optaarr-payload-borrow-flat", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) {
        var xs: Option[i32[]][] = [Some([w, w + 1]), None];
        match (xs[0]) { Some(p) => { acc = acc + p[0] + p[1]; }, None => {} }
        match (xs[1]) { Some(q) => { acc = acc + q[0]; }, None => { acc = acc + 1; } }
        w = w + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) {
        var xs2: Option[i32[]][] = [Some([i, i + 1]), None];
        match (xs2[0]) { Some(p) => { acc = (acc + p[0] + p[1]) % 251; }, None => {} }
        i = i + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ELEMENT-ALIAS negative: `var o = xs[0]` binds an option box — the
	// candidate is excluded (arrarr_row_escapes), values + detector hold.
	{"optaarr-elem-alias-safe", `function main(): i32 {
    var xs: Option[i32[]][] = [Some([7, 8]), None];
    var o = xs[0];
    var acc: i32 = 0;
    match (o) { Some(p) => { acc = p[0] + p[1]; }, None => {} }
    if (__rc_underflow() != 0) { return 99; }
    return acc;
}`, 15},
	// PAYLOAD-ESCAPE negative: a Some-arm binding returned out of the match —
	// excluded (optaarr_elem_payload_escapes), the escaped payload stays live.
	{"optaarr-payload-escape-safe", `function pick(xs: Option[i32[]][]): i32[] {
    match (xs[0]) { Some(p) => { return p; }, None => {} }
    return [0];
}
function main(): i32 {
    var xs: Option[i32[]][] = [Some([5, 6]), None];
    var p = pick(xs);
    if (__rc_underflow() != 0) { return 99; }
    return p[0] + p[1];
}`, 11},
}

// TestSelfHostOptAarrReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostOptAarrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optAarrReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = structure leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
