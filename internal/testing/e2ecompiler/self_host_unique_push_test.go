package e2ecompiler

import (
	"strings"
	"testing"
)

const uniquePushProg = "@noinline function zeros(own out: i32[], n: i32): i32[] { let i: i32 = 0; while (i < n) { out = out.append(i); i = i + 1; } return out; }\n" +
	"function main(): i32 { let z: i32[] = zeros([], 300); return z.len() - 300 + z[299] - 200; }\n"

// The owned array is checked once before the loop, so each push in the loop
// takes the owned path with no count test.
func TestSelfHostUniquePushSkipsCountTest(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range []struct{ target, countTest string }{
		{"x86-64-linux", "movl -8(%rdi), %ecx"},
		{"arm64-linux", "ldur w3, [x0, #-8]"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			body := asmFuncBody(t, cli.emit(t, tc.target, uniquePushProg), "__fn_zeros")
			if n := strings.Count(body, tc.countTest); n != 0 {
				t.Errorf("%d push count tests in zeros, want 0:\n%s", n, body)
			}
			if n := strings.Count(body, "zeros_uq0:"); n != 1 {
				t.Errorf("%d uniqueness checks before the loop, want 1:\n%s", n, body)
			}
			if !strings.Contains(body, "__fern_arr_push_owned") {
				t.Errorf("the push does not take the owned path:\n%s", body)
			}
		})
	}
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
