package e2ecompiler

import (
	"strings"
	"testing"
)

// uniquePushProg grows a fresh array one element at a time. The receiver is
// proved sole (an empty literal, then each append's own result), so every
// append is the consuming push alone and tests no count at run time; the first
// push, onto the empty literal's static box, has no room and copies.
const uniquePushProg = "@noinline function zeros(n: i32): i32[] { let out: i32[] = []; let i: i32 = 0; while (i < n) { out = out.append(i); i = i + 1; } return out; }\n" +
	"function main(): i32 { let z: i32[] = zeros(300); return z.len() - 300 + z[299] - 200; }\n"

func TestSelfHostUniquePushSkipsCountTest(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range []struct{ target, countTest string }{
		{"x86-64-linux", "movl -8(%rdi), %ecx"},
		{"arm64-linux", "ldur w3, [x0, #-8]"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			body := asmFuncBody(t, cli.emit(t, tc.target, uniquePushProg), "__fn_zeros")
			if n := strings.Count(body, tc.countTest); n != 0 {
				t.Errorf("%d push count tests in zeros, want 0 (the receiver is proved sole):\n%s", n, body)
			}
		})
	}
	// z[299] is 299, so 0 + 299 - 200.
	if got, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", uniquePushProg)); got != 99 {
		t.Errorf("x86-64 exited %d, want 99", got)
	}
	t.Run("arm64-run", func(t *testing.T) {
		arm64gcc, qemu := arm64Tooling(t)
		if got, _ := runArm64(t, arm64gcc, qemu, cli.emit(t, "arm64-linux", uniquePushProg)); got != 99 {
			t.Errorf("exited %d, want 99", got)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", uniquePushProg)); got != 99 {
			t.Errorf("exited %d, want 99", got)
		}
	})
}
