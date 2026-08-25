package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A struct local reassigned from an alias reclaims nothing ----------------
//
// `var p: P = P { xs: [7,8] }; var keep: P = P { xs: [0] }; keep = p;` freed NOT
// ONE of its four blocks — 80 allocs / 0 frees over 20 rounds against native's
// 80/80. The BIND form (`var keep: P = p;`) has been at parity all along, so the
// split is REASSIGN vs BIND, not struct-vs-anything.
//
// Two gates refused it, each deliberately and each with its reason written down:
//
//   - reassigned_from_alias: after `keep = p` the slot's rc fields are BORROWS of
//     memory `p` still owns, so reclaiming would free the source's live fields
//     (#3425 stage-2).
//   - struct_bare_assigned_src: `p` itself cannot be released either, because "a
//     plain assignment retains nothing on the self-host, so the assignee holds the
//     box uncounted". Its header names the precondition for lifting it: "family
//     knowledge UNTIL ASSIGNMENTS CARRY THE CO-EXTENSIVE RETAIN."
//
// lower_stmt_assign now carries that retain, so both premises are gone. The pair
// is kept co-extensive by construction: the static pass emits an "ALIASSRC:" row
// for a forgiven source, and the retain fires iff that row is present — a source
// the escape gate rejects earns no row, so no uncounted box is ever released.
//
// TWO THINGS THIS GOT WRONG FIRST, both worth keeping:
//
//  1. The release must be SINKSHARE: (the rc-gated field walk), not NODEEP:. A
//     reassigned slot has two ownership regimes on different paths — its own fresh
//     init OWNS its field buffers, the aliased value SHARES the source's — and
//     NODEEP: is per-slot so it describes only one. The rc gate decides per VALUE
//     at runtime: whichever owner finds rc 1 does the deep walk.
//
//  2. The retain has to be emitted where EVERY return path passes, right after the
//     RHS is lowered — not via emit_arr_store's alias_inc. The struct classes
//     return earlier (emit_field_reclaim_store and the snapshot paths take no
//     alias_inc at all), so routing it through emit_arr_store silently dropped it
//     while the credit was still granted. That produced exact native COUNT parity
//     — 80/80 — with exit 99. Only __rc_underflow_count() dissented.
//
// The string limb this slice left open is closed by the follow-up, and its gap row
// left with it — self_host_str_alias_reassign_test.go owns that shape now, with the
// accumulator and fresh-RHS controls the string class needs.
//
// Every want below was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count().

type structAliasReassignCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const sarMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func structAliasReassignCases() []structAliasReassignCase {
	return []structAliasReassignCase{
		{
			// THE REPRO. Base: 80 allocs / 0 frees.
			name: "struct_alias_reassign",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var p: P = P { xs: [7, 8] };
    var keep: P = P { xs: [0] };
    keep = p;
    return keep.xs[0] + keep.xs[keep.xs.len() - 1];
}
` + sarMain,
			want: 9, allocs: 80, frees: 80,
		},
		{
			// The same shape read back as a VALUE with fresh arrays allocated after
			// the reassign, so a buffer freed too early would be reused before the
			// read. This is the row that separates a correct release from an
			// over-release: counts alone read 120/120 either way. Native returns 9.
			name: "reassign_read_back_after_churn",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var p: P = P { xs: [7, 8] };
    var keep: P = P { xs: [0] };
    keep = p;
    var j1: i32[] = [111, 222];
    var j2: i32[] = [333, 444];
    return keep.xs[0] + keep.xs[keep.xs.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0];
}
` + sarMain,
			want: 9, allocs: 120, frees: 120,
		},
		{
			// The BIND form, which was already at parity. The control that says the
			// difference was the reassign and not the alias.
			name: "struct_alias_bind_unchanged",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var p: P = P { xs: [7, 8] };
    var keep: P = p;
    return keep.xs[0] + keep.xs[keep.xs.len() - 1];
}
` + sarMain,
			want: 9, allocs: 40, frees: 40,
		},
		{
			// The ARRAY reassign, which has carried its retain (inside
			// emit_arr_store) all along. Untouched by this change on purpose: its
			// retain still goes through emit_arr_store's alias_inc, and only the
			// struct classes take the hoisted one.
			name: "array_reassign_unchanged",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [7, 8];
    var keep: i32[] = [0];
    keep = xs;
    return keep[0] + keep[keep.len() - 1];
}
` + sarMain,
			want: 9, allocs: 40, frees: 40,
		},
		{
			// A reassign from a FRESH literal rather than an alias. This is what
			// established the diagnosis: the same slot reclaims fully here, so the
			// reclaim machinery was never the problem — only the alias was refused.
			name: "fresh_rhs_reassign_unchanged",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var keep: P = P { xs: [0] };
    keep = P { xs: [7, 8] };
    return keep.xs[0] + keep.xs[keep.xs.len() - 1];
}
` + sarMain,
			want: 9, allocs: 80, frees: 80,
		},
		{
			// The string-builder CONSUME-REBIND (`s = s + part`). A different path
			// entirely — emit_str_reclaim_store, whose RHS is a fresh box and which
			// deliberately emits no inc. Nothing here may disturb it.
			name: "string_accumulator_unchanged",
			src: `function round(i: i32): i32 {
    var s: string = "";
    var k: i32 = 0;
    while (k < 4) { s = s + "ab"; k = k + 1; }
    return s.len();
}
` + sarMain,
			want: 63, allocs: 160, frees: 160,
		},
	}
}

// TestSelfHostStructAliasReassignX86_64 is the leak-accounting leg.
func TestSelfHostStructAliasReassignX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structAliasReassignCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "sarea_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the credit was "+
					"granted without the retain that pays for it)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER means the ALIASSRC: forgiveness "+
					"stopped applying; MORE on the refused string row means the credit "+
					"reached a class whose reassign carries no retain", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStructAliasReassignWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostStructAliasReassignWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct alias-reassign wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structAliasReassignCases() {
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
			watFile := filepath.Join(dir, "sarea_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct alias-reassign wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStructAliasReassignIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStructAliasReassignIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structAliasReassignCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "sarea_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
