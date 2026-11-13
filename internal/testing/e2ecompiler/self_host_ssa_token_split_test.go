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

// The token split copies the explicit memcpy of run into both sides of the
// uniqueness test of first. Each copy has its own labels, so the arm64
// assembler takes the function.
const ssaTokenSplitMemcpyProg = `struct Dec { n: i32, lap: u8[], samples: u8[] }
struct Block { state: Dec, samples: u8[], next: i32, silent: boolean }
@noinline function make(n: i32): Result[Dec, string] {
  if (n < 0) { return Err("neg"); }
  return Ok(Dec { n: n, lap: __alloc_u8(n), samples: __alloc_u8(n) });
}
@noinline function step(own d: Dec, k: i32): Result[Block, string] {
  if (k < 0) { return Err("neg"); }
  return Ok(Block { state: Dec { ...d, n: d.n + k }, samples: d.samples, next: k, silent: false });
}
function run(): void {
  let Ok(template) = make(4) else {
    exit(1);
  };
  let Ok(first) = step(Dec { ...template }, 2) else {
    exit(1);
  };
  let retained = first.samples;
  let saved = __alloc_u8(retained.len());
  unsafe {
    __memcpy(saved as usize + 8 as usize, retained as usize + 8 as usize, retained.len());
  }
  let Ok(second) = step(first.state, 3) else {
    exit(1);
  };
  exit(second.state.n + saved.len());
}
function main(): i32 { run(); return 0; }
`

func TestSelfHostSSATokenSplitCopiesLabelledOps(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, ssaTokenSplitMemcpyProg, target); code != 13 {
				t.Fatalf("exit %d, want 13\n%s", code, stderr)
			}
		})
	}
}
