package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const customArrayModule = `pub function map[T, U](xs: T[], f: (T) => U): U[] {
  let out: U[] = [];
  let i: i32 = xs.len();
  while (i > 0) {
    i = i - 1;
    out = out.append(f(xs[i]));
  }
  return out;
}
pub function fold[T, U](xs: T[], init: U, f: (U, T) => U): U {
  let out = init;
  for x in xs { out = f(out, x); }
  return out;
}
`

const customArrayMain = `import "./array";
function main(): i32 {
  let xs = array.map([2, 3], (x: i32): i32 => x * 2);
  let n = array.fold(xs, 0, (a: i32, x: i32): i32 => a * 10 + x);
  if (n != 64) { return 7; }
  return 0;
}
`

const customArrayWrapperDecl = `import "std/array";
function wrap_map[T](xs: T[], f: (T) => T): T[] {
  let mapped = array.map(xs, f);
  let out: T[] = [];
  let i = mapped.len();
  while (i > 0) { i = i - 1; out = out.append(mapped[i]); }
  return out;
}
`

const customArrayWrapper = customArrayWrapperDecl + `function main(): i32 {
  let xs = wrap_map([2, 3], (x: i32): i32 => x * 2);
  let n = array.fold(xs, 0, (a: i32, x: i32): i32 => a * 10 + x);
  if (n != 64) { return 7; }
  return 0;
}
`

const customArrayScaleWrapper = customArrayWrapperDecl + `function main(): i32 {
  let xs = wrap_map([2.0, 3.0], (x: f64): f64 => x * 2.0);
  if (xs[0] != 6.0 || xs[1] != 4.0) { return 7; }
  return 0;
}
`

func TestSelfHostArrayFusionModuleOrigin(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source string }{
		{"custom-module", customArrayMain},
		{"custom-wrapper", customArrayWrapper},
		{"custom-scale-wrapper", customArrayScaleWrapper},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range map[string]string{"main.fern": tc.source, "array.fern": customArrayModule} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, target := range selfHostFusionTargets {
				t.Run(target, func(t *testing.T) {
					for _, disabled := range []string{"0", "1"} {
						t.Run("disabled="+disabled, func(t *testing.T) {
							out, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), target, nil, "FERN_NO_ARRAY_FUSION="+disabled)
							if code != 0 {
								t.Fatalf("custom array semantics: exit %d\n%s", code, out)
							}
						})
					}
				})
			}
		})
	}
}

// A free function that has the symbol of a declared array or slice method is
// a redeclaration. Without the diagnostic, calls to it reach the method.
func TestSelfHostFreeFunctionNamedLikeArrayMethod(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source, symbol string }{
		{"generic-array", strings.Replace(customArrayWrapper, "wrap_map", "__arrm_map", -1), "__arrm_map"},
		{"generic-slice", "function (xs: [T]) head(): T { return xs[0]; }\nfunction __viewm_head(x: i32): i32 { return x; }\nfunction main(): i32 { let a: i32[] = [1]; return __viewm_head(a[0]); }\n", "__viewm_head"},
		{"concrete-array", "function (xs: i32[]) total(): i32 { return xs.len(); }\nfunction __carrm_i32__total(xs: i32[]): i32 { return 0; }\nfunction main(): i32 { let a: i32[] = [1]; return a.total(); }\n", "__carrm_i32__total"},
		{"concrete-slice", "function (xs: [u8]) first(): u8 { return xs[0]; }\nfunction __cviewm_u8__first(xs: [u8]): u8 { return 0; }\nfunction main(): i32 { return 0; }\n", "__cviewm_u8__first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := cli.compileCmd("x86-64-linux", filepath.Join(dir, "prog"), src).CombinedOutput()
			want := "error[E006]: function \"" + tc.symbol + "\" redeclared"
			if err == nil || !strings.Contains(string(out), want) {
				t.Fatalf("want %q, got err=%v\n%s", want, err, out)
			}
		})
	}
}
