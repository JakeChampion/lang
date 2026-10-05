package e2eselfhost

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Compare non-NaN result bits with a scalar operation on the same target,
// including signed zero, infinities and subnormals. FS-04 leaves arithmetic
// NaN payloads unspecified: require NaN production, while unchanged inputs
// retain their exact payloads. Rebuilding the reference keeps donors unique.
const scaleReuseSrc = `import "std/ndarray";
import "std/i64";
@noinline function value(i: i32): f64 {
  let bits: i64[] = [0i64, 0i64 - 9223372036854775807i64 - 1i64,
    9218868437227405312i64, 0i64 - 4503599627370496i64,
    9221120237041090625i64, 9221120237041090626i64,
    9218868437227405313i64, 1i64, 0i64 - 9223372036854775807i64,
    4609434218613702656i64, 0i64 - 4613937818241073152i64];
  return f64_from_bits(bits[i % bits.len()]);
}
@noinline function scalar(x: f64, k: f64): f64 { return x * k; }
function same_result(actual: f64, expected: f64): boolean {
  if (expected != expected) { return actual != actual; }
  return f64_bits(actual) == f64_bits(expected);
}
function build(n: i32): f64[] {
  let xs: f64[] = [];
  let i: i32 = 0;
  while (i < n) { xs = xs.append(value(i)); i = i + 1; }
  return xs;
}
@noinline function consumed(own xs: f64[], k: f64): f64[] { return __scale_f64(xs, k); }
@noinline function borrowed(xs: f64[], k: f64): f64[] { return __scale_f64(xs, k); }
@noinline function borrowed_read(xs: f64[], k: f64): i64 {
  let out: f64[] = __scale_f64(xs, k);
  return f64_bits(out[0]);
}
@noinline function still_live(own xs: f64[], k: f64): (f64[], f64[]) {
  let out: f64[] = __scale_f64(xs, k);
  return (out, xs);
}
@noinline function consumed_nd(own a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
function correct(xs: f64[], n: i32, k: f64): boolean {
  if (xs.len() != n) { return false; }
  let i: i32 = 0;
  while (i < n) {
    let reference: f64 = scalar(value(i), k);
    let actual: i64 = f64_bits(xs[i]);
    let expected: i64 = f64_bits(reference);
    if (!same_result(xs[i], reference)) {
      eprint("scale mismatch: actual=" + actual.to_string() + " expected=" + expected.to_string()
        + " input=" + f64_bits(value(i)).to_string() + " factor=" + f64_bits(k).to_string());
      return false;
    }
    i = i + 1;
  }
  return true;
}
function unchanged(xs: f64[], n: i32): boolean {
  if (xs.len() != n) { return false; }
  let i: i32 = 0;
  while (i < n) {
    if (f64_bits(xs[i]) != f64_bits(value(i))) { return false; }
    i = i + 1;
  }
  return true;
}
function main(): i32 {
  // The NaN freedom must not hide finite errors or lost signed zero, and a
  // finite result must not satisfy a reference that should produce NaN.
  if (same_result(1.0, 2.0) || same_result(value(0), value(1))
    || same_result(value(4), 1.0) || same_result(1.0, value(4))
    || !same_result(value(4), value(5))) { return 10; }
  let n: i32 = 0;
  while (n < 42) {
    let j: i32 = 0;
    while (j < 11) {
      let k: f64 = value(j);
      let fresh: f64[] = consumed(build(n), k);
      if (!correct(fresh, n, k)) { return 1; }
      let alias: f64[] = build(n);
      let donor: f64[] = alias;
      donor = consumed(donor, k);
      if (!correct(donor, n, k) || !unchanged(alias, n)) { return 2; }
      if (!correct(borrowed(alias, k), n, k) || !unchanged(alias, n)) { return 3; }
      if (n > 0 && !same_result(f64_from_bits(borrowed_read(alias, k)), scalar(value(0), k))) { return 9; }
      let live: (f64[], f64[]) = still_live(build(n), k);
      if (!correct(live.0, n, k) || !unchanged(live.1, n)) { return 4; }
      j = j + 1;
    }
    let a: ndarray.NdArray[f64] = ndarray.from_flat(build(n), [1, n]);
    a = consumed_nd(a);
    if (!correct(a.to_flat(), n, 2.0) || a.shape()[0] != 1 || a.shape()[1] != n || !a.is_packed()) { return 5; }
    // A unique handle may still contain shared data. A shared handle also
    // holds a live child even when no separate array binding survives.
    let data: f64[] = build(n);
    let b: ndarray.NdArray[f64] = ndarray.from_flat(data, [1, n]);
    b = consumed_nd(b);
    if (!correct(b.to_flat(), n, 2.0) || !unchanged(data, n)) { return 6; }
    let handle: ndarray.NdArray[f64] = ndarray.from_flat(build(n), [1, n]);
    let nd_donor: ndarray.NdArray[f64] = handle;
    nd_donor = consumed_nd(nd_donor);
    if (!correct(nd_donor.to_flat(), n, 2.0) || !unchanged(handle.to_flat(), n)) { return 7; }
    n = n + 1;
  }
  // Literal storage may be immortal. It must never be overwritten in place.
  let literal: f64[] = [1.0, 2.0, 3.0];
  let literal_donor: f64[] = literal;
  literal_donor = consumed(literal_donor, 2.0);
  if (literal_donor[2] != 6.0 || literal[2] != 3.0) { return 8; }
  return 0;
}
`

