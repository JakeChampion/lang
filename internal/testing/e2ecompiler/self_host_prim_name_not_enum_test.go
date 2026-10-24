package e2ecompiler

import (
	"os/exec"
	"testing"
)

var primNameNotEnumCases = []struct {
	name string
	src  string
}{
	{"u64-array-tuple-element", "function mk(): (u64[], u64) {\n" +
		"    let q: u64[] = [];\n    q = q.append(18446744073709551615 as u64);\n    q = q.append(4294967296 as u64);\n" +
		"    return (q, 4294967297 as u64);\n}\n" +
		"function main(): i32 {\n" +
		"    let t: (u64[], u64) = mk();\n" +
		"    if (t.0[1] != (4294967296 as u64)) { return 1; }\n" +
		"    if (t.0[0] != (18446744073709551615 as u64)) { return 2; }\n" +
		"    if (t.1 != (4294967297 as u64)) { return 3; }\n" +
		"    let (a, b) = mk();\n" +
		"    if (a[1] != (4294967296 as u64)) { return 4; }\n" +
		"    if (b != (4294967297 as u64)) { return 5; }\n" +
		"    let xs: u64[] = t.0;\n" +
		"    if (xs[1] != (4294967296 as u64)) { return 6; }\n" +
		"    return 0;\n}"},
	{"u64-array-enum-payload", "enum E { Xs(u64[]), N }\n" +
		"function main(): i32 {\n" +
		"    let e: E = Xs([18446744073709551615 as u64, 5 as u64]);\n" +
		"    match (e) { Xs(v) => { return v.len() - 2; }, N => { return 3; } }\n" +
		"    return 9;\n}"},
	{"u64-array-struct-field", "struct Q { us: u64[] }\n" +
		"function main(): i32 {\n" +
		"    let q: Q = Q { us: [18446744073709551615 as u64, 2 as u64] };\n" +
		"    if (q.us[0] != (18446744073709551615 as u64)) { return 1; }\n" +
		"    if (q.us[1] != (2 as u64)) { return 2; }\n" +
		"    return 0;\n}"},
	{"char-array-struct-field", "struct C { cs: char[] }\n" +
		"function main(): i32 {\n" +
		"    let c: C = C { cs: [97 as char, 98 as char] };\n" +
		"    if ((c.cs[0] as i32) != 97) { return 1; }\n" +
		"    if ((c.cs[1] as i32) != 98) { return 2; }\n" +
		"    return 0;\n}"},
}

func TestSelfHostPrimNameNotEnumX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range primNameNotEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			progBin := buildBin(t, gcc, dir, "pnne_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally (signal): %v", tc.name, cmd.ProcessState)
			}
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostUsizeArrayFieldRefused(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `struct U { ps: usize[] }
function main(): i32 {
    let n: i32 = 0;
    let r: i32 = 0;
    while (r < 50) { let u: U = U { ps: [r as usize, 4294967300 as usize] }; n = n + ((u.ps[1] - 4294967296 as usize) as i32) + (u.ps[0] as i32) + u.ps.len(); r = r + 1; }
    return n % 256;
}
`
	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, "usize_field", asm))
	if exit != 245 {
		t.Fatalf("usize[] field exited %d, want 245", exit)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil {
		t.Fatalf("parse leakcheck summary from %q: %v", stderr, err)
	}
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("usize[] field: allocs=%d frees=%d live_bytes=%d, want 100/100/0", allocs, frees, live)
	}
}
