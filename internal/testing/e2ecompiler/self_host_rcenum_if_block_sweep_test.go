package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An rc-payload enum local declared inside an `if` block (#7360) ----------
//
// `if (…) { let o: R = R.Full([i + 2, i + 3]); … }` is released by the
// function-exit sweep, whose variant dispatch begins with op_variant_is — an op
// that DEREFERENCES the box for its tag. On a call where the branch is untaken
// the entry-zeroed slot is null, so the sweep must be null-guarded or the
// program SIGSEGVs (exit 139 where the interpreter answers 50). Two calls are
// the boundary: one call takes the branch and sweeps a live box; the second
// leaves the slot null.
//
// On wasm an i32.load at address 0 reads linear memory rather than trapping,
// so there the exit-code rows pin that the guarded sweep frees no live box.
//
// Every want was confirmed against `bin/fern -interp`, never read off the
// self-host run. Alloc/free counts are the self-host build's own, pinned
// exactly; a heap string is ONE block (#7351).

type rcEnumIfBlockCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func rcEnumIfBlockCases() []rcEnumIfBlockCase {
	return []rcEnumIfBlockCase{
		{
			// THE REPRO (#7360). Faulted at exit 139 before the guard.
			name: "if_block_local",
			src: `enum R { Full(i32[]), Empty }
function round(i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: R = R.Full([i + 2, i + 3]); t = t + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 50, allocs: 100, frees: 100,
		},
		{
			// The two-call boundary from the issue: call 1 takes the branch,
			// call 2 leaves the slot null for the sweep. The smallest program
			// that reached the fault.
			name: "two_calls",
			src: `enum R { Full(i32[]), Empty }
function round(i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: R = R.Full([i + 2, i + 3]); t = t + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 2) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 1, allocs: 2, frees: 2,
		},
		{
			// CONTROL — a plain `{ }` block always runs, so the sweep never saw
			// a null slot and this was already clean. Must stay balanced: MORE
			// frees here would mean the guard's body double-releases.
			name: "ctl_plain_block",
			src: `enum R { Full(i32[]), Empty }
function round(i: i32): i32 {
    let t: i32 = 0;
    { let o: R = R.Full([i + 2, i + 3]); t = t + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17, allocs: 200, frees: 200,
		},
		{
			// CONTROL — a scalar payload takes the "SCENUMS:" shallow free
			// (__fern_rc_dec, null-guarded on its own), not the variant
			// dispatch, so it never faulted.
			name: "ctl_scalar_payload",
			src: `enum R { Full(i32), Empty }
function round(i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: R = R.Full(i + 2); t = t + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 50, allocs: 50, frees: 50,
		},
		{
			// A SECOND return edge. The drop zeroes the slot, so when both
			// edges run the sweep the later one routes null into the new guard
			// — the shape a re-dec (double free) would surface on.
			name: "second_return_edge",
			src: `enum R { Full(i32[]), Empty }
function round(i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: R = R.Full([i + 2, i + 3]); t = t + 1; }
    if (i % 3 == 0) { return t + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 1, allocs: 100, frees: 100,
		},
		{
			// A string payload from a call to a fresh-string producer (#7364):
			// the sweep releases the string and the box.
			name: "string_payload_swept",
			src: `enum R { Full(string), Empty }
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: R = R.Full(w("x")); t = t + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 50, allocs: 100, frees: 100,
		}}
}

// TestSelfHostRcEnumIfBlockSweepX86_64 — the exit sweep of an rc-payload enum
// local declared in a conditional block null-guards its variant dispatch, and
// the branch-taken path still frees payload + box exactly.
func TestSelfHostRcEnumIfBlockSweepX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range rcEnumIfBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcenumif_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = the "+
					"unguarded variant_is deref of the entry-zeroed slot, #7360)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. MORE on a control/pinned row means "+
					"a release reached a shape it must decline; FEWER on a swept row "+
					"means the guard swallowed the branch-taken release", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostRcEnumIfBlockSweepWasmIR — the wasm sibling. Exit codes only: the
// leak row does not move one, so what this leg catches is a sweep that frees a
// LIVE box on wasm (where the null deref reads memory instead of trapping).
func TestSelfHostRcEnumIfBlockSweepWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping rc-enum if-block sweep wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range rcEnumIfBlockCases() {
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
			watFile := filepath.Join(dir, "rcenumif_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("rc-enum if-block sweep wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostRcEnumIfBlockSweepIRArm64 — the arm64 sibling under qemu. Like
// x86-64 this leg dereferences the null slot (ldr from x0=0 faults), so its
// exit codes carry the crash signal as well as the answer.
func TestSelfHostRcEnumIfBlockSweepIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range rcEnumIfBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rcenumif_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
