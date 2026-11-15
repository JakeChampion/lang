package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A counted loop whose bound the compiler cannot compare with the length of
// its array runs a copy with no checks when one test before the loop shows
// that every index is in range (#15975).
const versionedLoops = `import "std/array";
struct J { a: i32, b: i32 }
@noinline function slide(own a: i32[], n: i32): i32[] {
  for k in 0..n {
    let m = a[k];
    a = a.with(k, if (m >= 8) { m - 8 } else { 0 });
  }
  return a;
}
@noinline function shifted(own b: i32[], a: i32[], lo: i32, hi: i32): i32[] {
  for k in lo..hi { b = b.with(k, a[k + 2] + b[k]); }
  return b;
}
@noinline function counted(a: i32[], n: i32): i32 {
  let s = 0;
  let i = 0;
  while (i < n) { s = s + a[i]; i += 1; }
  return s * 100 + i;
}
@noinline function step(own z: u8[], x: i32): (u8[], u8[]) {
  let one: u8[] = [];
  return (z.append(x as u8), one.append(1u8));
}
@noinline function pairs(all: J[], first: i32, end: i32): u8[] {
  let z: u8[] = [];
  let out: u8[] = [];
  for i in first..end {
    let (next, bytes) = step(z, all[i].a);
    z = next;
    out = out.concat(bytes);
  }
  return out;
}
@noinline function seq(n: i32): i32[] {
  let a: i32[] = [];
  for i in 0..n { a = a.append(i * 3); }
  return a;
}
`

const versionedMain = `function main(): i32 {
  let s = slide(seq(10), 10);
  if (s[0] != 0 || s[2] != 0 || s[3] != 1 || s[9] != 19) { return 1; }
  let h = shifted(seq(8), seq(10), 1, 8);
  if (h[0] != 0 || h[1] != 12 || h[7] != 48) { return 2; }
  if (counted(seq(6), 6) != 4506 || counted(seq(6), 0) != 0) { return 3; }
  let all: J[] = [J { a: 1, b: 2 }, J { a: 3, b: 4 }, J { a: 5, b: 6 }];
  if (pairs(all, 0, 3).len() != 3 || pairs(all, 2, 1).len() != 0) { return 4; }
  return 0;
}`

func TestSelfHostLoopVersioning(t *testing.T) {
	cli := buildSelfHostCLI(t)
	program := versionedLoops + versionedMain
	src := filepath.Join(t.TempDir(), "version.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	loads := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movslq 8\(%r\w+,%r\w+,4\)`),
		"arm64-linux":  regexp.MustCompile(`ldrsw x\d+, \[x\d+, #8\]`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("versioned loops: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			for name, call := range map[string]string{"slide": "slide(seq(10), 11).len()", "shifted": "shifted(seq(8), seq(9), 1, 8).len()", "counted": "counted(seq(6), 7)"} {
				out := versionedLoops + "function main(): i32 { return " + call + "; }"
				stderr, code := cli.exitOf(t, out, target)
				if target == "wasm32-wasi" && code == 0 || target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Errorf("%s past the end exited %d, want the array abort\n%s", name, code, stderr)
				}
			}
			load, ok := loads[target]
			if !ok {
				return
			}
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			body := asmFunction(string(asm), "__fn_slide:")
			if got := len(load.FindAllString(body, -1)); got != 2 || strings.Count(body, "oob_abort") != 1 {
				t.Errorf("slide has %d element reads and %d checks, want two versions of the loop and one check\n%s", got, strings.Count(body, "oob_abort"), body)
			}
		})
	}
}
