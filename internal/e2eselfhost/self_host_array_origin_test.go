package e2eselfhost

import (
	"os"
	"path/filepath"
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

// Calling the genuine map from a user function with a mangled-looking name
// does not make it a stdlib combinator. This wrapper reverses its result.
const customArrayWrapperDecl = `import "std/array";
function __arrm_map[T](xs: T[], f: (T) => T): T[] {
  let mapped = array.map(xs, f);
  let out: T[] = [];
  let i = mapped.len();
  while (i > 0) { i = i - 1; out = out.append(mapped[i]); }
  return out;
}
`

const customArrayWrapper = customArrayWrapperDecl + `function main(): i32 {
  let xs = __arrm_map([2, 3], (x: i32): i32 => x * 2);
  let n = array.fold(xs, 0, (a: i32, x: i32): i32 => a * 10 + x);
  if (n != 64) { return 7; }
  return 0;
}
`

const customArrayScaleWrapper = customArrayWrapperDecl + `function main(): i32 {
  let xs = __arrm_map([2.0, 3.0], (x: f64): f64 => x * 2.0);
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
