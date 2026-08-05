package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A fn-typed PARAMETER whose name happens to match a module-level function must
// resolve to the parameter. The interpreter and the native x86-64 / arm64
// backends always did; the self-host lift pass did not.
//
// It boxes a bare fn-name ident into a `<fd>$wrapN` trampoline — an env-taking
// thunk that hardcodes `call __fn_<name>`, built by looking the name up in the
// module function table. It did that for a fn-typed PARAMETER too, so when a
// module function shared the parameter's name the trampoline called THE MODULE
// FUNCTION. Every other part of the compiler shadows correctly; only this
// lookup disagreed.
//
// The result was a silent miscompile (#6191): a running binary invoking the
// wrong function, with no diagnostic, no crash, and no IR bail. The emitted asm
// simply contained `call __fn_<other>` inside `__fn_<caller>$wrapN` where the
// caller had been handed a different function entirely.
//
// TWO THINGS THE ORIGINAL REPORT GOT WRONG, pinned here because they are what
// cost the first investigation its time:
//
//   - It framed the trigger as "a delegated fn parameter invokes the function
//     from an EARLIER call". It reproduces with a SINGLE call site, and the
//     other function need never be called — merely DECLARED. The first case
//     below has an uncalled shadowing function.
//   - It framed the trigger as delegation. Delegation only matters because it
//     puts the parameter in a fn-value ARGUMENT position, which is where the
//     boxing happens.
//
// Teeth verified by removing the guard and rebuilding the self-host compiler:
// `uncalled-module-fn` returns 1 instead of 42, `forwarded-through-two-levels`
// returns 1 instead of 2.
//
// NOT covered here, because it is not fixed: the same collision through a
// CAPTURE rather than a parameter. A lambda that captures a fn-typed parameter
// is lifted to a `$clo` function, and the capture is boxed by the same lookup —
// `std/fuzz`'s `r.fuzz(...)` receiver method miscompiles today for exactly that
// reason. #6191 stays open on it; see the issue for why declining there is not
// the fix (it segfaults instead, so the capture must be boxed against the env
// slot rather than skipped).
var fnParamShadowsModuleFnCases = []struct {
	name string
	src  string
	want int
}{
	// The reported shape, reduced and stdlib-free: a delegating wrapper
	// forwards its fn-typed parameter, and an UNRELATED, UNCALLED module
	// function shares that parameter's name.
	//
	// Variant names avoid Ok/Err/Some/None -- those clash with the built-in
	// Result/Option and make the program itself invalid (E036), which is a
	// compile error masquerading as a wrong answer.
	{"uncalled-module-fn", `
enum Verdict { Fine, Wrong(string) }

// Never called. Its mere existence used to redirect the wrapper.
function handler(input: string): Verdict { return Wrong("wrong function ran"); }

function good(input: string): Verdict { return Fine; }

function inner(xs: string[], handler: (string) => Verdict): Verdict {
    var i: i32 = 0;
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

	// The same collision with plain i32s and no enum, so a regression cannot be
	// blamed on enum payload handling.
	{"forwarded-through-two-levels", `
function pick(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function apply(f: (i32) => i32): i32 { return f(0); }
function fwd(pick: (i32) => i32): i32 { return apply(pick); }
function main(): i32 { return fwd(other); }`, 2},

	// Both candidates reachable, so a wrong resolution cannot be masked by the
	// shadowing function being dead: each call must run its own.
	{"both-candidates-called", `
function pick(x: i32): i32 { return 1; }
function other(x: i32): i32 { return 2; }
function apply(f: (i32) => i32): i32 { return f(0); }
function fwd(pick: (i32) => i32): i32 { return apply(pick); }
function main(): i32 {
    var a: i32 = fwd(other);
    var b: i32 = fwd(pick);
    return a * 10 + b;
}`, 21},
}

func TestSelfHostFnParamShadowsModuleFn(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern",
		"parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "shadowfn")

	for _, tc := range fnParamShadowsModuleFnCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "shadowfn_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// The interpreter is the oracle AND the validity check: a program
			// that fails to compile exits non-zero here, which would otherwise
			// read as a codegen difference.
			if _, want := runFixtureInterp(t, entry, ""); want != tc.want {
				t.Fatalf("%s: interp oracle = %d, want %d — the test program is invalid, "+
					"not the compiler", tc.name, want, tc.want)
			}
			asm := string(runCapture(t, gcc, runner, driver, []byte(tc.src+"\n"), "-ir"))
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
