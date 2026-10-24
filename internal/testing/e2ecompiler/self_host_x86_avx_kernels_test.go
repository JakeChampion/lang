package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostX86ByteKernelsRunAVX2(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "kernels.fern")
	prog := `import "std/string";
function main(): i32 {
    let s: string = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789";
    let n: i32 = s.count_byte(97) + __ascii_run(s, 0);
    match (s.index_of("z")) { Some(i) => { n = n + i; }, None => {} }
    match (s.last_index_of("z")) { Some(i) => { n = n + i; }, None => {} }
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
	for want, atLeast := range map[string]int{
		"vpbroadcastb %xmm1, %ymm1":    3,
		"vpcmpeqb (%r9), %ymm1, %ymm0": 2,
		"vpcmpeqb %ymm1, %ymm0, %ymm0": 1,
		"vmovdqu (%rax,%rdx), %ymm0":   2,
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
