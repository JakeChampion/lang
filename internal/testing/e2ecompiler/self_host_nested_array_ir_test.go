package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var nestedArrayIRCases = []struct {
	name string
	main string
}{
	{"read-2x2", `function main(): i32 { let m: i32[][] = [[1, 2], [3, 4]]; return m[0][1] + m[1][0]; }`},
	{"row-len", `function main(): i32 { let m: i32[][] = [[1, 2, 3], [4, 5]]; return m.len() + m[0].len(); }`},
	{"loop-sum", `function main(): i32 { let m: i32[][] = [[1, 2], [3, 4], [5, 6]]; let s = 0; let i = 0; while (i < m.len()) { let r = m[i]; let j = 0; while (j < r.len()) { s = s + r[j]; j = j + 1; } i = i + 1; } return s; }`},
	{"string-nested", `function main(): i32 { let g: string[][] = [["ab", "c"], ["de"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len(); }`},
	{"f64-2x2", `function main(): i32 { let m: f64[][] = [[2.5], [3.5]]; return (m[0][0] * 2.0) as i32; }`},
}

var nestedArrayI64IRCases = []struct {
	name string
	main string
}{
	{"i64-2x2", `function main(): i32 { let m: i64[][] = [[5000000000], [6000000000]]; return (m[0][0] / 1000000000) as i32; }`},
	{"i64-row-binding", `function main(): i32 { let m: i64[][] = [[5000000000, 1000000000], [2000000000]]; let r = m[0]; return (r[0] / 1000000000) as i32 + (r[1] / 1000000000) as i32; }`},
	{"i64-forin", `function main(): i32 { let m: i64[][] = [[5000000000], [6000000000]]; let t: i64 = 0; for row in m { for x in row { t = t + x; } } return (t / 1000000000) as i32; }`},
}

var nestedArrayStructIRCases = []struct {
	name string
	main string
}{
	{"struct-field", `struct P{x:i32,y:i32} function main(): i32 { let a: P[][] = [[P{x:3,y:4}]]; return a[0][0].x + a[0][0].y; }`},
	{"struct-field-var-idx", `struct P{x:i32} function main(): i32 { let a: P[][] = [[P{x:5},P{x:6}]]; let i = 0; let j = 1; return a[i][j].x; }`},
	{"struct-method", `struct P{x:i32} function (p: P) val(): i32 { return p.x*2; } function main(): i32 { let a: P[][] = [[P{x:3}]]; return a[0][0].val(); }`},
	{"struct-i64-field", `struct P{x:i64} function main(): i32 { let a: P[][] = [[P{x:5000000000}]]; return (a[0][0].x / 1000000000) as i32; }`},
	{"enum-method", `enum Sh{C(i32)} function (s: Sh) area(): i32 { match(s){Sh.C(r)=>{return r*r;}} } function main(): i32 { let a: Sh[][] = [[Sh.C(3)]]; return a[0][0].area(); }`},
}

func TestSelfHostNestedArrayIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	x86Cases := append(append(append([]struct {
		name string
		main string
	}{}, nestedArrayIRCases...), nestedArrayI64IRCases...), nestedArrayStructIRCases...)

	for _, tc := range x86Cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostNestedArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-array wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	wasmCases := append(append(append([]struct {
		name string
		main string
	}{}, nestedArrayIRCases...), nestedArrayI64IRCases...), nestedArrayStructIRCases...)

	for _, tc := range wasmCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "nested_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested-array wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
