package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const scalarZeroFillProgram = `
@noinline function zero_f32(n: i32): f32[] {
  let out: f32[] = [];
  for _ in 0..n { out = out.append(0.0f32); }
  return out;
}
@noinline function zero_f64(n: i32): f64[] {
  let out: f64[] = [];
  for _ in 0..n { out = out.append(0.0); }
  return out;
}
@noinline function zero_i32(n: i32): i32[] {
  let out: i32[] = [];
  for _ in 0..n { out = out.append(0); }
  return out;
}
@noinline function zero_i64(n: i32): i64[] {
  let out: i64[] = [];
  for _ in 0..n { out = out.append(0i64); }
  return out;
}
@noinline function zero_u64(n: i32): u64[] {
  let out: u64[] = [];
  for _ in 0..n { out = out.append(0u64); }
  return out;
}
@noinline function zero_u8(n: i32): u8[] {
  let out: u8[] = [];
  for _ in 0..n { out = out.append(0u8); }
  return out;
}
@noinline function zero_bool(n: i32): boolean[] {
  let out: boolean[] = [];
  for _ in 0..n { out = out.append(false); }
  return out;
}
@noinline function negative_zero(n: i32): f32[] {
  let out: f32[] = [];
  for _ in 0..n { out = out.append(-0.0f32); }
  return out;
}
@noinline function fill_value(n: i32, value: f32): f32[] {
  let out: f32[] = [];
  for _ in 0..n { out = out.append(value); }
  return out;
}
@noinline function changing_value(n: i32): i32[] {
  let out: i32[] = [];
  let value = 0;
  for _ in 0..n { out = out.append(value); value += 1; }
  return out;
}
@noinline function escaped_counter(n: i32): (i32[], i32) {
  let out: i32[] = [];
  let i = 0;
  while (i < n) { out = out.append(0); i += 1; }
  return (out, i);
}
@noinline function prefix(n: i32): i32 {
  let out: i32[] = [];
  let sum = 0;
  for _ in 0..n { sum += out.len(); out = out.append(0); }
  return sum;
}
@noinline function early(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n { if (i == 3) { break; } out = out.append(0); }
  return out;
}
@noinline function changing_bound(n: i32): i32[] {
  let out: i32[] = [];
  let i = 0;
  while (i < n) { out = out.append(0); i += 1; n = 3; }
  return out;
}
@noinline function effects(n: i32, calls: Cell[i32]): i32[] {
  let out: i32[] = [];
  for _ in 0..n { calls.set(calls.get() + 1); out = out.append(0); }
  return out;
}
function main(): i32 {
  let before = __heap_alloc_count();
  let a = zero_f32(3000);
  let allocations = __heap_alloc_count() - before;
  print(allocations);
  if (a.len() != 3000) { return 1; }
  let empty_before = __heap_alloc_count();
  if (zero_f32(0).len() != 0 || zero_f32(-7).len() != 0 || zero_u8(0).len() != 0 || zero_u8(-7).len() != 0) { return 2; }
  if (__heap_alloc_count() != empty_before) { return 3; }
  let b = zero_f64(3000); let c = zero_i32(3000); let d = zero_i64(3000);
  let e = zero_u64(3000);
  let byte_before = __heap_alloc_count();
  let f = zero_u8(3000);
  print(__heap_alloc_count() - byte_before);
  let g = zero_bool(3000);
  for i in 0..3000 {
    if (f32_bits(a[i]) != 0 || f64_bits(b[i]) != 0i64 || c[i] != 0 || d[i] != 0i64) { return 4; }
    if (e[i] != 0u64 || f[i] != 0u8 || g[i]) { return 5; }
  }
  let held = a;
  a = a.with(8, 3.0f32).append(2.0f32);
  if (held.len() != 3000 || held[8] != 0.0f32 || a.len() != 3001 || a[8] != 3.0f32 || a[3000] != 2.0f32) { return 6; }
  let held_bytes = f;
  f = f.with(7, 99u8).append(1u8);
  if (held_bytes.len() != 3000 || held_bytes[7] != 0u8 || f.len() != 3001 || f[7] != 99u8 || f[3000] != 1u8) { return 6; }
  let negative = negative_zero(32);
  let nan = fill_value(32, f32_from_bits(2143363909));
  let parameter = fill_value(32, 0.0f32);
  let changing = changing_value(32);
  for i in 0..32 {
    if (f32_bits(negative[i]) as u32 != 2147483648u32 || f32_bits(nan[i]) != 2143363909) { return 7; }
    if (f32_bits(parameter[i]) != 0 || changing[i] != i) { return 8; }
  }
  let (escaped, counter) = escaped_counter(32);
  let (empty, zero) = escaped_counter(-8);
  if (escaped.len() != 32 || counter != 32 || empty.len() != 0 || zero != 0 || prefix(32) != 496) { return 9; }
  let storage = __heap_bump_bytes();
  let stopped = early(2147483647); let changed = changing_bound(2147483647);
  if (stopped.len() != 3 || changed.len() != 3 || __heap_bump_bytes() - storage > 65536i64) { return 10; }
  let calls = cell_new(0);
  if (effects(32, calls).len() != 32 || calls.get() != 32) { return 11; }
  print("scalar zero fill controls passed");
  return 0;
}
`

func TestSelfHostScalarZeroFill(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, scalarZeroFillProgram, []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1", "FERN_NO_ARRAY_FUSION=" + disabled})
					out, err := runScaleTarget(t, target, bin).CombinedOutput()
					if err != nil {
						t.Fatalf("scalar fill: %v\n%s", err, out)
					}
					if disabled == "0" && !strings.HasPrefix(string(out), "1\n1\n") {
						t.Fatalf("positive float and byte fills need one allocation each:\n%s", out)
					}
					if !strings.Contains(string(out), "scalar zero fill controls passed") {
						t.Fatalf("missing control marker:\n%s", out)
					}
					assertBalancedCensus(t, string(out))
				})
			}
		})
	}
}

func TestSelfHostScalarZeroFillReport(t *testing.T) {
	c := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(scalarZeroFillProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(c.runner, c.bin, "-array-report", src, c.stdlib)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	for _, want := range []string{"zero_f32: counted append capacity", "zero_i32: counted append capacity", "zero_u8: counted append capacity", "reason=zero-filled storage=zeroed"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
