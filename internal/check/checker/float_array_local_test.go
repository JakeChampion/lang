package checker

import (
	"strings"
	"testing"
)

// A float-literal array local takes its width from its first typed use: an
// argument, a typed read of an element, or an append (#14328, #14340).
func TestFloatArrayLocalTakesTheWidthOfItsFirstUse(t *testing.T) {
	good := []struct{ name, src string }{
		{"append", "function main(): i32 { let z = [1.5, 2.0]; let f: f32 = 3.0; let w = z.append(f); let m: f32 = w[0]; return m as i32; }\n"},
		{"index read", "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; return m as i32; }\n"},
		{"argument", "function g(a: f32[]): i32 { return a.len(); }\nfunction main(): i32 { let z = [1.5, 2.0]; return g(z); }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	src := "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; let n: f64 = 7.0; let w = z.append(n); return w.len() + (m as i32); }\n"
	err := checkSource(t, src)
	if err == nil || !strings.Contains(err.Error(), `argument 1 to "append": expected f32, got f64`) {
		t.Errorf("an append at another width after the read: want E038, got %v", err)
	}
}
