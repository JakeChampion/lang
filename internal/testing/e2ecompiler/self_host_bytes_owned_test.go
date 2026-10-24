package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bytesOwnedProgram = `import "std/i32";
import "std/i64";

@noinline function owned(b: [u8]): u8[] {
  return __bytes_owned(b);
}

@noinline function sum(xs: u8[]): i32 {
  let t: i32 = 0;
  for x in xs {
    t = t * 7 + x as i32;
    t = t % 1000003;
  }
  return t;
}

function main(): i32 {
  let arr: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
  let a: u8[] = owned(arr[2:7]);
  let s: string = "hello, owned bytes";
  let b: u8[] = owned(s.as_bytes());
  let c: u8[] = owned(arr[3:3]);
  let d: u8[] = owned(arr);
  let e: u8[] = a.append(99);
  print(a.len().to_string() + " " + sum(a).to_string() + " " + b.len().to_string() + " " + sum(b).to_string() + " " + c.len().to_string() + " " + d.len().to_string() + " " + e.len().to_string() + " " + (arr[2] as i32).to_string());
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 100) {
    t = t + owned(arr[1:9]).len();
    i = i + 1;
  }
  let n: i64 = __heap_alloc_count() - before;
  print(t.to_string() + " " + n.to_string());
  return 0;
}
`

func TestSelfHostBytesOwned(t *testing.T) {
	native := "5 8869 18 721642 0 10 6 3\n800 100\n"
	runSemanticProgram(t, "bytesowned", bytesOwnedProgram, []string{"owned"},
		map[string]string{"arm64-linux": native, "x86-64-linux": native, "x86-64-sanitize": native, "wasm32-wasi": "5 8869 18 721642 0 10 6 3\n800 200\n"})
}

const bytesRangeProgram = `import "std/i64";

@noinline function ranged(b: [u8], from: i32, to: i32): u8[] {
  return __bytes_range(b, from, to);
}

function main(): i32 {
  let arr: u8[] = [10, 20, 30, 40, 50, 60];
  let s: string = "abcdefgh";
  let a: u8[] = ranged(arr, 1, 4);
  let b: u8[] = ranged(s.as_bytes(), 2, 5);
  let c: u8[] = ranged(arr, 3, 3);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 100) {
    t = t + ranged(arr, 0, 5).len();
    i = i + 1;
  }
  let n: i64 = __heap_alloc_count() - before;
  before = __heap_alloc_count();
  i = 0;
  while (i < 100) {
    t = t + ranged(arr, 2, 2).len() + ranged(s.as_bytes(), 8, 8).len();
    i = i + 1;
  }
  let e: i64 = __heap_alloc_count() - before;
  print((a.len() as i64).to_string() + " " + (a[0] as i64).to_string() + " " + (a[2] as i64).to_string() + " " + (b.len() as i64).to_string() + " " + (b[0] as i64).to_string() + " " + (c.len() as i64).to_string() + " " + (t as i64).to_string() + " " + n.to_string() + " " + e.to_string());
  return 0;
}
`

func TestSelfHostBytesRange(t *testing.T) {
	want := "3 20 40 3 99 0 500 100 0\n"
	runSemanticProgram(t, "bytesrange", bytesRangeProgram, []string{"ranged"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}

const keptRangeProgram = `import "std/i64";

struct T { bufs: u8[][], offs: i32[] }

@noinline function rest(buf: u8[], from: i32): u8[] {
  return __bytes_range(buf, from, buf.len());
}

@noinline function compact(t: T, at: i32): T {
  return T { ...t, bufs: t.bufs.with(at, rest(t.bufs[at], t.offs[at])), offs: t.offs.with(at, 0) };
}

function main(): i32 {
  let e: u8[] = [1, 2, 3];
  let t: T = T { bufs: [e, e], offs: [3, 1] };
  t = compact(t, 1);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 100) {
    t = compact(t, 0);
    t = T { ...t, offs: t.offs.with(0, t.bufs[0].len()) };
    i = i + 1;
  }
  let n: i64 = __heap_alloc_count() - before;
  print((t.bufs[1].len() as i64).to_string() + " " + (t.bufs[1][0] as i64).to_string() + " " + n.to_string());
  return 0;
}
`

func TestSelfHostBytesRangeKeptInPlace(t *testing.T) {
	want := "2 2 0\n"
	runSemanticProgram(t, "keptrange", keptRangeProgram, []string{"rest", "compact"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}

const bytesIntoProgram = `import "std/i64";

@noinline function into(own dst: u8[], at: i32, src: [u8], from: i32, n: i32): u8[] {
  return __bytes_into(dst, at, src, from, n);
}

@noinline function sum(xs: u8[]): i64 {
  let t: i64 = 0i64;
  for x in xs {
    t = t * 31i64 + x as i64;
  }
  return t;
}

function main(): i32 {
  let src: u8[] = [10, 20, 30, 40, 50, 60];
  let s: string = "abcdefgh";
  let a: u8[] = __alloc_u8(8);
  a = into(a, 1, src, 2, 3);
  a = into(a, 5, s.as_bytes(), 1, 3);
  let b: u8[] = a;
  let c: u8[] = into(a, 0, src[3:6], 1, 2);
  let d: u8[] = into(__alloc_u8(8), 8, src, 6, 0);
  print(sum(b).to_string() + " " + sum(c).to_string() + " " + (b[0] as i64).to_string() + " " + (c[0] as i64).to_string() + " " + (d.len() as i64).to_string());
  let buf: u8[] = __alloc_u8(64);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 100) {
    buf = into(buf, i % 60, src, 1, 4);
    i = i + 1;
  }
  let n: i64 = __heap_alloc_count() - before;
  print(sum(buf).to_string() + " " + n.to_string());
  return 0;
}
`

func TestSelfHostBytesInto(t *testing.T) {
	want := "27816549867 1430072365847 0 50 8\n5052777686297646788 0\n"
	runSemanticProgram(t, "bytesinto", bytesIntoProgram, []string{"into"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}

const bytesIntoOutOfRange = `@noinline function into(own dst: u8[], at: i32, src: [u8], from: i32, n: i32): u8[] {
  return __bytes_into(dst, at, src, from, n);
}
function main(): i32 {
  let src: u8[] = [1, 2, 3];
  let d: u8[] = into(__alloc_u8(4), 2, src, 0, 3);
  return d.len();
}
`

func TestSelfHostBytesIntoOutOfRange(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "oob.fern")
	if err := os.WriteFile(src, []byte(bytesIntoOutOfRange), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, "oob-"+tg.target)
		h.compileWith(t, tg, src, bin)
		cmd := exec.Command(bin)
		if len(tg.runner) > 0 {
			cmd = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "slice range out of bounds") {
			t.Errorf("%s: want the slice abort, got %v\n%s", tg.target, cmd.ProcessState, out)
		}
	}
}
