package printer

import "testing"

// A value match or value block keeps the lines it was written on, and the
// comments between them stay in place.
func TestFormatKeepsValueLayout(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"match and if with comments", `enum Shape { Circle(f64), Square(f64), Rect(f64, f64), Empty }

function area(s: Shape, k: i32): f64 {
  let a = match (s) {
    // round
    Circle(r) => 3.14159 * r * r,
    Square(w) => w * w,
    Rect(w, h) => w * h,  // two sides
    Empty => 0.0,
  };
  let b = if (k > 0) {
    let t = a * 2.0;
    t + 1.0
  } else {
    a
  };
  let c = match (k) { 0 => 1.0, _ => 2.0 };
  return a + b + c;
}
`},
		{"or-pattern group and guard", `enum Op { Add, Sub, Mul, Div }

function rank(o: Op, n: i32): i32 {
  return match (o) {
    Add | Sub => 1,
    Mul when n > 0 => 2,
    _ => 3,
  };
}
`},
		{"arms that share a line", `function pick(k: i32): i32 {
  return match (k) {
    0 => 10, 1 => 11,
    _ => 12,
  };
}
`},
		{"block-bodied arm", `function grow(k: i32): i32 {
  let v = match (k) {
    0 => {
      let z = k + 1;
      // doubled
      z * 2
    },
    _ => k,
  };
  return v;
}
`},
		{"nested value match", `enum Shape { Circle(f64), Square(f64) }

function size(s: Shape, k: i32): f64 {
  return match (s) {
    Circle(r) => match (k) {
      0 => r,
      _ => r * 2.0,
    },
    Square(w) => w,
  };
}
`},
		{"else-if chain", `function sign(k: i32): i32 {
  let s = if (k > 0) {
    1
  } else if (k < 0) {
    -1
  } else {
    0
  };
  return s;
}
`},
		{"if let", `function unwrap(o: Option[i32]): i32 {
  let v = if let Some(x) = o {
    x + 1
  } else {
    0
  };
  return v;
}
`},
		{"one line", `function pick(k: i32, o: Option[i32]): i32 {
  let a = match (k) { 0 => 1, _ => 2 };
  let b = if (k > 0) { let t = k * 2; t + 1 } else { k };
  let c = if let Some(x) = o { x } else { 0 };
  return a + b + c;
}
`},
		{"scrutinee on several lines", `function pick(k: i32): i32 {
  let d = match (pick(
    k,
  )) { 0 => 1, _ => 2 };
  return d;
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.source)
			if got != tc.source {
				t.Fatalf("layout changed:\ngot:\n%s\nwant:\n%s", got, tc.source)
			}
			if again := formatSrc(t, got); again != got {
				t.Fatalf("format is not stable:\n%s", again)
			}
		})
	}
}
