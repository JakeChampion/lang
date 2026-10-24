package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var nestedArrFieldCases = []struct {
	name string
	src  string
	exit int
}{
	{"field-2d-append", `struct Acc { xs: i32[][] } function add(a: Acc, r: i32[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [7]); return a.xs[0][0] + 35; }`, 42},
	{"field-2d-append-f64", `struct Acc { xs: f64[][] } function add(a: Acc, r: f64[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [7.0]); return (a.xs[0][0] as i32) + 35; }`, 42},
	{"field-2d-append-i64", `struct Acc { xs: i64[][] } function add(a: Acc, r: i64[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [7i64]); return (a.xs[0][0] as i32) + 35; }`, 42},
	{"field-2d-append-u8", `struct Acc { xs: u8[][] } function add(a: Acc, r: u8[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [7 as u8]); return (a.xs[0][0] as i32) + 35; }`, 42},
	{"field-2d-append-boolean", `struct Acc { xs: boolean[][] } function add(a: Acc, r: boolean[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [true]); if (a.xs[0][0]) { return 42; } return 90; }`, 42},
	{"field-2d-append-struct-rows", `struct P { n: i32 } struct Acc { xs: P[][] } function add(a: Acc, r: P[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [P { n: 7 }]); return a.xs[0][0].n + 35; }`, 42},
	{"field-2d-with", `struct Acc { xs: i32[][] } function put(a: Acc, r: i32[]): Acc { return Acc { ...a, xs: a.xs.with(0, r) }; } function main(): i32 { let a: Acc = Acc { xs: [[1], [2]] }; a = put(a, [7]); return a.xs[0][0] + a.xs[1][0] + 33; }`, 42},
	{"field-2d-with-f64", `struct G { rows: f64[][] } function put(g: G, r: f64[]): G { return G { ...g, rows: g.rows.with(0, r) }; } function main(): i32 { let g: G = G { rows: [[1.0], [2.0]] }; g = put(g, [40.0]); return (g.rows[0][0] as i32) + (g.rows[1][0] as i32); }`, 42},
	{"field-2d-read", `struct G { rows: i32[][] } function main(): i32 { let g: G = G { rows: [[1, 2], [3, 4]] }; let i: i32 = 1; return g.rows[i][0] * 10 + g.rows[0].len() * 5 + 2; }`, 42},
	{"field-2d-read-f64", `struct G { rows: f64[][] } function main(): i32 { let g: G = G { rows: [[7.0], [3.0]] }; return (g.rows[0][0] as i32) + g.rows.len() * 10 + 15; }`, 42},
	{"field-2d-read-i64", `struct G { rows: i64[][] } function main(): i32 { let g: G = G { rows: [[7i64], [3i64]] }; return (g.rows[0][0] as i32) + g.rows.len() * 10 + 15; }`, 42},
	{"field-2d-foreach", `struct G { rows: i32[][] } function main(): i32 { let g: G = G { rows: [[7], [3]] }; let s: i32 = 0; for row in g.rows { s = s + row[0]; } return s + 32; }`, 42},
	{"field-2d-foreach-f64", `struct G { rows: f64[][] } function main(): i32 { let g: G = G { rows: [[1.5, 2.5], [3.0]] }; let t: f64 = 0.0; for row in g.rows { for x in row { t = t + x; } } return (t as i32) + 35; }`, 42},
	{"field-2d-foreach-struct-rows", `struct P { n: i32 } struct G { rows: P[][] } function main(): i32 { let g: G = G { rows: [[P { n: 7 }], [P { n: 3 }]] }; let s: i32 = 0; for row in g.rows { for p in row { s = s + p.n; } } return s + 32; }`, 42},
	{"field-2d-row-foreach", `struct G { rows: i32[][] } function main(): i32 { let g: G = G { rows: [[7, 3], [1]] }; let s: i32 = 0; for x in g.rows[0] { s = s + x; } return s + 32; }`, 42},
	{"field-2d-row-foreach-f64", `struct G { rows: f64[][] } function main(): i32 { let g: G = G { rows: [[7.5, 3.5], [1.0]] }; let t: f64 = 0.0; for x in g.rows[0] { t = t + x; } return (t as i32) + 31; }`, 42},
	{"field-2d-row-bind", `struct G { rows: f64[][] } function main(): i32 { let g: G = G { rows: [[7.0, 1.0], [3.0]] }; let r = g.rows[0]; return (r[0] as i32) + (r[1] as i32) * 10 + 25; }`, 42},
	{"field-2d-row-len", `struct G { rows: i32[][] } function main(): i32 { let g: G = G { rows: [[1, 2, 3], [4]] }; return g.rows[0].len() * 10 + g.rows[1].len() * 5 + 7; }`, 42},
	{"field-2d-bind-unannotated", `struct G { rows: i32[][] } function main(): i32 { let g: G = G { rows: [[7], [3]] }; let t = g.rows; return t[0][0] + t.len() * 10 + 15; }`, 42},
	{"field-2d-append-loop", `struct Acc { xs: i32[][] } function add(a: Acc, r: i32[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; let i: i32 = 0; while (i < 5) { a = add(a, [i, i + 1]); i = i + 1; } let s: i32 = 0; let j: i32 = 0; while (j < a.xs.len()) { s = s + a.xs[j][0] + a.xs[j][1]; j = j + 1; } return s + 17; }`, 42},
	{"field-2d-append-value-semantics", `struct Acc { xs: i32[][] } function main(): i32 { let a: Acc = Acc { xs: [[1], [2]] }; let b: i32[][] = a.xs.append([3]); return a.xs.len() * 10 + b.len() * 5 + b[0][0] + 11; }`, 47},
	{"field-2d-no-rc-underflow", `struct Acc { xs: i32[][] } function add(a: Acc, r: i32[]): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let i: i32 = 0; while (i < 20) { let a: Acc = Acc { xs: [[1]] }; a = add(a, [2]); if (a.xs.len() != 2) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"field-2d-alias-no-rc-underflow", `struct G { rows: i32[][] } function main(): i32 { let i: i32 = 0; while (i < 20) { let g: G = G { rows: [[1, 2], [3]] }; let s: i32 = 0; for x in g.rows[0] { s = s + x; } let t = g.rows; for row in t { s = s + row[0]; } if (s != 7) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"field-1d-append-unchanged", `struct Acc { xs: i32[] } function add(a: Acc, r: i32): Acc { return Acc { ...a, xs: a.xs.append(r) }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, 7); return a.xs[0] + 35; }`, 42},
	{"local-2d-append-unchanged", `struct Acc { xs: i32[][] } function add(a: Acc, r: i32[]): Acc { let t: i32[][] = a.xs; t = t.append(r); return Acc { ...a, xs: t }; } function main(): i32 { let a: Acc = Acc { xs: [] }; a = add(a, [7]); return a.xs[0][0] + 35; }`, 42},
	{"param-2d-append-unchanged", `function add(xs: i32[][], r: i32[]): i32[][] { return xs.append(r); } function main(): i32 { let a: i32[][] = []; a = add(a, [7]); return a[0][0] + 35; }`, 42},
	{"struct-method-append-unchanged", `struct Bag { n: i32 } function (b: Bag) append(v: i32): i32 { return b.n + v; } function main(): i32 { let b: Bag = Bag { n: 40 }; return b.append(2); }`, 42},
	{"str-field-method-unchanged", `struct S { s: string } function main(): i32 { let v: S = S { s: "abcdefghij" }; return v.s.len() * 4 + 2; }`, 42},
	{"strarr-field-append-unchanged", `struct S { ss: string[] } function add(s: S, x: string): S { return S { ...s, ss: s.ss.append(x) }; } function main(): i32 { let s: S = S { ss: ["a"] }; s = add(s, "bb"); return s.ss[1].len() * 20 + 2; }`, 42},
}

func TestSelfHostNestedArrFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedArrFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostNestedArrFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedArrFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostNestedArrFieldIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested array field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range nestedArrFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
