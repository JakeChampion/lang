package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrOwnedRetReleaseCases pin the caller-side release of an ARRAY-returning
// call whose callee leaves this frame owning one reference (#7259).
//
// `return h.xs` and `return a` (a borrowed array param) are RETAINED on the way
// out — the return-transfer Perceus dup — so the result is a counted reference
// somebody owes a dec for. A caller that BINDS it pays that from the slot's exit
// sweep. A caller that consumes it in place — `h.get().len()`, `grab(h)[0]`, a
// discarded `h.get();` — has no slot, and nothing paid it. Two things then went
// wrong at once, and each alone measures identically to no fix at all:
//
//   - the retain was never released, so the buffer's rc ratcheted up per call;
//   - a method returning a field cost its RECEIVER the deep drop entirely
//     (moves_fields_expr marks every method receiver a field-move hazard), so
//     the receiver's own reference was never dec'd either — and that marker is
//     whole-local, so it stranded every rc field of the struct, including ones
//     the method never names.
//
// Every case is LOOP-RESIDENT: the struct is constructed inside the loop, so the
// leak is per ROUND rather than per object. The issue's original probe hoisted
// it out and therefore reported "flat" for something unbounded. Measured on the
// x86-64 self-host before the fix, live bytes at 100 / 200 / 400 rounds:
// 4800 / 9600 / 19200 — exactly 48 B/round — where native is 0 on every row.
//
// The byte cases return `(heap after churn 2 − heap after churn 1) / rounds`,
// which is that same per-round rate: what the first churn failed to give back is
// exactly what the second has to allocate fresh. A one-time cost would read 0
// here, so a non-zero exit IS the growth.
var arrOwnedRetReleaseCases = []struct {
	name string
	src  string
	want int
}{
	// The `.len()` receiver, an ARRAY-field-returning METHOD.
	{"arrown-method-len-recv", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var keep: H = H { xs: [1, 2, 3] }; t = t + keep.get().len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// The result DISCARDED outright — the statement is its whole lifetime.
	{"arrown-method-discarded", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var keep: H = H { xs: [1, 2, 3] }; keep.get(); t = t + keep.xs[0]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// The INDEX read, through a FREE function rather than a method.
	{"arrown-free-fn-index", `struct H { xs: i32[] }
function grab(h: H): i32[] { return h.xs; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var keep: H = H { xs: [1, 2, 3] }; t = t + grab(keep)[1]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// A bare BORROWED param returned (the #4357 return-transfer limb) with the
	// result never bound. Its owner `s` is read afterwards, so the release has
	// to give back the retain and nothing more.
	{"arrown-borrowed-param-len-recv", `function id(a: i32[]): i32[] { return a; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var s: i32[] = [1, 2, 3]; t = t + id(s).len() + s[0]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// MIXED per-path returns: one path retains a borrowed field, the other moves
	// a fresh literal out. Both leave the caller owning one reference, so the
	// unconditional release is balanced on either.
	{"arrown-mixed-returns", `struct H { xs: i32[] }
function pick(h: H, c: i32): i32[] { if (c > 0) { return h.xs; } return [7, 8]; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var keep: H = H { xs: [1, 2, 3] }; t = t + pick(keep, i % 2).len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// BOUND result: the binding slot's exit sweep already pays the dec, so this
	// is the case that would go 99 if the in-place release also fired on it.
	{"arrown-bound-result-balanced", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var keep: H = H { xs: [1, 2, 3] };
        var a: i32[] = keep.get();
        t = t + a.len() + a[2] + keep.xs[0];
        i = i + 1;
    }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// The whole-local half: `ys` is never passed to the method, never returned
	// and never aliased, and it leaked all the same because one field-returning
	// method disabled the receiver's deep drop for EVERY field. 112 B/round
	// before (48 + 64, both buffers), 0 after.
	{"arrown-sibling-field-deep-drop", `struct H2 { xs: i32[], ys: i32[] }
function (h: H2) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var keep: H2 = H2 { xs: [1, 2, 3], ys: [4, 5, 6, 7, 8] };
        t = t + keep.get().len() + keep.ys.len();
        i = i + 1;
    }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var b1: i64 = __heap_bump_bytes();
    var x: i32 = churn(200);
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	// USE-AFTER-FREE control, and the reason the rows above are a reclaim rather
	// than a dangle: the receiver OUTLIVES the loop, so the release must give
	// back only the retain. Its buffer is read after decoy allocations that would
	// be handed the block if it had really been freed.
	{"arrown-shared-buffer-still-live", `struct H { xs: i32[] }
function (h: H) get(): i32[] { return h.xs; }
function churn(n: i32): i32 {
    var keep: H = H { xs: [11, 22, 33] };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = (t + keep.get().len()) % 251; i = i + 1; }
    var d1: i32[] = [777, 888, 999];
    var d2: i32[] = [111, 222, 333];
    return (t + keep.xs[0] + keep.xs[1] + keep.xs[2] + d1[0] + d2[0]) % 9973;
}
function main(): i32 {
    var w: i32 = churn(400);
    if (__rc_underflow() != 0) { return 99; }
    if (w != 1150) { return 97; }
    return 0;
}`, 0},
	// An `own` param returned bare is REFUSED by the registry, and this is the
	// row that says why: it moves the caller's own reference back out, so a
	// release would be the second dec on one reference. The self-host checker
	// has no E051, so it lowers `id(s)` over a live local — which native rejects
	// — and admitting the shape took that program to an rc underflow. It keeps
	// its leak instead; only the safety property is pinned here.
	{"arrown-own-param-refused", `function id(own a: i32[]): i32[] { return a; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + id([1, 2, 3]).len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    var w: i32 = churn(200);
    var x: i32 = churn(200);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return 0;
}`, 0},
}

const arrOwnedRetFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostArrOwnedRetReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf(arrOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrOwnedRetReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(arrOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrOwnedRetReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping owned array return release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf(arrOwnedRetFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
