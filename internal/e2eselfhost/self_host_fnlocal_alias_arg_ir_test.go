package e2eselfhost

import "testing"

// A fn value passed through a local bound to a bare module-function name
// (#9010). The lift boxes every fn-value argument whose callee parameter is
// fn-typed, because a module function's fn-typed parameter dispatches
// env-first; it found the callee by name in the module table or, for a
// local holding a lambda, in the lambda's params, and a local bound to a
// NAMED function matched neither — so the argument went raw through the
// binding's trampoline into a callee that read slot 0 of a code address.
// The lift now follows the binding to that function's parameter.
//
// The shadowed spelling (`var withRes = taker; use x <- withRes()`) is
// pinned in internal/e2e's shadowed-generic gate; these are the spellings
// with no shadow to lean on, which failed before #8982 too. Every row is
// also run through the interpreter here, so the expected values are not
// pinned by hand alone.
const fnLocalAliasArgProlog = "function withRes(cb: (i32) => i32): i32 { return cb(4); }\n" +
	"function other(cb: (i32) => i32): i32 { return cb(40); }\n" +
	"function taker(f: (string) => i32): i32 { return f(\"hi\"); }\n" +
	"function inc(v: i32): i32 { return v + 17; }\n"

var fnLocalAliasArgCases = []struct {
	name string
	body string
	want int
}{
	{"bare_fn", "var w = withRes; return w(inc);", 21},
	{"inline_lambda", "var w = withRes; return w((v: i32): i32 => v + 17);", 21},
	{"annotated_local", "var w: ((i32) => i32) => i32 = withRes; return w(inc);", 21},
	{"string_callback", "var t = taker; return t((s: string): i32 => s.len() + 19);", 21},
	{"shadow_bound_elsewhere", "var withRes = other; return withRes(inc);", 57},
	{"lambda_bound_first", "var w = withRes; var l = (v: i32): i32 => v + 17; return w(l);", 21},
}

func TestSelfHostFnLocalAliasArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
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
