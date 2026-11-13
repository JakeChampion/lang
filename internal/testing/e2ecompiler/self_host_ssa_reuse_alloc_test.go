package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reuse allocation tests its token inline (ssa.reuse_alloc_call). bump gets
// a shared box first, which gives a null token, and then a unique box. The
// call to the helper stays for a token of another size only.
func TestSelfHostSSAReuseAllocIsInline(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaBoxedCountProg), 0o644); err != nil {
		t.Fatal(err)
	}
	size := map[string]string{"x86-64-linux": "cmpq %rsi, -16(%rax)", "arm64-linux": "ldur x30, [x0, #-16]"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := tailCallBody(t, string(asm), "bump")
			for _, want := range []string{size[target], "__fn___fern_alloc_reuse.r", "__fern_arr_box"} {
				if !strings.Contains(bump, want) {
					t.Fatalf("bump has no %q:\n%s", want, bump)
				}
			}
			if stderr, code := cli.exitOf(t, ssaBoxedCountProg, target); code != 55 {
				t.Fatalf("exit %d, want 55\n%s", code, stderr)
			}
		})
	}
}
