package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A pool larger than the register argument set must preserve runtime argument
// cycles, stack arguments and callee-saved floating-point values.
func TestSelfHostArm64ExtraCallerRegisters(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for name, source := range map[string]string{
		"runtime-argument-cycle":  armRuntimeArgumentCycle,
		"float-argument-boundary": armFloatArgumentBoundary,
	} {
		t.Run(name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, "arm64-linux"); code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
		})
	}
	// Nine simultaneously live, short-lived integer parameters fit the caller
	// pool. None needs a saved integer register just to reach its last use.
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(armRuntimeArgumentCycle), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(asm), "wrap")
	if regexp.MustCompile(`(?m)^\s+(?:stp|str)\s+x(?:19|2[0-8])\b`).MatchString(body) {
		t.Fatalf("short-lived parameters unnecessarily consume callee-saved registers:\n%s", body)
	}
}

const armRuntimeArgumentCycle = `@noinline function wrap(n: i32, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, p: usize): string {
  __store_i64(p, b);
  __store_i64(p + 8, c);
  __store_i64(p + 16, d);
  __store_i64(p + 24, e);
  __store_i64(p + 32, f);
  __store_i64(p + 40, g);
  __store_i64(p + 48, h);
  return __raw_string(p, n);
}
function main(): i32 {
  let p = __raw_alloc(64);
  let s = wrap(3, 6513249i64, 2i64, 3i64, 4i64, 5i64, 6i64, 7i64, p);
  if (__raw_load8(p, 48) != 7) { return 1; }
  if (s != "abc") { return 2; }
  return 0;
}
`

const armFloatArgumentBoundary = `@noinline function sum9(a: f64, b: f64, c: f64, d: f64, e: f64, f: f64, g: f64, h: f64, i: f64): f64 {
  return a + b + c + d + e + f + g + h + i;
}
@noinline function indirect(f: (f64, f64, f64, f64, f64, f64, f64, f64, f64) => f64, x: f64): f64 {
  return f(1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0) + x;
}
function main(): i32 {
  let salt = args().len() as f64;
  let got = indirect(sum9, salt + 10.5);
  if (got != salt + 55.5) { return 1; }
  return 0;
}
`
