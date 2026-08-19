package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// resultElemReclaimCases pin a tuple one of whose elements is a freshly
// constructed built-in `Result` — `(i, Ok(i))` / `(i, Err(i))`.
//
// The Option twin `(i, Some(i))` has reclaimed since the union-only-child
// slice, because emit_tuple_child_drops' union arm asks tuple_union_elem_fresh,
// which resolved a construction through expr_opt_elem_tag. That answers a TAG,
// and a bare `Ok(x)` cannot name the Result's E arm, so every `Ok` / `Err`
// element answered "not a construction" and its box leaked while the rest of
// the tuple — buffer, array elements, its own box — reclaimed around it.
//
// Byte cases return measured bytes per round, before as x86-64 | arm64 | wasm,
// native flat on all three: 40 | 40 | 16.
//
// The last three cases are safety controls and pass either way. `Ok` shadowed
// by a free function is the one the is_user_fn gate carries: the box comes back
// from a call that did not allocate it, so releasing it would over-release a
// value `keep` still holds. Native compiles that shape to the built-in Ok and
// answers 97 where interp and all three self-host backends answer 2 — a
// separate frontend bug, not something these cases assert against.
var resultElemReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"result-elem-ok", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Result[i32, i32]) = (i, Ok(i));
        var r: i32 = 0;
        match (t.1) { Ok(v) => { r = t.0 + v; }, Err(e) => { r = e; } }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"result-elem-err", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Result[i32, i32]) = (i, Err(i));
        var r: i32 = 0;
        match (t.1) { Ok(v) => { r = t.0 + v; }, Err(e) => { r = e; } }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The array sibling isolates the union box as the only thing leaking: this
	// shape's buffer and array element already reclaimed, so it measured the
	// same 40 | 40 | 16 as the two-element tuple above.
	{"result-elem-beside-array", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Result[i32, i32]) = (i, [i, i + 1], Ok(i));
        var r: i32 = t.0 + t.1[0];
        match (t.2) { Ok(v) => { r = r + v; }, Err(e) => { r = e; } }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// SHADOW negative: a free function named `Ok` returns a box `keep` still
	// holds, so the element is not a construction and must keep its leak.
	{"ok-shadowed-by-free-fn", `function Ok(r: Result[i32, i32]): Result[i32, i32] { return r; }
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var keep: Result[i32, i32] = Err(i);
        var t: (i32, Result[i32, i32]) = (i, Ok(keep));
        var r: i32 = 0;
        match (t.1) { Ok(v) => { r = v; }, Err(e) => { r = e; } }
        match (keep) { Ok(v) => { r = r + v; }, Err(e) => { r = r + e; } }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 2},
	// BARE-IDENT negative: the element aliases a live local, so it is skipped
	// by the literal-driven walk exactly as before.
	{"bare-ident-elem-still-refused", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var keep: Result[i32, i32] = Ok(i);
        var t: (i32, Result[i32, i32]) = (i, keep);
        var r: i32 = 0;
        match (t.1) { Ok(v) => { r = v; }, Err(e) => { r = e; } }
        match (keep) { Ok(v) => { r = r + v; }, Err(e) => { r = r + e; } }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 2},
	// POINTER-PAYLOAD safety: the arm binds the `Ok` payload and carries it out
	// of the loop, so it outlives every reclaim point. The reclaim releases the
	// union BOX and never its payload, which is what keeps this readable.
	{"ok-payload-carried-out-safe", `function churn(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Result[i32[], i32]) = (i, [i, i + 1], Ok([i, i + 2]));
        match (t.2) { Ok(v) => { keep = v; }, Err(e) => {} }
        acc = (acc + t.0 + t.1[0]) % 91;
        i = i + 1;
    }
    return (acc + keep[0] + keep[1] + 5) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 5},
}

const resultElemReclaimFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostResultElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range resultElemReclaimCases {
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
				t.Errorf(resultElemReclaimFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostResultElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range resultElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(resultElemReclaimFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostResultElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Result-element reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range resultElemReclaimCases {
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
				t.Errorf(resultElemReclaimFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
