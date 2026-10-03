package e2eselfhost

import "testing"

// Issue #2649 — the raw-memory intrinsic floor (Tier-2; docs/RUNTIME-INTRINSICS.md).
//
// rawIntrinsicsProg exercises every intrinsic end-to-end with no runtime helper
// consumer: __raw_alloc a buffer, fill it with __raw_store8, box it with
// __raw_string and read a byte back via s[i]; separately round-trip a word slot
// through __raw_store_ptr / __raw_load_ptr and a byte through __raw_store8 /
// __raw_load8. An address is a `usize`. The arithmetic collapses the
// round-trips to 0 so the exit code is exactly s.len() + s[0] = 3 + 72 = 75 —
// any miscompiled load/store/box shifts it off 75.
const rawIntrinsicsProg = `function build_str(): string {
    let p: usize = __raw_alloc(3);
    __raw_store8(p, 0, 72);
    __raw_store8(p, 1, 105);
    __raw_store8(p, 2, 33);
    return __raw_string(p, 3);
}
function main(): i32 {
    let s: string = build_str();
    let p2: usize = __raw_alloc(16);
    __raw_store_ptr(p2, 0, 1234 as usize);
    __raw_store8(p2, 8, 200);
    let w: usize = __raw_load_ptr(p2, 0);
    let b: i32 = __raw_load8(p2, 8);
    return s.len() + (s[0] as i32) + ((w as i32) - 1234) + (b - 200);
}`

// TestSelfHostRawIntrinsicsX86_64 runs the intrinsic probe through the x86-64
// self-host backend and checks exit 75. arm64 is left to CI.
func TestSelfHostRawIntrinsicsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", rawIntrinsicsProg)); code != 75 {
		t.Errorf("raw-intrinsic probe exited %d, want 75 (s.len()=3 + s[0]=72)", code)
	}
}
