package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type armReturnDropCase struct {
	name string
	src  string
	want int
}

const armReturnDropMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func armReturnDropCases() []armReturnDropCase {
	return []armReturnDropCase{
		{
			name: "optopt_return_in_arm",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			name: "optopt_return_after_match",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { t = v.len(); }, None => { t = 3; } } }, None => { t = 2; } }
    return t;
}` + armReturnDropMain,
			want: 19,
		},
		{
			name: "optopt_return_in_arm_nested_block",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    if (i >= 0) {
        let o: Option[Option[string]] = Some(Some(w("ab")));
        match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			name: "optopt_none_return_in_arm",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			name: "optstruct_return_in_arm",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { return p.n % 7; }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 13,
		},
		{
			name: "optstruct_return_after_match",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let t: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { t = p.n % 7; }, None => { t = 2; } }
    return t;
}` + armReturnDropMain,
			want: 13,
		},
		{
			name: "optstruct_return_in_arm_nested_block",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    if (i >= 0) {
        let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
        match (o) { Some(p) => { return p.n % 7; }, None => { return 2; } }
    }
    return 0;
}` + armReturnDropMain,
			want: 13,
		},
		{
			name: "str_return_in_arm",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(s) => { return s.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			name: "strarr_return_in_arm",
			src: `function round(i: i32): i32 {
    let o: Option[string[]] = Some(["a" + "b", "c" + "d"]);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 68,
		},
		{
			name: "tagged_return_in_arm",
			src: `function mk(i: i32): Option[i32[]] { if (i % 2 == 0) { return None; } return Some([i, i + 1]); }
function round(i: i32): i32 {
    let o: Option[i32[]] = mk(i);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 68,
		},
		{
			name: "arr_return_in_arm",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 68,
		},
	}
}

func TestSelfHostArmReturnConsumingDropX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range armReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "armretdrop_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the payload "+
					"released by both the return-path sweep and the post-match drop)",
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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A "+
					"short free count is the return path taking a shallower release "+
					"than the post-match drop selected", tc.name, summary)
			}
		})
	}
}

func TestSelfHostArmReturnConsumingDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arm-return consuming-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range armReturnDropCases() {
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
			watFile := filepath.Join(dir, "armretdrop_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("arm-return consuming drop wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostArmReturnConsumingDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range armReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "armretdrop_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("arm-return consuming drop arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
