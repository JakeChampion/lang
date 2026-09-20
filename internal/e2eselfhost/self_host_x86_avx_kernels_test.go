package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-host's four x86-64 byte kernels (memchr, rmemchr, count_byte,
// ascii_run) run the same three tiers as native's: 32 bytes an iteration
// with AVX2 while a whole block remains, 16 with SSE2, then a scalar tail,
// with the upper halves cleared on every way out of the AVX loop. The
// kernel sweeps prove the answers; this reads the emitted text and proves
// the tiers are there, so a kernel that quietly fell back to its 16-byte
// loop would not pass as merely slower.
func TestSelfHostX86ByteKernelsRunAVX2(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "kernels.fern")
	prog := `import "std/string";
function main(): i32 {
    var s: string = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789";
    var n: i32 = s.count_byte(97) + s.index_of("z") + s.last_index_of("z") + __ascii_run(s, 0);
    return n % 100;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "kernels.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if report, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, report)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(asm)
	// One broadcast per needle kernel (memchr, rmemchr, count_byte); the
	// ascii_run kernel has no needle. An AVX loop clears the upper halves on
	// each of its exits: the hit and the hand-over for the three scans, the
	// hand-over alone for the count, seven in all.
	for want, atLeast := range map[string]int{
		"vpbroadcastb %xmm1, %ymm1":    3,
		"vpcmpeqb %ymm1, %ymm0, %ymm0": 3,
		"vmovdqu (%rax,%rdx), %ymm0":   3,
		"vmovdqu (%rax,%r9), %ymm0":    1,
		"vpmovmskb %ymm0,":             4,
		"vzeroupper":                   7,
	} {
		if got := strings.Count(text, want); got < atLeast {
			t.Errorf("%q appears %d times in the x86-64 text, want at least %d", want, got, atLeast)
		}
	}
	if t.Failed() {
		t.Logf("--- kernels.s ---\n%s", text)
	}
}
