package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// concatDoublingSrc doubles a string to 2^30 bytes, then concatenates it with
// itself: 2^31 bytes, one past the i32 length ceiling. Each doubling is a
// memcpy, so the run takes seconds.
const concatDoublingSrc = `function main(): i32 {
    var a: string = "x";
    var i: i32 = 0;
    while (i < 30) { a = a + a; i = i + 1; }
    var b: string = a + a;
    if (b.len() < 0) { return 3; }
    return 0;
}
`

const allocSizeAbortMsg = "fern: allocation size out of range"

// TestSelfHostConcatPastLengthCeilingAborts is the self-host half of #8457
// (#8519). __fern_str_concat is Fern source whose `la + lb` wraps negative past
// 2 GiB; __raw_alloc now refuses a size that is negative as an i32, so the
// program aborts the way the natives do instead of carrying on with a string
// whose length is -2147483648.
func TestSelfHostConcatPastLengthCeilingAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 1.5 GiB; not a -short test")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI takes host paths as argv; native x86-64 only")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	src := filepath.Join(dir, "concat_overflow.fern")
	if err := os.WriteFile(src, []byte(concatDoublingSrc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("x86-64-linux", func(t *testing.T) {
		bin := filepath.Join(dir, "concat_overflow")
		if o, err := exec.Command(driverBin, "-target", "x86-64-linux", "-o", bin, src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("self-host compile failed: %v\n%s", err, o)
		}
		var out []byte
		var code int
		if err := withBuildMemoryMB(2000, func() error {
			cmd := exec.Command(bin)
			out, _ = cmd.CombinedOutput()
			code = cmd.ProcessState.ExitCode()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if code != 134 || !strings.Contains(string(out), allocSizeAbortMsg) {
			t.Fatalf("exit %d, output %q; want exit 134 and %q", code, out, allocSizeAbortMsg)
		}
	})

	// A 1 GiB copy is slow under qemu, so the arm64 leg pins the guard in the
	// emitted helper instead of running it.
	t.Run("arm64-linux", func(t *testing.T) {
		asm := filepath.Join(dir, "concat_overflow_arm64.s")
		if o, err := exec.Command(driverBin, "-target", "arm64-linux", "-emit", "asm", "-o", asm, src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("self-host compile failed: %v\n%s", err, o)
		}
		b, err := os.ReadFile(asm)
		if err != nil {
			t.Fatalf("read asm: %v", err)
		}
		text := string(b)
		body := text[strings.Index(text, "__fn___fern_str_concat:"):]
		if end := strings.Index(body, "\n.size"); end > 0 {
			body = body[:end]
		}
		if !strings.Contains(body, "tbz w0, #31, 1f\n    b __fern_alloc_size_abort\n") {
			t.Fatalf("__fern_str_concat's raw_alloc is not guarded against a negative size:\n%s", body)
		}
		if !strings.Contains(text, allocSizeAbortMsg) {
			t.Fatalf("the emitted runtime does not carry %q", allocSizeAbortMsg)
		}
	})
}
