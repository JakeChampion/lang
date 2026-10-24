package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const arrFreshMethProlog = "struct H { v: i32 }\n" +
	"function (h: H) mkm(): i32[] { return [h.v, h.v + 1]; }\n"

const arrFreshMethMain = "\nfunction main(): i32 { let hh: H = H { v: 5 }; let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 100) { BODY i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

type arrFreshMethCase struct {
	name string
	src  string
	want int
}

func arrFreshMethBody(body string) string {
	return arrFreshMethProlog + strings.Replace(arrFreshMethMain, "BODY", body, 1)
}

func arrFreshMethCases() []arrFreshMethCase {
	return []arrFreshMethCase{
		{
			name: "method_len",
			src:  arrFreshMethBody("t = t + hh.mkm().len();"),
			want: 34,
		},
		{
			name: "method_discarded",
			src:  arrFreshMethBody("hh.mkm();"),
			want: 0,
		},
		{
			name: "method_index_read",
			src:  arrFreshMethBody("t = t + hh.mkm()[0];"),
			want: 2,
		},
		{
			name: "method_result_bound",
			src:  arrFreshMethBody("let a: i32[] = hh.mkm(); t = t + a[0] + a.len();"),
			want: 36,
		},
		{
			name: "method_derived_from_field",
			src: "struct G { xs: i32[] }\n" +
				"function (g: G) copy(): i32[] { return [g.xs[0], g.xs[1]]; }\n" +
				"function main(): i32 { let gg: G = G { xs: [1, 2, 3] }; let t: i32 = 0; let i: i32 = 0; " +
				"while (i < 100) { t = t + gg.copy().len(); i = i + 1; } " +
				"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }",
			want: 34,
		},
	}
}

func TestSelfHostArrFreshRetMethodX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrFreshMethCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrfreshmeth_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a reclaim site "+
					"released a buffer a live binding still owns)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — the returned buffer is allocated per round, so a "+
					"missing release scales with the loop", tc.name, summary)
			}
		})
	}
}

func TestSelfHostArrFreshRetMethodRefusedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := "struct H { xs: i32[] }\n" +
		"function (h: H) get(): i32[] { return h.xs; }\n" +
		"function main(): i32 { let hh: H = H { xs: [1, 2, 3] }; let t: i32 = 0; let i: i32 = 0; " +
		"while (i < 100) { t = t + hh.get().len(); i = i + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "arrfreshmeth_borrowed_field_return", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 51 {
		t.Fatalf("borrowed_field_return exited %d, want 51 (99 = rc underflow: the "+
			"registry admitted a method that hands back its receiver's own buffer)", exit)
	}
	allocs, frees, live := parseLeakcheck(t, "borrowed_field_return", stderr)
	if live != 0 || allocs != frees {
		t.Errorf("borrowed_field_return: allocs=%d frees=%d live_bytes=%d, want a balanced census",
			allocs, frees, live)
	}
}

func TestSelfHostArrFreshRetMethodWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh-ret method wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrFreshMethCases() {
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
			watFile := filepath.Join(dir, "arrfreshmeth_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("fresh-ret method wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostArrFreshRetMethodIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrFreshMethCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "arrfreshmeth_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
