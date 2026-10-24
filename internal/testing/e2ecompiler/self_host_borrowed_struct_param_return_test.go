package e2ecompiler

import "testing"

var selfHostBorrowedStructParamCases = []struct {
	name string
	src  string
}{
	{"return-param", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_param(s: St): St { return s; }\nfunction main(): i32 {\n    let s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    let s1: St = s.emit(1);\n    let a: St = ret_param(s1);\n    let junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	{"return-alias-passthrough", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_alias(n: i32, s: St): St {\n    let st: St = s;\n    let i: i32 = 0;\n    while (i < n) { st = st.emit(i); i = i + 1; }\n    return st;\n}\nfunction main(): i32 {\n    let s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    let s1: St = s.emit(1);\n    let a: St = ret_alias(0, s1);\n    let junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	{"return-alias-rebound", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_alias(n: i32, s: St): St {\n    let st: St = s;\n    let i: i32 = 0;\n    while (i < n) { st = st.emit(i); i = i + 1; }\n    return st;\n}\nfunction main(): i32 {\n    let s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    let s1: St = s.emit(1);\n    let a: St = ret_alias(2, s1);\n    let junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	{"array-param-control", "@noinline\nfunction ret_alias(n: i32, a: i32[]): i32[] {\n    let t: i32[] = a;\n    let i: i32 = 0;\n    while (i < n) { t = t.append(i); i = i + 1; }\n    return t;\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    let a1: i32[] = a.append(1);\n    let a2: i32[] = ret_alias(0, a1);\n    let junk: i32[] = [7, 7, 7, 7, 7];\n    if (junk[0] != 7) { return 81; }\n    return a2.len() + __rc_underflow_count();\n}"},
}

func TestSelfHostBorrowedStructParamReturnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostBorrowedStructParamCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "bsp_"+tc.name, string(asm))
			stderr, code := hevRun(t, runner, progBin)
			if code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if allocs == 0 || allocs != frees || live != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}

func TestSelfHostBorrowedStructParamReturnArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostBorrowedStructParamCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if code := arm64Census(t, arm64gcc, qemu, "bsp_"+tc.name, string(asm)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
