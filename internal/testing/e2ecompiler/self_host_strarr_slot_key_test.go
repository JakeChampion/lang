package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type strarrKeyCase struct {
	name string
	src  string
	want int
}

const strarrW = "function w(a: string): string { return a + \"!\"; }\n" +
	"function mk(): string[] { let a: string[] = [w(\"p\"), w(\"q\")]; return a; }\n"

const strarrMain = "\nfunction main(): i32 { let b: string[] = [w(\"a\"), w(\"b\")]; " +
	"let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func strarrKeyCases() []strarrKeyCase {
	return []strarrKeyCase{
		{
			name: "param_alias",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: string[] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { let v: string[] = base;  t = t + v.len(); }
    return t;
}` + strarrMain,
			want: 34,
		},
		{
			name: "param_rename",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: string[] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { let u: string[] = base;  t = t + u.len(); }
    return t;
}` + strarrMain,
			want: 34,
		},
		{
			name: "struct_field_alias",
			src: `struct H { xs: string[] }
` + strarrW + `function round(h: H, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: string[] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { let v: string[] = h.xs;  t = t + v.len(); }
    return t;
}
function main(): i32 { let h: H = H { xs: [w("a"), w("b")] }; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(h, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34,
		},
		{
			name: "fresh_only",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: string[] = mk();  t = t + v.len(); }
    return t;
}` + strarrMain,
			want: 17,
		},
		{
			name: "alias_only",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 1) { let v: string[] = base;  t = t + v.len(); }
    return t;
}` + strarrMain,
			want: 17,
		},
		{
			name: "scalar_arr_unaffected",
			src: `function mkI(n: i32): i32[] { let a: i32[] = [n, n + 1, n + 2]; return a; }
function round(base: i32[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: i32[] = mkI(i); t = t + v.len(); }
    if (i % 2 == 1) { let v: i32[] = base;   t = t + v.len(); }
    return t;
}
function main(): i32 { let b: i32[] = [7, 8, 9]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 51,
		},
	}
}

func TestSelfHostStrArrSlotKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	summaries := map[string]string{}
	for _, tc := range strarrKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: an aliasing string[] "+
					"local inherited a same-named sibling's SARR: credit and freed a live "+
					"buffer)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			summaries[tc.name] = summary
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path. "+
					"Every string here goes through w(); a constant-folded one is an "+
					"immortal literal (rc = -1) and measures nothing", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0; a site key that "+
					"resolves to no credit leaks here, which the exit code cannot show",
					tc.name, summary)
			}
		})
	}

	if a, b := summaries["param_alias"], summaries["param_rename"]; a != "" && b != "" && a != b {
		t.Errorf("param_alias and param_rename must measure identically once the credit "+
			"is keyed on the binding — the programs differ only by one identifier:\n"+
			"  param_alias  %s\n  param_rename %s", a, b)
	}
}

func TestSelfHostStrArrSlotKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[] slot-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strarrKeyCases() {
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
			watFile := filepath.Join(dir, "strarrkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("string[] slot-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStrArrSlotKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strarrKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "strarrkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("string[] slot-key arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
