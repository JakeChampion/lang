package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func innerMulAddSource() string {
	src := strings.Replace(ndarrayInnerBitsSrc, `import "std/ndarray";`, "import \"std/ndarray\";\nimport \"std/array\";", 1)
	src = strings.Replace(src, "let out = a.inner(b, init, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);", "let out = ndarray.from_flat(array.inner_mul_add_f64(xs, ys, rows, extent, columns, init), [rows, columns]);", 1)
	src = strings.Replace(src, "// A reassociated sum", `if (array.inner_mul_add_f64([], [], 2147483647, 0, 0, -0.0).len() != 0) { return 7; }
  if (array.inner_mul_add_f64([], [], 0, 0, 2147483647, -0.0).len() != 0) { return 8; }
  // The separately rounded product is 1.0. An FMA retains its small error.
  let rounded = array.inner_mul_add_f64([1.0000000000000002], [0.9999999999999998, 0.9999999999999998, 0.9999999999999998], 1, 1, 3, -1.0);
  for x in rounded { if (f64_bits(x) != 0i64) { return 10; } }
  n = 0;
  while (n < 18) {
    let columns: i32 = 0;
    while (columns < 18) {
      let rows: i32 = n % 3;
      if (!check(build(rows * n, 0), build(n * columns, 2), rows, n, columns, value(columns))) { return 9; }
      columns = columns + 1;
    }
    n = n + 1;
  }
  // A reassociated sum`, 1)
	return src
}

func TestSelfHostInnerMulAddBits(t *testing.T) {
	src := innerMulAddSource()
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, src, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("inner kernel: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			line := leakSummaryLine(string(out))
			if _, err := fmtSscan(line, &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("inner kernel ownership: %v\n%s", err, out)
			}
		})
	}
	t.Run("interpreters", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "main.fern")
		oracle := strings.Replace(src, "if (__rc_underflow_count() != 0) { return 6; }", "", 1)
		if err := os.WriteFile(path, []byte(oracle), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
			cmd := exec.Command(compiler, "-interp", path, e2eharness.SelfHostStdlibRoot(t))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", compiler, err, out)
			}
		}
	})
}

func TestSelfHostInnerMulAddGeometry(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, dims := range []string{"-1, 0, 0", "0, -1, 0", "0, 0, -1", "1, 1, 1", "2147483647, 0, 2", "65536, 65536, 0", "0, 65536, 65536"} {
				src := `import "std/array"; function main(): i32 { return array.inner_mul_add_f64([], [], ` + dims + `, 0.0).len(); }`
				bin := e2eharness.CompileSelfHostSource(t, target, src, nil)
				cmd := runScaleTarget(t, target, bin)
				out, err := cmd.CombinedOutput()
				if target == e2eharness.TargetWasm32Wasi {
					if err == nil || !strings.Contains(string(out), "unreachable") {
						t.Fatalf("expected geometry trap for %s: %v\n%s", dims, err, out)
					}
				} else if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
					t.Fatalf("expected geometry exit 134 for %s: %v\n%s", dims, err, out)
				}
			}
		})
	}
	t.Run("interpreters", func(t *testing.T) {
		for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
			for _, dims := range []string{"-1, 0, 0", "0, -1, 0", "0, 0, -1", "1, 1, 1", "2147483647, 0, 2", "65536, 65536, 0", "0, 65536, 65536"} {
				path := filepath.Join(t.TempDir(), "geometry.fern")
				src := `function main(): i32 { return __inner_mul_add_f64([], [], ` + dims + `, 0.0).len(); }`
				if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(compiler, "-interp", path, e2eharness.SelfHostStdlibRoot(t))
				out, err := cmd.CombinedOutput()
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
					t.Fatalf("%s: expected geometry exit 134 for %s: %v\n%s", compiler, dims, err, out)
				}
			}
		}
	})
}

func TestSelfHostInnerMulAddEmissionAndShadow(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, _ := compileSourceModload(t, runner, driver, innerMulAddSource())
	body := emittedBody(t, asm, "__fn_array__inner_mul_add_f64")
	for _, op := range []string{"mulpd", "addpd", "mulsd", "addsd", "__fern_oob_abort"} {
		if !strings.Contains(body, op) {
			t.Fatalf("inner kernel missing %s:\n%s", op, body)
		}
	}
	for _, op := range []string{"fmadd", "hadd", "dppd"} {
		if strings.Contains(body, op) {
			t.Fatalf("inner kernel changes reduction order with %s:\n%s", op, body)
		}
	}
	const shadow = `function __inner_mul_add_f64(a: f64[], b: f64[], rows: i32, extent: i32, columns: i32, init: f64): f64[] { return [99.0]; }
function main(): i32 {
  let out = __inner_mul_add_f64([2.0], [3.0], 1, 1, 1, 0.0);
  if (out.len() != 1 || out[0] != 99.0) { return 1; }
  return 0;
}`
	asm, dir := compileSourceModload(t, runner, driver, shadow)
	bin := buildBin(t, gcc, dir, "inner-shadow", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("inner builtin shadow: %v\n%s", err, out)
	}
}
