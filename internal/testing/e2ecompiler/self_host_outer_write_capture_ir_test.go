package e2ecompiler

import (
	"testing"
)

func TestSelfHostOuterWriteCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			"toplevel",
			`function main(): i32 { let i: i32 = 0; let f: (i32) => i32 = ((x: i32) => x + i); i = 3; return f(0); }`,
			3,
		},
		{
			"in-loop",
			`function main(): i32 { let fs: ((i32) => i32)[] = []; let i: i32 = 0; while (i < 3) { let g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`,
			3,
		},
		{
			"nested-if-in-loop",
			`function main(): i32 { let fs: ((i32) => i32)[] = []; let i: i32 = 0; while (i < 2) { if (i >= 0) { let g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); } i = i + 1; } return (fs[0])(0); }`,
			2,
		},
		{
			"struct-literal-field",
			`struct C { f: (i32) => i32, n: i32 } function main(): i32 { let cs: C[] = []; let i: i32 = 0; while (i < 2) { cs = cs.append(C { f: ((x: i32) => x + i), n: i }); i = i + 1; } return (cs[0].f)(0); }`,
			2,
		},
		{
			"call-argument",
			`function take(f: (i32) => i32): i32 { return f(0); } function main(): i32 { let i: i32 = 0; let fs: ((i32) => i32)[] = []; fs = fs.append(((x: i32) => x + i)); i = 4; return take(fs[0]); }`,
			4,
		},
		{
			"readonly-capture-control",
			`function main(): i32 { let fs: ((i32) => i32)[] = []; let k: i32 = 5; let i: i32 = 0; while (i < 2) { let g: (i32) => i32 = ((x: i32) => x + k); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`,
			5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "outerwrite_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s: exited %d, want %d — a mutated scalar capture must be read at "+
					"CALL time, not snapshotted when the closure is built", tc.name, exit, tc.want)
			}
		})
	}
}
