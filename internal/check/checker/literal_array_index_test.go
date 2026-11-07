package checker

import (
	"strings"
	"testing"
)

// A literal array local takes its element type from a typed destination of
// one of its elements (#14340).
func TestLiteralArrayTakesTheElementTypeOfAnIndexRead(t *testing.T) {
	good := []struct{ name, src string }{
		{"typed let", "function main(): i32 { let z = [1, 2]; let m: u8 = z[0]; return m as i32; }\n"},
		{"argument", "function g(b: u8): i32 { return b as i32; }\nfunction main(): i32 { let z = [1, 2]; return g(z[0]); }\n"},
		{"two reads at one width", "function main(): i32 { let z = [1, 2]; let m: u8 = z[0]; let k: u8 = z[1]; return (m as i32) + (k as i32); }\n"},
		{"float typed let", "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; return m as i32; }\n"},
		{"float argument", "function g(x: f32): i32 { return x as i32; }\nfunction main(): i32 { let z = [1.5, 2.0]; return g(z[0]); }\n"},
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
	src = "function main(): i32 { let z = [1.5, 2.0]; let m: f32 = z[0]; let n: f64 = 7.0; let w = z.append(n); return w.len() + (m as i32); }\n"
	err = checkSource(t, src)
	if err == nil || !strings.Contains(err.Error(), `argument 1 to "append": expected f32, got f64`) {
		t.Errorf("a float append at another width after the read: want E038, got %v", err)
	}
}

// A literal tuple local takes the type of a typed destination of one of its
// elements, element by element.
func TestLiteralTupleTakesTheTypeOfAnElementRead(t *testing.T) {
	good := []struct{ name, src string }{
		{"typed let", "function main(): i32 { let t = (1, 2); let y: u8 = t.0; return y as i32; }\n"},
		{"argument", "function g(b: u8): i32 { return b as i32; }\nfunction main(): i32 { let t = (1, 2); return g(t.0); }\n"},
		{"float element", "function main(): i32 { let t = (1.5, 2.0); let y: f32 = t.0; return y as i32; }\n"},
		{"each element on its own", "function main(): i32 { let t = (1, 2.5); let y: u8 = t.0; let f: f32 = t.1; return (y as i32) + (f as i32); }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	err := checkSource(t, "function main(): i32 { let t = (300, 2); let y: u8 = t.0; return y as i32; }\n")
	if err == nil || !strings.Contains(err.Error(), "literal 300 does not fit in u8") {
		t.Errorf("a literal out of range of the read's type: want E047, got %v", err)
	}
}

func TestLiteralTupleRejectsAnOverflowingIndex(t *testing.T) {
	for _, index := range []string{"2147483647", "2147483648", "4294967295", "4294967296", "18446744073709551616"} {
		t.Run(index, func(t *testing.T) {
			src := "function main(): i32 { let t = (1, 2); let x: u8 = t." + index + "; return x as i32; }\n"
			err := checkSource(t, src)
			if errCodes(err) != "E046" {
				t.Fatalf("tuple index %s: want E046, got %v", index, err)
			}
		})
	}
}
