package checker

import (
	"strings"
	"testing"
)

// An integer-literal array local takes its element type from a typed
// argument to append or with, as a literal takes the type of the other
// operand of a binary operator (#14322).
func TestLiteralArrayTakesTheElementTypeOfAppend(t *testing.T) {
	good := []struct{ name, src string }{
		{"append", "function main(): i32 { let z = [120, 156]; let b: u8 = 3; z = z.append(b); let n: u8 = z[0]; return n as i32; }\n"},
		{"with", "function main(): i32 { let z = [1, 2]; let b: u8 = 3; z = z.with(0, b); let n: u8 = z[0]; return n as i32; }\n"},
		{"append into a new local", "function main(): i32 { let z = [120, 156]; let b: u8 = 3; let w = z.append(b); let n: u8 = w[0]; return n as i32; }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	src := "function main(): i32 { let z = [1, 2]; let b: u8 = 3; let w = z.append(b); let q: i64 = 7; let v = z.append(q); return w.len() + v.len(); }\n"
	err := checkSource(t, src)
	if err == nil || !strings.Contains(err.Error(), `argument 1 to "append": expected u8, got i64`) {
		t.Errorf("a second append at another width: want E038, got %v", err)
	}
}
