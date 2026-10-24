package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostWasmIRWasiHelpers(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IR WASI-helper e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		src    string
		stdout string
		oracle bool
	}{
		{
			name:   "env-some",
			src:    `function main(): i32 { match (env("FERNTEST")) { Some(v) => { write(v); return 0; }, None => { write("none"); return 0; } } return 1; }`,
			stdout: "hello123",
		},
		{
			name:   "env-none",
			src:    `function main(): i32 { match (env("FERN_NOT_SET")) { Some(v) => { write(v); return 0; }, None => { write("none"); return 0; } } return 1; }`,
			stdout: "none",
		},
		{
			name:   "args-index",
			src:    `function main(): i32 { let a = args(); write(a[1]); return 0; }`,
			stdout: "ALPHA",
		},
		{
			name:   "write-file-roundtrip",
			src:    `function main(): i32 { match (write_file("rt.txt", "roundtrip!")) { Err(e) => { return 1; }, Ok(_) => { } } match (read_file("rt.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 2; } } return 3; }`,
			stdout: "roundtrip!",
		},
		{
			name:   "clock-monotonic-non-decreasing",
			src:    `function main(): i32 { let a: i64 = monotonic_ns(); let b: i64 = monotonic_ns(); if (b >= a) { print_int(1); } else { print_int(0); } return 0; }`,
			stdout: "1",
		},
		{
			name:   "clock-now-ns-positive",
			src:    `function main(): i32 { let t: i64 = now_ns(); if (t > 0) { print_int(1); } else { print_int(0); } return 0; }`,
			stdout: "1",
		},
		{
			name:   "const-i64",
			src:    `const BIG: i64 = 5000000000; function main(): i32 { print_i64(BIG + 1); return (BIG % 97) as i32; }`,
			stdout: "5000000001",
			oracle: true,
		},
		{
			name:   "const-f64",
			src:    `const HALF: f64 = 3.5; function main(): i32 { return (HALF * 2.0) as i32; }`,
			oracle: true,
		},
		{
			name:   "const-i32-unchanged",
			src:    `const N: i32 = 41; function main(): i32 { return N + 1; }`,
			oracle: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(withPrintInt(tc.src)))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}

			rcmd := exec.Command("wasmtime", "run",
				"--env", "FERNTEST=hello123", "--dir", dir, watFile, "ALPHA", "BETA")
			out, _ := rcmd.Output()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (likely an unlinkable module — the bug):\n%s", wat)
			}
			want := 0
			if tc.oracle {
				want = interpExit(t, interpBin, withPrintInt(tc.src))
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s: wasm exited %d, want %d", tc.name, got, want)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q", tc.name, string(out), tc.stdout)
			}
		})
	}
}
