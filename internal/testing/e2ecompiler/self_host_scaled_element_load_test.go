package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pairs, wide and half read two elements of one array in each trip, of 4
// bytes with the sign, 4 bytes with no sign and 2 bytes with no sign. once
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
  if (once(a, 5) != 0i64 - 5i64 + 7i64 - 2000000000i64 + 9i64 + 11i64) { return 4; }
  return 0;
}`

var (
	arm64ScaledLoad  = regexp.MustCompile(`(?:ldrsw x\d+|ldr w\d+|ldrh w\d+), \[x\d+, x\d+, lsl #[12]\]`)
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
	for _, name := range []string{"pairs", "wide", "half"} {
		body := tailCallBody(t, string(asm), name)
		if !arm64ScaledLoad.MatchString(body) {
			t.Errorf("%s has no load with a scaled index:\n%s", name, body)
		}
		if arm64ElementAddr.MatchString(body) {
			t.Errorf("%s forms the address of an element for a read:\n%s", name, body)
		}
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
