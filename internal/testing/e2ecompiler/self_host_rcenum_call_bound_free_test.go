package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A CALL-bound rc-enum earns the consuming-match free too ------------------
//
// `let v: E = mkv(i)` followed by a sole top-level consuming match must free
// the box and its payload every round, exactly as the byte-identical shape with
// the constructor written INLINE does (it reclaimed nothing: 200 allocs / 0
// frees over 100 rounds).
//
// This is the rc-payload user-enum analogue of #6360, which made the same
// admission for scalar Option/Result (self_host_call_bound_enum_reclaim_test.go).
//
// Exit 99 is reserved for __rc_underflow_count().
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. A pre-fusion number in a row note below is the
// older one.

type rcenumCallFreeCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const recfMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func rcenumCallFreeCases() []rcenumCallFreeCase {
	const decls = `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []rcenumCallFreeCase{
		{
			// THE REPRO. Base: 200 allocs / 0 frees.
			name: "call_bound_sole_match",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// The INLINE ctor bind, which was already flat. It is the control that
			// says the difference was the call and not the match.
			name: "inline_ctor_sole_match_unchanged",
			src: decls + `function round(i: i32): i32 {
    let v: E = E.A([i, i + 1]);
    let a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// A REBOUND call-bound name: the rebind's release and the final
			// value's free must both fire. Base: 400 / 200, when only the rebind
			// released.
			name: "call_bound_rebound",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    v = mkv(i + 3);
    let a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len() + xs[0]; }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 2, allocs: 400, frees: 400,
		},
		{
			// Call-bound inside a LOOP body, which is the block-level sibling of the
			// pass (lower_block runs it per block). Base: 400 / 200.
			name: "call_bound_in_loop_block",
			src: decls + `function round(i: i32): i32 {
    let a: i32 = 0;
    let k: i32 = 0;
    while (k < 2) {
        let v: E = mkv(i + k);
        match (v) { E.A(xs) => { a = a + xs.len(); }, E.B => { a = a; } }
        k = k + 1;
    }
    return a % 101;
}
` + recfMain,
			want: 12, allocs: 400, frees: 400,
		},
		{
			// A STRING payload through a producer call: the deep drop must free
			// the string payload too. Base: 300 / 0.
			name: "call_bound_string_payload",
			src: `enum T { W(string), N }
function mkt(i: i32): T { return T.W("ab" + "cd"); }
function round(i: i32): i32 {
    let v: T = mkt(i);
    let a: i32 = 0;
    match (v) { T.W(s) => { a = s.len(); }, T.N => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 12, allocs: 200, frees: 200,
		},
		{
			// A STRUCT payload through a producer call: the deep drop must free
			// the payload struct and its array field too.
			name: "call_bound_struct_payload",
			src: `struct P { xs: i32[] }
enum S { V(P), N }
function mks(i: i32): S { return S.V(P { xs: [i, i + 1] }); }
function round(i: i32): i32 {
    let v: S = mks(i);
    let a: i32 = 0;
    match (v) { S.V(p) => { a = p.xs.len(); }, S.N => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 300, frees: 300,
		},
		{
			// The payload read back as a VALUE after the match. Counts and the
			// underflow guard are both blind to a use-after-READ — #7505 was
			// exactly that, and passed them plus FERN_SANITIZE=1. Native returns
			// 9. The arrays after the match are constant and not heap-allocated
			// on the typed lowering. The payload reaches its variant through
			// `id`, which hides the constant from the static-box plan, so the
			// variant is still allocated.
			//
			// The modulus is 97 because the wasm leg reads the value through the
			// exit code and WASI rejects a status outside [0, 126).
			name: "payload_read_back_after_churn",
			src: `enum E { A(i32[]), B }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mkv(): E { return E.A(id([7, 8])); }
function round(i: i32): i32 {
    let v: E = mkv();
    let a: i32 = 0;
    match (v) { E.A(xs) => { a = xs[0] + xs[xs.len() - 1]; }, E.B => { a = 0; } }
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    let j3: i32[] = [555, 666];
    return a + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, allocs: 20, frees: 20,
		},
		{
			// A callee returning its PARAM is not an "RCE:" producer, but it is an
			// "ENUM:" member: the return retains `e`, so `v` carries a count of its
			// own and `s`, lent at a counted position, keeps its release (#10410).
			// `s` is dead right after the lend, so its last-use drop runs while `v`
			// still shares the box. That drop's payload walk is is_unique-gated;
			// ungated, it freed the array under `v` (99).
			name: "param_handback_counted",
			src: `enum E { A(i32[]), B }
@noinline function passthru(e: E): E { return e; }
function round(i: i32): i32 {
    let s: E = E.A([i, i + 1]);
    let v: E = passthru(s);
    let a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// A GUARDED arm whose payload is stored out; a guard could
			// divert execution to an arm that did not move the payload.
			name: "guarded_arm_store_reclaimed",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let keep: i32[] = [0];
    match (v) { E.A(xs) when i % 2 == 0 => { keep = xs; }, E.A(ys) => { keep = [ys.len()]; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + recfMain,
			want: 81, allocs: 250, frees: 250,
		},
	}
}

// TestSelfHostRcEnumCallBoundFreeX86_64 is the leak-accounting leg.
func TestSelfHostRcEnumCallBoundFreeX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range rcenumCallFreeCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcecall_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a release ran "+
					"under a live claim)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostRcEnumCallBoundFreeWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostRcEnumCallBoundFreeWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping rc-enum call-bound free wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range rcenumCallFreeCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "rcecall_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("rc-enum call-bound free wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostRcEnumCallBoundFreeIRArm64 — the arm64 sibling under qemu.
func TestSelfHostRcEnumCallBoundFreeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range rcenumCallFreeCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rcecall_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