func TestSelfHostScaleReuseDecisions(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	dir := e2eharness.WriteSourceModloadProject(t, scaleReuseSrc)
	compile := func(report, disabled string) (string, string) {
		t.Helper()
		cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
		cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT="+report, "FERN_SELFHOST_NO_REUSE="+disabled)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		asm, err := cmd.Output()
		if err != nil {
			t.Fatalf("compile scale reuse: %v\n%s", err, stderr.String())
		}
		return string(asm), stderr.String()
	}
	asm, report := compile("1", "0")
	quietASM, quiet := compile("0", "0")
	if quiet != "" || quietASM != asm {
		t.Fatal("reporting changed emitted code or printed when disabled")
	}
	for _, tc := range []struct{ name, reason string }{
		{"consumed", "guarded-reuse"},
		{"consumed_nd", "guarded-reuse"},
		{"borrowed", "borrow-linked-result"},
		{"borrowed_read", "borrow-linked-result"},
		{"still_live", "receiver-still-live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := strings.Index(report, tc.name+": scale; ownership lowering\n")
			if start < 0 || !strings.Contains(strings.SplitN(report[start:], "\n", 3)[1], "reason="+tc.reason+" ") {
				t.Fatalf("missing %s decision for %s:\n%s", tc.reason, tc.name, report)
			}
			body := emittedBody(t, asm, "__fn_"+tc.name)
			guard := strings.Contains(body, "cmpl $1, -8(")
			copy := strings.Contains(body, "call __fn___fern_arr_slice")
			fresh := strings.Contains(body, "call __fern_arr_box")
			if tc.reason == "guarded-reuse" {
				if !guard || copy || !fresh || strings.Count(body, "unpcklpd") != 2 {
					t.Fatalf("missing guarded SIMD reuse, guard=%t copy=%t fresh=%t\n%s", guard, copy, fresh, body)
				}
			} else if !fresh {
				t.Fatalf("refused site did not allocate a fresh kernel buffer:\n%s", body)
			}
		})
	}
	for _, disabled := range []string{"0", "1"} {
		code, diagnostic := asm, report
		if disabled == "1" {
			code, diagnostic = compile("1", "1")
			if strings.Contains(diagnostic, "reason=guarded-reuse ") || !strings.Contains(diagnostic, "reason=reuse-disabled ") {
				t.Fatalf("disabled reuse reported success:\n%s", diagnostic)
			}
			for _, name := range []string{"borrowed", "borrowed_read", "still_live"} {
				start := strings.Index(report, name+": scale; ownership lowering\n")
				if start < 0 {
					t.Fatalf("missing refusal for %s:\n%s", name, report)
				}
				lines := strings.SplitN(report[start:], "\n", 3)
				if !strings.Contains(diagnostic, lines[0]+"\n"+lines[1]) {
					t.Fatalf("disabled reuse lost intrinsic refusal for %s:\n%s", name, diagnostic)
				}
			}
		}
		bin := buildBin(t, gcc, dir, "scale-reuse-"+disabled, code)
		if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
			t.Fatalf("scale reuse disabled=%s: %v\n%s", disabled, err, out)
		}
	}
	shadow := `function __scale_f64(xs: f64[], k: f64): f64[] { return [99.0]; }
@noinline function consumed(own xs: f64[]): f64[] { return __scale_f64(xs, 2.0); }
function main(): i32 {
  let xs: f64[] = [1.0, 2.0];
  xs = consumed(xs);
  if (xs.len() != 1 || xs[0] != 99.0) { return 1; }
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(shadow), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, disabled := range []string{"0", "1"} {
		code, diagnostic := compile("1", disabled)
		if !strings.Contains(diagnostic, "reason=builtin-shadowed storage=user-call") {
			t.Fatalf("shadowed intrinsic reported kernel storage:\n%s", diagnostic)
		}
		bin := buildBin(t, gcc, dir, "scale-shadow-"+disabled, code)
		if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
			t.Fatalf("shadowed intrinsic: %v\n%s", err, out)
		}
	}
}

func runScaleTarget(t *testing.T, target, bin string, args ...string) *exec.Cmd {
	t.Helper()
	switch target {
	case e2eharness.TargetWasm32Wasi:
		return e2eharness.RunWasmCore(t, bin, args...)
	case e2eharness.TargetArm64Linux:
		return e2eharness.RunArm64Bin(e2eharness.Arm64Runner(t), bin, args...)
	default:
		return runX86_64Bin(e2eharness.X86_64Runner(t), bin, args...)
	}
}

func TestSelfHostScaleReuseValues(t *testing.T) {
	for _, target := range []string{e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					src := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(src, []byte(scaleReuseSrc), 0o644); err != nil {
						t.Fatal(err)
					}
					bin := e2eharness.CompileSelfHostFile(t, target, src, []string{"FERN_SELFHOST_NO_REUSE=" + disabled})
					if out, err := runScaleTarget(t, target, bin).CombinedOutput(); err != nil {
						t.Fatalf("scale reuse: %v\n%s", err, out)
					}
				})
			}
		})
	}
}

func TestSelfHostNdarrayScaleReuseAllocation(t *testing.T) {
	src := filepath.Join(repoRootFromTest(t), "examples", "array_pipeline", "ndarray_owned_scale.fern")
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			type counters struct {
				Calls int64 `json:"alloc_calls"`
				Bytes int64 `json:"fresh_bytes"`
			}
			results := map[string]counters{}
			for _, disabled := range []string{"0", "1"} {
				bin := e2eharness.CompileSelfHostFile(t, target, src, []string{"FERN_SELFHOST_NO_REUSE=" + disabled})
				for _, shared := range []string{"0", "1"} {
					out, err := runScaleTarget(t, target, bin, "1000", "2", shared).CombinedOutput()
					if err != nil {
						t.Fatalf("disabled=%s shared=%s: %v\n%s", disabled, shared, err, out)
					}
					var got counters
					if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
						t.Fatalf("decode counters: %v\n%s", err, out)
					}
					results[disabled+shared] = got
					t.Logf("disabled=%s shared=%s: %+v", disabled, shared, got)
				}
			}
			unique, shared, fresh := results["00"], results["01"], results["10"]
			if unique.Calls >= shared.Calls || unique.Bytes >= shared.Bytes || unique.Calls >= fresh.Calls || unique.Bytes >= fresh.Bytes {
				t.Fatalf("reuse did not reduce both counters: unique=%+v shared=%+v disabled=%+v", unique, shared, fresh)
			}
		})
	}
}
