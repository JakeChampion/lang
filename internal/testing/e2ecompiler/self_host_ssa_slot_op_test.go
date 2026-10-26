package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A spilled result that takes its operand's slot is one ALU instruction on the
// slot, not a load, the operation and a store.
func TestSelfHostSSASlotOperation(t *testing.T) {
	const program = `@noinline function churn(n: i64, k: i64): i64 {
  let a: i64 = n * 3i64;
  let b: i64 = n * 5i64;
  let c: i64 = n * 7i64;
  let d: i64 = n * 11i64;
  let e: i64 = n * 13i64;
  let f: i64 = n * 17i64;
  let g: i64 = n * 19i64;
  let h: i64 = n * 23i64;
  let i: i64 = n * 29i64;
  let j: i64 = n * 31i64;
  let l: i64 = n * 37i64;
  let m: i64 = n * 41i64;
  let o: i64 = n * 43i64;
  let p: i64 = n * 47i64;
  let q: i64 = n * 53i64;
  let left: i64 = k;
  while (left > 0i64) {
    a = a ^ b + 1i64;
    b = b ^ c + 2i64;
    c = c ^ d + 3i64;
    d = d ^ e + 4i64;
    e = e ^ f + 5i64;
    f = f ^ g + 6i64;
    g = g ^ h + 7i64;
    h = h ^ i + 8i64;
    i = i ^ j + 9i64;
    j = j ^ l + 10i64;
    l = l ^ m + 11i64;
    m = m ^ o + 12i64;
    o = o ^ p + 13i64;
    p = p ^ q + 14i64;
    q = q ^ a + 15i64;
    left = left - 1i64;
  }
  return a ^ b ^ c ^ d ^ e ^ f ^ g ^ h ^ i ^ j ^ l ^ m ^ o ^ p ^ q;
}

function main(): i32 {
  return (churn(3i64, 1000i64) & 127i64) as i32;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		if stderr, code := cli.exitOf(t, program, target); code != 94 {
			t.Errorf("%s: exit %d, want 94\n%s", target, code, stderr)
		}
	}
	src := filepath.Join(t.TempDir(), "slot.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	assembly, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(string(assembly), "__fn_churn")
	inSlot := regexp.MustCompile(`(?m)^\s+(?:add|sub|and|or|xor)q (?:%r\w+|\$-?\d+), -\d+\(%rbp\)$`)
	if !inSlot.MatchString(body) {
		t.Errorf("no ALU operation writes a spill slot:\n%s", body)
	}
}
