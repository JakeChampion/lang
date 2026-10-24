package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var fnParamShadowsModuleFnCases = []struct {
	name string
	src  string
	want int
}{
	{"uncalled-module-fn", `
enum Verdict { Fine, Wrong(string) }

// Never called. Its mere existence used to redirect the wrapper.
function handler(input: string): Verdict { return Wrong("wrong function ran"); }

function good(input: string): Verdict { return Fine; }

function inner(xs: string[], handler: (string) => Verdict): Verdict {
    let i: i32 = 0;
    while (i < xs.len()) {
        match (handler(xs[i])) { Wrong(m) => { return Wrong(m); }, Fine => { } }
        i = i + 1;
    }
    return Fine;
}

function outer(xs: string[], handler: (string) => Verdict): Verdict {
    return inner(xs, handler);
}

function main(): i32 {
    match (outer(["a", "b"], good)) { Wrong(m) => { return 1; }, Fine => { return 42; } }
}`, 42},

	{"forwarded-through-two-levels", `
function pick(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function apply(f: (i32) => i32): i32 { return f(0); }
function fwd(pick: (i32) => i32): i32 { return apply(pick); }
function main(): i32 { return fwd(other); }`, 2},

	{"captured-fn-param", `
function target(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function run(f: (i32) => i32): i32 { return f(0); }
function call_thunk(t: () => i32): i32 { return t(); }
function outer(target: (i32) => i32): i32 { return call_thunk(() => run(target)); }
function main(): i32 { return outer(other); }`, 2},

	{"captured-fn-param-called-directly", `
function target(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function call_thunk(t: () => i32): i32 { return t(); }
function outer(target: (i32) => i32): i32 { return call_thunk(() => target(0)); }
function main(): i32 { return outer(other); }`, 2},

	{"captured-mixed-with-real-global", `
function target(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function bump(x: i32): i32 { return x + 30; }
function call_thunk(t: () => i32): i32 { return t(); }
function outer(target: (i32) => i32): i32 { return call_thunk(() => bump(target(0))); }
function main(): i32 { return outer(other); }`, 32},

	{"both-candidates-called", `
function pick(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function apply(f: (i32) => i32): i32 { return f(0); }
function fwd(pick: (i32) => i32): i32 { return apply(pick); }
function main(): i32 {
    let a: i32 = fwd(other);
    let b: i32 = fwd(pick);
    return a * 10 + b;
}`, 21},
}

func TestSelfHostFnParamShadowsModuleFn(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern",
		"parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "shadowfn")

	for _, tc := range fnParamShadowsModuleFnCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "shadowfn_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			if _, want := runFixtureInterp(t, entry, ""); want != tc.want {
				t.Fatalf("%s: interp oracle = %d, want %d — the test program is invalid, "+
					"not the compiler", tc.name, want, tc.want)
			}
			asm := string(runCapture(t, gcc, runner, driver, []byte(tc.src+"\n")))
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "shadowfn_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host run = %d, want %d — a fn-typed parameter was resolved "+
					"to the module function of the same name", tc.name, code, tc.want)
			}
		})
	}
}
