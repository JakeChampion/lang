package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type earlyReturnDropCase struct {
	name string
	src  string
	want int
}

func earlyReturnDropMain(rounds string) string {
	return "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
		"while (i < " + rounds + ") { t = t + round(i); i = i + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"
}

func earlyReturnDropCases() []earlyReturnDropCase {
	const arrEarly = `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) { return 5; }
    match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
    return 0;
}`
	return []earlyReturnDropCase{
		{
			name: "arr_early_return",
			src:  arrEarly + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			name: "arr_early_return_400_rounds",
			src:  arrEarly + earlyReturnDropMain("400"),
			want: 8,
		},
		{
			name: "arr_return_after_match",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let src: Option[i32[]] = Some([i, i + 1]);
    match (src) { Some(b) => { t = b.len(); }, None => { t = 2; } }
    if (i >= 0) { return t + 5; }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 36,
		},
		{
			name: "rcenum_early_return",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    let src: E = E.Full([i, i + 1]);
    if (i >= 0) { return 5; }
    match (src) { E.Full(b) => { return b.len(); }, E.None => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			name: "scalar_enum_early_return",
			src: `enum S { A(i32), B }
function round(i: i32): i32 {
    let src: S = S.A(i);
    if (i >= 0) { return 5; }
    match (src) { S.A(b) => { return b; }, S.B => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			name: "str_early_return_unchanged",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: Option[string] = Some(w("ab"));
    if (i >= 0) { return 5; }
    match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			name: "early_return_conditional",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { return 5; }
    let t: i32 = 0;
    match (src) { Some(b) => { t = b.len(); }, None => { t = 2; } }
    return t;
}` + earlyReturnDropMain("100"),
			want: 18,
		},
		{
			name: "early_return_inside_a_loop",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let k: i32 = 0;
    while (k < 3) { if (k == 1) { return 5; } k = k + 1; }
    let t: i32 = 0;
    match (src) { Some(b) => { t = b.len(); }, None => { t = 2; } }
    return t;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			name: "nested_block_early_return",
			src: `function round(i: i32): i32 {
    if (i >= 0) {
        let src: Option[i32[]] = Some([i, i + 1]);
        if (i >= 0) { return 5; }
        match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
    }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			name: "struct_payload_early_return",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let src: Option[P] = Some(P { xs: [i, i + 1], n: i });
    if (i >= 0) { return 5; }
    match (src) { Some(p) => { return p.n % 7; }, None => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
	}
}

func TestSelfHostEarlyReturnConsumingDropX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range earlyReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "earlyret_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the pending entry "+
					"and the post-match drop both fired on one path)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A short "+
					"free count is a `return` jumping the post-match drop with nothing "+
					"armed in its place", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "earlyret_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

func TestSelfHostEarlyReturnConsumingDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping early-return consuming-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range earlyReturnDropCases() {
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
			watFile := filepath.Join(dir, "earlyret_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("early-return consuming drop wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostEarlyReturnConsumingDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range earlyReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "earlyret_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("early-return consuming drop arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
