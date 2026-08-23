package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- `.trim()` returns an owned copy (#7393) ---------------------------------
//
// rt_src_str_trim used to return the bare slice `s[start:end]` — a zero-copy
// view whose backing buffer the producer's exit sweep frees. A view that
// crossed its frame was a deterministic wrong-answer UAF on the register
// backends: the control read 0 from the freed (zeroed) block, and a recycling
// allocation between the free and the read handed the view the recycler's
// bytes — the looped repro's self-host exit differed from the oracles by
// exactly 'p'-'Z'. Wasm answered correctly only because its slice op already
// copies. Meanwhile every memory instrument read clean: leakcheck saw only the
// 24-byte view-box floor, the underflow counter never moved, and the
// exit-code gates matched on programs that did not read the view.
//
// The fix follows docs/STR-VIEW-CONTRACT.md step 1: the helper returns
// `s[start:end] + ""` — an owned copy, native and interp's semantics — and
// every "trim is a view" classification flips to the ordinary fresh-producer
// set (str_fresh_alloc_method / str_free_producer_ident /
// str_producer_ownership), which DELETED the trim-specific credit machinery
// (init_is_str_trim, trim_str_init, collect_trim_local_names) outright: an
// owned trim is just a fresh string.
//
// Every want was confirmed against BOTH oracles; counts are the self-host
// build's own, measured through this harness (the CLI const-folds
// literal-literal concats, so probe strings route through a call).

type trimOwnedCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func trimOwnedCases() []trimOwnedCase {
	return []trimOwnedCase{
		{
			// THE REPRO (#7393): the view crossed its frame, a later allocation
			// recycled the freed buffer, and v[0] read the recycler's bytes —
			// self-host exit differed from the oracles by 'p'-'Z'.
			name: "returned_across_frame_with_recycler",
			src: `import "std/string";
function mk(a: string): string { return a + "abcdefghijklmnopqrstuvwxyz0123456789"; }
function mkv(): str { var s: string = mk("  pad  "); return s.trim(); }
function round(i: i32): i32 {
    var v: str = mkv();
    var clobber: string = mk("ZZZZZZZ");
    return (v[0] as i32 + clobber.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 17, allocs: 600, frees: 600,
		},
		{
			// The control: no recycler, one call. Used to read 0 — the freed
			// block's zeroed byte — where the oracles read 'p'%101 = 11.
			name: "returned_across_frame_control",
			src: `import "std/string";
function mk(a: string): string { return a + "abcdefghijklmnopqrstuvwxyz0123456789"; }
function mkv(): str { var s: string = mk("  pad  "); return s.trim(); }
function main(): i32 {
    var v: str = mkv();
    if (__rc_underflow_count() != 0) { return 99; }
    return (v[0] as i32) % 101;
}`,
			want: 11, allocs: 4, frees: 4,
		},
		{
			// Same-frame trim + receiver used after — the shape that was
			// already CORRECT under the view (the receiver outlived the view)
			// and must stay correct and balanced under the copy.
			name: "same_frame_receiver_after",
			src: `import "std/string";
function mk(a: string): string { return a + "abcdefghijklmnopqrstuvwxyz0123456789"; }
function round(i: i32): i32 {
    var s: string = mk("  pad  ");
    var v: str = s.trim();
    return (v[0] as i32 + s.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 17, allocs: 400, frees: 400,
		},
	}
}

// TestSelfHostTrimOwnedCopyX86_64 — a returned trim is an owned copy: right
// answers under recycling pressure, balanced census, underflow 0.
func TestSelfHostTrimOwnedCopyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range trimOwnedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "trimown_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; a wrong answer "+
					"here is #7393's UAF — the view reading freed or recycled bytes)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs || frees != tc.frees {
				t.Errorf("%s: %s — want allocs=%d frees=%d", tc.name, summary, tc.allocs, tc.frees)
			}
		})
	}
}

// TestSelfHostTrimOwnedCopyWasmIR — wasm already copied at the slice op, so
// this leg pins that the added concat kept the answers identical.
func TestSelfHostTrimOwnedCopyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping trim owned-copy wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range trimOwnedCases() {
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
			watFile := filepath.Join(dir, "trimown_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("trim owned-copy wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTrimOwnedCopyIRArm64 — the other register backend had the same
// dangling view, so its exit codes carry the crash-class signal too.
func TestSelfHostTrimOwnedCopyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range trimOwnedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "trimown_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
