package e2e

import "testing"

// byteTypeCases pin `s[i]` yielding the byte type `u8` rather than `i32`
// (#5629 acceptance item 2). The type-side contract is checker-tested; these
// pin the RUNTIME half across the backends, because the flip is only sound if
// a byte still round-trips through an 8-byte-free slot with no representation
// change — `char`/`u8` occupy the same slot as `i32`, so a miscompile here
// would show up as a wrong exit code rather than a type error.
//
// The interesting cases are the ones where a byte flows into i32 arithmetic.
// Fern has no implicit unsigned widening, so those need an explicit `as i32`
// — and the cast must happen BEFORE any shift. `(s[i] & 63) << 6` computed in
// u8 would overflow 8 bits and silently corrupt a multi-byte UTF-8 decode;
// `((s[i] as i32) & 63) << 6` is the correct form. The utf8-* cases below are
// the regression guard for exactly that.
var byteTypeCases = []struct {
	name string
	src  string
	exit int
}{
	// A byte read binds to a u8 local and widens explicitly.
	{"byte-local", `function main(): i32 { var s: string = "*"; var b: u8 = s[0]; return b as i32; }`, 42},
	// Widening inline at the read site.
	{"byte-widen-inline", `function main(): i32 { var s: string = "*"; return s[0] as i32; }`, 42},
	// Byte arithmetic stays in u8 and widens at the end.
	{"byte-arith-u8", `function main(): i32 { var s: string = "("; var b: u8 = s[0]; return (b + 2) as i32; }`, 42},
	// Widen first, then do i32 arithmetic.
	{"byte-arith-i32", `function main(): i32 { var s: string = "("; return (s[0] as i32) + 2; }`, 42},
	// Comparing a byte against an integer literal (literal adopts u8).
	{"byte-cmp-literal", `function main(): i32 { var s: string = "A"; if (s[0] == 65) { return 42; } return 1; }`, 42},
	// Two byte reads compared directly — both sides u8, no cast needed.
	{"byte-cmp-byte", `function main(): i32 { var s: string = "AA"; if (s[0] == s[1]) { return 42; } return 1; }`, 42},
	// A byte read indexes back into the string (byte used as a value, not an index).
	{"byte-high-bit", `function main(): i32 { var s: string = "\xFF"; return s[0] as i32; }`, 255},
	// Shift AFTER widening — the precedence trap. In u8 this would overflow.
	{"byte-shift-widened", `function main(): i32 { var s: string = "\x02"; return ((s[0] as i32) & 63) << 6; }`, 128},
	// Two-byte UTF-8 decode (U+00E9 é = C3 A9) — the real-world shape of the
	// shift trap above.
	{"utf8-2byte", `function main(): i32 { var s: string = "\xC3\xA9"; var cp: i32 = (((s[0] as i32) & 31) << 6) | ((s[1] as i32) & 63); if (cp == 233) { return 42; } return cp; }`, 42},
	// Three-byte decode (U+20AC € = E2 82 AC), two shifted continuation bytes.
	{"utf8-3byte", `function main(): i32 { var s: string = "\xE2\x82\xAC"; var cp: i32 = (((s[0] as i32) & 15) << 12) | (((s[1] as i32) & 63) << 6) | ((s[2] as i32) & 63); if (cp == 8364) { return 42; } return 1; }`, 42},
}

// TestX86_64ByteType — `s[i]` as u8 through the native x86-64 backend.
func TestX86_64ByteType(t *testing.T) {
	for _, c := range byteTypeCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}

// TestArm64ByteType — the arm64 sibling (qemu-gated like every arm64 e2e).
func TestArm64ByteType(t *testing.T) {
	for _, c := range byteTypeCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}
