package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The fill and move loops of sembytecopy (#15974). Each `ref` function does
// the same writes through a call, which keeps its loop element by element.
const fillMoveLoopFunctions = `@noinline function fill8(own a: u8[], at: i32, n: i32, v: u8): u8[] {
  for i in at..at + n { a = a.with(i, v); }
  return a;
}
@noinline function zero32(own a: i32[], n: i32): i32[] {
  for i in 0..n { a = a.with(i, 0); }
  return a;
}
@noinline function fill32(own a: i32[], at: i32, n: i32, v: i32): i32[] {
  let i = 0;
  while (i < n) { a = a.with(at + i, v); i = i + 1; }
  return a;
}
@noinline function fill64(own a: i64[], at: i32, n: i32): i64[] {
  for i in at..at + n { a = a.with(i, 7i64); }
  return a;
}
@noinline function fillc(own a: char[], n: i32): char[] {
  for i in 0..n { a = a.with(i, 'x'); }
  return a;
}
@noinline function slide8(own w: u8[], n: i32): u8[] {
  let i = 0;
  while (i < n) { w = w.with(i, w[i + 7]); i += 1; }
  return w;
}
@noinline function move8(own w: u8[], at: i32, from: i32, n: i32): u8[] {
  let i = 0;
  while (i < n) { w = w.with(at + i, w[from + i]); i += 1; }
  return w;
}
@noinline function move32(own w: i32[], at: i32, from: i32, n: i32): i32[] {
  let i = 0;
  while (i < n) { w = w.with(at + i, w[from + i]); i += 1; }
  return w;
}
@noinline function move64(own w: f64[], at: i32, from: i32, n: i32): f64[] {
  for i in 0..n { w = w.with(at + i, w[from + i]); }
  return w;
}
@noinline function both(own a: i32[], own c: char[], n: i32): (i32[], char[]) {
  for i in 0..n { a = a.with(i, 4); }
  for i in 0..n { c = c.with(i, 'y'); }
  return (a, c);
}
@noinline function set8(own a: u8[], i: i32, v: u8): u8[] { return a.with(i, v); }
@noinline function set32(own a: i32[], i: i32, v: i32): i32[] { return a.with(i, v); }
@noinline function setf(own a: f64[], i: i32, v: f64): f64[] { return a.with(i, v); }
@noinline function rmove8(own w: u8[], at: i32, from: i32, n: i32): u8[] {
  let i = 0;
  while (i < n) { let x = w[from + i]; w = set8(w, at + i, x); i += 1; }
  return w;
}
@noinline function rmove32(own w: i32[], at: i32, from: i32, n: i32): i32[] {
  let i = 0;
  while (i < n) { let x = w[from + i]; w = set32(w, at + i, x); i += 1; }
  return w;
}
@noinline function rmovef(own w: f64[], at: i32, from: i32, n: i32): f64[] {
  let i = 0;
  while (i < n) { let x = w[from + i]; w = setf(w, at + i, x); i += 1; }
  return w;
}
@noinline function seq8(n: i32): u8[] {
  let a: u8[] = [];
  for i in 0..n { a = a.append((i * 7 + 3) as u8); }
  return a;
}
@noinline function seq32(n: i32): i32[] {
  let a: i32[] = [];
  for i in 0..n { a = a.append(i * 1000003 - 77); }
  return a;
}
@noinline function seqf(n: i32): f64[] {
  let a: f64[] = [];
  for i in 0..n { a = a.append(i as f64 * 0.5 - 3.0); }
  return a;
}
@noinline function chars(n: i32): char[] {
  let a: char[] = [];
  for i in 0..n { a = a.append('a'); }
  return a;
}
@noinline function same8(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) { return false; }
  for i in 0..a.len() { if (a[i] != b[i]) { return false; } }
  return true;
}
@noinline function same32(a: i32[], b: i32[]): boolean {
  if (a.len() != b.len()) { return false; }
  for i in 0..a.len() { if (a[i] != b[i]) { return false; } }
  return true;
}
@noinline function samef(a: f64[], b: f64[]): boolean {
  if (a.len() != b.len()) { return false; }
  for i in 0..a.len() { if (a[i] != b[i]) { return false; } }
  return true;
}
`

const fillMoveLoopMain = `function main(): i32 {
  let dists: i32[] = [0 - 33, 0 - 32, 0 - 9, 0 - 8, 0 - 3, 0 - 1, 0, 1, 2, 3, 7, 8, 31, 32, 33];
  for d in dists {
    for n in [0, 1, 5, 40, 60] {
      let from = 34;
      let at = from + d;
      if (n <= 100 - from && n <= 100 - at) {
        if (!same8(move8(seq8(100), at, from, n), rmove8(seq8(100), at, from, n))) { return 1; }
        if (!same32(move32(seq32(100), at, from, n), rmove32(seq32(100), at, from, n))) { return 2; }
        if (!samef(move64(seqf(100), at, from, n), rmovef(seqf(100), at, from, n))) { return 3; }
      }
    }
  }
  let s = slide8(seq8(64), 50);
  if (!same8(s, rmove8(seq8(64), 0, 7, 50))) { return 4; }
  let f = fill8(seq8(50), 3, 9, 200u8);
  if (f.len() != 50 || f[2] != seq8(50)[2] || f[3] != 200u8 || f[11] != 200u8 || f[12] != seq8(50)[12]) { return 5; }
  let z = zero32(seq32(40), 40);
  let v = fill32(seq32(40), 5, 30, 0 - 7);
  let w = fill64(__alloc_i64(9), 2, 6);
  let c = fillc(chars(6), 4);
  if (z[0] != 0 || z[39] != 0 || v[4] != seq32(40)[4] || v[5] != 0 - 7 || v[34] != 0 - 7 || v[35] != seq32(40)[35]) { return 6; }
  if (w[1] != 0i64 || w[2] != 7i64 || w[7] != 7i64 || w[8] != 0i64 || c[3] != 'x' || c[4] != 'a') { return 7; }
  let (ba, bc) = both(seq32(8), chars(8), 8);
  if (ba[7] != 4 || bc[7] != 'y') { return 8; }
  let u = seq8(30);
  let before = __heap_alloc_count();
  u = fill8(u, 0, 30, 1u8);
  u = move8(u, 1, 0, 20);
  if (__heap_alloc_count() != before || u[29] != 1u8) { return 9; }
  let kept = u;
  let before2 = __heap_alloc_count();
  let changed = move8(fill8(u, 10, 5, 9u8), 0, 10, 5);
  if (__heap_alloc_count() - before2 != 1i64 || kept[0] != 1u8 || kept[10] != 1u8 || changed[0] != 9u8 || changed[14] != 9u8) { return 10; }
  let none = move8(fill8(seq8(4), 0 - 2147483647, 0 - 1, 3u8), 2147483647, 0 - 2147483647, 0);
  if (!same8(none, seq8(4))) { return 11; }
  return 0;
}`

func TestSelfHostFillMoveLoops(t *testing.T) {
	cli := buildSelfHostCLI(t)
	program := fillMoveLoopFunctions + fillMoveLoopMain
	src := filepath.Join(t.TempDir(), "fill.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	kernels := []string{"__fern_arr_fill_u8", "__fern_arr_fill_u32", "__fern_arr_fill_u64", "__fern_arr_move_u8", "__fern_arr_move_u32", "__fern_arr_move_u64"}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("fill and move loops: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			stderr, code = cli.exitOf(t, program, target, "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("fill and move loops without the sanitizer: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range kernels {
				call := regexp.MustCompile(`(?m)^\s+(?:call|bl|jmp|b) L?__fn_` + name + `(?:\.r)?\s*$`)
				if target == "wasm32-wasi" {
					call = regexp.MustCompile(`(?m)call \$` + name + `\b`)
				}
				if !call.MatchString(string(assembly)) {
					t.Errorf("no loop became a call to %s", name)
				}
			}
			if target == "wasm32-wasi" {
				bin := cli.wasmComponent(t, src, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				out, err := exec.Command(e2eharness.Wasmtime(t), "run", bin).CombinedOutput()
				if err != nil {
					t.Fatalf("encoded WASI fill and move: %v\n%s", err, out)
				}
				assertBalancedCensus(t, string(out))
			}
		})
	}
}

// A kernel checks its whole range before it writes, so a loop that leaves the
// array aborts as the loop did.
func TestSelfHostFillMoveLoopBounds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	calls := map[string]string{
		"fill-end":   "fill8(seq8(10), 5, 6, 1u8)",
		"fill-start": "fill8(seq8(10), 0 - 1, 2, 1u8)",
		"move-read":  "move8(seq8(10), 0, 5, 6)",
		"move-write": "move8(seq8(10), 5, 0, 6)",
		"fill-wide":  "fill32(seq32(10), 9, 2, 3)",
		"move-wide":  "move32(seq32(10), 0, 9, 2)",
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for name, call := range calls {
			t.Run(fmt.Sprintf("%s/%s", target, name), func(t *testing.T) {
				program := fillMoveLoopFunctions + "function main(): i32 { let out = " + call + "; return out.len(); }"
				stderr, code := cli.exitOf(t, program, target)
				if target == "wasm32-wasi" {
					if code == 0 {
						t.Fatalf("out-of-range %s: exit 0, want a trap\n%s", name, stderr)
					}
					return
				}
				if code != 134 || !strings.Contains(stderr, "fern: array index out of range") {
					t.Fatalf("out-of-range %s: exit %d, want array abort 134\n%s", name, code, stderr)
				}
			})
		}
	}
}

