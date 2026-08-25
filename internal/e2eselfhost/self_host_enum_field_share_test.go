package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An enum local handed to a struct FIELD is a counted share ---------------
//
// `var src: E = E.A([..]); var p: P = P { e: src, … }` stranded src's box AND
// its payload once per construction. The construction retains the value (the
// k_enum arm's dec is what that balances), but every rc-enum release credit —
// the RCENUMS sweep, the loop rebind, the consuming-match free — refused a name
// that reached a struct-literal field, because their escape walks read that
// position as an escape. One inc, no dec: the source's own claim had nothing to
// release it.
//
// The carve-out (ef_escape_names / body_unsafe_for_enumfield) forgives exactly
// that position, and the releases it feeds now walk the payload under
// __fern_rc_is_unique — the gate emit_struct_enum_field_payload_drops already
// applies from the struct side. Both owners gated means whichever drops LAST
// finds rc 1 and does the deep work, in either order, and neither order
// double-frees. The rows below pin both orders: `local_then_match` releases the
// source first (its consuming match runs before the holder dies),
// `local_no_match` releases the holder first.
//
// The over-release direction is what the exit codes pin: every probe returns 99
// from __rc_underflow_count() if any dec ran past zero. That is not theoretical
// here — the first cut of this slice tripped it, because the rc-enum sweep was
// missing the moved_elided conjunct its rc-tuple sibling has, so a construction
// that MOVED the local (the #6726 elided retain) was swept anyway.
//
// Every want was confirmed against the native x86-64 backend, which is clean on
// all five shapes; the self-host numbers below now match it — including the
// call-argument row, which this suite originally pinned at a leak and called a
// refusal.

type enumFieldShareCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

// efsMain is the shared 100-round driver: it returns 99 if anything
// over-released, so an exit of 99 fails the row on the dangling direction
// rather than the leaking one.
const efsMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func enumFieldShareCases() []enumFieldShareCase {
	const decls = `struct P { e: E, n: i32 }
enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []enumFieldShareCase{
		{
			// THE REPRO. A fresh ctor bound to a local, shared into a field,
			// nothing else. Base: allocs=300 frees=100 — the enum box and its
			// payload stranded every round, only the holder's box came back.
			name: "local_no_match",
			src: decls + `function round(i: i32): i32 {
    var src: E = E.A([i, i + 5]);
    var p: P = P { e: src, n: i };
    return p.n % 101;
}
` + efsMain,
			want: 3, allocs: 300, frees: 300,
		},
		{
			// The source is CONSUMED by its own match before the holder dies, so
			// the source's release runs first and finds the box shared (rc 2):
			// it takes the shallow path and the holder's is_unique-gated drop
			// does the payload. Base: allocs=300 frees=100.
			name: "local_then_match",
			src: decls + `function round(i: i32): i32 {
    var src: E = E.A([i, i + 5]);
    var p: P = P { e: src, n: i };
    var t: i32 = 0;
    match (src) { E.A(xs) => { t = xs.len(); }, E.B => { t = 0; } }
    return (t + p.n) % 101;
}
` + efsMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			// Bound from a strict-fresh PRODUCER rather than an inline ctor —
			// the "RCE:" registry's admission, sharing the same field position.
			name: "call_bound_local",
			src: decls + `function round(i: i32): i32 {
    var src: E = mkv(i);
    var p: P = P { e: src, n: i };
    return p.n % 101;
}
` + efsMain,
			want: 3, allocs: 300, frees: 300,
		},
		{
			// The share happens in a CONDITIONAL block, so on half the rounds the
			// construction never runs and the source's own release is the only
			// one. Both paths have to balance, which is what makes a static
			// ownership hand-off (rather than the runtime gate) wrong here.
			name: "conditional_share",
			src: decls + `function round(i: i32): i32 {
    var src: E = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) {
        var p: P = P { e: src, n: i };
        t = (match (p.e) { E.A(xs) => xs.len(), E.B => 0 }) + p.n;
    }
    return t % 101;
}
` + efsMain,
			want: 28, allocs: 250, frees: 250,
		},
		{
			// The source reaches a call ARGUMENT as well as the field. This row
			// was written as a correct REFUSAL and pinned at 100 frees; it was
			// not correct, it was a leak — native is 300/300 on this exact
			// program. `sink` only READS its param, through a match expression,
			// so the param is borrowable and the source keeps its claim; what
			// refused it was the value-block walk losing the match-borrow
			// reading on the way through `+`. See
			// docs/rc-log/2026-08-25-enum-value-block-borrow.md.
			name: "call_arg_borrowed_by_callee",
			src: decls + `function sink(e: E, k: i32): i32 {
    return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + k;
}
function round(i: i32): i32 {
    var src: E = E.A([i, i + 5]);
    var p: P = P { e: src, n: i };
    return (sink(src, p.n)) % 101;
}
` + efsMain,
			want: 5, allocs: 300, frees: 300,
		},
	}
}

// TestSelfHostEnumFieldShareX86_64 is the leak-accounting leg: alloc/free counts
// per row, with 99 reserved for an over-release.
func TestSelfHostEnumFieldShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range enumFieldShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "enumfieldshare_"+tc.name, asm)
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
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means a release credit stopped "+
					"resolving; MORE on the refused row means the carve-out reached a "+
					"position no retain covers", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostEnumFieldShareWasmIR — the wasm sibling. Exit codes only (the leak
// rows do not move one), so what this leg catches is a release that frees a LIVE
// box on wasm, including the 99 the underflow guard reports.
func TestSelfHostEnumFieldShareWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum field-share wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range enumFieldShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "enumfieldshare_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("enum field-share wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostEnumFieldShareIRArm64 — the arm64 sibling under qemu.
func TestSelfHostEnumFieldShareIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range enumFieldShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "enumfieldshare_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
