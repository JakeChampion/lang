package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Two raw blocks leak, of 60 and 100 bytes. A raw block has no shape.
const censusRawLeakSrc = `function main(): i32 {
  unsafe {
      let a: usize = __alloc(60);
      let b: usize = __alloc(60);
      let c: usize = __alloc(100);
      __free(a, 60);
      if (b == c) { return 9; }
      return 42;
  }
}
`

// A raw block of 16 bytes leaks with the header word of a Chain$Link box.
// TAG is the word that make writes, which the test reads from the asm first.
const censusShapeLeakSrc = `enum Chain { Link(i32, Chain), End }
@noinline function make(n: i32): Chain { return Link(n, Link(n + 1, End)); }
@noinline function sum(c: Chain): i32 { return match (c) { Link(v, rest) => v + sum(rest), End => 0 }; }
function main(): i32 {
  let c = make(3);
  unsafe {
    let q: usize = __alloc(16);
    __store_i32(q + 4, TAG);
  }
  return sum(c) - 7;
}
`

var shapeWordRe = regexp.MustCompile(`movl \$(\d+), -4\(%r\w+\)`)

// Under -sanitize the census names each shape that leaked, with its number
// of blocks and bytes. A block whose header is no shape of the program
// counts as a block with no shape. A clean run prints no census line.
func TestSelfHostSanitizeCensusNamesTheShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	src := filepath.Join(t.TempDir(), "probe.fern")
	if err := os.WriteFile(src, []byte(strings.Replace(censusShapeLeakSrc, "TAG", "1", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	m := shapeWordRe.FindStringSubmatch(tailCallBody(t, string(asm), "make"))
	if m == nil {
		t.Fatalf("make writes no header word:\n%s", tailCallBody(t, string(asm), "make"))
	}
	cases := []struct {
		name, src string
		want      []string
	}{
		{"raw", censusRawLeakSrc, []string{"fern-sanitizer: leak 168 bytes in 2 blocks", "fern-sanitizer:  2 blocks with no shape, 168 bytes"}},
		{"shape", strings.Replace(censusShapeLeakSrc, "TAG", m[1], 1), []string{"fern-sanitizer: leak 16 bytes in 1 blocks", "fern-sanitizer:  1 Chain$Link, 16 bytes"}},
		{"no-shape", strings.Replace(censusShapeLeakSrc, "TAG", "1", 1), []string{"fern-sanitizer:  1 blocks with no shape, 16 bytes"}},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, c := range cases {
			t.Run(target+"/"+c.name, func(t *testing.T) {
				r := quarantineBuild(t, cli, qemu, target, c.src, []string{"-sanitize"})
				for _, want := range c.want {
					if !strings.Contains(r.stderr, want) {
						t.Errorf("stderr has no %q:\n%s", want, r.stderr)
					}
				}
			})
		}
		t.Run(target+"/clean", func(t *testing.T) {
			r := quarantineBuild(t, cli, qemu, target, strings.Replace(censusShapeLeakSrc, "    let q: usize = __alloc(16);\n    __store_i32(q + 4, TAG);\n", "", 1), []string{"-sanitize"})
			if r.code != 0 || strings.Contains(r.stderr, "fern-sanitizer") {
				t.Errorf("exit %d, want a clean run\n%s", r.code, r.stderr)
			}
		})
	}
}
