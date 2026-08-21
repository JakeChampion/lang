package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// fnValueParamShadowCases pin a local binding whose initialiser is an ident that
// SHADOWS a same-named module function (#6854). The lift pass env-boxed
// `var g = <module fn name>` into a `__mkclo$` trampoline without checking
// whether the name was bound locally first, so `var cur = p` inside a function
// taking `p: SomeStruct` bound a closure slot: the struct type was lost and
// every later `cur.field` bailed the whole module.
//
// std/json's `__json_p_skip_ws(p: __JsonParser)` is the shape it was found on —
// a user program declaring any function named `p` and importing std/test (which
// pulls std/json in) could not be compiled at all, on a function it never
// reaches. The same guard already exists for fn-typed struct FIELDS (#6191).
var fnValueParamShadowCases = []struct {
	name string
	src  string
}{
	// A struct PARAM shadowed by a module function of the same name.
	{"struct_param_shadows_fn", `struct Q { s: string, pos: i32 }

function q_skip(p: Q): i32 {
    var cur: Q = p;
    return cur.s.len() + cur.pos;
}

function p(x: i32): i32 { return x; }

function main(): i32 {
    return q_skip(Q { s: "abc", pos: 1 }) * 10 + p(2);
}`},
	// A local `var` shadowing a module function, rebound to another local.
	{"local_var_shadows_fn", `struct Q { s: string, pos: i32 }

function run(): i32 {
    var p: Q = Q { s: "abcd", pos: 2 };
    var cur: Q = p;
    return cur.s.len() + cur.pos;
}

function p(x: i32): i32 { return x + 1; }

function main(): i32 {
    return run() * 10 + p(3);
}`},
	// The control: with no shadowing, `var g = <module fn>` still binds a
	// callable function value.
	{"unshadowed_fn_value_still_boxed", `function dbl(x: i32): i32 { return x * 2; }

function main(): i32 {
    var g = dbl;
    return g(21);
}`},
}

// TestSelfHostFnValueParamShadowIR_X86_64 drives fnValueParamShadowCases through
// the self-host x86-64 IR path under FERN_STRICT_IR.
func TestSelfHostFnValueParamShadowIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range fnValueParamShadowCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, cerr := cmd.Output()
			if cerr != nil {
				t.Fatalf("strict-IR compile: %v: %s", cerr, exitStderr(cerr))
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "fnshadow_"+tc.name, string(asm))
			run := runX86_64Bin(runner, progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
