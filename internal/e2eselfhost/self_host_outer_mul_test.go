package e2eselfhost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

const outerMulSrc = `import "std/array";
@noinline function value(i: i32): f64 {
  let bits: i64[] = [0i64, 0i64 - 9223372036854775807i64 - 1i64,
    9218868437227405312i64, 0i64 - 4503599627370496i64,
    9221120237041090625i64, 9221120237041090626i64,
    9218868437227405313i64, 1i64, 0i64 - 9223372036854775807i64,
    4609434218613702656i64, 0i64 - 4613937818241073152i64];
  return f64_from_bits(bits[i % bits.len()]);
}
@noinline function scalar(x: f64, y: f64): f64 { return x * y; }
function build(n: i32): f64[] {
  let xs: f64[] = [];
  let i: i32 = 0;
  while (i < n) { xs = xs.append(value(i)); i = i + 1; }
  return xs;
}
@noinline function kernel(a: f64[], b: f64[]): f64[] { return array.outer_mul_f64(a, b); }
function check(a: f64[], b: f64[]): boolean {
  let out: f64[] = kernel(a, b);
  if (out.len() != a.len() * b.len()) { return false; }
  let i: i32 = 0;
  while (i < a.len()) {
    let j: i32 = 0;
    while (j < b.len()) {
      if (f64_bits(out[i * b.len() + j]) != f64_bits(scalar(a[i], b[j]))) { return false; }
      if (f64_bits(b[j]) != f64_bits(value(j))) { return false; }
      j = j + 1;
    }
    if (f64_bits(a[i]) != f64_bits(value(i))) { return false; }
    i = i + 1;
  }
  return true;
}
function main(): i32 {
  let n: i32 = 0;
  while (n < 18) {
    let m: i32 = 0;
    while (m < 18) {
      let a: f64[] = build(n);
      let b: f64[] = build(m);
      if (!check(a, b)) { return 1; }
      m = m + 1;
    }
    let both: f64[] = build(n);
    if (!check(both, both)) { return 2; }
    n = n + 1;
  }
  let literal: f64[] = [2.0, 3.0, 4.0];
  let square: f64[] = kernel(literal, literal);
  if (square[0] != 4.0 || square[1] != 6.0 || square[2] != 8.0 || square[3] != 6.0 || square[8] != 16.0 || literal[1] != 3.0) { return 3; }
  if (__rc_underflow_count() != 0) { return 4; }
  return 0;
}
`

func TestSelfHostOuterMulBits(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, outerMulSrc, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("outer multiplication: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			line := leakSummaryLine(string(out))
			if _, err := fmtSscan(line, &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("outer multiplication ownership: %v\n%s", err, out)
			}
		})
	}
	t.Run("interpreters", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		// The Fern interpreter has no physical RC counter. The compiled legs
		// above check that counter as well as the complete exit-time census.
		oracle := strings.Replace(outerMulSrc, "if (__rc_underflow_count() != 0) { return 4; }", "", 1)
		if err := os.WriteFile(src, []byte(oracle), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
			cmd := exec.Command(compiler, "-interp", src, e2eharness.SelfHostStdlibRoot(t))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", compiler, err, out)
			}
		}
	})
}

func TestSelfHostOuterMulEmissionAndShadow(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, _ := compileSourceModload(t, runner, driver, outerMulSrc)
	caller := emittedBody(t, asm, "__fn_kernel")
	if !strings.Contains(caller, "call __fn_array__outer_mul_f64") {
		t.Fatalf("missing stdlib kernel wrapper call:\n%s", caller)
	}
	body := emittedBody(t, asm, "__fn_array__outer_mul_f64")
	for _, op := range []string{"unpcklpd", "mulpd", "mulsd", "__fern_oob_abort"} {
		if !strings.Contains(body, op) {
			t.Fatalf("outer kernel missing %s:\n%s", op, body)
		}
	}
	shadow := `function __outer_mul_f64(a: f64[], b: f64[]): f64[] { return [99.0]; }
function main(): i32 {
  let out = __outer_mul_f64([2.0], [3.0]);
  if (out.len() != 1 || out[0] != 99.0) { return 1; }
  return 0;
}`
	asm, dir := compileSourceModload(t, runner, driver, shadow)
	bin := buildBin(t, gcc, dir, "outer-shadow", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("outer builtin shadow: %v\n%s", err, out)
	}
}

func TestSelfHostOuterMulOverflow(t *testing.T) {
	const src = `function main(): i32 {
  let a: f64[] = [];
  let i: i32 = 0;
  while (i < 46341) { a = a.append(1.0); i = i + 1; }
  let out = __outer_mul_f64(a, a);
  return out.len();
}`
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, src, nil)
			cmd := runScaleTarget(t, target, bin)
			out, err := cmd.CombinedOutput()
			if target == e2eharness.TargetWasm32Wasi {
				if err == nil || !strings.Contains(string(out), "unreachable") {
					t.Fatalf("expected bounds trap: %v\n%s", err, out)
				}
			} else if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
				t.Fatalf("expected overflow exit 134: %v\n%s", err, out)
			}
		})
	}
	t.Run("interpreters", func(t *testing.T) {
		// Grow through the intrinsic so the Fern interpreter does not spend
		// quadratic time interpreting immutable append for a large input.
		const oracle = `function main(): i32 {
  let small: f64[] = [];
  let i: i32 = 0;
  while (i < 256) { small = small.append(1.0); i = i + 1; }
  let a = __outer_mul_f64(small, small);
  let out = __outer_mul_f64(a, a);
  return out.len();
}`
		src := filepath.Join(t.TempDir(), "overflow.fern")
		if err := os.WriteFile(src, []byte(oracle), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
			cmd := exec.Command(compiler, "-interp", src, e2eharness.SelfHostStdlibRoot(t))
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
				t.Fatalf("%s: expected overflow exit 134: %v\n%s", compiler, err, out)
			}
		}
	})
}

func TestSelfHostOuterMulBenchmark(t *testing.T) {
	src := filepath.Join(repoRootFromTest(t), "examples", "array_pipeline", "ndarray_outer.fern")
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostFile(t, target, src, nil)
			type counters struct {
				Calls int64 `json:"alloc_calls"`
				Bytes int64 `json:"fresh_bytes"`
			}
			results := map[string]counters{}
			for _, mode := range []string{"0", "1", "2"} {
				out, err := runScaleTarget(t, target, bin, "32", "32", "2", mode).CombinedOutput()
				if err != nil {
					t.Fatalf("outer benchmark mode=%s: %v\n%s", mode, err, out)
				}
				var got counters
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatalf("benchmark report: %v\n%s", err, out)
				}
				results[mode] = got
			}
			kernel, ordinary := results["2"], results["0"]
			if kernel.Calls <= 0 || kernel.Bytes <= 0 || kernel.Calls >= ordinary.Calls || kernel.Bytes >= ordinary.Bytes {
				t.Fatalf("kernel must reduce both counters: kernel=%+v ordinary=%+v", kernel, ordinary)
			}
		})
	}
}
