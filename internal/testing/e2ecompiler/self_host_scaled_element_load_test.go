package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pairs, wide, half and signed_half read two elements of one array in each
// trip, covering both signed and unsigned 4-byte and 2-byte loads. once
// reads one element in each trip.
const scaledElementLoadFunctions = `@noinline function pairs(xs: i32[], n: i32, k: i32): i64 {
  let s = 0i64;
  for i in 0..n { s += xs[i] as i64 * 3i64 - xs[i + k] as i64; }
  return s;
}
@noinline function wide(xs: u32[], n: i32, k: i32): u64 {
  let s = 0u64;
  for i in 0..n { s += xs[i] as u64 + xs[i + k] as u64; }
  return s;
}
@noinline function half(xs: u16[], n: i32, k: i32): i32 {
  let s = 0;
  for i in 0..n { s += xs[i] as i32 - xs[i + k] as i32 * 2; }
  return s;
}
@noinline function signed_half(xs: i16[], n: i32, k: i32): i64 {
  let s = 0i64;
  for i in 0..n { s += xs[i] as i64 - 2i64 * xs[i + k] as i64; }
  return s;
}
@noinline function float_pairs(xs: f64[], n: i32, k: i32): f64 {
  let s = 0.0;
  for i in 0..n { s += xs[i] * 2.0 - xs[i + k]; }
  return s;
}
@noinline function once(xs: i32[], n: i32): i64 {
  let s = 0i64;
  for i in 0..n { s += xs[i] as i64; }
  return s;
}
`

const scaledElementLoadMain = `function main(): i32 {
  let a: i32[] = [0 - 5, 7, 0 - 2000000000, 9, 11];
  if (pairs(a, 4, 1) != (0i64 - 15i64 - 7i64) + (21i64 + 2000000000i64) + (0i64 - 6000000000i64 - 9i64) + (27i64 - 11i64)) { return 1; }
  let w: u32[] = [4000000000u32, 1u32, 4294967295u32];
  if (wide(w, 2, 1) != 4000000000u64 + 1u64 + 1u64 + 4294967295u64) { return 2; }
  let h: u16[] = [65535u16, 3u16, 40000u16];
  if (half(h, 2, 1) != 65535 - 6 + 3 - 80000) { return 3; }
  let sh: i16[] = [(0 - 32768) as i16, 32767 as i16, -1 as i16];
  if (signed_half(sh, 2, 1) != -65533i64) { return 5; }
  let floats: f64[] = [1.25, -2.5, 7.75];
  if (float_pairs(floats, 2, 1) != -7.75) { return 6; }
  if (once(a, 5) != 0i64 - 5i64 + 7i64 - 2000000000i64 + 9i64 + 11i64) { return 4; }
  return 0;
}`

var (
	arm64ScaledLoad  = regexp.MustCompile(`(?:ldrsw x\d+|ldrsh x\d+|ldr w\d+|ldrh w\d+), \[x\d+, x\d+, lsl #[12]\]`)
	arm64FloatLoad   = regexp.MustCompile(`ldr d\d+, \[x\d+, x\d+, lsl #3\]`)
	arm64ElementAddr = regexp.MustCompile(`add x5, x\d+, x\d+, lsl #[12]`)
)

// On arm64 the elements of an array start 8 bytes after its base, and no
// load has two registers and an offset. A loop that reads one array two
// times or more forms the address of the elements one time, and each read is
// one load with a scaled index. A loop that reads the array one time keeps
// the two instructions, because the address holds a register. The sign of a
// 4-byte element and a read past the end are as before.
func TestSelfHostScaledElementLoad(t *testing.T) {
	cli := buildSelfHostCLI(t)
	program := scaledElementLoadFunctions + scaledElementLoadMain
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pairs", "wide", "half", "signed_half"} {
		body := tailCallBody(t, string(asm), name)
		if !arm64ScaledLoad.MatchString(body) {
			t.Errorf("%s has no load with a scaled index:\n%s", name, body)
		}
		if arm64ElementAddr.MatchString(body) {
			t.Errorf("%s forms the address of an element for a read:\n%s", name, body)
		}
	}
	if body := tailCallBody(t, string(asm), "float_pairs"); len(arm64FloatLoad.FindAllString(body, -1)) < 2 {
		t.Errorf("float_pairs needs two indexed loads into floating-point registers:\n%s", body)
	}
	if body := tailCallBody(t, string(asm), "once"); arm64ScaledLoad.MatchString(body) {
		t.Errorf("once reads its array one time in a trip and has a load with a scaled index:\n%s", body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, program, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
		past := scaledElementLoadFunctions + "function main(): i32 { let a: i32[] = [1, 2, 3, 4, 5]; return pairs(a, 5, 1) as i32; }"
		stderr, code := cli.exitOf(t, past, target)
		if target == "wasm32-wasi" {
			if code == 0 {
				t.Errorf("%s: a read past the end exits 0, want a trap\n%s", target, stderr)
			}
			continue
		}
		if code != 134 || !strings.Contains(stderr, "fern: array index out of range") {
			t.Errorf("%s: a read past the end exits %d, want array abort 134\n%s", target, code, stderr)
		}
	}
}

