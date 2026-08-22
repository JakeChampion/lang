package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- `var v: T = t` on an rc container (#7282) -------------------------------
//
// A plain alias bind released NOTHING — not the box, not its payload — because
// three things all pointed at the alias at once: the bind emitted no retain
// (the alias-inc was gated on `is_arr_slot`), the source lost its credit to the
// escape gate, and the alias earned none of its own. Four releases lost, not
// one, and `frees=0` rather than a partial count.
//
// THE MODEL IS DUPLICATION, NOT TRANSFER. Both slots own a counted reference and
// both release it; the refcount arbitrates. `alias_in_a_conditional` is why:
// under a transfer model `if (c) { var v = t; }` leaves the source un-swept on
// the path where no transfer happened, so a leak becomes branch-dependent —
// strictly worse than the leak it replaces. Duplication emits the inc and the
// dec on the same path by construction.
//
// THE INVARIANT: only the BOX is retained at the bind, so only the BOX may be
// released twice. The alias therefore takes the box-only release and the source
// keeps the deep one — `"NODEEP:"` for a struct (a field walk plus a box dec)
// and the shallow `"TUP:"` for an rc-tuple (whose `"TUPRCS:"` release is a
// type-driven deep free). Both deep classes were measured double-freeing at
// exit 99 before that split, with `allocs == frees` at `live_bytes == 0` — the
// census silent, as it is for every over-release.
//
// The ARRAY rows are the reference implementation and must stay byte-neutral:
// arrays already retained at the bind, and their exit sweep is driven by the
// `is_arr` slot FLAG rather than a credit an escape scan can deny, which is why
// they never had the bug — and why they could not have warned anyone about the
// threading defect the block-scoped rows caught.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type containerAliasCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func containerAliasCases() []containerAliasCase {
	return []containerAliasCase{
		{
			// #7282's repro. `var t: (i32, i32[]) = (i, xs); var v = t;` — the
			// bind now retains the box, the alias carries the source's shallow
			// credit, and both slots sweep. Base: allocs=200 frees=0, 8000.
			name: "tuple_alias",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// THE DEEP-RELEASE CASE. `"TUPRCS:"` frees by TYPE — every rc
			// position, then the box — so giving the alias that credit freed the
			// element twice: exit 99, with allocs == frees at live_bytes 0. The
			// alias takes the shallow `"TUP:"` box dec instead. Base 200/0, 8000.
			name: "tuple_alias_fresh_element",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// A box-only tuple: no element release either side. Base 100/0, 4000.
			name: "tuple_alias_scalar",
			src: `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var v: (i32, i32) = t;
    return v.0 + v.1;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			// THE OTHER DEEP-RELEASE CASE, and the one that generalised the
			// rule: a struct release is a field WALK plus a box dec, so sharing the
			// credit walked the fields twice — exit 99 again. The alias is marked
			// `"NODEEP:"` so its sweep is box-only. Base 200/0, 8000.
			name: "struct_alias",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var t: P = P { xs: [i, i + 1] };
    var v: P = t;
    return v.xs[0] + v.xs[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// The source read AFTER the alias, so both are live across the
			// bind. This is the row that proved the residual was block scope and
			// not the extra read — it was already clean when the block-scoped
			// shapes were not.
			name: "alias_with_post_read",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    return v.1[0] + t.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// BLOCK SCOPE. A first version threaded the escape forgiveness only
			// through the top-level statement loop, so anything nested fell into the
			// un-forgiving walker: function scope measured 200/200 while this sat at
			// 200/100, two dec sites missing from the emitted asm and nothing else
			// to see. Base 200/0, 8000.
			name: "alias_in_a_plain_block",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var acc: i32 = 0;
    { var v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			// THE SHAPE THAT DECIDED THE MODEL. Under a TRANSFER model the
			// source is left un-swept on the path where no transfer happened, so a
			// leak becomes branch-dependent. Duplication emits the inc and the dec
			// on the same path by construction, which is why this measures like
			// every other row. Base 200/0, 8000.
			name: "alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var acc: i32 = 0;
    if (i % 2 == 0) { var v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 200, frees: 200,
		},
		{
			// Both factors at once. Base 200/0, 8000.
			name: "conditional_alias_with_post_read",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var acc: i32 = 0;
    if (i % 2 == 0) { var v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc + t.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 30, allocs: 200, frees: 200,
		},
		{
			// THE REFERENCE IMPLEMENTATION — clean before this change and after.
			// Arrays already retained at the bind and are swept by the `is_arr` slot
			// FLAG rather than by a credit an escape scan can deny, which is why
			// they never had this bug. These three rows pin that the change is
			// byte-neutral for them.
			name: "array_alias_reference",
			src: `function round(i: i32): i32 {
    var t: i32[] = [i, i + 1];
    var v: i32[] = t;
    return v[0] + v[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			// The array control for block scope. Clean throughout — the array
			// path consults no escape scan, so it could not have warned anyone about
			// the threading bug the tuple rows caught.
			name: "array_alias_in_a_block",
			src: `function round(i: i32): i32 {
    var t: i32[] = [i, i + 1];
    var acc: i32 = 0;
    { var v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 100, frees: 100,
		},
		{
			// The array control for the conditional. Clean throughout.
			name: "array_alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    var t: i32[] = [i, i + 1];
    var acc: i32 = 0;
    if (i % 2 == 0) { var v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 100, frees: 100,
		},
		{
			// REFUSED — the alias is RETURNED, so a third reference leaves the
			// frame and nothing downstream accounts for it. Unchanged at 8000.
			name: "refused_alias_escapes",
			src: `function sink(q: (i32, i32[])): i32 { return q.1[0]; }
function mk(i: i32): (i32, i32[]) {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    return v;
}
function round(i: i32): i32 { var r: (i32, i32[]) = mk(i); return r.1[0]; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 0,
		},
		{
			// REFUSED — the alias is REASSIGNED, so its final value is not the
			// box the credit describes. Unchanged at 12000.
			name: "refused_alias_reassigned",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    v = (i + 1, xs);
    return v.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 300, frees: 0,
		},
		{
			// NOT WIRED YET, deliberately. The string class is the fourth
			// container and takes the same rule, but it is left for its own change
			// so this one lands on the three that are complete. The row pins that it
			// does not start OVER-releasing in the meantime, which is the direction a
			// careless extension would take it.
			//
			// Native allocates 0 here (SSO, #7351) where the self-host allocates 200;
			// that divergence is not this change's.
			name: "string_alias_still_leaks",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: string = w("ab");
    var v: string = t;
    return v.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 0,
		}}
}

// TestSelfHostContainerAliasBindX86_64 — a plain alias of an rc container shares
// its credit, and the shapes that must stay refused still are.
func TestSelfHostContainerAliasBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "alias_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the alias took a "+
					"DEEP release it did not earn — only the box is retained at the bind)",
					tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER means the alias forgiveness "+
					"stopped reaching this shape (a partial thread shows up as a "+
					"scope-dependent result); MORE on a refused row means it reached "+
					"one it must decline", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostContainerAliasBindWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for the two deep-release rows: an over-release moves
// no byte count on any backend.
func TestSelfHostContainerAliasBindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping container alias-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range containerAliasCases() {
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
			watFile := filepath.Join(dir, "alias_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("container alias-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostContainerAliasBindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostContainerAliasBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "alias_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
