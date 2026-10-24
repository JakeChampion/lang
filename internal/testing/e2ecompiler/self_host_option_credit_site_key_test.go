package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type optKeyCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func optKeyCases() []optKeyCase {
	return []optKeyCase{
		{
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
			name: "optstruct_collide",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
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
			name: "optstruct_renamed",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
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
			name: "optarrarr_collide",
			src: `@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let keep: Option[i32[][]] = Some([id([7, 8]), [9, 10]]);
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
			name: "optarrarr_renamed",
			src: `@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let keep: Option[i32[][]] = Some([id([7, 8]), [9, 10]]);
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
			name: "optarr_collide",
			src: `@noinline function hold(o: Option[i32[]]): i32 { return 0; }
function round(i: i32): i32 {
    let keep: Option[i32[]] = Some([7, 8]);
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[i32[]] = Some([i, i + 1]); t = t + 1 + hold(o); }
    if (i % 2 == 1) { let o: Option[i32[]] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q[0]; }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 20, allocs: 200, frees: 200,
		},
		{
			name: "optarr_renamed",
			src: `@noinline function hold(o: Option[i32[]]): i32 { return 0; }
function round(i: i32): i32 {
    let keep: Option[i32[]] = Some([7, 8]);
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[i32[]] = Some([i, i + 1]); t = t + 1 + hold(o); }
    if (i % 2 == 1) { let u: Option[i32[]] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q[0]; }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 20, allocs: 200, frees: 200,
		},
		{
			name: "optstr_collide",
			src: `@noinline function hold(o: Option[string]): i32 { return 0; }
function round(i: i32): i32 {
    let keep: Option[string] = Some("keeper");
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[string] = Some("fresh"); t = t + 1 + hold(o); }
    if (i % 2 == 1) { let o: Option[string] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 3, allocs: 150, frees: 150,
		},
		{
			name: "optstr_renamed",
			src: `@noinline function hold(o: Option[string]): i32 { return 0; }
function round(i: i32): i32 {
    let keep: Option[string] = Some("keeper");
    let t: i32 = 0;
    if (i % 2 == 0) { let o: Option[string] = Some("fresh"); t = t + 1 + hold(o); }
    if (i % 2 == 1) { let u: Option[string] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 3, allocs: 150, frees: 150,
		},
		{
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
			name: "credited_optaarr",
			src: `function round(i: i32): i32 {
    let xs: Option[i32[]][] = [Some([i, i + 1]), None];
    return xs.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		},
		{
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
			name: "credited_optarr",
			src: `@noinline function hold(o: Option[i32[]]): i32 { return 0; }
function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    return i + 1 + hold(o);
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 200, frees: 200,
		},
		{
			name: "credited_optstr",
			src: `@noinline function hold(o: Option[string]): i32 { return 0; }
function round(i: i32): i32 {
    let o: Option[string] = Some("fresh");
    return i + 1 + hold(o);
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 100, frees: 100,
		},
		{
			name: "binder_forin_collide",
			src: `@noinline function hold(o: Option[i32[]]): i32 { return 0; }
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    { let o: Option[i32[]] = Some([i + 7, i + 8]); t = t + 1 + hold(o); }
    for o in keep { match (o) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 25, allocs: 700, frees: 700,
		},
		{
			name: "binder_forin_renamed",
			src: `@noinline function hold(o: Option[i32[]]): i32 { return 0; }
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    { let o: Option[i32[]] = Some([i + 7, i + 8]); t = t + 1 + hold(o); }
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 25, allocs: 700, frees: 700,
		}}
}

func TestSelfHostOptionCreditSiteKeyX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostOptionCreditSiteKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Option credit site-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
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

func TestSelfHostOptionCreditSiteKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
