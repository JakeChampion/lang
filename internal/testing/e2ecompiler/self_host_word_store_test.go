package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Each function writes the bytes of one integer, low byte first or high
// byte first, in any order of the writes. le32_copy writes into a shared
// array, which must stay unchanged. With an argument, main writes past the
// end of an array.
const wordStoreProgram = `@noinline
function le32(own d: u8[], at: i32, v: u32): u8[] {
  return d.with(at, (v & 255) as u8).with(at + 1, (v >> 8 & 255) as u8).with(at + 2, (v >> 16 & 255) as u8).with(at + 3, (v >> 24 & 255) as u8);
}

@noinline
function le32_copy(d: u8[], at: i32, v: u32): u8[] {
  return d.with(at, (v & 255) as u8).with(at + 1, (v >> 8 & 255) as u8).with(at + 2, (v >> 16 & 255) as u8).with(at + 3, (v >> 24 & 255) as u8);
}

@noinline
function be32(own d: u8[], at: i32, v: u32): u8[] {
  d = d.with(at, (v >> 24) as u8);
  d = d.with(at + 1, (v >> 16 & 255) as u8);
  d = d.with(at + 2, (v >> 8 & 255) as u8);
  d = d.with(at + 3, (v & 255) as u8);
  return d;
}

@noinline
function le64(own d: u8[], at: i32, v: u64): u8[] {
  d = d.with(at + 7, (v >> 56 as u64) as u8);
  d = d.with(at, (v & 255 as u64) as u8);
  d = d.with(at + 1, (v >> 8 as u64 & 255 as u64) as u8);
  d = d.with(at + 2, (v >> 16 as u64 & 255 as u64) as u8);
  d = d.with(at + 3, (v >> 24 as u64 & 255 as u64) as u8);
  d = d.with(at + 4, (v >> 32 as u64 & 255 as u64) as u8);
  d = d.with(at + 5, (v >> 40 as u64 & 255 as u64) as u8);
  d = d.with(at + 6, (v >> 48 as u64 & 255 as u64) as u8);
  return d;
}

@noinline
function be64(own d: u8[], at: i32, v: i64): u8[] {
  return d.with(at, (v >> 56) as u8).with(at + 1, (v >> 48) as u8).with(at + 2, (v >> 40) as u8).with(at + 3, (v >> 32) as u8)
    .with(at + 4, (v >> 24) as u8).with(at + 5, (v >> 16) as u8).with(at + 6, (v >> 8) as u8).with(at + 7, v as u8);
}

@noinline
function low32(own d: u8[], v: u64): u8[] {
  return d.with(0, v as u8).with(1, (v >> 8 as u64) as u8).with(2, (v >> 16 as u64) as u8).with(3, (v >> 24 as u64) as u8);
}

@noinline
function two(own d: u8[], at: i32, a: u32, b: u32): u8[] {
  return d.with(at, a as u8).with(at + 1, (a >> 8) as u8).with(at + 2, (a >> 16) as u8).with(at + 3, (a >> 24) as u8)
    .with(at + 4, b as u8).with(at + 5, (b >> 8) as u8).with(at + 6, (b >> 16) as u8).with(at + 7, (b >> 24) as u8);
}

@noinline
function le64_loop(own d: u8[], at: i32, v: u64): u8[] {
  let i = 0;
  while (i < 8) {
    d = d.with(at + i, (v >> (i * 8) as u64 & 255 as u64) as u8);
    i += 1;
  }
  return d;
}

@noinline
function be64_loop(own d: u8[], at: i32, v: u64): u8[] {
  let i = 0;
  while (i < 8) {
    d = d.with(at + i, (v >> ((7 - i) * 8) as u64 & 255 as u64) as u8);
    i += 1;
  }
  return d;
}

@noinline
function le32_loop(own d: u8[], at: i32, v: u32): u8[] {
  for i in 0..4 {
    d = d.with(at + i, (v >> (i * 8) as u32) as u8);
  }
  return d;
}

@noinline
function lane(own d: u8[], at: i32, v: u64, n: i32): u8[] {
  let i = 0;
  while (i < 8 && at + i < n) {
    d = d.with(at + i, (v >> (i * 8) as u64 & 255 as u64) as u8);
    i += 1;
  }
  return d;
}

function sum(d: u8[]): i32 {
  let s = 0;
  for b in d {
    s = s * 31 + b as i32;
  }
  return s;
}

function main(): i32 {
  let base = __alloc_u8(20);
  let a = le32_copy(base, 1, 0xa1b2c3d4u32);
  if (a[1] != 0xd4u8 || a[2] != 0xc3u8 || a[3] != 0xb2u8 || a[4] != 0xa1u8 || a[0] != 0u8 || a[5] != 0u8) { return 1; }
  if (sum(base) != 0 || base.len() != 20) { return 2; }
  let b = be32(a, 5, 0x01020304u32);
  if (b[5] != 1u8 || b[6] != 2u8 || b[7] != 3u8 || b[8] != 4u8 || b[4] != 0xa1u8) { return 3; }
  let c = le64(__alloc_u8(12), 3, 0x0807060504030201u64);
  for i in 0..8 {
    if (c[3 + i] != (i + 1) as u8) { return 4; }
  }
  if (c[2] != 0u8 || c[11] != 0u8) { return 5; }
  let e = be64(__alloc_u8(9), 1, -2i64);
  for i in 1..8 {
    if (e[i] != 255u8) { return 6; }
  }
  if (e[8] != 254u8 || e[0] != 0u8) { return 7; }
  let l = low32(__alloc_u8(5), 0xffeeddccbbaa9988u64);
  if (l[0] != 0x88u8 || l[3] != 0xbbu8 || l[4] != 0u8) { return 8; }
  let t = two(__alloc_u8(9), 1, 0x04030201u32, 0x08070605u32);
  for i in 0..8 {
    if (t[1 + i] != (i + 1) as u8) { return 9; }
  }
  if (t[0] != 0u8) { return 10; }
  if (le32(__alloc_u8(4), 0, 9u32)[0] != 9u8) { return 11; }
  let ll = le64_loop(__alloc_u8(10), 1, 0x0807060504030201u64);
  let bl = be64_loop(__alloc_u8(10), 1, 0x0102030405060708u64);
  let wl = le32_loop(__alloc_u8(6), 1, 0x04030201u32);
  for i in 0..8 {
    if (ll[1 + i] != (i + 1) as u8 || bl[1 + i] != (i + 1) as u8) { return 12; }
  }
  if (ll[0] != 0u8 || ll[9] != 0u8 || bl[0] != 0u8 || bl[9] != 0u8) { return 13; }
  if (wl[1] != 1u8 || wl[4] != 4u8 || wl[0] != 0u8 || wl[5] != 0u8) { return 14; }
  for n in [0, 2, 4, 9, 10, 30] {
    let w = lane(__alloc_u8(10), 1, 0x0807060504030201u64, n);
    for i in 0..10 {
      let want = if (i >= 1 && i < 9 && i < n) { i as u8 } else { 0u8 };
      if (w[i] != want) { return 15; }
    }
  }
  let held = __alloc_u8(9);
  let shared = held;
  let lc = lane(shared, 1, 0x0807060504030201u64, 9);
  if (sum(held) != 0 || lc[1] != 1u8 || lc[8] != 8u8) { return 16; }
  if (args().len() > 1) {
    if (args()[1] == "lane") {
      return sum(lane(__alloc_u8(12), args().len() + 3, 7u64, 100));
    }
    if (args()[1] == "loop") {
      return sum(le64_loop(__alloc_u8(12), args().len() + 3, 7u64));
    }
    return sum(le32_copy(base, 17, 7u32));
  }
  return 0;
}
`

