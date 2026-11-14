package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reuse allocation tests its token inline (ssa.inline_reuse_call), and calls
// the helper only for a token of another size. sink of ssaTokenKeepProg keeps
// such an allocation for a token that is null or has another shape. bump of
// ssaBoxedCountProg allocates on the null side of a test of its token, so it
// calls the fresh entry of the helper with no test (ssa.fresh_boxes).
func TestSelfHostSSAReuseAllocIsInline(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	inline := filepath.Join(dir, "inline.fern")
	if err := os.WriteFile(inline, []byte(ssaTokenKeepProg), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "fresh.fern")
	if err := os.WriteFile(fresh, []byte(ssaBoxedCountProg), 0o644); err != nil {
		t.Fatal(err)
	}
	size := map[string]string{"x86-64-linux": "cmpl %esi, -4(%rax)", "arm64-linux": "ldur w30, [x0, #-4]"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, inline, target))
			if err != nil {
				t.Fatal(err)
			}
			sink := tailCallBody(t, string(asm), "sink")
			for _, want := range []string{size[target], "__fn___fern_alloc_reuse.r", "__fern_rec_box"} {
				if !strings.Contains(sink, want) {
					t.Fatalf("sink has no %q:\n%s", want, sink)
				}
			}
			asm, err = os.ReadFile(cli.emit(t, fresh, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := tailCallBody(t, string(asm), "bump")
			if !strings.Contains(bump, "__fn___fern_alloc_reuse.f") || strings.Contains(bump, size[target]) {
				t.Fatalf("bump does not call the fresh entry only:\n%s", bump)
			}
		})
	}
}
