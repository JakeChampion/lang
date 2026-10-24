package e2ecompiler

import (
	"testing"
)

var pipeHoleIRCases = []struct {
	name string
	main string
	want int
}{
	{"hole-second", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { let x: i32 = 3; return x |> sub(10, _); }`, 7},
	{"hole-middle", `function pick(a: i32, b: i32, c: i32): i32 { return a + b * 10 + c; }
function main(): i32 { return 20 |> pick(1, _, 3); }`, 204},
	{"nested", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { let x: i32 = 3; return 20 |> sub(_, x |> sub(5, _)); }`, 18},
	{"chained", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 4 |> sub(9, _) |> sub(8, _); }`, 3},
	{"hole-then-prepend", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 4 |> sub(10, _) |> sub(2); }`, 4},
	{"named-hole", `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; }
function main(): i32 { return (9 |> diff(b = _)) + 20; }`, 11},
	{"named-hole-after-positional", `function pick(a: i32, b: i32 = 0, c: i32 = 0): i32 { return a + b * 10 + c; }
function main(): i32 { return 20 |> pick(1, c = _); }`, 21},
}

func TestSelfHostPipeHoleIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	for _, tc := range pipeHoleIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != tc.want {
				t.Errorf("%s exited %d, want %d\n%s", tc.name, code, tc.want, stderr)
			}
		})
	}
}