// Each function writes two elements of one array in each trip: 4 bytes, 2
// bytes with no sign, 2 bytes with the sign, and bytes.
const indexedElementStoreFunctions = `@noinline function swap32(own xs: i32[], n: i32, k: i32): i32[] {
  for i in 0..n {
    let t = xs[i];
    xs = xs.with(i, xs[i + k]);
    xs = xs.with(i + k, t - 1);
  }
  return xs;
}
@noinline function fill16(own xs: u16[], n: i32, k: i32): u16[] {
  for i in 0..n {
    xs = xs.with(i, (i * 7) as u16);
    xs = xs.with(i + k, 65535u16);
  }
  return xs;
}
@noinline function neg16(own xs: i16[], n: i32, k: i32): i16[] {
  for i in 0..n {
    xs = xs.with(i, (0 - i - 5) as i16);
    xs = xs.with(i + k, 9 as i16);
  }
  return xs;
}
@noinline function bytes(own xs: u8[], n: i32, k: i32): u8[] {
  for i in 0..n {
    xs = xs.with(i, (i + 200) as u8);
    xs = xs.with(i + k, 7u8);
  }
  return xs;
}
`

const indexedElementStoreMain = `function main(): i32 {
  let a = swap32([0 - 5, 7, 0 - 2000000000, 9], 3, 1);
  if (a[0] != 7 || a[1] != 0 - 2000000000 || a[2] != 9 || a[3] != 0 - 8) { return 1; }
  let h = fill16([1u16, 2u16, 3u16, 4u16], 3, 1);
  if (h[0] != 0u16 || h[1] != 7u16 || h[2] != 14u16 || h[3] != 65535u16) { return 2; }
  let g = neg16([1 as i16, 2 as i16, 3 as i16], 2, 1);
  if (g[0] as i32 != 0 - 5 || g[1] as i32 != 0 - 6 || g[2] as i32 != 9) { return 3; }
  let b = bytes([1u8, 2u8, 3u8, 4u8], 3, 1);
  if (b[0] != 200u8 || b[1] != 201u8 || b[2] != 202u8 || b[3] != 7u8) { return 4; }
  return 0;
}`

var arm64IndexedStore = regexp.MustCompile(`(?:str w\d+|strh w\d+|strb w\d+), \[x\d+, x\d+(?:, lsl #[12])?\]`)

// A loop that writes one array two times or more writes each element with
// one store at the address of the elements and the index. A write past the
// end aborts as before.
func TestSelfHostIndexedElementStore(t *testing.T) {
	cli := buildSelfHostCLI(t)
	program := indexedElementStoreFunctions + indexedElementStoreMain
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"swap32", "fill16", "neg16", "bytes"} {
		body := tailCallBody(t, string(asm), name)
		if !arm64IndexedStore.MatchString(body) {
			t.Errorf("%s has no store with a register index:\n%s", name, body)
		}
		if strings.Contains(body, "add x5,") {
			t.Errorf("%s forms the address of an element for an access:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, program, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
		past := indexedElementStoreFunctions + "function main(): i32 { return fill16([1u16, 2u16, 3u16], 3, 1).len(); }"
		stderr, code := cli.exitOf(t, past, target)
		if target == "wasm32-wasi" {
			if code == 0 {
				t.Errorf("%s: a write past the end exits 0, want a trap\n%s", target, stderr)
			}
			continue
		}
		if code != 134 || !strings.Contains(stderr, "fern: array index out of range") {
			t.Errorf("%s: a write past the end exits %d, want array abort 134\n%s", target, code, stderr)
		}
	}
}
