package e2e

import "testing"

var mapBuilderCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "insert-while",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
  let m: Map[i32, i32] = Map.build((b: MapBuilder[i32, i32]): void => {
    let i: i32 = 0;
    while (i < 5) { b.insert(i, i * 10); i = i + 1; }
  });
  return m.get_or(3, -1) + m.len();
}`,
		want: 35,
	},
	{
		name: "insert-for-in",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
  let xs: i32[] = [1, 2, 3];
  let m: Map[i32, i32] = Map.build((b: MapBuilder[i32, i32]): void => {
    for x in xs { b.insert(x, x * x); }
  });
  return m.get_or(3, -1) + m.len();
}`,
		want: 12,
	},
	{
		name: "len-read",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
  let m: Map[i32, i32] = Map.build((b: MapBuilder[i32, i32]): void => {
    let i: i32 = 0;
    while (i < 100) {
      if (b.len() < 3) { b.insert(i, i); }
      i = i + 1;
    }
  });
  return m.len();
}`,
		want: 3,
	},
	{
		name: "string-keys-overwrite",
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
  let m: Map[string, i32] = Map.build((b: MapBuilder[string, i32]): void => {
    b.insert("a", 1);
    b.insert("b", 2);
    b.insert("b", 9);
  });
  return m.get_or("a", -1) + m.get_or("b", -1) + m.len();
}`,
		want: 12,
	},
	{
		name: "churn",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
  let acc: i32 = 0;
  let c: i32 = 0;
  while (c < 200) {
    let m: Map[i32, i32] = Map.build((b: MapBuilder[i32, i32]): void => {
      b.insert(0, c);
      b.insert(1, c + 1);
    });
    acc = acc + m.get_or(0, 0) + m.get_or(1, 0);
    c = c + 1;
  }
  return acc - 40000;
}`,
		want: 0,
	},
}

func TestMapBuilderX86_64(t *testing.T) {
	for _, c := range mapBuilderCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("%s: got %d, want %d", c.name, code, c.want)
			}
		})
	}
}

func TestMapBuilderWASM(t *testing.T) {
	for _, c := range mapBuilderCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("%s: got %d, want %d", c.name, got, c.want)
			}
		})
	}
}

func TestMapBuilderInterp(t *testing.T) {
	for _, c := range mapBuilderCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpByte(t, c.src); got != c.want {
				t.Errorf("%s: got %d, want %d", c.name, got, c.want)
			}
		})
	}
}
