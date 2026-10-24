package e2ecompiler

import "testing"

const freshByteKernelProgram = `function take(own xs: u8[]): i32 {
  return xs.len();
}

function copy(b: [u8]): u8[] {
  return __bytes_owned(b);
}

function main(): i32 {
  let v: u8[] = [1, 2, 3];
  return take(__bytes_owned(v)) + take(__bytes_range(v, 0, 2)) + take(copy(v));
}
`

func TestSelfHostFreshByteKernelResults(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, freshByteKernelProgram, target)
			if code != 8 {
				t.Fatalf("exit %d, want 8\n%s", code, stderr)
			}
		})
	}
}
