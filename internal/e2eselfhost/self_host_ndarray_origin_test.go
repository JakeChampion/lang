package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Matching the stdlib's basename, record fields, method names and signatures
// does not give a user module its semantics. These methods reverse the left
// input, which both packed kernel rewrites would incorrectly discard.
const customNdarrayModule = `pub struct NdArray[T] { data: T[], shape: i32[], strides: i32[], offset: i32 }
pub function from_flat[T](data: T[], shape: i32[]): NdArray[T] {
  return NdArray[T] { data: data, shape: shape, strides: [1], offset: 0 };
}
pub function join(a: i32[], b: i32[]): i32[] {
  let out = a;
  for n in b { out = out.append(n); }
  return out;
}
pub function check_shape(shape: i32[]): void {}
pub function (a: NdArray[T]) map[T, U](f: (T) => U): NdArray[U] {
  let out: U[] = [];
  let i: i32 = a.data.len();
  while (i > 0) {
    i = i - 1;
    out = out.append(f(a.data[i]));
  }
  return from_flat(out, a.shape);
}
pub function (a: NdArray[T]) outer[T, U, V](b: NdArray[U], f: (T, U) => V): NdArray[V] {
  let out: V[] = [];
  let i: i32 = a.data.len();
  while (i > 0) {
    i = i - 1;
    for y in b.data { out = out.append(f(a.data[i], y)); }
  }
  return from_flat(out, join(a.shape, b.shape));
}
`

const customNdarrayChecks = `
  let a = custom.from_flat([2.0, 3.0], [2]);
  let b = a.map((x: f64): f64 => x * 2.0);
  if (b.data[0] != 6.0 || b.data[1] != 4.0) { return 7; }
  let c = a.outer(a, (x: f64, y: f64): f64 => x * y);
  if (c.data.len() != 4 || c.data[0] != 6.0 || c.data[1] != 9.0 || c.data[2] != 4.0 || c.data[3] != 6.0) { return 8; }
`

const standardNdarrayChecks = `
  let s = standard.from_flat([2.0, 3.0], [2]);
  let m = s.map((x: f64): f64 => x * 2.0).to_flat();
  if (m[0] != 4.0 || m[1] != 6.0) { return 9; }
  let o = s.outer(s, (x: f64, y: f64): f64 => x * y).to_flat();
  if (o[0] != 4.0 || o[1] != 6.0 || o[2] != 6.0 || o[3] != 9.0) { return 10; }
`

func TestSelfHostNdarrayModuleOrigin(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name, imports, checks string
	}{
		{"custom-only", `import "./ndarray" as custom;`, customNdarrayChecks},
		{"stdlib-fallback", `import "std/ndarray" as custom;`, customNdarrayChecks},
		{"custom-first", "import \"./ndarray\" as custom;\nimport \"std/ndarray\" as standard;", customNdarrayChecks + standardNdarrayChecks},
		{"standard-first", "import \"std/ndarray\" as standard;\nimport \"./ndarray\" as custom;", customNdarrayChecks + standardNdarrayChecks},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			compiler := *cli
			if tc.name == "stdlib-fallback" {
				// A missing configured stdlib can fall back to a local basename.
				// The import spelling alone cannot grant stdlib provenance.
				compiler.stdlib = t.TempDir()
			}
			for name, source := range map[string]string{
				"ndarray.fern": customNdarrayModule,
				"main.fern":    tc.imports + "\nfunction main(): i32 {\n" + tc.checks + "return 0;\n}\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "standard-first" {
				asm := filepath.Join(dir, "report.s")
				cmd := runX86_64Bin(compiler.runner, compiler.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", asm, filepath.Join(dir, "main.fern"), compiler.stdlib)
				cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT=1", "FERN_NO_SCALE_KERNEL=0", "FERN_NO_PRODUCT_KERNEL=0")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("primary compiler report: %v\n%s", err, out)
				}
				for _, reason := range []string{"scale-kernel", "outer-mul-kernel"} {
					if !strings.Contains(string(out), "reason="+reason+" ") {
						t.Fatalf("stdlib %s must remain selected beside a custom module:\n%s", reason, out)
					}
				}
			}
			for _, target := range selfHostFusionTargets {
				t.Run(target, func(t *testing.T) {
					for _, disabled := range []string{"0", "1"} {
						t.Run("disabled="+disabled, func(t *testing.T) {
							out, code := compiler.exitOfFile(t, filepath.Join(dir, "main.fern"), target, nil,
								"FERN_NO_SCALE_KERNEL="+disabled, "FERN_NO_PRODUCT_KERNEL="+disabled)
							if code != 0 {
								t.Fatalf("module origin: exit %d\n%s", code, out)
							}
						})
					}
				})
			}
		})
	}
}
