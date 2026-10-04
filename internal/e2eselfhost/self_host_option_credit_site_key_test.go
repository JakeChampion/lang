package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The Option-family reclaim credits, keyed on the binding (#7253 step 1) --
//
// A name has no scope, so two same-named Option locals in sibling blocks must
// each keep their own reclaim verdict; a collision releases a payload the other
// local owns. That shows as exit 99 on __rc_underflow_count() where the source
// has another owner, and as the colliding row's census differing from its
// rename control where it does not. The census alone cannot see the first, so
// every row is asserted on the exit code AND on exact counts. On the typed
// lowering every pair has identical, balanced censuses.
//
// The `credited_*` rows pin that every class still reclaims where there is no
// collision.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type optKeyCase struct {
	name string
	src  string
	// want is the exit code; 99 means the rc over-release detector fired.
	want int
	// allocs/frees are the SELF-HOST's numbers. Several of these shapes carry
	// residual leaks belonging to other issues (#7351's string doubling among
	// them), so a balance assertion would pin the wrong thing.
	allocs int64
	frees  int64
}

func optKeyCases() []optKeyCase {
	return []optKeyCase{
		{
			// Two `let o` in sibling `if` arms: the first a fresh
			// Some((..)), the second a bare alias of a local that outlives
			// the block AND is released elsewhere.
			name: "opttup_collide",
			src: `
function round(b: Option[(i32, i32[])], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { t = t + p.0; }, None => {} } }
    if (i % 2 == 1) { let o: Option[(i32, i32[])] = b; match (o) { Some(p) => { t = t + p.0; }, None => {} } }
    return t;
}

function main(): i32 {
    let b: Option[(i32, i32[])] = Some((7, [7, 8]));
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 61, allocs: 152, frees: 152,
		},
		{
			// The pairwise control — the same program with the second local
			// named `u`. The colliding program must measure identically to
			// it, exit code and census alike.
			name: "opttup_renamed",
			src: `
function round(b: Option[(i32, i32[])], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { t = t + p.0; }, None => {} } }
    if (i % 2 == 1) { let u: Option[(i32, i32[])] = b; match (u) { Some(p) => { t = t + p.0; }, None => {} } }
    return t;
}

function main(): i32 {
    let b: Option[(i32, i32[])] = Some((7, [7, 8]));
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 61, allocs: 152, frees: 152,
		},
		{
			// The same shape on an Option of a struct. `b`'s array goes
			// through `id`, which hides the constant from the static-box plan, so its P is a
			// heap box rather than a static one.
			name: "optstruct_collide",
			src: `struct P { xs: i32[] }
function id(xs: i32[]): i32[] { return xs; }
function round(b: Option[P], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[P] = Some(P { xs: [i, i + 1] }); match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    if (i % 2 == 1) { let o: Option[P] = b; match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    return t;
}

function main(): i32 {
    let b: Option[P] = Some(P { xs: id([7, 8]) });
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 34, allocs: 152, frees: 152,
		},
		{
			// Its pairwise control.
			name: "optstruct_renamed",
			src: `struct P { xs: i32[] }
function id(xs: i32[]): i32[] { return xs; }
function round(b: Option[P], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[P] = Some(P { xs: [i, i + 1] }); match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    if (i % 2 == 1) { let u: Option[P] = b; match (u) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    return t;
}

function main(): i32 {
    let b: Option[P] = Some(P { xs: id([7, 8]) });
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 34, allocs: 152, frees: 152,
		},
		{
			// The same shape on an Option of a struct array. A collision
			// here does not show in the census at all, only in the exit
			// code (99).
			name: "optaarr_collide",
			src: `function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([7, 8]), None];
    let t: i32 = 0;
    if (i % 2 == 0) { let xs: Option[i32[]][] = [Some([i, i + 1]), None]; t = t + xs.len(); }
    if (i % 2 == 1) { let xs: Option[i32[]][] = keep; t = t + xs.len(); }
    return t + keep.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 350, frees: 350,
		},
		{
			// Its pairwise control.
			name: "optaarr_renamed",
			src: `function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([7, 8]), None];
    let t: i32 = 0;
    if (i % 2 == 0) { let xs: Option[i32[]][] = [Some([i, i + 1]), None]; t = t + xs.len(); }
    if (i % 2 == 1) { let ys: Option[i32[]][] = keep; t = t + ys.len(); }
    return t + keep.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 350, frees: 350,
		},
		{
			// The same shape on an Option of an array of arrays, whose
			// source has no other owner: a stray release would show as the
			// census moving away from its rename control, not as an
			// underflow.
			name: "optarrarr_collide",
			src: `
function round(i: i32): i32 {
    let keep: Option[i32[][]] = Some([[7, 8], [9, 10]]);
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(p) => { t = t + p.len(); }, None => {} } }
    if (i % 2 == 1) { let o: Option[i32[][]] = keep; match (o) { Some(p) => { t = t + p.len(); }, None => {} } }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 400, frees: 400,
		},
		{
			// Its pairwise control.
			name: "optarrarr_renamed",
			src: `
function round(i: i32): i32 {
    let keep: Option[i32[][]] = Some([[7, 8], [9, 10]]);
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(p) => { t = t + p.len(); }, None => {} } }
    if (i % 2 == 1) { let u: Option[i32[][]] = keep; match (u) { Some(p) => { t = t + p.len(); }, None => {} } }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 400, frees: 400,
		},
		{
			// The same shape on an Option of an array (the unmatched half).
			name: "optarr_collide",
			src: `
function round(i: i32): i32 {
    let keep: Option[i32[]] = Some([7, 8]);
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[i32[]] = Some([i, i + 1]); t = t + 1; }
    if (i % 2 == 1) { let o: Option[i32[]] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q[0]; }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 20, allocs: 200, frees: 200,
		},
		{
			// Its pairwise control.
			name: "optarr_renamed",
			src: `
function round(i: i32): i32 {
    let keep: Option[i32[]] = Some([7, 8]);
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[i32[]] = Some([i, i + 1]); t = t + 1; }
    if (i % 2 == 1) { let u: Option[i32[]] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q[0]; }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 20, allocs: 200, frees: 200,
		},
		{
			// The same shape on an Option of a string.
			name: "optstr_collide",
			src: `function round(i: i32): i32 {
    let keep: Option[string] = Some("keeper");
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[string] = Some("fresh"); t = t + 1; }
    if (i % 2 == 1) { let o: Option[string] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 3, allocs: 150, frees: 150,
		},
		{
			// Its pairwise control.
			name: "optstr_renamed",
			src: `function round(i: i32): i32 {
    let keep: Option[string] = Some("keeper");
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[string] = Some("fresh"); t = t + 1; }
    if (i % 2 == 1) { let u: Option[string] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 3, allocs: 150, frees: 150,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling.
			// This is the silent half of a key migration: a site key that resolves
			// to nothing denies the credit, which no exit code would show. All six
			// of these must keep balancing at live_bytes 0.
			name: "credited_opttup",
			src: `function round(i: i32): i32 {
    let o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    let t: i32 = 0;
    match (o) { Some(p) => { t = t + p.0; }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 53, allocs: 300, frees: 300,
		},
		{
			// Positive control.
			name: "credited_optstruct",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    let o: Option[P] = Some(P { xs: [i, i + 1] });
    let t: i32 = 0;
    match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		},
		{
			// Positive control.
			name: "credited_optaarr",
			src: `function round(i: i32): i32 {
    let xs: Option[i32[]][] = [Some([i, i + 1]), None];
    return xs.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		},
		{
			// Positive control.
			name: "credited_optarrarr",
			src: `function round(i: i32): i32 {
    let o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]);
    let t: i32 = 0;
    match (o) { Some(p) => { t = t + p.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// Positive control.
			name: "credited_optarr",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    return i + 1;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 200, frees: 200,
		},
		{
			// Positive control.
			name: "credited_optstr",
			src: `function round(i: i32): i32 {
    let o: Option[string] = Some("fresh");
    return i + 1;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 100, frees: 100,
		},
		{
			// A `for` element binder in one block and a same-named `let o`
			// in the sibling: the binder must not inherit the var's verdict
			// and release elements of `keep` that `keep` still owns.
			name: "binder_forin_collide",
			src: `function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    { let o: Option[i32[]] = Some([i + 7, i + 8]); t = t + 1; }
    for o in keep { match (o) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 25, allocs: 700, frees: 700,
		},
		{
			// Its pairwise control — the element binder named `e`. It and the
			// colliding row move together, which is the property this pair
			// exists to assert.
			name: "binder_forin_renamed",
			src: `function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    { let o: Option[i32[]] = Some([i + 7, i + 8]); t = t + 1; }
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 25, allocs: 700, frees: 700,
		}}
}

// TestSelfHostOptionCreditSiteKeyX86_64 — each Option binding resolves the
// credit it earned itself, and no same-named sibling inherits one.
func TestSelfHostOptionCreditSiteKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a same-named "+
					"Option local inherited another binding's reclaim credit)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want allocs=%d. A change in what is ALLOCATED means the "+
					"probe stopped measuring this shape", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostOptionCreditSiteKeyWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for the three faulting rows: an over-release moves
// no byte count on any backend.
func TestSelfHostOptionCreditSiteKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Option credit site-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
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
			watFile := filepath.Join(dir, "optkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("Option credit site-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOptionCreditSiteKeyIRArm64 — the arm64 sibling under qemu.
func TestSelfHostOptionCreditSiteKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "optkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
