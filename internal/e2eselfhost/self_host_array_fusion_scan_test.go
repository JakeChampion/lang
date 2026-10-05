package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

const arrayFusionScanSrc = `import "std/array";
@noinline function mapped(xs: i32[], offset: i64): i64[] {
  return xs.map((x: i32): i64 => x as i64 + offset)
    .scan(7i64, (a: i64, x: i64): i64 => a * 2i64 - x);
}
@noinline function filtered(xs: i32[], limit: i32): i64[] {
  return xs.filter((x: i32): boolean => x % 2 == 0)
    .map((x: i32): i64 => x as i64 + 1i64)
    .filter((x: i64): boolean => x > limit as i64)
    .scan(7i64, (a: i64, x: i64): i64 => a * 2i64 - x);
}
@noinline function chained(xs: i32[]): i64 {
  return xs.map((x: i32): i64 => x as i64)
    .scan(0i64, (a: i64, x: i64): i64 => a + x)
    .map((x: i64): i64 => x * 2i64)
    .fold(0i64, (a: i64, x: i64): i64 => a + x);
}
function check(xs: i32[], offset: i64, limit: i32): boolean {
  let a = mapped(xs, offset);
  let b = filtered(xs, limit);
  let acc = 7i64;
  let selected = 7i64;
  let prefixes = 0i64;
  let total = 0i64;
  let j: i32 = 0;
  let i: i32 = 0;
  if (a.len() != xs.len()) { return false; }
  while (i < xs.len()) {
    acc = acc * 2i64 - (xs[i] as i64 + offset);
    if (a[i] != acc) { return false; }
    if (xs[i] % 2 == 0 && xs[i] + 1 > limit) {
      selected = selected * 2i64 - (xs[i] as i64 + 1i64);
      if (j >= b.len() || b[j] != selected) { return false; }
      j = j + 1;
    }
    prefixes = prefixes + xs[i] as i64;
    total = total + prefixes * 2i64;
    i = i + 1;
  }
  return b.len() == j && chained(xs) == total;
}
function main(): i32 {
  let xs: i32[] = [];
  let n: i32 = 0;
  while (n <= 17) {
    if (!check(xs, 3i64, 4) || !check(xs, -2i64, 100)) { return 1; }
    let i: i32 = 0;
    while (i < xs.len()) { if (xs[i] != i - 3) { return 2; } i = i + 1; }
    xs = xs.append(n - 3);
    n = n + 1;
  }
  if (__rc_underflow_count() != 0) { return 3; }
  return 0;
}`

func TestSelfHostArrayFusionScanValues(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, arrayFusionScanSrc,
						[]string{"FERN_NO_ARRAY_FUSION=" + disabled, "FERN_LEAKCHECK=1"})
					out, err := runScaleTarget(t, target, bin).CombinedOutput()
					if err != nil {
						t.Fatalf("scan: %v\n%s", err, out)
					}
					var allocs, frees, live int64
					if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("scan ownership: %v\n%s", err, out)
					}
				})
			}
		})
	}
}

func TestSelfHostArrayFusionScanEmission(t *testing.T) {
	_, runner, driver := buildModloadDriverX86(t)
	dir := e2eharness.WriteSourceModloadProject(t, arrayFusionScanSrc)
	cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
	cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT=1")
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("compile scan: %v\n%s", err, diagnostic.String())
	}
	for _, name := range []string{"mapped", "filtered", "chained"} {
		body := emittedBody(t, string(asm), "__fn_"+name)
		if strings.Contains(body, "call __fn___arrm_") || strings.Contains(body, "call __fn_array__") {
			t.Fatalf("retained combinator in %s:\n%s", name, body)
		}
	}
	for _, plan := range []string{
		"mapped: map -> scan; one traversal", "filtered: filter -> map -> filter -> scan; one traversal",
		"chained: map -> scan; one traversal", "chained: map -> fold; one traversal",
	} {
		if !strings.Contains(diagnostic.String(), plan) {
			t.Fatalf("missing scan decision %q:\n%s", plan, diagnostic.String())
		}
	}
	if strings.Count(diagnostic.String(), "storage=no-intermediate-arrays/one-output-buffer") != 3 {
		t.Fatalf("scan output storage was not reported:\n%s", diagnostic.String())
	}
}

func TestSelfHostArrayFusionScanKeepsEffectOrder(t *testing.T) {
	const src = `import "std/array";
import "std/i32";
function main(): i32 {
  let out = [1, 2, 3].map((x: i32): i32 => { print("m" + x.to_string()); return x; })
    .scan(0, (a: i32, x: i32): i32 => { print("s" + x.to_string()); return a + x; });
  if (out.len() != 3 || out[2] != 6) { return 1; }
  return 0;
}`
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			out, code := runSelfHostFusionProgram(t, target, src)
			if code != 0 || strings.Join(strings.Fields(out), "") != "m1m2m3s1s2s3" {
				t.Fatalf("scan effects: exit %d\n%s", code, out)
			}
		})
	}
}

// Reservation uses only local memory, so both default component framings
// must admit it without requiring an extra WASI import.
func TestSelfHostArrayFusionScanComponents(t *testing.T) {
	for _, output := range []bool{false, true} {
		name, src, want := "pure", arrayFusionScanAllocationSrc, ""
		if output {
			name, want = "stdout", "ok\n"
			src = strings.Replace(src, "function main(): i32", "function check_main(): i32", 1)
			src += "\nfunction main(): i32 { let code = check_main(); if (code == 0) { print(\"ok\"); } return code; }\n"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			entry, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, entry, bin)
			cmd.Env = e2eharness.SelfHostChildEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("scan component compile: %v\n%s", err, out)
			}
			if out, err := e2eharness.RunWasmCore(t, bin).CombinedOutput(); err != nil || string(out) != want {
				t.Fatalf("scan component: %v, output %q, want %q", err, out, want)
			}
		})
	}
}
