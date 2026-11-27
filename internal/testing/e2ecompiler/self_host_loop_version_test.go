package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A loop whose bounds the compiler cannot compare with the length of its
// array runs a copy with no checks when one test before the loop shows that
// every index is in range (#15975). The loop can count up or down by a
// constant, test a second counter in its body, and leave from more than one
// block (#15650). An index can be the counter times a constant, and a lane
// access covers each element that it reads or writes.
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
@noinline function snake(xv: i32[], yv: i32[], x0: i32, y0: i32, xlim: i32, ylim: i32): i32 {
  let x = x0;
  let y = y0;
  while (x < xlim && y < ylim && xv[x] == yv[y]) { x += 1; y += 1; }
  return x * 1000 + y;
}
@noinline function back(xv: i32[], yv: i32[], x0: i32, y0: i32, xoff: i32, yoff: i32): i32 {
  let x = x0;
  let y = y0;
  while (xoff < x && yoff < y && xv[x - 1] == yv[y - 1]) { x -= 1; y -= 1; }
  return x * 1000 + y;
}
@noinline function diagonals(own v: i32[], fo: i32, fmin: i32, fmax: i32, stop: i32): i32[] {
  let d = fmax;
  while (d >= fmin) {
    let lo = v[fo + d - 1];
    let hi = v[fo + d + 1];
    let x = if (lo < hi) { hi } else { lo + 1 };
    v = v.with(fo + d, x);
    if (x == stop) { return v.with(0, d); }
    d -= 2;
  }
  return v;
}
@noinline function wraps(a: i32[], from: i32, top: i32, k: i32): i32 {
  let s = 0;
  let i = from;
  while (i <= top) { s += a[i - k]; i += 2; }
  return s;
}
@noinline function scaled(a: i32[], n: i32): i32 {
  let s = 0;
  for i in 0..n { s += a[i * 2 + 1]; }
  return s;
}
@noinline function fseq(n: i32): f64[] {
  let a = __alloc_f64(n);
  for i in 0..n { a = a.with(i, i as f64); }
  return a;
}
@noinline function lanes(a: f64[], n: i32): i32 {
  let s = 0.0;
  for i in 0..n { s += __f64x2_lane(__f64x2_load(a, i * 2 + 1), 1); }
  return s as i32;
}
@noinline function fill(own a: f64[], n: i32): f64[] {
  for i in 0..n { a = __f64x2_with(a, i * 2, __f64x2_splat(i as f64)); }
  return a;
}
@noinline function bseq(n: i32): u8[] {
  let b: u8[] = [];
  for i in 0..n { b = b.append(i as u8); }
  return b;
}
@noinline function bytes(b: u8[], off: i32, n: i32): i32 {
  let s = 0;
  for i in 0..n { s += __u8x16_lane(__u8x16_load(b, i * 16 + off), 15) as i32; }
  return s;
}
@noinline function reverse_into(own b: i32[], a: i32[], n: i32): i32[] {
  for i in 0..n { b = b.with(n - 1 - i, a[i]); }
  return b;
}
enum Op { Set(Slot), Skip(i32) }
struct Slot { name: string, at: i32 }
@noinline function named(ops: Op[], from: i32, name: string): i32 {
  for i in from..ops.len() {
    match (ops[i]) {
      Op.Set(s) => { if (s.name == name) { return s.at; } },
      _ => {}
    }
  }
  return -1;
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
  let a = seq(10);
  if (snake(a, seq(10), 2, 2, 10, 10) != 10010 || snake(a, a, 2, 3, 10, 10) != 2003 || snake(a, a, 4, 4, 7, 10) != 7007) { return 5; }
  if (back(a, seq(10), 9, 9, 0, 0) != 0 || back(a, a, 9, 8, 0, 0) != 9008 || back(a, a, 9, 9, 3, 5) != 5005) { return 6; }
  let v = diagonals(seq(12), 5, -4, 4, 1000);
  if (v[0] != 0 || v[9] != 30 || v[7] != 24 || v[1] != 6) { return 7; }
  let w = diagonals(seq(12), 5, -4, 4, 12);
  if (w[0] != -2 || w[3] != 12 || w[9] != 30 || w[1] != 3) { return 8; }
  if (wraps(a, 3, 9, 3) != 36) { return 9; }
  let ops: Op[] = [Op.Skip(1), Op.Set(Slot { name: "a", at: 7 }), Op.Set(Slot { name: "b", at: 9 })];
  if (named(ops, 0, "b") != 9 || named(ops, 2, "a") != -1 || named(ops, h[0], "a") != 7) { return 10; }
  if (scaled(a, 5) != 75 || scaled(a, 0) != 0) { return 11; }
  if (lanes(fseq(8), 3) != 12) { return 12; }
  let f = fill(fseq(8), 4);
  if (f[0] != 0.0 || f[3] != 1.0 || f[5] != 2.0 || f[7] != 3.0) { return 13; }
  if (bytes(bseq(32), 0, 2) != 46 || bytes(bseq(40), 8, 2) != 62) { return 14; }
  let r = reverse_into(seq(10), seq(10), 10);
  if (r[0] != 27 || r[9] != 0 || r[4] != 15) { return 15; }
  return 0;
}`

func TestSelfHostLoopVersioning(t *testing.T) {
	cli := buildSelfHostCLI(t)
	program := versionedLoops + versionedMain
	src := filepath.Join(t.TempDir(), "version.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	// The tag test of an element of `ops`, one in each version of the loop
	// of named.
	tags := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`cmpl \$\d+, -4\(%r\w+\)`),
		"arm64-linux":  regexp.MustCompile(`ldr w\d+, \[x\d+, #-4\]`),
	}
	loads := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movslq 8\(%r\w+,%r\w+,4\)`),
		"arm64-linux":  regexp.MustCompile(`ldrsw x\d+, \[x\d+, #8\]`),
	}
	laneLoads := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movupd 8\(%r\w+,%r\w+(,8)?\), %xmm`),
		"arm64-linux":  regexp.MustCompile(`ldr q\d+, \[x17, #8\]`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("versioned loops: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			past := map[string]string{
				"slide":     "slide(seq(10), 11).len()",
				"shifted":   "shifted(seq(8), seq(9), 1, 8).len()",
				"counted":   "counted(seq(6), 7)",
				"snake":     "snake(seq(10), seq(12), 0, 0, 11, 11)",
				"back":      "back(seq(10), seq(10), 11, 11, 0, 0)",
				"diagonals": "diagonals(seq(8), 5, -4, 4, 1000).len()",
				// The last write of n - 1 - i is past the end of seq(9).
				"reverse_into": "reverse_into(seq(9), seq(10), 10).len()",
				// The indices 0 to 7 fit, but the counter steps past the top of
				// the i32 range, so it wraps and the next index is 10.
				"wraps":  "wraps(seq(10), 2147483640, 2147483647, 2147483640)",
				"named":  "named([Op.Skip(1)], -1, \"a\")",
				"scaled": "scaled(seq(10), 6)",
				// Index 7 is in range, and the second lane of its load is not.
				"lanes": "lanes(fseq(8), 4)",
				"fill":  "fill(fseq(7), 4).len()",
				"bytes": "bytes(bseq(32), 1, 2)",
			}
			for name, call := range past {
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
			// The reads and checks of two versions: the checks are in one
			// version, and the write of diagonals after its loop keeps one.
			for _, c := range []struct {
				name          string
				reads, checks int
			}{{"snake", 4, 2}, {"back", 4, 2}, {"diagonals", 4, 4}, {"wraps", 2, 1}, {"scaled", 2, 1}, {"reverse_into", 2, 2}} {
				body := asmFunction(string(asm), "__fn_"+c.name+":")
				if got, checks := len(load.FindAllString(body, -1)), strings.Count(body, "oob_abort"); got != c.reads || checks != c.checks {
					t.Errorf("%s has %d element reads and %d checks, want %d and %d: two versions of the loop\n%s", c.name, got, checks, c.reads, c.checks, body)
				}
			}
			// Each lane read has two checks, and only the version with the
			// checks has them.
			for _, name := range []string{"lanes", "bytes"} {
				body := asmFunction(string(asm), "__fn_"+name+":")
				if got, checks := len(laneLoads[target].FindAllString(body, -1)), strings.Count(body, "oob_abort"); got != 2 || checks != 2 {
					t.Errorf("%s has %d lane reads and %d checks, want 2 and 2: two versions of the loop\n%s", name, got, checks, body)
				}
			}
			// The block after the loop projects the element that the loop
			// tested, through the phi that joins the two versions.
			body = asmFunction(string(asm), "__fn_named:")
			if got := len(tags[target].FindAllString(body, -1)); got != 2 {
				t.Errorf("named has %d tag tests, want two versions of the loop\n%s", got, body)
			}
		})
	}
}
