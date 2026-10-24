package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostErasedWideContainerWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide container wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	const some = `function some1[T](x: T): Option[T] { return Some(x); }`
	const dup = `function dup[T](x: T): T[] { return [x, x]; }`
	const okr = `function okr[T](x: T): Result[T, string] { return Ok(x); }`
	const errg = `function errg[E](e: E): Result[i32, E] { return Err(e); }`
	const okg = `function okg[T, E](x: T): Result[T, E] { return Ok(x); }`
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"opt-i64",
			some + ` function main(): i32 { let a: Option[i64] = some1[i64](5000000000 as i64); let r: i64 = 0; match (a) { Some(v) => { r = v; }, None => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		{"opt-f64",
			some + ` function main(): i32 { let a: Option[f64] = some1[f64](2.5); let r: f64 = 0.0; match (a) { Some(v) => { r = v; }, None => {} } return (r * 2.0) as i32 + 37; }`,
			42},
		{"opt-bare-literal",
			some + ` function main(): i32 { let a: Option[i64] = some1(5000000000); let r: i64 = 0; match (a) { Some(v) => { r = v; }, None => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		{"arr-i64",
			dup + ` function main(): i32 { let a: i64[] = dup[i64](5000000000 as i64); if (a.len() == 2 && a[0] == 5000000000 as i64 && a[1] == 5000000000 as i64) { return 42; } return 38; }`,
			42},
		{"opt-string",
			some + ` function main(): i32 { let a: Option[string] = some1[string]("hello"); let r: i32 = 0; match (a) { Some(v) => { r = v.len(); }, None => {} } return r + 37; }`,
			42},
		{"opt-i32",
			some + ` function main(): i32 { let a: Option[i32] = some1[i32](5); let r: i32 = 0; match (a) { Some(v) => { r = v; }, None => {} } return r + 37; }`,
			42},
		{"result-ok-wide",
			okr + ` function main(): i32 { let a: Result[i64, string] = okr[i64](5000000000 as i64); let r: i64 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		{"result-err-wide",
			errg + ` function main(): i32 { let a: Result[i32, i64] = errg[i64](5000000000 as i64); let r: i64 = 0; match (a) { Ok(v) => {}, Err(e) => { r = e; } } return (r / 1000000000) as i32 + 37; }`,
			42},
		{"result2-ok-wide-explicit",
			okg + ` function main(): i32 { let a: Result[i64, string] = okg[i64, string](5000000000 as i64); let r: i64 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		{"result2-ok-wide-inferred",
			okg + ` function main(): i32 { let a: Result[i64, string] = okg(5000000000 as i64); let r: i64 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		{"result2-err-wide",
			okg + ` function main(): i32 { let a: Result[i32, i64] = okg[i32, i64](5); let r: i32 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return r + 37; }`,
			42},
		{"result2-f64",
			okg + ` function main(): i32 { let a: Result[f64, string] = okg[f64, string](2.5); let r: f64 = 0.0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r * 2.0) as i32 + 37; }`,
			42},
		{"result2-narrow",
			okg + ` function main(): i32 { let a: Result[i32, string] = okg[i32, string](5); let r: i32 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return r + 37; }`,
			42},
		{"multi-tparam-unpromoted",
			`function mk2[T, A](t: T, init: A): A[] { let out: A[] = [init]; return out.append(init); } function main(): i32 { let r: i32[] = mk2[i32, i32](7, 5); return r[0] + r[1] + 32; }`,
			42},
	}
	for _, tc := range cases {
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
			watFile := filepath.Join(dir, "cont_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s (an invalid module fails to load)", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("erased-wide container wasm IR %s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
