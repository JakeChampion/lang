package e2e

import "testing"

var byteTypeCases = []struct {
	name string
	src  string
	exit int
}{
	{"byte-local", `function main(): i32 { let s: string = "*"; let b: u8 = s[0]; return b as i32; }`, 42},
	{"byte-widen-inline", `function main(): i32 { let s: string = "*"; return s[0] as i32; }`, 42},
	{"byte-arith-u8", `function main(): i32 { let s: string = "("; let b: u8 = s[0]; return (b + 2) as i32; }`, 42},
	{"byte-arith-i32", `function main(): i32 { let s: string = "("; return (s[0] as i32) + 2; }`, 42},
	{"byte-cmp-literal", `function main(): i32 { let s: string = "A"; if (s[0] == 65) { return 42; } return 1; }`, 42},
	{"byte-cmp-byte", `function main(): i32 { let s: string = "AA"; if (s[0] == s[1]) { return 42; } return 1; }`, 42},
	{"byte-high-bit", `function main(): i32 { let s: string = "\u{FF}"; return s[1] as i32; }`, 191},
	{"byte-shift-widened", `function main(): i32 { let s: string = "\x02"; return ((s[0] as i32) & 63) << 6; }`, 128},
	{"utf8-2byte", `function main(): i32 { let s: string = "\u{E9}"; let cp: i32 = (((s[0] as i32) & 31) << 6) | ((s[1] as i32) & 63); if (cp == 233) { return 42; } return cp; }`, 42},
	{"utf8-3byte", `function main(): i32 { let s: string = "\u{20AC}"; let cp: i32 = (((s[0] as i32) & 15) << 12) | (((s[1] as i32) & 63) << 6) | ((s[2] as i32) & 63); if (cp == 8364) { return 42; } return 1; }`, 42},
}

func TestX86_64ByteType(t *testing.T) {
	for _, c := range byteTypeCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}

func TestArm64ByteType(t *testing.T) {
	for _, c := range byteTypeCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}
