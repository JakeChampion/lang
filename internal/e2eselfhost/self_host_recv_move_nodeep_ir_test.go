package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// recvMoveNoDeepCases pin the "NODEEP:" gate on the sweeps' deep field drop
// (#3425 residual miscompile): the exit/return/precise sweeps free a
// reclaimable struct local via emit_struct_field_drops → __struct_drop_<T>,
// which walk-frees the box's rc-array fields (elements + buffer). That deep
// walk is sound only while the dead box still OWNS those fields — but the
// #3456 credit deliberately admits builder locals used as method RECEIVERS
// (`ms.emit(op)`), and such a method can MOVE the receiver's field into its
// result with no counted reference: `ops: self.ops.append(op)` hands the SAME
// rc==1 buffer to the result whenever the append is in-place (spare
// capacity). Deep-dropping the dead receiver then frees element boxes the
// returned value still holds; the next allocation reuses them and the live
// value reads foreign data. This is the exact mechanism that corrupted the
// IR-built compiler's own Op streams when the merged bundle first compiled
// irlower through the IR path. The fix keeps the credit (rebind reclaim +
// box-only sweep dec both stay) but marks receiver / call-arg / struct-base
// used locals "NODEEP:" so the sweeps withhold the deep drop.
var recvMoveNoDeepCases = []struct {
	name string
	src  string
	want int
}{
	// The builder chain: three grows leave spare capacity, so the final
	// receiver-position emit appends IN PLACE — result.ops shares ms's rc==1
	// buffer. Pre-fix the return sweep deep-dropped ms, freeing the shared
	// element boxes; churn() then reused them (P{x:7}) and main read 7 where
	// ops[0].x == 1 (observed exit 52, want 46).
	{"receiver-move-inplace-append-survives-sweep", `struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S {
    return S { ops: self.ops.append(P { x: v }), n: self.n + 1 };
}
function build(): S {
    var ms: S = S { ops: [], n: 0 };
    ms = ms.emit(1);
    ms = ms.emit(2);
    ms = ms.emit(3);
    return ms.emit(4);
}
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var r: S = build();
    var c: i32 = churn(64);
    if (r.ops.len() != 4) { return 90; }
    if (r.ops[0].x != 1 || r.ops[1].x != 2 || r.ops[2].x != 3 || r.ops[3].x != 4) { return 91; }
    if (r.n != 4) { return 92; }
    if (c != 64) { return 93; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The SNAPSHOT-LOCAL credit sibling: same chain, but ms is bound from a
	// fresh-struct-returning CALL (`mk()`), so its bare reclaim credit comes
	// from snapshot_local_names_of — a SECOND source appended after
	// reclaimable_names_of that originally bypassed the NODEEP marking (this
	// is the exact credit path behind the IR-built compiler's Op-stream
	// corruption: lower_stmt_var/lower_expr's `var sb = se.add_local(..)` …
	// `return sb.emit(..)` locals). Pre-fix exit 91, same mechanism.
	{"snapshot-local-receiver-move-survives-sweep", `struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S {
    return S { ops: self.ops.append(P { x: v }), n: self.n + 1 };
}
function mk(): S {
    return S { ops: [], n: 0 };
}
function build(): S {
    var ms: S = mk();
    ms = ms.emit(1);
    ms = ms.emit(2);
    ms = ms.emit(3);
    return ms.emit(4);
}
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var r: S = build();
    var c: i32 = churn(64);
    if (r.ops.len() != 4) { return 90; }
    if (r.ops[0].x != 1 || r.ops[1].x != 2 || r.ops[2].x != 3 || r.ops[3].x != 4) { return 91; }
    if (r.n != 4) { return 92; }
    if (c != 64) { return 93; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Read-only control: a local whose struct-array field is only READ
	// (indexed, never a receiver / call arg) keeps the deep sweep — its
	// fields reclaim (bounded churn, detector at zero). Guards that the
	// NODEEP marker didn't turn off the field reclaim for intact locals.
	{"read-only-local-still-deep-reclaims", `struct P { x: i32 }
struct S { ops: P[], n: i32 }
function go(k: i32): i32 {
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 3) { ps = ps.append(P { x: k + i }); i = i + 1; }
    var s: S = S { ops: ps, n: k };
    return s.ops[0].x + s.n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) { acc = (acc + go(i)) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 98; }
    return 0;
}`, 0},
}

// TestSelfHostRecvMoveNoDeepIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run) — value-correct + underflow-guarded.
func TestSelfHostRecvMoveNoDeepIRX86_64(t *testing.T) {
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

	for _, tc := range recvMoveNoDeepCases {
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
				t.Errorf("%s = %d, want %d (91 = moved element boxes freed by the receiver's sweep deep-drop and reused; 99 = over-release/underflow; 139 = heap corruption segfault)", tc.name, code, tc.want)
			}
		})
	}
}
