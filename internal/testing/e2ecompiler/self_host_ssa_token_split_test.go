package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A rebuild of a matched value tests once that the value is unique. Each
// side of that test has its own code, so the reuse token is not tested again
// (ssa.split_tokens). bump of ssaBoxedCountProg is such a rebuild.
func TestSelfHostSSATokenSplit(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaBoxedCountProg), 0o644); err != nil {
		t.Fatal(err)
	}
	nullTest := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+(cmpq \$0, %r|testq %r)`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+(cmp x[0-9]+, #0$|cbz x|cbnz x)`),
	}
	unique := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+cmpl \$1, -8\(`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+cmp w5, #1$`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := tailCallBody(t, string(asm), "bump")
			if got := len(unique[target].FindAllString(bump, -1)); got != 1 {
				t.Fatalf("bump has %d uniqueness tests, want 1:\n%s", got, bump)
			}
			if at := nullTest[target].FindString(bump); at != "" {
				t.Fatalf("bump tests a value against null (%q), so the reuse token is tested again:\n%s", at, bump)
			}
			if stderr, code := cli.exitOf(t, ssaBoxedCountProg, target); code != 55 {
				t.Fatalf("exit %d, want 55\n%s", code, stderr)
			}
		})
	}
}
