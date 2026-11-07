package checker

import (
	"strings"
	"testing"
)

// A float-literal array local takes its element type from a typed argument
// to append or with, and from a typed read of one element, as an
// integer-literal array does (#14328, #14340).
func TestFloatLiteralArrayTakesItsElementTypeFromItsFirstUse(t *testing.T) {
	good := []struct{ name, src string }{
		{"append", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; let w = z.append(f); let m: f32 = w[0]; return m as i32; }\n"},
		{"with", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; z = z.with(0, f); let m: f32 = z[0]; return m as i32; }\n"},
		{"index read", "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; return m as i32; }\n"},
		{"an untyped float keeps f64", "function main(): i32 { let z = [1.5, 2.0]; let w = z.append(3.0); let m: f64 = w[0]; return m as i32; }\n"},
		{"no use keeps f64", "function main(): i32 { let z = [1.5, 2.0]; let m: f64 = z[0] + z[1]; return m as i32; }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, src string }{
		{"a second append at another width", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; let w = z.append(f); let g: f64 = 4.0; let v = z.append(g); return w.len() + v.len(); }\n"},
		{"an append at another width after the read", "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; let n: f64 = 7.0; let w = z.append(n); return w.len(); }\n"},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), `argument 1 to "append": expected f32, got f64`) {
			t.Errorf("%s: want E038, got %v", c.name, err)
		}
	}
}
