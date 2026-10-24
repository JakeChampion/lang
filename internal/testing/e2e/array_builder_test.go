package e2e

import "testing"

var arrayBuilderCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "append-while",
		src: `
import "core/int";
function main(): i32 {
  let out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    let i: i32 = 0;
    while (i < 5) { b.append(i * 2); i = i + 1; }
  });
  return out[0] + out[1] + out[2] + out[3] + out[4];
}`,
		want: 20,
	},
	{
		name: "append-for-in",
		src: `
import "core/int";
function main(): i32 {
  let xs: i32[] = [1, 2, 3];
  let out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    for x in xs { b.append(x * x); }
  });
  return out[0] + out[1] + out[2];
}`,
		want: 14,
	},
	{
		name: "with-elem-set",
		src: `
import "core/int";
function main(): i32 {
  let out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    b.append(0); b.append(0); b.append(0);
    b.with(1, 99);
  });
  return out[1];
}`,
		want: 99,
	},
	{
		name: "len-read",
		src: `
import "core/int";
function main(): i32 {
  let out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
    let i: i32 = 1;
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
		name: "churn",
		src: `
import "core/int";
function main(): i32 {
  let acc: i32 = 0;
  let c: i32 = 0;
  while (c < 200) {
    let out: i32[] = Array.build((b: ArrayBuilder[i32]): void => {
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
