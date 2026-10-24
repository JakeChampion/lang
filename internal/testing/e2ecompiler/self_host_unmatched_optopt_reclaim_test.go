package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type unmatchedOptoptCase struct {
	name string
	src  string
	want int
}

const unmatchedOptoptMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func unmatchedOptoptCases() []unmatchedOptoptCase {
	return []unmatchedOptoptCase{
		{
			name: "unmatched_nested_option",
			src: `@noinline function hold(o: Option[Option[i32]]): i32 { return 0; }
function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    return i % 7 + hold(o);
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			name: "unmatched_nested_none",
			src: `@noinline function hold(o: Option[Option[i32]]): i32 { return 0; }
function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(None);
    return i % 7 + hold(o);
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			name: "matched_control",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63,
		},
		{
			name: "matched_wildcard_control",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 4,
		},
		{
			name: "reassigned_unmatched",
			src: `@noinline function hold(o: Option[Option[i32]]): i32 { return 0; }
function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(Some(i + 1)); }
    return i % 7 + hold(o);
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			name: "reassigned_matched",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(Some(i + 1)); }
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 80,
		},
		{
			name: "reassigned_matched_multi_rebind",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    o = Some(Some(i + 1));
    if (i % 2 == 0) { o = Some(Some(i + 2)); }
    o = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19,
		},
		{
			name: "refuses_rebind_aliasing_inner",
			src: `function round(i: i32): i32 {
    let keep: Option[i32] = Some(i);
    let o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(keep); }
    match (keep) { Some(v) => { return v; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63,
		},
		{
			name: "rc_inner_unmatched",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some(w("ab")));
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			name: "rc_inner_none_payload",
			src: `@noinline function hold(o: Option[Option[string]]): i32 { return 0; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    return i % 7 + hold(o);
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			name: "rc_inner_matched_balances",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19,
		},
		{
			name: "rc_inner_matched_literal",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some("ab"));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 68,
		},
		{
			name: "rc_inner_matched_none",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19,
		},
		{
			name: "refuses_aliased_inner",
			src: `function round(i: i32): i32 {
    let inner: Option[i32] = Some(i);
    let o: Option[Option[i32]] = Some(inner);
    match (inner) { Some(v) => { return v; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63,
		},
	}
}

func TestSelfHostUnmatchedOptoptX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "unmoptopt_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the inner box "+
					"released under a live reference)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
		})
	}
}

func TestSelfHostUnmatchedOptoptWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping unmatched nested-Option wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
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
			watFile := filepath.Join(dir, "unmoptopt_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("unmatched nested-Option wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostUnmatchedOptoptIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "unmoptopt_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("unmatched nested-Option arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
