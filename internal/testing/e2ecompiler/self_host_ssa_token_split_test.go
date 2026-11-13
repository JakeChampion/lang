package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// The split reuses a field load that holds above the uniqueness test, and
// moves a store that writes a field of the unique box back to itself to the
// shared side. ins of the red-black tree workload has both shapes. Before
// these two steps its register form held 22 loads of the color field and 13
// blocks that store the value field with no allocation.
func TestSelfHostSSATokenSplitReusesLoads(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../bench/perceus/fern/run_rbtree.fern")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rbtree")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", out, src, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC=rbtree__ins")
	msg, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	form := string(msg)
	if end := strings.Index(form, "\n}\n"); end >= 0 {
		form = form[:end]
	}
	colorLoads := strings.Count(form, "ld_at 8 rc ")
	writeBacks := 0
	for _, block := range regexp.MustCompile(`\n  b\d+:\n`).Split(form, -1) {
		if strings.Contains(block, "st_at 24 ") && !strings.Contains(block, "__fern_alloc_reuse") && !strings.Contains(block, "arr_box") {
			writeBacks++
		}
	}
	if colorLoads == 0 || colorLoads > 17 {
		t.Errorf("ins loads the color field %d times, want 1 to 17", colorLoads)
	}
	if writeBacks > 11 {
		t.Errorf("%d blocks of ins store the value field with no allocation, want at most 11", writeBacks)
	}
}

// A flat op names its labels after the index of its op, so the split must
// not copy one to both sides. examples/tests/ulc_codec.fern has a byte copy
// between two token tests: on arm64 the copy defined its four labels twice.
func TestSelfHostSSATokenSplitCopiesNoLabel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/tests/ulc_codec.fern")
	if err != nil {
		t.Fatal(err)
	}
	label := regexp.MustCompile(`(?m)^([.A-Za-z_][^ \n]*):$`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, m := range label.FindAllStringSubmatch(string(asm), -1) {
				if seen[m[1]] {
					t.Fatalf("the label %s is defined twice", m[1])
				}
				seen[m[1]] = true
			}
			if len(seen) == 0 {
				t.Fatal("the assembly holds no label")
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
