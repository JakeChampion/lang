package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleStructReclaimCases pin the #4365 tuple-with-STRUCT-element reclaim: a
// `var t: (i32, P) = (i, P { xs: [..], y: i })` loop-local — a fresh scalar-tuple
// whose element is a fresh reclaim-struct box (P sole-owns a rc-array field) —
// leaked the struct's field buffers + the struct box + the tuple box every
// iteration on the self-host IR path (native bounds it). The TUPRC class now admits
// a struct-literal element that routes field-reclaim (tuple_lit_rc_reclaimable /
// tuple_lit_has_array) and emit_tuple_child_drops deep-drops it: for the struct
// position, __struct_drop_<P> (backend-complete, decs the struct's rc-array fields,
// balanced by the construction alias-inc) then the struct box dec, then the tuple box
// — at the loop-rebind and the exit sweep. No dedicated runtime helper; it lowers
// through op_tuple_get / __struct_drop_<P> / __fern_rc_dec, shared by all backends.
//
// SOUNDNESS: the element uses are checked by rctuple_payload_escapes (gated on the
// tuple type annotation) — a scalar read (t.0), a struct scalar-field read (t.1.y),
// an indexed struct array-field read (t.1.xs[j]) and t.1.xs.len() are borrows
// (reclaim proceeds); a WHOLE struct extraction (`keep = t.1`, store / return / pass)
// escapes and the local is left leak-safe (never over-released). The same gate closes
// a PRE-EXISTING TUPRC gap for the plain array element (`keep = t.1` on `(i32, i32[])`
// used to over-release; it is now leak-safe) — see the arrtuple-whole-extract case.
var tupleStructReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, scalar + struct-scalar-field reads only.
	{"tuplestruct-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var t: (i32, P) = (i, P { xs: [i, i + 1, i + 2], y: i }); acc = (acc + t.0 + t.1.y) % 251; i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var t2: (i32, P) = (j, P { xs: [j, j + 1], y: j }); acc = (acc + t2.1.y) % 251; j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Full borrow set: scalar (t.0), struct scalar field (t.1.y), indexed struct
	// array field (t.1.xs[j]) and t.1.xs.len() are all admitted — still reclaims.
	{"tuplestruct-borrow-full", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var t: (i32, P) = (i, P { xs: [i, i + 1], y: i });
        acc = (acc + t.0 + t.1.y + t.1.xs[0] + t.1.xs.len()) % 251;
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var t2: (i32, P) = (j, P { xs: [j, j + 1], y: j }); acc = (acc + t2.1.xs[1]) % 251; j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-STORE negative: `keep = t.1` extracts the WHOLE struct out of the
	// tuple — the local is NOT credited (leak-safe), and MUST NOT be over-released.
	{"tuplestruct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var keep: P = P { xs: [0, 0], y: 0 };
    var i: i32 = 0;
    while (i < 50) {
        var t: (i32, P) = (i, P { xs: [i, i + 1], y: i });
        keep = t.1;
        i = i + 1;
    }
    var acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PAYLOAD-ESCAPE-CALL negative: `take(t.1)` passes the whole struct to a call (a
	// retain) — un-credited, leak-safe, detector zero.
	{"tuplestruct-escape-call-safe", `struct P { xs: i32[], y: i32 }
function take(p: P): i32 { return p.y; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var t: (i32, P) = (i, P { xs: [i, i + 1], y: i });
        acc = (acc + take(t.1)) % 251;
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// PRE-EXISTING GAP, now closed by the same rctuple_payload_escapes gate: extracting
	// the WHOLE array element (`keep = t.1` on `(i32, i32[])`) used to over-release (99);
	// it is now disqualified (leak-safe) like the struct case above.
	{"arrtuple-whole-extract-safe", `function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) {
        var t: (i32, i32[]) = (i, [i, i + 1]);
        keep = t.1;
        i = i + 1;
    }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// REGRESSION guard: the borrow-only array-element tuple STILL reclaims (the gate
	// only rejects whole extraction, not `t.1[j]` reads). Heap-bounded, no underflow.
	{"arrtuple-borrow-reclaims", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var t: (i32, i32[]) = (i, [i, i + 1, i + 2]); acc = acc + t.0 + t.1[0] + t.1[1] + t.1[2]; i = i + 1; }
    if (acc != 80200) { return 99; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 5000) { var t2: (i32, i32[]) = (j, [j, j + 1]); acc = (acc + t2.1[0]) % 251; j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

// TestSelfHostTupleStructReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTupleStructReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupleStructReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = tuple/struct leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
