package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// numericMethodsIRCases are self-contained programs exercising the i64 / u32
// numeric-method LOGIC (abs / min / max / clamp + unsigned compare) that
// std/i64 and std/u32 wrap, verified through the self-hosted compiler's x86-64
// IR path. The single-program self-host driver resolves no imports, so the
// method bodies are inlined — this verifies that the language constructs the
// stdlib methods compile to (64-bit and unsigned arithmetic / compare / branch
// across function calls) lower correctly on the IR path.
//
// The u32 case deliberately uses a value above 2^31 (signed-negative) so a
// signed comparison would give the wrong answer — confirming the IR path
// selects the unsigned compare. Each program's exit code is oracle-checked
// against the reference interpreter rather than hardcoded, so a wrong-but-
// stable result can't slip through (cf. the hardcoded-expectation gap in
// #2908). Goal-1 self-host IR coverage; FEATURE-AUDIT std/i64 · std/u32 rows.
//
// Scope note: the wasm IR backend still lowers u32/u64 comparisons as SIGNED
// (this test surfaced it) and a >2^63 u64 value built by addition is not yet
// IR-eligible on x86 — both tracked as a follow-up, so this test stays on the
// x86-64 IR path where the unsigned semantics are correct.
var numericMethodsIRCases = []struct {
	name string
	src  string
}{
	// i64 abs / min / max / clamp, composed across helper calls.
	// 7 + 5 + 9 = 21, then clamp(12,3,9)==9 adds 100 → 121.
	{"i64-abs-min-max-clamp", `function i64_abs(n: i64): i64 { if (n < (0 as i64)) { return (0 as i64) - n; } return n; }
function i64_min(a: i64, b: i64): i64 { if (a < b) { return a; } return b; }
function i64_max(a: i64, b: i64): i64 { if (a > b) { return a; } return b; }
function main(): i32 {
    var r: i64 = i64_abs(0 as i64 - 7 as i64) + i64_min(5 as i64, 9 as i64) + i64_max(5 as i64, 9 as i64);
    if (i64_max(i64_min(12 as i64, 9 as i64), 3 as i64) == 9 as i64) { r = r + 100 as i64; }
    return r as i32;
}`},
	// u32 min / max with a value above 2^31 (signed-negative): unsigned
	// max(4e9, 1) == 4e9 and min == 1. A signed compare would invert both.
	{"u32-unsigned-min-max", `function u32_min(a: u32, b: u32): u32 { if (a < b) { return a; } return b; }
function u32_max(a: u32, b: u32): u32 { if (a > b) { return a; } return b; }
function main(): i32 {
    var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32;
    if (u32_max(big, one) == big && u32_min(big, one) == one) { return 42; }
    return 0;
}`},
	// i64 bit-op family (the std/i64 additions): count_zeros / leading_zeros /
	// trailing_zeros / rotate_left / rotate_right, over u64 shifts & masks.
	// Exercises 64-bit shift / and / or / compare across function calls on the
	// IR path; oracle-checked, so the wide-shift logic must be correct, not just
	// stable. count_zeros(7)=61, then four predicate hits → 65.
	{"i64-bitops", `function count_ones(n: i64): i32 {
    var u: u64 = n as u64; var c: i32 = 0; var i: i32 = 0;
    while (i < 64) { if ((u & (1 as u64)) != (0 as u64)) { c = c + 1; } u = u >> (1 as u64); i = i + 1; }
    return c;
}
function count_zeros(n: i64): i32 { return 64 - count_ones(n); }
function leading_zeros(n: i64): i32 {
    if (n == (0 as i64)) { return 64; }
    var u: u64 = n as u64; var top: u64 = (1 as u64) << (63 as u64); var c: i32 = 0;
    while ((u & top) == (0 as u64)) { c = c + 1; u = u << (1 as u64); }
    return c;
}
function trailing_zeros(n: i64): i32 {
    if (n == (0 as i64)) { return 64; }
    var u: u64 = n as u64; var c: i32 = 0;
    while ((u & (1 as u64)) == (0 as u64)) { c = c + 1; u = u >> (1 as u64); }
    return c;
}
function rotl(n: i64, bits: i32): i64 {
    var k: i32 = bits & 63; if (k == 0) { return n; }
    var u: u64 = n as u64;
    var left: u64 = u << (k as u64); var right: u64 = u >> ((64 - k) as u64);
    return (left | right) as i64;
}
function rotr(n: i64, bits: i32): i64 {
    var k: i32 = bits & 63; if (k == 0) { return n; }
    var u: u64 = n as u64;
    var right: u64 = u >> (k as u64); var left: u64 = u << ((64 - k) as u64);
    return (left | right) as i64;
}
function main(): i32 {
    var r: i32 = count_zeros(7 as i64);                             // 61
    if (leading_zeros(1 as i64) == 63) { r = r + 1; }              // 62
    if (trailing_zeros(8 as i64) == 3) { r = r + 1; }             // 63
    if (rotl(1 as i64, 1) == (2 as i64)) { r = r + 1; }          // 64
    if (rotr((1 as i64) << (63 as i64), 63) == (1 as i64)) { r = r + 1; }   // 65
    return r;
}`},
	// u32 bit-op family (the std/u32 additions): count_ones / leading_zeros /
	// trailing_zeros / rotate over u32 shifts & masks. Only ==/!= compares
	// (never signed </>) so the x86-64 IR path is exact for unsigned; oracle-
	// checked. count_ones(255)=8, then four predicate hits → 12.
	{"u32-bitops", `function count_ones(n: u32): i32 {
    var u: u32 = n; var c: i32 = 0; var i: i32 = 0;
    while (i < 32) { if ((u & (1 as u32)) != (0 as u32)) { c = c + 1; } u = u >> (1 as u32); i = i + 1; }
    return c;
}
function leading_zeros(n: u32): i32 {
    if (n == (0 as u32)) { return 32; }
    var u: u32 = n; var top: u32 = (1 as u32) << (31 as u32); var c: i32 = 0;
    while ((u & top) == (0 as u32)) { c = c + 1; u = u << (1 as u32); }
    return c;
}
function trailing_zeros(n: u32): i32 {
    if (n == (0 as u32)) { return 32; }
    var u: u32 = n; var c: i32 = 0;
    while ((u & (1 as u32)) == (0 as u32)) { c = c + 1; u = u >> (1 as u32); }
    return c;
}
function rotl(n: u32, bits: i32): u32 {
    var k: i32 = bits & 31; if (k == 0) { return n; }
    var left: u32 = n << (k as u32); var right: u32 = n >> ((32 - k) as u32);
    return left | right;
}
function main(): i32 {
    var r: i32 = count_ones(255 as u32);                            // 8
    if (leading_zeros(1 as u32) == 31) { r = r + 1; }             // 9
    if (trailing_zeros(8 as u32) == 3) { r = r + 1; }            // 10
    if (rotl(1 as u32, 1) == (2 as u32)) { r = r + 1; }         // 11
    if (rotl((1 as u32) << (31 as u32), 1) == (1 as u32)) { r = r + 1; }   // 12
    return r;
}`},
	// byte_swap (the std/u32 · std/i64 · std/u64 additions): endianness
	// reversal over unsigned shift/mask lanes. u32 swaps 4 bytes, u64 loops
	// over 8; ==-only, oracle-checked. Four hits → 42.
	{"int-byte-swap", `function u32_bswap(n: u32): u32 {
    var b0: u32 = n & (255 as u32);
    var b1: u32 = (n >> (8 as u32)) & (255 as u32);
    var b2: u32 = (n >> (16 as u32)) & (255 as u32);
    var b3: u32 = (n >> (24 as u32)) & (255 as u32);
    return (b0 << (24 as u32)) | (b1 << (16 as u32)) | (b2 << (8 as u32)) | b3;
}
function u64_bswap(n: u64): u64 {
    var r: u64 = 0 as u64; var i: i32 = 0;
    while (i < 8) { var b: u64 = (n >> ((i * 8) as u64)) & (255 as u64); r = r | (b << (((7 - i) * 8) as u64)); i = i + 1; }
    return r;
}
function main(): i32 {
    var r: i32 = 0;
    if (u32_bswap(16909060 as u32) == (67305985 as u32)) { r = r + 10; }
    if (u32_bswap(4278190080 as u32) == (255 as u32)) { r = r + 10; }
    if (u64_bswap(72623859790382856 as u64) == (578437695752307201 as u64)) { r = r + 10; }
    if (u64_bswap(u64_bswap(123456789 as u64)) == (123456789 as u64)) { r = r + 12; }
    return r;
}`},
	// Single-bit accessors (the std/i64 · u32 · u64 additions): read / set /
	// clear a bit via shift/mask over u64 lanes, incl. the top-bit path.
	// ==/!=-only, oracle-checked. Four hits → 42.
	{"int-bit-accessors", `function i64_bit(n: i64, i: i32): boolean {
    if (i < 0 || i >= 64) { return false; }
    var mask: u64 = (1 as u64) << (i as u64);
    return ((n as u64) & mask) != (0 as u64);
}
function i64_set(n: i64, i: i32): i64 {
    if (i < 0 || i >= 64) { return n; }
    return ((n as u64) | ((1 as u64) << (i as u64))) as i64;
}
function i64_clear(n: i64, i: i32): i64 {
    if (i < 0 || i >= 64) { return n; }
    var inv: u64 = (((0 as i64) - (1 as i64)) as u64) ^ ((1 as u64) << (i as u64));
    return ((n as u64) & inv) as i64;
}
function main(): i32 {
    var r: i32 = 0;
    if (i64_bit(5 as i64, 0)) { r = r + 10; }
    if (!i64_bit(5 as i64, 1)) { r = r + 10; }
    if (i64_set(0 as i64, 3) == (8 as i64)) { r = r + 10; }
    if (i64_clear(15 as i64, 1) == (13 as i64)) { r = r + 12; }
    return r;
}`},
	// checked_div (the std/i64 · u32 · u64 additions): guarded division with
	// the divide-by-zero and i64::MIN / -1 overflow cases. Inlined with a -1
	// sentinel standing in for None; oracle-checked. Four hits → 42.
	{"int-checked-div", `function i64_cdiv(n: i64, other: i64): i64 {
    if (other == (0 as i64)) { return (0 as i64) - (1 as i64); }
    var min64: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    if (n == min64 && other == ((0 as i64) - (1 as i64))) { return (0 as i64) - (1 as i64); }
    return n / other;
}
function u32_cdiv(n: u32, other: u32): i32 {
    if (other == (0 as u32)) { return -1; }
    return (n / other) as i32;
}
function main(): i32 {
    var r: i32 = 0;
    if (i64_cdiv(10 as i64, 2 as i64) == (5 as i64)) { r = r + 10; }
    if (i64_cdiv(10 as i64, 0 as i64) == ((0 as i64) - (1 as i64))) { r = r + 10; }
    var min64: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    if (i64_cdiv(min64, (0 as i64) - (1 as i64)) == ((0 as i64) - (1 as i64))) { r = r + 10; }
    if (u32_cdiv(9 as u32, 3 as u32) == 3) { r = r + 12; }
    return r;
}`},
	// log10_floor (the std/i32 · i64 · u32 additions): floor(log10 n) by
	// counting divisions by ten, with the -1 sentinel for n <= 0 (n == 0
	// unsigned). Exercises i32/i64/u32 divide + unsigned compare + branch
	// across function calls on the IR path; the u32 case uses a value above
	// 2^31 so a signed compare/divide would truncate the loop early (the
	// same unsigned-correctness the u32 min/max case guards). Oracle-checked,
	// so the count must be right, not just stable. Five hits → 42.
	{"int-log10-floor", `function i32_log10(n: i32): i32 {
    if (n <= 0) { return 0 - 1; }
    var r: i32 = 0; var m: i32 = n;
    while (m >= 10) { m = m / 10; r = r + 1; }
    return r;
}
function i64_log10(n: i64): i32 {
    if (n <= (0 as i64)) { return 0 - 1; }
    var r: i32 = 0; var m: i64 = n;
    while (m >= (10 as i64)) { m = m / (10 as i64); r = r + 1; }
    return r;
}
function u32_log10(n: u32): i32 {
    if (n == (0 as u32)) { return 0 - 1; }
    var r: i32 = 0; var m: u32 = n;
    while (m >= (10 as u32)) { m = m / (10 as u32); r = r + 1; }
    return r;
}
function main(): i32 {
    var r: i32 = 0;
    if (i32_log10(999) == 2) { r = r + 10; }
    if (i32_log10(1000) == 3) { r = r + 10; }
    if (i32_log10(0) == (0 - 1)) { r = r + 10; }
    if (i64_log10(1000000000000 as i64) == 12) { r = r + 6; }        // 10^12, 13 digits
    if (u32_log10(4000000000 as u32) == 9) { r = r + 6; }            // > 2^31, 10 digits
    return r;
}`},
	// checked_mul (the std/i32 · i64 additions): overflow-checked multiply.
	// i32 widens to i64 and range-checks (a -1 sentinel stands in for None on
	// overflow); i64 has no wider type so it detects overflow with the inverse
	// division (p/n == other) and rejects the MIN * -1 wrap explicitly (a -2
	// sentinel). Exercises i64 multiply + divide + compare across function
	// calls on the IR path; oracle-checked, so the overflow verdict must be
	// right, not just stable. Four hits → 42.
	{"int-checked-mul", `function i32_cmul(n: i32, other: i32): i64 {
    var p: i64 = (n as i64) * (other as i64);
    var lo: i64 = (0 as i64) - (2147483647 as i64) - (1 as i64);
    var hi: i64 = 2147483647 as i64;
    if (p < lo || p > hi) { return (0 as i64) - 1; }
    return p;
}
function i64_cmul(n: i64, other: i64): i64 {
    if (n == (0 as i64) || other == (0 as i64)) { return 0 as i64; }
    var min64: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    var neg1: i64 = (0 as i64) - (1 as i64);
    if ((n == min64 && other == neg1) || (other == min64 && n == neg1)) { return (0 as i64) - 2; }
    var p: i64 = n * other;
    if (p / n != other) { return (0 as i64) - 2; }
    return p;
}
function main(): i32 {
    var r: i32 = 0;
    if (i32_cmul(6, 7) == (42 as i64)) { r = r + 10; }
    if (i32_cmul(100000, 100000) == ((0 as i64) - 1)) { r = r + 10; }        // 10^10 overflows i32
    if (i64_cmul(6 as i64, 7 as i64) == (42 as i64)) { r = r + 11; }
    if (i64_cmul(3037000500 as i64, 3037000500 as i64) == ((0 as i64) - 2)) { r = r + 11; }  // ~9.22e18 overflows i64
    return r;
}`},
	// saturating_mul (the std/i32 · i64 additions): multiply clamped to the
	// type range instead of wrapping. i32 reads the clamp off the i64-widened
	// product; i64 detects overflow via inverse division and picks MAX/MIN by
	// the product's sign. Exercises i64 multiply + divide + a boolean-equality
	// sign test across function calls on the IR path; oracle-checked, so both
	// the clamp direction and the in-range path must be right. Five hits → 42.
	{"int-saturating-mul", `function i32_smul(n: i32, other: i32): i32 {
    var p: i64 = (n as i64) * (other as i64);
    if (p > (2147483647 as i64)) { return 2147483647; }
    if (p < ((0 as i64) - (2147483647 as i64) - (1 as i64))) { return 0 - 2147483647 - 1; }
    return p as i32;
}
function i64_smul(n: i64, other: i64): i64 {
    if (n == (0 as i64) || other == (0 as i64)) { return 0 as i64; }
    var max64: i64 = 9223372036854775807 as i64;
    var min64: i64 = (0 as i64) - max64 - (1 as i64);
    var neg1: i64 = (0 as i64) - (1 as i64);
    if ((n == min64 && other == neg1) || (other == min64 && n == neg1)) { return max64; }
    var p: i64 = n * other;
    if (p / n != other) {
        if ((n > (0 as i64)) == (other > (0 as i64))) { return max64; }
        return min64;
    }
    return p;
}
function main(): i32 {
    var max32: i32 = 2147483647;
    var min32: i32 = 0 - 2147483647 - 1;
    var r: i32 = 0;
    if (i32_smul(6, 7) == 42) { r = r + 8; }
    if (i32_smul(100000, 100000) == max32) { r = r + 8; }               // +overflow -> MAX
    if (i32_smul(0 - 100000, 100000) == min32) { r = r + 8; }           // -overflow -> MIN
    if (i64_smul(3037000500 as i64, 3037000500 as i64) == (9223372036854775807 as i64)) { r = r + 9; }
    if (i64_smul((0 as i64) - 3037000500, 3037000500 as i64) == ((0 as i64) - 9223372036854775807 - 1)) { r = r + 9; }
    return r;
}`},
	// reverse_bits (the std/i32 · i64 additions): bit-order reversal over the
	// unsigned twin's logical shifts (bit i -> bit width-1-i). The bit-level
	// counterpart of byte_swap; exercises u32/u64 shift | & across function
	// calls on the IR path (same shape as int-byte-swap), plus the involution.
	// Oracle-checked. Five hits → 42.
	{"int-reverse-bits", `function i32_rev(n: i32): i32 {
    var u: u32 = n as u32;
    var r: u32 = 0 as u32;
    var i: i32 = 0;
    while (i < 32) { r = (r << (1 as u32)) | (u & (1 as u32)); u = u >> (1 as u32); i = i + 1; }
    return r as i32;
}
function i64_rev(n: i64): i64 {
    var u: u64 = n as u64;
    var r: u64 = 0 as u64;
    var i: i32 = 0;
    while (i < 64) { r = (r << (1 as u64)) | (u & (1 as u64)); u = u >> (1 as u64); i = i + 1; }
    return r as i64;
}
function main(): i32 {
    var i32min: i32 = 0 - 2147483647 - 1;
    var i64min: i64 = (0 as i64) - 9223372036854775807 - 1;
    var r: i32 = 0;
    if (i32_rev(1) == i32min) { r = r + 8; }                          // bit 0 -> bit 31
    if (i32_rev(15) == (0 - 268435456)) { r = r + 8; }               // 0x0000000F -> 0xF0000000
    if (i32_rev(i32_rev(12345)) == 12345) { r = r + 8; }            // involution
    if (i64_rev(1 as i64) == i64min) { r = r + 9; }                 // bit 0 -> bit 63
    if (i64_rev(i64_rev(987654321 as i64)) == (987654321 as i64)) { r = r + 9; }
    return r;
}`},
	// div_euclid / rem_euclid (the std/i32 · i64 additions): Euclidean division
	// with a remainder always in [0, |rhs|). Exercises i32/i64 divide + remainder
	// + signed compare + branch across function calls on the IR path (i64 / is
	// already covered by int-checked-div; this adds i64 %). Oracle-checked, so
	// the negative-dividend rounding must be right. Five hits → 42.
	{"int-euclid", `function i32_de(n: i32, rhs: i32): i32 {
    var q: i32 = n / rhs;
    if (n % rhs < 0) { if (rhs > 0) { return q - 1; } return q + 1; }
    return q;
}
function i32_re(n: i32, rhs: i32): i32 {
    var r: i32 = n % rhs;
    if (r < 0) { if (rhs < 0) { return r - rhs; } return r + rhs; }
    return r;
}
function i64_re(n: i64, rhs: i64): i64 {
    var r: i64 = n % rhs;
    if (r < (0 as i64)) { if (rhs < (0 as i64)) { return r - rhs; } return r + rhs; }
    return r;
}
function main(): i32 {
    var r: i32 = 0;
    if (i32_de(0 - 7, 3) == (0 - 3)) { r = r + 8; }                  // rounds away from zero
    if (i32_re(0 - 7, 3) == 2) { r = r + 8; }                        // non-negative remainder
    if (i32_re(0 - 1, 3) == 2) { r = r + 8; }                        // wrap-around
    if (i64_re((0 as i64) - 7, 3 as i64) == (2 as i64)) { r = r + 9; }
    if (i64_re((0 as i64) - 1000000000001, 7 as i64) == (5 as i64)) { r = r + 9; }  // past i32 range
    return r;
}`},
	// checked_pow (the std/i32 · i64 · u32 · u64 additions): overflow-checked
	// exponentiation by squaring. The inlined i32 form widens to i64 and
	// range-checks each product (a -2 sentinel stands in for None on overflow,
	// -1 for a negative exponent). Exercises the pow loop — i64 multiply +
	// compare + branch over an i32 exponent halving — on the IR path.
	// Oracle-checked, so the exact / overflow verdict must match the
	// interpreter. Five hits → 42.
	{"int-checked-pow", `function i32_cpow(n: i32, exp: i32): i64 {
    if (exp < 0) { return (0 as i64) - 1; }
    var result: i64 = 1 as i64;
    var base: i64 = n as i64;
    var e: i32 = exp;
    var lo: i64 = (0 as i64) - (2147483647 as i64) - (1 as i64);
    var hi: i64 = 2147483647 as i64;
    while (e > 0) {
        if (e % 2 == 1) {
            result = result * base;
            if (result < lo || result > hi) { return (0 as i64) - 2; }
        }
        e = e / 2;
        if (e > 0) {
            base = base * base;
            if (base < lo || base > hi) { return (0 as i64) - 2; }
        }
    }
    return result;
}
function main(): i32 {
    var r: i32 = 0;
    if (i32_cpow(2, 10) == (1024 as i64)) { r = r + 8; }
    if (i32_cpow(2, 30) == (1073741824 as i64)) { r = r + 8; }       // exactly fits
    if (i32_cpow(2, 31) == ((0 as i64) - 2)) { r = r + 8; }          // overflow sentinel
    if (i32_cpow(10, 9) == (1000000000 as i64)) { r = r + 9; }
    if (i32_cpow(0 - 3, 3) == ((0 as i64) - 27)) { r = r + 9; }      // negative base
    return r;
}`},
}

// TestSelfHostNumericMethodsIRX86_64 routes each case through the self-hosted
// x86-64 driver (IR on), asserts the exit code matches the interpreter oracle,
// AND probes the routing (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostNumericMethodsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range numericMethodsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
