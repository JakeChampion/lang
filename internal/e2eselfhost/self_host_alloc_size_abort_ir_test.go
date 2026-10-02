package e2eselfhost

import (
	"strings"
	"testing"
)

// An allocation size that wraps in i32 aborts with the named cause and exit
// 134 rather than under-allocating (#10771). `__alloc_u8` of a negative length
// went straight to the allocator, and the self-host's `repeat` intrinsic
// formed its total as an i32 product, which wrapped to 0 before its copy ran
// past the buffer.
const allocNegativeLengthSrc = `function main(): i32 {
    var width: i32 = 16;
    var count: i32 = 268435455;
    var bs: u8[] = __alloc_u8(width * count);
    return bs.len();
}
`

// 4 x 2^30 wraps to exactly 0, the product a wrapped-size allocation would
// not refuse.
const repeatWrapsToZeroSrc = `import "std/string";
function main(): i32 {
    var s: string = "abcd";
    var r: string = s.repeat(1073741824);
    return r.len();
}
`

const allocSizeCause = "fern: allocation size out of range"

func assertX86SizeAbort(t *testing.T, cli *strictCLI, src string) {
	t.Helper()
	bin := buildBin(t, cli.gcc, t.TempDir(), "prog", cli.emit(t, "x86-64-linux", src))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 134 || !strings.Contains(stderr, allocSizeCause) {
		t.Fatalf("exit %d, stderr %q; want 134 and %q", exit, stderr, allocSizeCause)
	}
}

func TestSelfHostAllocSizeAbortIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	t.Run("alloc-u8-negative-length", func(t *testing.T) { assertX86SizeAbort(t, cli, allocNegativeLengthSrc) })
	t.Run("repeat-wraps-to-zero", func(t *testing.T) { assertX86SizeAbort(t, cli, repeatWrapsToZeroSrc) })
}

func TestSelfHostAllocSizeAbortIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for name, src := range map[string]string{
		"alloc-u8-negative-length": allocNegativeLengthSrc,
		"repeat-wraps-to-zero":     repeatWrapsToZeroSrc,
	} {
		t.Run(name, func(t *testing.T) {
			if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src)); code != 134 || out != "" {
				t.Fatalf("arm64: exit %d, stdout %q; want 134 and nothing printed", code, out)
			}
		})
	}
}
