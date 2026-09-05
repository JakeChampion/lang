package e2e

import "testing"

// Array.build — the scoped linear builder (docs/ARRAY-BUILDER-PLAN.md).
// `Array.build(function(b: ArrayBuilder[T]): void { ... b.append(x) ... })`
// desugars (parser) to a unique-local IIFE: `b` is a fresh non-escaping
// array, `b.append`/`b.with` become in-place reassignments, and the array
// is returned frozen. Pure desugar, so every Go backend gets it; verified
// here on x86-64, wasm, and the interpreter.
var arrayBuilderCases = []struct {
	name string
	src  string
	want int
}{
	{
		// append in a while loop: [0,2,4,6,8]; sum = 20.
		name: "append-while",
		src: `
import "core/int";
function main(): i32 {
  var out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    var i: i32 = 0;
    while (i < 5) { b.append(i * 2); i = i + 1; }
  });
  return out[0] + out[1] + out[2] + out[3] + out[4];
}`,
		want: 20,
	},
	{
		// append over a for-in loop (the canonical map-like use): builds
		// [1,4,9] from [1,2,3]; sum = 14.
		name: "append-for-in",
		src: `
import "core/int";
function main(): i32 {
  var xs: i32[] = [1, 2, 3];
  var out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    for x in xs { b.append(x * x); }
  });
  return out[0] + out[1] + out[2];
}`,
		want: 14,
	},
	{
		// b.with(i, v) — in-place element set inside the builder. Build
		// [0,0,0] then overwrite index 1 with 99; return out[1].
		name: "with-elem-set",
		src: `
import "core/int";
function main(): i32 {
  var out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    b.append(0); b.append(0); b.append(0);
    b.with(1, 99);
  });
  return out[1];
}`,
		want: 99,
	},
	{
		// b.len() read inside the builder drives a conditional: append 1..=n
		// but stop appending once len reaches 3. Result [1,2,3]; sum 6.
		name: "len-read",
		src: `
import "core/int";
function main(): i32 {
  var out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    var i: i32 = 1;
    while (i <= 10) {
      if (b.len() < 3) { b.append(i); }
      i = i + 1;
    }
  });
  return out.len() * 10 + (out[0] + out[1] + out[2]);
}`,
		want: 36,
	},
	{
		// Churn: build a fresh array each iteration and sum its contents,
		// 200x. Exercises the in-place fast path + per-iteration reclaim
		// of the builder local. sum over c in 0..199 of (c + c) = 2*19900.
		name: "churn",
		src: `
import "core/int";
function main(): i32 {
  var acc: i32 = 0;
  var c: i32 = 0;
  while (c < 200) {
    var out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
      b.append(c);
      b.append(c);
    });
    acc = acc + out[0] + out[1];
    c = c + 1;
  }
  return acc - 39800;
}`,
		want: 0,
	},
}

func TestArrayBuilderX86_64(t *testing.T) {
	for _, c := range arrayBuilderCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("%s: got %d, want %d", c.name, code, c.want)
			}
		})
	}
}

func TestArrayBuilderWASM(t *testing.T) {
	for _, c := range arrayBuilderCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("%s: got %d, want %d", c.name, got, c.want)
			}
		})
	}
}

func TestArrayBuilderInterp(t *testing.T) {
	for _, c := range arrayBuilderCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpByte(t, c.src); got != c.want {
				t.Errorf("%s: got %d, want %d", c.name, got, c.want)
			}
		})
	}
}
