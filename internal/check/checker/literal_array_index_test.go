package checker

import (
	"strings"
	"testing"
)

// An integer-literal array local takes its element type from a typed
// destination of one of its elements (#14340).
func TestLiteralArrayTakesTheElementTypeOfAnIndexRead(t *testing.T) {
	good := []struct{ name, src string }{
		{"typed let", "function main(): i32 { let z = [1, 2]; let m: u8 = z[0]; return m as i32; }\n"},
		{"argument", "function g(b: u8): i32 { return b as i32; }\nfunction main(): i32 { let z = [1, 2]; return g(z[0]); }\n"},
		{"two reads at one width", "function main(): i32 { let z = [1, 2]; let m: u8 = z[0]; let k: u8 = z[1]; return (m as i32) + (k as i32); }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	src := "function main(): i32 { let z = [1, 2]; let m: u8 = z[0]; let n: i64 = 7; let w = z.append(n); return w.len() + (m as i32); }\n"
	err := checkSource(t, src)
	if err == nil || !strings.Contains(err.Error(), `argument 1 to "append": expected u8, got i64`) {
		t.Errorf("an append at another width after the read: want E038, got %v", err)
	}
}
