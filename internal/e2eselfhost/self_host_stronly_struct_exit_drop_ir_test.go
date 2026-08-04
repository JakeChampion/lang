package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strOnlyStructExitDropCases pin the string-ONLY-rc struct exit-sweep reclaim
// (#4355 slice-3 completion, found probing #4354's struct-capture kind): a
// fresh struct-literal local whose only rc field is a STRING (`P { s: string,
// n: i32 }`) was reclaimed box-only at scope exit — emit_struct_field_drops
// gated on struct_has_reclaim_array_field, which a string-only type never
// passes — so its fresh string field leaked per bind. Native reclaims the
// shape. The construction-side retain (slit_reclaim) and the consume-rebind
// path already routed on the wider struct_routes_field_reclaim (rc-array /
// nested-struct / enum field OR a STRFLDOK-admitted string-fielded type); the
// exit sweep was the last unwidened consumer, so its k_str decs were already
// balanced by construction. Detector guards prove no over-release; the
// param-embed case proves a caller-owned field value survives (retain → dec
// nets zero).
var strOnlyStructExitDropCases = []struct {
	name string
	src  string
	want int
}{
	// Fresh string field, read locally, dies at exit — the core leak shape.
	{"stronly-exit-flat", `struct P { s: string, n: i32 }
function go(pre: string): i32 { var p: P = P { s: pre + "x", n: 1 }; return p.n + p.s.len(); }
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
	// The struct passed to a borrow-only helper first — the borrow gate must
	// keep it credited and the callee must not double-release.
	{"stronly-borrow-arg-flat", `struct P { s: string, n: i32 }
function rd(p: P): i32 { return p.n + p.s.len(); }
function go(pre: string): i32 { var p: P = P { s: pre + "x", n: 1 }; return rd(p); }
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
	// PARAM-EMBED safety: the field aliases the caller's string — the
	// construction retain (rc 2) balances the k_str dec (→1), the caller's
	// string stays readable, detector zero.
	{"stronly-param-embed-safe", `struct P { s: string, n: i32 }
function go(pre: string): i32 { var p: P = P { s: pre, n: 1 }; return p.n + p.s.len(); }
function churn(m: i32): i32 { var pre: string = "ab" + "cd"; var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(pre) != 5) { bad = 1; } i = i + 1; } if (pre.len() != 4) { bad = 2; } return bad; }
function main(): i32 { var v: i32 = churn(3000); if (__rc_underflow() != 0) { return 99; } return v; }`, 0},
	// Loop-local rebind in one frame: each iteration's box + fresh string field
	// reclaim at the rebind, flat across the second churn.
	{"stronly-loop-rebind-flat", `struct P { s: string, n: i32 }
function main(): i32 {
    var pre: string = "ab";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) { var p: P = P { s: pre + "x", n: i }; acc = (acc + p.n + p.s.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { var p2: P = P { s: pre + "x", n: j }; acc = (acc + p2.n + p2.s.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The #4354 probe that surfaced this: a string-fielded struct captured by a
	// directly-called closure (param-lifted to a borrowing call) now reclaims.
	{"stronly-closure-capture-flat", `struct P { s: string, n: i32 }
function go(pre: string): i32 {
    var p: P = P { s: pre + "x", n: 1 };
    var c = () => p.n + p.s.len();
    return c();
}
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
}

// TestSelfHostStrOnlyStructExitDropIRX86_64 drives the cases through the
// self-hosted x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostStrOnlyStructExitDropIRX86_64(t *testing.T) {
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

	for _, tc := range strOnlyStructExitDropCases {
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
				t.Errorf("%s = %d, want %d (98 = string field leaked; 99 = over-release/underflow; 97 = value corrupted; 1/2 = live value lost)", tc.name, code, tc.want)
			}
		})
	}
}
