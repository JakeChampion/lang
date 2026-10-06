package e2ecompiler

import "testing"

// stateMachineIRCases is a COMBINATION/integration pin: a word-counting state
// machine that exercises, in one module, an interaction no single-construct pin
// covers — a struct-update (`M { ...m, … }`) emitted from inside a `match` arm
// whose scrutinee is itself a struct field (`m.state`, an enum), guarded by
// `if`/else, driven by a `while` loop over string byte indices (`s[i]`), with the
// updated struct flowing through a by-value function parameter and back as a
// struct return, then read field-wise. Each constituent already lowers and is
// individually pinned (struct-update, nested match, while, enum-from-struct-field,
// string byte index, struct-by-value param + struct return); this checks their
// composition on the IR path.
//
// Oracle-checked against the interpreter; every result is <= 126 (wasmtime
// exit-code truncation, cf. #2908). Mirrors self_host_nested_tuple_ir_test.go.
var stateMachineIRCases = []struct {
	name string
	main string
}{
	// "ab cd ef": 3 words, 6 non-space chars -> 3*10 + 6 = 36.
	{"multi-word", stateMachineProg(`ab cd ef`)},
	// "hello": 1 word, 5 chars -> 15.
	{"single-word", stateMachineProg(`hello`)},
	// " a  bb ": 2 words (a, bb), 3 chars -> 2*10 + 3 = 23.
	{"leading-trailing-double-space", stateMachineProg(` a  bb `)},
	// "": loop body never runs; struct-update never fires -> 0.
	{"empty", stateMachineProg(``)},
}

// stateMachineProg builds the word-counter program over the given input string.
func stateMachineProg(s string) string {
	return `enum St { InWord, Between }
struct M { state: St, words: i32, chars: i32 }
function step(m: M, c: i32): M {
    match (m.state) {
        Between => { if (c == 32) { return M { ...m, state: Between }; } return M { ...m, state: InWord, words: m.words + 1, chars: m.chars + 1 }; },
        InWord => { if (c == 32) { return M { ...m, state: Between }; } return M { ...m, chars: m.chars + 1 }; },
    }
}
function main(): i32 {
    let s: string = "` + s + `";
    let m: M = M { state: Between, words: 0, chars: 0 };
    let i: i32 = 0;
    while (i < s.len()) { m = step(m, s[i] as i32); i = i + 1; }
    return m.words * 10 + m.chars;
}`
}

// TestSelfHostStateMachineIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostStateMachineIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range stateMachineIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