// The stores of one word: four bytes, then eight bytes, and the byte store
// of the first write, which makes the array unique.
var wordStores = map[string][3]*regexp.Regexp{
	"x86-64-linux": {
		regexp.MustCompile(`movl %\w+, -?\d*\(%r\w+\)`),
		regexp.MustCompile(`movq %r\w+, 8\(%r\w+\)`),
		regexp.MustCompile(`\bmovb `),
	},
	"arm64-linux": {
		regexp.MustCompile(`\bstr w\d+, \[x\d+`),
		regexp.MustCompile(`\bstr x\d+, \[x\d+, #8\]`),
		regexp.MustCompile(`\bstrb `),
	},
	"wasm32-wasi": {
		regexp.MustCompile(`i32\.store\b[^8]`),
		regexp.MustCompile(`i64\.store\b[^8]`),
		regexp.MustCompile(`i32\.store8\b`),
	},
}

var byteSwap = map[string]string{"x86-64-linux": "bswap", "arm64-linux": "rev ", "wasm32-wasi": "bswap"}

// A chain of byte writes that fills the bytes of one integer is one word
// store, on each target, also when it comes from an unrolled loop or from
// the unrolled copy of a loop that also stops at a bound (#17409).
func TestSelfHostWordStore(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "wordstore.fern")
	if err := os.WriteFile(src, []byte(wordStoreProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	words := map[string][2]int{
		"le32": {1, 0}, "le32_copy": {1, 0}, "be32": {1, 0}, "low32": {1, 0}, "two": {2, 0},
		"le64": {0, 1}, "be64": {0, 1},
		"le64_loop": {0, 1}, "be64_loop": {0, 1}, "le32_loop": {1, 0},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			text, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			re := wordStores[target]
			for name, want := range words {
				var body string
				if target == "wasm32-wasi" {
					bodies := wasmFuncBodies(string(text), "$"+name+" ")
					if len(bodies) != 1 {
						t.Fatalf("%d bodies of $%s in the wat", len(bodies), name)
					}
					body = bodies[0]
				} else {
					body = asmFuncBody(t, string(text), "__fn_"+name+".r")
				}
				for k := 0; k < 2; k++ {
					if got := len(re[k].FindAllString(body, -1)); got != want[k] {
						t.Errorf("%s: %s has %d of %s, want %d:\n%s", target, name, got, re[k], want[k], body)
					}
				}
				if got := len(re[2].FindAllString(body, -1)); got != want[0]+want[1] {
					t.Errorf("%s: %s has %d byte stores, want %d:\n%s", target, name, got, want[0]+want[1], body)
				}
				big := strings.HasPrefix(name, "be")
				if strings.Contains(body, byteSwap[target]) != big {
					t.Errorf("%s: %s has a byte swap %v, want %v:\n%s", target, name, !big, big, body)
				}
			}
			// A loop that also stops at a bound n is versioned: a test of
			// at + 7 < n selects the unrolled copy, which is one word store.
			var laneBody string
			if target == "wasm32-wasi" {
				laneBody = strings.Join(wasmFuncBodies(string(text), "$lane "), "\n")
			} else {
				laneBody = asmFuncBody(t, string(text), "__fn_lane.r")
			}
			if got := len(re[1].FindAllString(laneBody, -1)); got != 1 {
				t.Errorf("%s: lane has %d of %s, want 1:\n%s", target, got, re[1], laneBody)
			}
			if stderr, code := cli.exitOfFile(t, src, target, nil); code != 0 {
				t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
			}
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"past-the-end"}); code != 134 {
				t.Errorf("%s: a write past the end exits %d, want the bounds abort 134\n%s", target, code, stderr)
			}
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"loop"}); code != 134 {
				t.Errorf("%s: an unrolled write past the end exits %d, want the bounds abort 134\n%s", target, code, stderr)
			}
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"lane"}); code != 134 {
				t.Errorf("%s: a bounded write past the end exits %d, want the bounds abort 134\n%s", target, code, stderr)
			}
		})
	}
}
