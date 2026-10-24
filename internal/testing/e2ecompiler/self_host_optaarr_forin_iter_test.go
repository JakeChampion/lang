package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

var optaarrForInCases = []struct {
	name string
	src  string
	want int
}{
	{"forin-repro-50", optaarrProg(50, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}`), 10},
	{"forin-repro-200", optaarrProg(200, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}`), 10},
	{"forin-none-mixed", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), None, Some([i + 2, i + 3])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0] + p.len(); }, None => { t = t + 1; } } }
    return t + keep.len();
}`), 16},
	{"forin-f64-payload", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[f64[]][] = [Some([1.0, 2.0]), Some([3.0, 4.0])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p.len(); }, None => {} } }
    return t + keep.len() + i - i;
}`), 6},
	{"len-only-control", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    return keep.len();
}`), 2},
	{"index-match-control", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    match (keep[0]) { Some(p) => { t = t + p[0]; }, None => {} }
    return t + keep.len();
}`), 5},

	{"guarded-arm-refused", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) when p[0] > 100000 => { t = t + 7; }, Some(q) => { t = t + q[0]; }, None => {} } }
    return t + keep.len();
}`), 10},
	{"payload-escapes-arm-refused", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let held: i32[] = [0];
    for e in keep { match (e) { Some(p) => { held = p; }, None => {} } }
    return held[0] + keep.len();
}`), 7},
	{"payload-to-call-refused", optaarrProg(150, `
function sink(xs: i32[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + sink(p); }, None => {} } }
    return t + keep.len();
}`), 6},
	{"box-escapes-loop-uaf", `
function pick(i: i32): Option[i32[]] {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let last: Option[i32[]] = None;
    for e in keep { last = e; }
    return last;
}

function round(i: i32): i32 {
    let o: Option[i32[]] = pick(i);
    let a: i32[] = [7777, 7777];
    let b: i32[] = [7777, 7777];
    let c: i32[] = [7777, 7777];
    let t: i32 = 0 - 9;
    match (o) { Some(p) => { t = p[0]; }, None => { t = 0 - 5; } }
    return t - i - 2 + a[0] - b[0] + c[0] - 7777;
}

function main(): i32 {
    let i: i32 = 0;
    let bad: i32 = 0;
    while (i < 150) { if (round(i) != 0) { bad = bad + 1; } i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"elem-bind-refused", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    let o = keep[0];
    let t: i32 = 0;
    match (o) { Some(p) => { t = t + p[0]; }, None => {} }
    return t + keep.len();
}`), 5},
	{"string-payload-out-of-class", optaarrProg(150, `
function round(i: i32): i32 {
    let pre: string = "abcdefgh";
    let keep: Option[string[]][] = [Some([pre + "x", pre + "y"]), Some([pre + "z"])];
    let t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p.len(); }, None => {} } }
    return t + keep.len() + i - i;
}`), 5},
	{"nested-option-out-of-class", optaarrProg(150, `
function round(i: i32): i32 {
    let keep: Option[Option[i32[]]][] = [Some(Some([i, i + 1])), Some(None)];
    let t: i32 = 0;
    for e in keep { match (e) { Some(inner) => { match (inner) { Some(p) => { t = t + p[0]; }, None => { t = t + 1; } } }, None => {} } }
    return t + keep.len();
}`), 6},
}

func optaarrProg(rounds int, round string) string {
	n := strconv.Itoa(rounds)
	return round + `
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) { acc = (acc + round(i)) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return round(3);
}`
}

func TestSelfHostOptaarrForInIterX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range optaarrForInCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optaarrforin_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the payload read back wrong; "+
					"97 = value corrupted; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must reclaim everything", tc.name, summary)
			}
		})
	}
}

func TestSelfHostOptaarrForInIterArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range optaarrForInCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "optaarrforin_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (99 = over-release/underflow; 100 = the payload read "+
					"back wrong; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostOptaarrForInIterWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping optaarr for-in wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range optaarrForInCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "optaarrforin_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (99 = over-release/underflow; 100 = the payload read "+
					"back wrong; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
