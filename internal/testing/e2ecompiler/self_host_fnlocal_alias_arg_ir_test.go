package e2ecompiler

import "testing"

const fnLocalAliasArgProlog = "function withRes(cb: (i32) => i32): i32 { return cb(4); }\n" +
	"function other(cb: (i32) => i32): i32 { return cb(40); }\n" +
	"function taker(f: (string) => i32): i32 { return f(\"hi\"); }\n" +
	"function inc(v: i32): i32 { return v + 17; }\n"

var fnLocalAliasArgCases = []struct {
	name string
	body string
	want int
}{
	{"bare_fn", "let w = withRes; return w(inc);", 21},
	{"inline_lambda", "let w = withRes; return w((v: i32): i32 => v + 17);", 21},
	{"annotated_local", "let w: ((i32) => i32) => i32 = withRes; return w(inc);", 21},
	{"string_callback", "let t = taker; return t((s: string): i32 => s.len() + 19);", 21},
	{"shadow_bound_elsewhere", "let withRes = other; return withRes(inc);", 57},
	{"lambda_bound_first", "let w = withRes; let l = (v: i32): i32 => v + 17; return w(l);", 21},
}

func TestSelfHostFnLocalAliasArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interp := buildLangBinForInterp(t)

	for _, tc := range fnLocalAliasArgCases {
		t.Run(tc.name, func(t *testing.T) {
			src := fnLocalAliasArgProlog + "function g(): i32 { " + tc.body + " }\nfunction main(): i32 { return g(); }\n"
			if got := interpExit(t, interp, src); got != tc.want {
				t.Fatalf("%s: interp exit %d, want %d", tc.name, got, tc.want)
			}
			asm := hevCompile(t, runner, driverBin, src, nil)
			progBin := buildBin(t, gcc, dir, "fnlocalalias_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s: self-host exit %d, want %d (a signal here is the callee reading a raw code address as an env box)", tc.name, exit, tc.want)
			}
		})
	}
}