// An element that is not 1, 4 or 8 bytes on each target, or that has a
// reference count, has no kernel. The append lets the pass see the function.
const fillMoveOtherElements = `@noinline function fillz(own a: usize[], n: i32, v: usize): usize[] {
  for i in 0..n { a = a.with(i, v); }
  a = a.append(v);
  return a;
}
@noinline function moves(own w: string[], at: i32, from: i32, n: i32): string[] {
  let i = 0;
  while (i < n) { w = w.with(at + i, w[from + i]); i += 1; }
  w = w.append("tail");
  return w;
}
@noinline function moveb(own w: boolean[], at: i32, from: i32, n: i32): boolean[] {
  let i = 0;
  while (i < n) { w = w.with(at + i, w[from + i]); i += 1; }
  return w.append(true);
}
@noinline function seqz(n: i32): usize[] {
  let out: usize[] = [];
  for i in 0..n { out = out.append((i + 1) as usize); }
  return out;
}
@noinline function names(n: i32): string[] {
  let out: string[] = [];
  for i in 0..n { out = out.append("name-" + i.to_string()); }
  return out;
}
@noinline function flags(n: i32): boolean[] {
  let out: boolean[] = [];
  for i in 0..n { out = out.append(i >= 4); }
  return out;
}
function main(): i32 {
  let b = fillz(seqz(8), 3, 9 as usize);
  if (b.len() != 9 || b[0] != 9 as usize || b[2] != 9 as usize || b[3] != 4 as usize || b[7] != 8 as usize) { return 1; }
  let s = moves(names(6), 0, 3, 3);
  if (s.len() != 7 || s[0] != "name-3" || s[2] != "name-5" || s[3] != "name-3" || s[5] != "name-5") { return 2; }
  let f = moveb(flags(8), 0, 4, 4);
  if (f.len() != 9 || !f[0] || !f[3] || !f[4] || !f[7]) { return 3; }
  return 0;
}`

func TestSelfHostFillMoveKeepsOtherElements(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "other.fern")
	if err := os.WriteFile(src, []byte(fillMoveOtherElements), 0o644); err != nil {
		t.Fatal(err)
	}
	kernel := regexp.MustCompile(`(?m)(?:call|bl|jmp|b) \$?L?(?:__fn_)?__fern_arr_(?:fill|move)`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, fillMoveOtherElements, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("loops over other elements: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if call := kernel.Find(assembly); call != nil {
				t.Errorf("a loop became a kernel call: %s", call)
			}
			if target == "wasm32-wasi" {
				bin := cli.wasmComponent(t, src)
				if out, err := exec.Command(e2eharness.Wasmtime(t), "run", bin).CombinedOutput(); err != nil {
					t.Fatalf("encoded WASI loops over other elements: %v\n%s", err, out)
				}
			}
		})
	}
}
