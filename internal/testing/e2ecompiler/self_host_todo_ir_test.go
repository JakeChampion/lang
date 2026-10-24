package e2ecompiler

import "testing"

var todoIRCases = []struct {
	name string
	main string
	want int
}{
	{"stub-not-taken", `function helper(): i32 { todo; }
function main(): i32 { if (false) { return helper(); } return 9; }`, 9},
	{"stub-reached", `function helper(): i32 { todo("not written"); }
function main(): i32 { return helper(); }`, 101},
	{"bare-reached", `function main(): i32 { todo; }`, 101},
	{"identifier", `function main(): i32 { let todo: i32 = 5; todo = todo + 1; return todo + 2; }`, 8},
}

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
