package checker

import "testing"

// A float-literal array local takes its width from an argument at a typed
// array parameter, and a nested one keeps the width of its first use.
func TestFloatArrayLocalTakesTheWidthOfAnArrayArgument(t *testing.T) {
	src := "function g(a: f32[]): i32 { return a.len(); }\nfunction main(): i32 { let z = [1.5, 2.0]; let n = g(z); let f: f64 = 3.0; let w = z.append(f); return n + w.len(); }\n"
	if got := errCodes(checkSource(t, src)); got != "E038" {
		t.Fatalf("an append at another width after the argument: want E038, got %q", got)
	}
}

func TestNestedFloatArrayLocalKeepsTheWidthOfItsFirstUse(t *testing.T) {
	for _, test := range []struct {
		name, width, codes string
	}{
		{"matching width", "f32", ""},
		{"different width", "f64", "E038"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "function consume(xs: f32[][]): i32 { return xs.len(); }\n" +
				"function main(): i32 { let z = [[1.5, 2.0]]; let n = consume(z); " +
				"let f: " + test.width + " = 3.0; let w = z[0].append(f); return n + w.len(); }\n"
			err := checkSource(t, src)
			if got := errCodes(err); got != test.codes {
				t.Fatalf("want codes %q, got %q: %v", test.codes, got, err)
			}
		})
	}
}
