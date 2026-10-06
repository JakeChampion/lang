package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Cell reads must retain the runtime scalar representation as well as its
// bits. Unsigned division and f32 arithmetic distinguish values whose bits
// happen to survive storage under the wrong interpreter tag.
func TestSelfHostScalarCellValues(t *testing.T) {
	cli := buildSelfHostCLI(t)
	reference := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, typ, first, next, observe, firstWant, nextWant string }{
		{"u8", "u8", "255u8", "128u8", "x as i32", "255", "128"},
		{"i32", "i32", "-2147483647", "2147483647", "x", "-2147483647", "2147483647"},
		{"u32", "u32", "4294967295u32", "2147483648u32", "x / 2u32", "2147483647u32", "1073741824u32"},
		{"i64", "i64", "-9223372036854775807i64", "9223372036854775807i64", "x", "-9223372036854775807i64", "9223372036854775807i64"},
		{"i64-small", "i64", "100000i64", "200000i64", "x * x", "10000000000i64", "40000000000i64"},
		{"u64", "u64", "18446744073709551615u64", "9223372036854775808u64", "x / cell_new(2u64).get()", "9223372036854775807u64", "4611686018427387904u64"},
		{"usize", "usize", "4294967295u32 as usize", "2147483648u32 as usize", "x / (2 as usize)", "2147483647 as usize", "1073741824 as usize"},
		{"boolean", "boolean", "true", "false", "x", "true", "false"},
		{"f32", "f32", "16777216.0 as f32", "33554432.0 as f32", "(x + (1.0 as f32)) == x", "true", "true"},
		{"f32-bits", "f32", "f32_from_bits(-2147483647 - 1)", "f32_from_bits(1)", "f32_bits(x)", "-2147483647 - 1", "1"},
		{"f32-nan", "f32", "f32_from_bits(2143289345)", "f32_from_bits(2139095040)", "f32_bits(x)", "2143289345", "2139095040"},
		{"f64", "f64", "f64_from_bits(-9223372036854775807i64 - 1i64)", "f64_from_bits(1i64)", "f64_bits(x)", "-9223372036854775807i64 - 1i64", "1i64"},
		{"string", "string", "\"first\"", "\"replacement\"", "x", "\"first\"", "\"replacement\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := strings.ReplaceAll(tc.observe, "x", "c.get()")
			source := strings.NewReplacer("TYPE", tc.typ, "FIRST", tc.first, "NEXT", tc.next, "OBSERVE", tc.observe, "DIRECT", direct, "OLD_WANT", tc.firstWant, "NEW_WANT", tc.nextWant).Replace(`
function old_value(x: TYPE): void { assert((OBSERVE) == (OLD_WANT)); }
function new_value(x: TYPE): void { assert((OBSERVE) == (NEW_WANT)); }
function main(): i32 {
  let initial: TYPE = FIRST;
  let c: Cell[TYPE] = cell_new(initial);
  let alias = c;
  let snapshot: TYPE = c.get();
  assert((DIRECT) == (OLD_WANT));
  old_value(snapshot);
  old_value(alias.get());
  let replace = (): void => { alias.set(NEXT); };
  replace();
  assert((DIRECT) == (NEW_WANT));
  new_value(c.get());
  old_value(snapshot);
  old_value(initial);
  c.set(c.get());
  new_value(alias.get());
  return 0;
}

`)
			path := filepath.Join(t.TempDir(), "scalar-cell.fern")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
			t.Run("interpreter", func(t *testing.T) {
				if out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib).CombinedOutput(); err != nil {
					t.Fatalf("interpreter: %v\n%s", err, out)
				}
			})
			t.Run("reference-interpreter", func(t *testing.T) {
				if out, err := exec.Command(reference, "-interp", path).CombinedOutput(); err != nil {
					t.Fatalf("reference interpreter: %v\n%s", err, out)
				}
			})
		})
	}
}

func TestSelfHostInterpreterScalarCellValues(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	bin := filepath.Join(dir, "interp")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", bin, filepath.Join(dir, "drivers/interp_run.fern"), cli.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile interpreter: %v\n%s", err, out)
	}
	cmd = runX86_64Bin(cli.runner, bin)
	cmd.Stdin = bytes.NewBufferString(`function main(): i32 {
  let small: Cell[i64] = cell_new(100000i64);
  assert(small.get() * small.get() == 10000000000i64);
  let wide: Cell[u64] = cell_new(18446744073709551615u64);
  let divisor: Cell[u64] = cell_new(2u64);
  assert(wide.get() / divisor.get() == 9223372036854775807u64);
  let narrow: Cell[u32] = cell_new(4294967295u32);
  assert(narrow.get() / 2u32 == 2147483647u32);
  let floating: Cell[f32] = cell_new(16777216.0 as f32);
  assert(floating.get() + (1.0 as f32) == floating.get());
  floating.set(f32_from_bits(-2147483647 - 1));
  assert(f32_bits(floating.get()) == -2147483647 - 1);
  return 0;
}
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-built interpreter: %v\n%s", err, out)
	}
}
