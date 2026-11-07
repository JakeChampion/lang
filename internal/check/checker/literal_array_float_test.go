package checker

import (
	"strings"
	"testing"
)

// A float-literal array local takes its element type from its first typed
// use, as an integer-literal array does (#14328, #14340).
func TestFloatLiteralArrayTakesTheElementTypeOfItsFirstUse(t *testing.T) {
	good := []struct{ name, src string }{
		{"append", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; let w = z.append(f); let m: f32 = w[0]; return m as i32; }\n"},
		{"append into the local", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; z = z.append(f); let m: f32 = z[0]; return m as i32; }\n"},
		{"with", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; z = z.with(0, f); let m: f32 = z[0]; return m as i32; }\n"},
		{"typed let of an element", "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; return m as i32; }\n"},
		{"argument of an element", "function g(x: f32): i32 { return x as i32; }\nfunction main(): i32 { let z = [1.5, 2.0]; return g(z[0]); }\n"},
		{"no typed use stays f64", "function main(): i32 { let z = [1.5, 2.0]; let m: f64 = z[0]; let w = z.append(m); return w.len(); }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, src, want string }{
		{"append at a second width", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; let w = z.append(f); let d: f64 = 4.0; let v = z.append(d); return w.len() + v.len(); }\n", `argument 1 to "append": expected f32, got f64`},
		{"append after a read at f32", "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; let d: f64 = 7.0; let w = z.append(d); return w.len() + (m as i32); }\n", `argument 1 to "append": expected f32, got f64`},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}
