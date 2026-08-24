package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleOwnedRetReleaseCases pin the caller-side release of a TUPLE-returning
// call (the owned-return admission — the #7464 review's p9 family). Before it,
// every function handing back an rc-element tuple leaked at a bound call site:
// the fresh-ret registry admitted only direct literal returns, its "TUP:"
// credit was the shallow box dec (element arrays stranded), and a bare
// `return t` disqualified the whole function — so even the plainest producer,
// `var t = (i, [i, i+1]); return t;`, left the caller with nothing to release
// and the callee refusing to sweep a returned name. Measured on the x86-64
// self-host before the fix: allocs=200 frees=0 live_bytes=8000 per 100
// rounds on the bare-return shapes, frees=100 (box only) on the
// direct-literal one, where native is clean on all of them.
//
// Every case is LOOP-RESIDENT (the caller binds the result inside the loop),
// so the leak is per ROUND: the byte cases return
// `(heap after churn 2 - heap after churn 1) / rounds`, the per-round growth
// rate. 0 = clean; a small non-zero is the leaked bytes per round; 99 is an
// over-release (__rc_underflow); 97 a corrupted value.
var tupleOwnedRetReleaseCases = []struct {
	name string
	src  string
	want int
}{
	// The base p9 shape: an early conditional literal return, then the local.
	{"tupown-cond-lit-then-local", `function mk(i: i32): (i32, i32[]) {
    if (i % 7 == 0) { return (0, [0]); }
    var t: (i32, i32[]) = (i, [i, i + 1]);
    return t;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// The plainest producer: an unconditional bare return of the local.
	{"tupown-uncond-local", `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    return t;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// Swapped paths: the local declared and returned inside the if-arm (its
	// slot is block-scoped and entry-zeroed on the literal path — the sweep's
	// null guards are load-bearing here).
	{"tupown-early-local-late-lit", `function mk(i: i32): (i32, i32[]) {
    if (i % 7 != 0) { var t: (i32, i32[]) = (i, [i, i + 1]); return t; }
    return (0, [0]);
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// Direct rc-element literal returns only: the ARRF-flagged element kinds
	// on the bound slot are what free the array the box sole-owns (the box
	// itself was already freed by the shallow "TUP:" credit pre-fix).
	{"tupown-both-literal", `function mk(i: i32): (i32, i32[]) {
    if (i % 7 == 0) { return (0, [0]); }
    return (i, [i, i + 1]);
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// The local constructed BEFORE the early literal return: the literal
	// path's per-return keep-sweep must free the not-returned local (the
	// callee half of the admission), and the returning path must keep it.
	{"tupown-local-before-early-lit", `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    if (i % 7 == 0) { return (0, [0]); }
    return t;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// TWO different frame-fresh locals returned on different paths: each
	// passes the admission independently (the bare-return exemption is
	// per-name), the flags AND both declaration shapes, and each return
	// path keeps only the local it moves out while the other is swept.
	{"tupown-two-locals", `function mk(i: i32): (i32, i32[]) {
    if (i % 2 == 0) { var t: (i32, i32[]) = (i, [i, i + 1]); return t; }
    var u: (i32, i32[]) = (i, [i, i + 1]);
    return u;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// A LOOP-RESIDENT annotated local in the callee, conditionally returned:
	// the credit requires the ret-forgiving payload-escape gate (the
	// rc-tuple class's second scan) or the annotated local is denied
	// TUPRC:/TUPRCS: and swept on NO path — the rebind reclaim then frees
	// each superseded box and the sweep-or-keep pair handles the last one.
	{"tupown-loop-resident-callee", `function mk(n: i32): (i32, i32[]) {
    var j: i32 = 0;
    while (j < n) {
        var t: (i32, i32[]) = (j, [j, j + 1]);
        if (j >= n - 1) { return t; }
        j = j + 1;
    }
    return (0, [0]);
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var r: (i32, i32[]) = mk(3 + (i % 4)); t = t + r.0 + r.1.len(); i = i + 1; }
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
	// ELEMENT EXTRACTION boundary (the review's blocker shape): the caller
	// returns r.1, handing the element's reference to a new owner, so the
	// "TUPELEMOK:" gate refuses the element kinds — WITHOUT the gate this
	// was a sanitizer use-after-free with a silent census (the pre-return
	// sweep dec'd the array being returned). Gated, the shape is fully
	// clean: pick's sweep decs only the box, and the extracted array's one
	// reference rides to the outer caller's slot, whose release is the
	// is_arr slot-flag sweep no credit can deny. 99 = the gate regressed;
	// 97 = the returned array was freed under the caller.
	{"tupown-elem-extract-return", `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    return t;
}
function pick(i: i32): i32[] {
    var r: (i32, i32[]) = mk(i);
    return r.1;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var xs: i32[] = pick(i); t = t + xs.len() + xs[0]; i = i + 1; }
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
}

const tupleOwnedRetFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostTupleOwnedRetReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedRetReleaseCases {
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
				t.Errorf(tupleOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTupleOwnedRetReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTupleOwnedRetReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping owned tuple return release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedRetReleaseCases {
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
			if code := rcmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
