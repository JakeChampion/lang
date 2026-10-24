package e2ecompiler

import "testing"

var stateMachineIRCases = []struct {
	name string
	main string
}{
	{"multi-word", stateMachineProg(`ab cd ef`)},
	{"single-word", stateMachineProg(`hello`)},
	{"leading-trailing-double-space", stateMachineProg(` a  bb `)},
	{"empty", stateMachineProg(``)},
}

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
