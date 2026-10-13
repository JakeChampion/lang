package e2ecompiler

import "testing"

// ssaParamIntervalCases pin the register allocator's view of a parameter: it
// arrives at entry, so it is live from the first position, not from its first
// read. When a parameter's interval began at its first read, a parameter read
// only late (an array a defer walks) and one read early shared a home, and the
// entry's moves wrote the early one over the late one before it was read: the
// defer then walked an i64 as an array pointer and the program segfaulted. The
// division in the early return is what moves `n` out of %rax and so puts a
// move on the entry. Reduced from a fernsmith repro.
var ssaParamIntervalCases = []struct {
	name string
	src  string
	exit int
}{
	{"late-read-array-param-keeps-its-home", "function f(n: i32, p0: u64, p1: i32[], p3: i32[]): i32 { if (n <= 0) { return n / -3; } defer { for x in p1 { } } return n; }\nfunction main(): i32 { let a: i32 = f(0, 5u64, [0], [1]); let b: i32 = f(1, 5u64, [0], [2]); return a + b; }\n", 1},
	{"late-read-array-param-sums", "function f(n: i32, p0: i64, p1: i32[], p3: i32[]): i32 { if (n <= 0) { return n / -3; } let t: i32 = 0; for x in p1 { t = t + x; } return t; }\nfunction main(): i32 { let a: i32 = f(0, 5, [7], [1]); let b: i32 = f(1, 5, [20, 22], [2]); return a + b; }\n", 42},
}

func TestSelfHostSSAParamIntervalX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range ssaParamIntervalCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostSSAParamIntervalArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range ssaParamIntervalCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
