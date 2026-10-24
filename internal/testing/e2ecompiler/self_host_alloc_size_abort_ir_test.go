package e2ecompiler

import (
	"strings"
	"testing"
)

const allocNegativeLengthSrc = `function main(): i32 {
    let width: i32 = 16;
    let count: i32 = 268435455;
    let bs: u8[] = __alloc_u8(width * count);
    return bs.len();
}
`

const repeatWrapsToZeroSrc = `import "std/string";
function main(): i32 {
    let s: string = "abcd";
    let r: string = s.repeat(1073741824);
    return r.len();
}
`

const allocSizeCause = "fern: allocation size out of range"

const replaceWrapsToZeroSrc = `import "std/string";
function main(): i32 {
    let s: string = "x".repeat(65536);
    let replacement: string = "y".repeat(65536);
    return s.replace("x", replacement).len();
}
`

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
	t.Run("replace-wraps-to-zero", func(t *testing.T) { assertX86SizeAbort(t, cli, replaceWrapsToZeroSrc) })
}

func TestSelfHostAllocSizeAbortIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for name, src := range map[string]string{
		"alloc-u8-negative-length": allocNegativeLengthSrc,
		"repeat-wraps-to-zero":     repeatWrapsToZeroSrc,
		"replace-wraps-to-zero":    replaceWrapsToZeroSrc,
	} {
		t.Run(name, func(t *testing.T) {
			if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src)); code != 134 || out != "" {
				t.Fatalf("arm64: exit %d, stdout %q; want 134 and nothing printed", code, out)
			}
		})
	}
}

func TestSelfHostTransformSizeAbortWASM(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, src string }{
		{"repeat", repeatWrapsToZeroSrc},
		{"replace", replaceWrapsToZeroSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, code := cli.exitOf(t, tc.src, "wasm32-wasi")
			if code == 0 || !strings.Contains(stderr, "unreachable") || strings.Contains(stderr, "out of bounds") {
				t.Fatalf("want a deliberate size-guard trap before copying, got exit %d\n%s", code, stderr)
			}
		})
	}
}
