package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

var fnValueParamShadowCases = []struct {
	name string
	src  string
}{
	{"struct_param_shadows_fn", `struct Q { s: string, pos: i32 }

function q_skip(p: Q): i32 {
    let cur: Q = p;
    return cur.s.len() + cur.pos;
}

function p(x: i32): i32 { return x; }

function main(): i32 {
    return q_skip(Q { s: "abc", pos: 1 }) * 10 + p(2);
}`},
	{"local_var_shadows_fn", `struct Q { s: string, pos: i32 }

function run(): i32 {
    let p: Q = Q { s: "abcd", pos: 2 };
    let cur: Q = p;
    return cur.s.len() + cur.pos;
}

function p(x: i32): i32 { return x + 1; }

function main(): i32 {
    return run() * 10 + p(3);
}`},
	{"unshadowed_fn_value_still_boxed", `function dbl(x: i32): i32 { return x * 2; }

function main(): i32 {
    let g = dbl;
    return g(21);
}`},
}

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
