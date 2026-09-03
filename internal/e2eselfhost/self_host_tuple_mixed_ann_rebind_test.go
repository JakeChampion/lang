package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Compound scalar elements and the assign-form rebind (#7226) -------------
//
// Two residues of the bare-ident tuple element retain/release pair:
//
//  1. A tuple literal with ANY compound scalar element — `(i + 1, ys)` — was
//     refused by tuple_lit_is_fresh_scalar (Number/Bool/Ident leaves only) and
//     by the all-scalar annotation leg, so the whole tuple lost its release:
//     allocs=2 frees=0 on one round against 0 on native. The binding's
//     annotation names every element's type, so tuple_ann_admits_fresh_mixed
//     admits per-position: a scalar position may hold any expression, an rc
//     position must stay a bare ident.
//  2. The assign-form rebind `t = (k, ys)` freed the superseded box through
//     emit_arr_store's SHALLOW dec, stranding the box's retained element
//     buffer (40 B/round). The StmtVar re-declaration has driven
//     emit_tup_elem_reclaim_store all along; the assign path now takes it too.
//
// The STRING position at that same rebind, and the writer agreement it needs,
// are in self_host_tuple_str_rebind_test.go.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and
// the native x86-64 backend agreed on each — never read off the self-host run
// under test.

type tupMixedAnnCase struct {
	name string
	src  string
	want int
}

func tupMixedAnnCases() []tupMixedAnnCase {
	return []tupMixedAnnCase{
		{
			// The headline refusal: a compound scalar element beside a bare-ident
			// array element. Before the per-position admission this tuple was in
			// no class at all — neither the box nor the retained buffer was freed.
			name: "compound_scalar_elem",
			src: `function round(i: i32): i32 {
    var ys: i32[] = [i, i + 1, i + 2, i + 3];
    var u: (i32, i32[]) = (i + 1, ys);
    return u.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 75,
		},
		{
			// The assign-form rebind. The superseded box's retained element is
			// given back by the element walk; scope exit covers the final value.
			name: "assign_rebind",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 2, i + 3];
    var t: (i32, i32[]) = (i, xs);
    t = (i + 1, ys);
    return t.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 8,
		},
		{
			// Two tuples over one source ident, the second with a compound
			// scalar. The buffer is retained twice and both walks give one back;
			// this was #7226's "same ident in two tuples" row, and the compound
			// scalar was the actual denial, not the sharing.
			name: "two_tuples_one_ident",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var u: (i32, i32[]) = (i + 1, xs);
    return t.1[0] + u.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 39,
		},
		{
			// The rebind on one branch only: on the untaken branch the walk's cow
			// guard and null handling must leave the original value alone.
			name: "branch_rebind",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 2, i + 3];
    var t: (i32, i32[]) = (i, xs);
    if (i % 2 == 0) { t = (i + 1, ys); }
    return t.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 75,
		},
		{
			// The superseded element's SOURCE local is read after the rebind. The
			// walk gives back only the tuple's reference, so the local's own must
			// still see live bytes — the case a source-releasing walk corrupts.
			name: "old_elem_read_after_rebind",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 2, i + 3];
    var t: (i32, i32[]) = (i, xs);
    t = (i + 1, ys);
    return t.1[0] + xs[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 83,
		},
		{
			// The widened tuple's element read through a separate binding after
			// the tuple — the exit release runs after every use.
			name: "elem_read_after",
			src: `function round(i: i32): i32 {
    var ys: i32[] = [i, i + 1, i + 2, i + 3];
    var u: (i32, i32[]) = (i + 1, ys);
    var s: i32 = u.1[1];
    return s + ys[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 39,
		},
	}
}

// TestSelfHostTupleMixedAnnRebindX86_64 — the widened class and the assign-path
// element walk balance exactly. allocs == frees is load-bearing in both
// directions: short is the leak these close, above is a double free. The
// __rc_underflow_count() folded into every exit code separates a real balance
// from a freelist recycle.
func TestSelfHostTupleMixedAnnRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupMixedAnnCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupmixed_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (both oracles agree on %d; an offset of "+
					"+N is N rc underflows)", tc.name, exit, tc.want, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
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
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0; the retain is per round, so "+
					"anything stranded scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly", tc.name, summary)
			}
		})
	}
}

// TestSelfHostTupleMixedAnnHazardsX86_64 — the shapes the widening must NOT
// admit, each pinned by answer + __rc_underflow_count() (the counter is the
// load-bearing half: a doubly-released block recycles and the arithmetic still
// comes out right). These assert answers, not leak counts — the refusals fall
// back to leak-mode, the safe direction.
func TestSelfHostTupleMixedAnnHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []tupMixedAnnCase{
		{
			// The element leaves the frame: the extraction gate refuses the
			// element release for a widened tuple exactly as for an all-ident one.
			name: "elem_extracted_escaping",
			src: `function keep(i: i32): i32[] {
    var ys: i32[] = [i, i + 1];
    var u: (i32, i32[]) = (i + 1, ys);
    return u.1;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { var k: i32[] = keep(r); x = x + k[0]; r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 53,
		},
		{
			// A compound scalar beside an array LITERAL element stays the deep
			// TUPRC: class's — both classes crediting one slot is the double box
			// dec the recorded #7226 negative result measured.
			name: "rc_literal_child_stays_tuprc",
			src: `function round(i: i32): i32 {
    var u: (i32, i32[]) = (i + 1, [i, i + 2]);
    return u.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 53,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tupmixedhaz_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d (an offset of +N is N rc underflows — "+
					"a wrongly granted credit, not a wrong sum)", tc.name, exit, tc.want)
			}
		})
	}
}

// TestSelfHostTupleMixedAnnRebindWasmIR — the wasm sibling. Exit codes only:
// FERN_LEAKCHECK is x86-64-only, and the answer (with the underflow count
// folded in) is what proves the releases claimed no live buffer.
func TestSelfHostTupleMixedAnnRebindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple mixed-ann rebind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupMixedAnnCases() {
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
			watFile := filepath.Join(dir, "tupmixed_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple mixed-ann rebind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleMixedAnnRebindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleMixedAnnRebindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupMixedAnnCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupmixed_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
