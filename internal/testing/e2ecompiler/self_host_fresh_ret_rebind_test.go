package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const freshRetRebindDecl = `struct S { name: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mk(i: i32): S { return S { name: w("k"), n: i }; }
function churn(i: i32): i32 {
    let a: string = w("chunkA");
    let b: string = w("chunkB");
    return a.len() + b.len() + i;
}
`

const freshRetRebindMain = `
function main(): i32 {
    let acc: i32 = 0; let r: i32 = 0;
    while (r < 200) { acc = acc + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

const freshRetRebindCheckedMain = `
function main(): i32 {
    let acc: i32 = 0; let r: i32 = 0; let bad: i32 = 0;
    while (r < 200) { let v: i32 = round(r); if (v < 0) { bad = bad + 1; } acc = acc + v; r = r + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

type freshRetRebindCase struct {
	name string
	src  string
	want int
}

func freshRetRebindCases() []freshRetRebindCase {
	return []freshRetRebindCase{
		{
			name: "call_init_rebind",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    let s: S = mk(i);
    s = mk(i + 1);
    return (s.name.len() + s.n) % 101;
}` + freshRetRebindMain,
			want: 79,
		},
		{
			name: "call_init_literal_rebind",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    let s: S = mk(i);
    s = S { name: w("z"), n: i + 1 };
    return (s.name.len() + s.n) % 101;
}` + freshRetRebindMain,
			want: 79,
		},
		{
			name: "literal_init_control",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    let s: S = S { name: w("k"), n: i };
    s = S { name: w("z"), n: i + 1 };
    return (s.name.len() + s.n) % 101;
}` + freshRetRebindMain,
			want: 79,
		},
		{
			name: "scalar_struct_rebind",
			src: `struct N { a: i32, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkn(i: i32): N { let junk: string = w("x"); return N { a: junk.len(), n: i }; }
function round(i: i32): i32 {
    let s: N = mkn(i);
    s = mkn(i + 1);
    return (s.a + s.n) % 101;
}` + freshRetRebindMain,
			want: 79,
		},
		{
			name: "arr_field_rebind",
			src: `struct A { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mka(i: i32): A { let o: string[] = []; o = o.append(w("a")); return A { f: o, n: i }; }
function churn(i: i32): i32 { let a: string = w("chunkA"); let b: string = w("chunkB"); return a.len() + b.len() + i; }
function round(i: i32): i32 {
    let s: A = mka(i);
    s = mka(i + 1);
    let j: i32 = churn(i);
    if (s.f[0].len() != 31) { return 0 - 1; }
    return (s.f.len() + s.n + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 81,
		},
		{
			name: "loop_rebind",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    let s: S = mk(i);
    let k: i32 = 0;
    while (k < 3) { s = mk(i + k); k = k + 1; }
    let j: i32 = churn(i);
    if (s.name.len() != 31) { return 0 - 1; }
    return (s.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
		{
			name: "alias_into_container",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    let s: S = mk(i);
    let keep: S[] = [];
    keep = keep.append(s);
    s = mk(i + 1);
    let j: i32 = churn(i);
    if (keep[0].name.len() != 31) { return 0 - 1; }
    return (s.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
		{
			name: "field_moved_out",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    let s: S = mk(i);
    let held: string = s.name;
    s = mk(i + 1);
    let j: i32 = churn(i);
    if (held.len() != 31) { return 0 - 1; }
    return (s.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
		{
			name: "rebind_then_return",
			src: freshRetRebindDecl + `function build(i: i32): S {
    let s: S = mk(i);
    s = mk(i + 1);
    return s;
}
function round(i: i32): i32 {
    let p: S = build(i);
    let j: i32 = churn(i);
    if (p.name.len() != 31) { return 0 - 1; }
    return (p.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
	}
}

func TestSelfHostFreshRetRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "frrb_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a rebind released a box "+
					"it did not own; 100 = a held string read back short, i.e. the release "+
					"beat a live reader; 139 = it read freed memory)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}

func TestSelfHostFreshRetRebindSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			progBin := buildBin(t, gcc, dir, "frrbsan_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s under FERN_SANITIZE exited %d, want %d "+
					"(134 = the quarantine caught a write to freed memory)", tc.name, exit, tc.want)
			}
		})
	}
}

func TestSelfHostFreshRetRebindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh-ret rebind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
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
			watFile := filepath.Join(dir, "frrb_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("fresh-ret rebind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostFreshRetRebindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "frrb_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
