package e2eselfhost

import "testing"

// structLenMethodIRCases pin the #3478 fix: a user-defined receiver method named
// `len` on a struct must shadow the builtin `.len()`, not be intercepted as a
// string/array length read. Pre-fix, irlower.fern intercepted *every* zero-arg
// `.len()` call and — for a struct receiver, which isn't a string — fell through
// to `op_arr_len`, reading the struct box as an array header (garbage: x86-64
// returned 26, the local lookup-string length; wasm returned 0). The two cases
// have identical bodies (`return b.items.len();`) and differ only in the method
// NAME (`len` vs `count`); pre-fix `len` diverged from `count` on the self-host
// IR path while both are correct natively. Hardcoded expectations verified
// against the native x86-64 backend.
const structLenMethodIRPrelude = `struct Box { items: string[] }
function helper(s: string): string {
    var alpha: string = "abcdefghijklmnopqrstuvwxyz";
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
	// user method named `len` must shadow the builtin (#3478): two adds -> 2.
	{"len-method", `var b: Box = Box { items: [] }; b = b.add("a"); b = b.add("b"); return b.len();`, 2},
	// control: a differently-named method with the same body is unaffected.
	{"count-method", `var b: Box = Box { items: [] }; b = b.add("a"); b = b.add("b"); return b.count();`, 2},
	// the builtin array `.len()` on a real array still works (no regression).
	{"builtin-arr-len", `var a: i32[] = [10, 20, 30]; return a.len();`, 3},
	// the builtin string `.len()` still works (no regression).
	{"builtin-str-len", `var s: string = "hello"; return s.len();`, 5},
}

func structLenMethodIRSrc(mainBody string) string {
	return structLenMethodIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostStructLenMethodIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
