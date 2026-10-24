package e2ecompiler

import "testing"

const structLenMethodIRPrelude = `struct Box { items: string[] }
function helper(s: string): string {
    let alpha: string = "abcdefghijklmnopqrstuvwxyz";
    return slice_unchecked(alpha, 0, 1) + s;
}
function (b: Box) add(x: string): Box { return Box { ...b, items: b.items.append(helper(x)) }; }
function (b: Box) len(): i32 { return b.items.len(); }
function (b: Box) count(): i32 { return b.items.len(); }
`

var structLenMethodIRCases = []struct {
	name string
	main string
	want int
}{
	{"len-method", `let b: Box = Box { items: [] }; b = b.add("a"); b = b.add("b"); return b.len();`, 2},
	{"count-method", `let b: Box = Box { items: [] }; b = b.add("a"); b = b.add("b"); return b.count();`, 2},
	{"builtin-arr-len", `let a: i32[] = [10, 20, 30]; return a.len();`, 3},
	{"builtin-str-len", `let s: string = "hello"; return s.len();`, 5},
}

func structLenMethodIRSrc(mainBody string) string {
	return structLenMethodIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostStructLenMethodIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range structLenMethodIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, structLenMethodIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
