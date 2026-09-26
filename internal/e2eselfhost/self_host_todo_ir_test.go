package e2eselfhost

import "testing"

// The `todo;` / `todo("msg");` stub statement desugars in the self-host
// parser (mirroring the native parser) to
// `while (true) { eprint("todo[: msg]"); exit(101); }` — the native
// desugar's `loop { ... }` wrapper lowers to the same while-true shape.
// The always-true loop makes the stub diverge for the checker's
// missing-return analysis, so a bare `todo;` can stand in for a whole
// non-void function body; a REACHED todo aborts with exit 101. The
// constructs the desugar uses (`while`, string `+`, `eprint`, `exit`)
// all already lower on the self-host IR path, so there is no dedicated
// codegen — same contract as the assert desugar this mirrors (#4416).
var todoIRCases = []struct {
	name string
	main string
	want int
}{
	// The stubbed function isn't called on the live path → normal return.
	// This is also the E052 shape: `helper` is a non-void function whose
	// body is nothing but `todo;`, and the self-host checker must accept it.
	{"stub-not-taken", `function helper(): i32 { todo; }
function main(): i32 { if (false) { return helper(); } return 9; }`, 9},
	// A reached whole-function stub aborts with 101.
	{"stub-reached", `function helper(): i32 { todo("not written"); }
function main(): i32 { return helper(); }`, 101},
	// Bare reached form.
	{"bare-reached", `function main(): i32 { todo; }`, 101},
	// `todo` stays usable as an ordinary identifier.
	{"identifier", `function main(): i32 { var todo: i32 = 5; todo = todo + 1; return todo + 2; }`, 8},
}

// TestSelfHostTodoIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostTodoIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range todoIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
