package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- split/lines segments are owned copies (#7230) ---------------------------
//
// rt_src_str_split and rt_src_str_lines appended bare segment slices — views
// whose backing buffer is the receiver's. A result that crossed its frame
// dangled exactly like #7393's trim: pre-fix, both probes below answered 36 on
// the self-host x86-64 leg against the oracles' 29, reading the recycler's
// bytes, with underflow 0 and no diagnostic. The fix appends `slice + ""` (the
// #7393 idiom), and the SARRB release routing collapses: owned elements take
// the ordinary rc-aware full free — the view-aware helpers, which free boxes
// alone, would strand each element's data buffer. The strarr_builtin flag
// survives only as the SARRB credit's binding-site type confirmation.
//
// The escaping result itself is REFUSED a credit (the non-escape gate), so
// these shapes now leak their arrays soundly instead of dangling — the pinned
// counts say so explicitly, and the exits are the part that must hold.

type splitOwnedCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func splitOwnedCases() []splitOwnedCase {
	return []splitOwnedCase{
		{
			// THE #7230 SHAPE: a split result returned across its frame, an
			// element byte read under recycling pressure. Pre-fix: 36, the
			// recycler's bytes.
			name: "split_returned_across_frame",
			src: `import "std/string";
function mk(a: string): string { return a + "abcdefghijklmnopqrstuvwxyz0123456789"; }
function parts(): string[] { var s: string = mk("aa,bb,cc"); return s.split(","); }
function round(i: i32): i32 {
    var ps: string[] = parts();
    var clobber: string = mk("ZZZZZZZ");
    return (ps[0][0] as i32 + ps.len() + clobber.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 29, allocs: 1200, frees: 600,
		},
		{
			// The lines sibling — same mechanism, same pre-fix wrong answer.
			name: "lines_returned_across_frame",
			src: `import "std/string";
function mk(a: string): string { return a + "abcdefghijklmnopqrstuvwxyz0123456789"; }
function rows(): string[] { var s: string = mk("aa\nbb\ncc:"); return s.lines(); }
function round(i: i32): i32 {
    var rs: string[] = rows();
    var clobber: string = mk("ZZZZZZZ");
    return (rs[0][0] as i32 + rs.len() + clobber.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 29, allocs: 1200, frees: 600,
		},
		{
			// Same-frame split, receiver read after — correct under the views
			// (the receiver outlived them); must stay correct AND swept under
			// the owned copies through the collapsed (full-free) routing.
			name: "split_same_frame_swept",
			src: `import "std/string";
function mk(a: string): string { return a + "abcdefghijklmnopqrstuvwxyz0123456789"; }
function round(i: i32): i32 {
    var s: string = mk("aa,bb,cc");
    var ps: string[] = s.split(",");
    return (ps[0][0] as i32 + ps.len() + s.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 28, allocs: 1000, frees: 1000,
		},
	}
}

// TestSelfHostSplitLinesOwnedCopyX86_64 — escaped split/lines results carry
// owned segments: right answers under recycling pressure, underflow 0, and the
// same-frame case fully swept through the collapsed release routing.
func TestSelfHostSplitLinesOwnedCopyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range splitOwnedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "splitown_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; a wrong answer is "+
					"#7230's UAF — a segment view reading freed or recycled bytes)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want allocs=%d frees=%d. The escaping rows pin a "+
					"SOUND refusal-leak; MORE frees there must come with a credit that "+
					"proves the escape, never from re-viewing the segments", tc.name, summary, tc.allocs, tc.frees)
			}
		})
	}
}

// TestSelfHostSplitLinesOwnedCopyWasmIR — wasm's slice op already copied, so
// this leg pins the answers stayed identical through the routing collapse.
func TestSelfHostSplitLinesOwnedCopyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping split/lines owned-copy wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range splitOwnedCases() {
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
			watFile := filepath.Join(dir, "splitown_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("split/lines owned-copy wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostSplitLinesOwnedCopyIRArm64 — the other register backend shared
// the dangling segments, so its exits carry the crash-class signal too.
func TestSelfHostSplitLinesOwnedCopyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range splitOwnedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "splitown_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
