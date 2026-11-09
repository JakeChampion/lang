package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const countedAppendProgram = `
@noinline function scaled(i: i32): i32 { return i * 3 - 11; }
@noinline function build_i32(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n { out = out.append(scaled(i)); }
  return out;
}
@noinline function copied(xs: i32[]): i32[] {
  let out: i32[] = [];
  for x in xs { out = out.append(x + 1); }
  return out;
}
@noinline function wide(n: i32): i64[] {
  let out: i64[] = [];
  for i in 0..n { out = out.append((i as i64) * 10000000000i64); }
  return out;
}
@noinline function unsigned(n: i32): u32[] {
  let out: u32[] = [];
  for i in 0..n { out = out.append((i as u32) ^ 4294967295u32); }
  return out;
}
@noinline function floats(n: i32): f64[] {
  let out: f64[] = [];
  for i in 0..n { out = out.append((i as f64) * 0.5); }
  return out;
}
@noinline function small_floats(n: i32): f32[] {
  let out: f32[] = [];
  for i in 0..n { out = out.append((i as f32) * (0.5 as f32)); }
  return out;
}
@noinline function flags(n: i32): boolean[] {
  let out: boolean[] = [];
  for i in 0..n { out = out.append(i % 2 == 0); }
  return out;
}
@noinline function bytes(n: i32): u8[] {
  let out: u8[] = [];
  for i in 0..n { out = out.append(i as u8); }
  return out;
}
@noinline function early(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n { if (i == 3) { break; } out = out.append(i); }
  return out;
}
@noinline function changing(n: i32): i32[] {
  let out: i32[] = [];
  let i = 0;
  while (i < n) { out = out.append(i); n = 3; i += 1; }
  return out;
}
@noinline function history(n: i32): i32 {
  let out: i32[] = [];
  let snapshots: i32[][] = [];
  for i in 0..n { snapshots = snapshots.append(out); out = out.append(i); }
  for i in 0..n {
    if (snapshots[i].len() != i) { return 1; }
    for j in 0..i { if (snapshots[i][j] != j) { return 2; } }
  }
  return if (out.len() == n) { 0 } else { 3 };
}
@noinline function observed(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n { out = out.append(out.len()); }
  return out;
}
@noinline function filtered(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n { if (i % 2 == 0) { out = out.append(i); } }
  return out;
}
@noinline function captured(n: i32): i32[] {
  let count = 0;
  let item = (i: i32): i32 => { count += 1; return i + count; };
  let out: i32[] = [];
  for i in 0..n { out = out.append(item(i)); }
  return out;
}
@noinline function held_empty(n: i32): i32 {
  let out: i32[] = [];
  let held = out;
  for i in 0..n { out = out.append(i); }
  return if (held.len() == 0 && out.len() == n) { 0 } else { 1 };
}
function main(): i32 {
  let before = __heap_alloc_count();
  let out = build_i32(96000);
  let allocations = __heap_alloc_count() - before;
  print(allocations);
  if (out.len() != 96000) { return 1; }
  for i in 0..out.len() { if (out[i] != i * 3 - 11) { return 2; } }
  let copy_before = __heap_alloc_count();
  let copy = copied(out);
  print(__heap_alloc_count() - copy_before);
  if (copy.len() != 96000 || copy[0] != -10 || copy[95999] != out[95999] + 1) { return 15; }
  let empty_before = __heap_alloc_count();
  if (build_i32(0).len() != 0 || build_i32(-7).len() != 0) { return 3; }
  if (__heap_alloc_count() != empty_before) { return 4; }
  let a = wide(5000); let b = unsigned(5000); let c = floats(5000);
  let d = small_floats(5000); let e = flags(5000); let f = bytes(5000);
  for i in 0..5000 {
    if (a[i] != (i as i64) * 10000000000i64 || b[i] != ((i as u32) ^ 4294967295u32)) { return 5; }
    if (c[i] != (i as f64) * 0.5 || d[i] != (i as f32) * (0.5 as f32)) { return 6; }
    if (e[i] != (i % 2 == 0) || f[i] != i as u8) { return 7; }
  }
  let storage_before = __heap_bump_bytes();
  let stopped = early(2147483647); let changed = changing(2147483647);
  if (stopped.len() != 3 || changed.len() != 3) { return 8; }
  if (__heap_bump_bytes() - storage_before > 65536i64) { return 9; }
  for i in 0..3 { if (stopped[i] != i || changed[i] != i) { return 10; } }
  if (history(20) != 0 || held_empty(20) != 0) { return 11; }
  let prefix = observed(20); let chosen = filtered(20); let closed = captured(20);
  for i in 0..20 {
    if (prefix[i] != i || closed[i] != i * 2 + 1) { return 12; }
  }
  if (chosen.len() != 10) { return 13; }
  for i in 0..10 { if (chosen[i] != i * 2) { return 14; } }
  print("counted append controls passed");
  return 0;
}
`

func TestSelfHostCountedAppendReserve(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, countedAppendProgram, []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1", "FERN_NO_ARRAY_FUSION=" + disabled})
					cmd := runScaleTarget(t, target, bin)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("counted append: %v\n%s", err, out)
					}
					lines := strings.Split(strings.TrimSpace(string(out)), "\n")
					for _, line := range lines[:2] {
						count, err := strconv.Atoi(line)
						if err != nil || disabled == "0" && count != 1 || disabled == "1" && count < 10 {
							t.Errorf("construction allocations %q with disabled=%s", line, disabled)
						}
					}
					if !strings.Contains(string(out), "counted append controls passed") {
						t.Errorf("missing completed controls: %s", out)
					}
					assertBalancedCensus(t, string(out))
				})
			}
		})
	}
}

func TestSelfHostCountedAppendReserveReport(t *testing.T) {
	c := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(countedAppendProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(c.runner, c.bin, "-array-report", src, c.stdlib)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"counted append capacity decisions:", "build_i32: counted append capacity", "copied: counted append capacity", "reason=reserved storage=reserved", "reason=array-observed", "reason=effect-boundary", "reason=shape-unsupported", "reason=non-scalar"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
}

func TestSelfHostCountedAppendReserveEffectBoundary(t *testing.T) {
	const source = `@noinline function first(i: i32): i32 { print("first"); exit(0); return i; }
function main(): i32 {
  let out: i32[] = [];
  for i in 0..2147483647 { out = out.append(first(i)); }
  return out.len();
}`
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, source, []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil || !strings.HasPrefix(string(out), "first\n") {
				t.Fatalf("effect boundary: %v\n%s", err, out)
			}
			allocs, frees, live := parseLeakcheck(t, "effect boundary", string(out))
			if allocs != 0 || frees != 0 || live != 0 {
				t.Fatalf("effect boundary: allocs=%d frees=%d live_bytes=%d, want zero", allocs, frees, live)
			}
		})
	}
}
