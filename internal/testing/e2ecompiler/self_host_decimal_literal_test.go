package e2ecompiler

import "testing"

// A leading zero in a decimal literal is decimal in Fern, and octal to GNU as
// and the LLVM assembler (#10530). The unsigned and wide literals reach the
// assembler as text, through a static box's `.quad` (u_field), an immediate
// (u_local, wide) and the arm64 literal pool, so each is read back here; hex
// and binary keep their prefix and must still mean what they say.
const decimalLiteralProgram = `struct U { x: u32 }
@noinline function u_field(): U { return U { x: 010u32 }; }
@noinline function u_local(k: u32): u32 { let v: u32 = 0010u32; return v + k; }
@noinline function wide(k: i64): i64 { let v: i64 = 0100i64; let w: u64 = 017u64; return v + (w as i64) + k; }
@noinline function radix(k: u32): u32 { return 0x1Fu32 + 0b101u32 + k; }
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(u_field().x as i32); print("");
    print_int(u_local(0u32) as i32); print("");
    print_int(wide(0i64) as i32); print("");
    print_int(radix(0u32) as i32); print("");
    return 0;
}
`

func TestSelfHostLeadingZeroLiteralIsDecimal(t *testing.T) {
	want := "10\n10\n117\n36\n"
	runSemanticProgram(t, "decimallit", decimalLiteralProgram, []string{"u_field", "u_local", "wide", "radix"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "wasm32-wasi": want})
}
