package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

var numericMethodsIRCases = []struct {
	name string
	src  string
}{
	{"i64-abs-min-max-clamp", `function i64_abs(n: i64): i64 { if (n < (0 as i64)) { return (0 as i64) - n; } return n; }
function i64_min(a: i64, b: i64): i64 { if (a < b) { return a; } return b; }
function i64_max(a: i64, b: i64): i64 { if (a > b) { return a; } return b; }
function main(): i32 {
    let r: i64 = i64_abs(0 as i64 - 7 as i64) + i64_min(5 as i64, 9 as i64) + i64_max(5 as i64, 9 as i64);
    if (i64_max(i64_min(12 as i64, 9 as i64), 3 as i64) == 9 as i64) { r = r + 100 as i64; }
    return r as i32;
}`},
	{"u32-unsigned-min-max", `function u32_min(a: u32, b: u32): u32 { if (a < b) { return a; } return b; }
function u32_max(a: u32, b: u32): u32 { if (a > b) { return a; } return b; }
function main(): i32 {
    let big: u32 = 4000000000 as u32; let one: u32 = 1 as u32;
    if (u32_max(big, one) == big && u32_min(big, one) == one) { return 42; }
    return 0;
}`},
	{"i64-bitops", `function count_ones(n: i64): i32 {
    let u: u64 = n as u64; let c: i32 = 0; let i: i32 = 0;
    while (i < 64) { if ((u & (1 as u64)) != (0 as u64)) { c = c + 1; } u = u >> (1 as u64); i = i + 1; }
    return c;
}
function count_zeros(n: i64): i32 { return 64 - count_ones(n); }
function leading_zeros(n: i64): i32 {
    if (n == (0 as i64)) { return 64; }
    let u: u64 = n as u64; let top: u64 = (1 as u64) << (63 as u64); let c: i32 = 0;
    while ((u & top) == (0 as u64)) { c = c + 1; u = u << (1 as u64); }
    return c;
}
function trailing_zeros(n: i64): i32 {
    if (n == (0 as i64)) { return 64; }
    let u: u64 = n as u64; let c: i32 = 0;
    while ((u & (1 as u64)) == (0 as u64)) { c = c + 1; u = u >> (1 as u64); }
    return c;
}
function rotl(n: i64, bits: i32): i64 {
    let k: i32 = bits & 63; if (k == 0) { return n; }
    let u: u64 = n as u64;
    let left: u64 = u << (k as u64); let right: u64 = u >> ((64 - k) as u64);
    return (left | right) as i64;
}
function rotr(n: i64, bits: i32): i64 {
    let k: i32 = bits & 63; if (k == 0) { return n; }
    let u: u64 = n as u64;
    let right: u64 = u >> (k as u64); let left: u64 = u << ((64 - k) as u64);
    return (left | right) as i64;
}
function main(): i32 {
    let r: i32 = count_zeros(7 as i64);                             // 61
    if (leading_zeros(1 as i64) == 63) { r = r + 1; }              // 62
    if (trailing_zeros(8 as i64) == 3) { r = r + 1; }             // 63
    if (rotl(1 as i64, 1) == (2 as i64)) { r = r + 1; }          // 64
    if (rotr((1 as i64) << (63 as i64), 63) == (1 as i64)) { r = r + 1; }   // 65
    return r;
}`},
	{"u32-bitops", `function count_ones(n: u32): i32 {
    let u: u32 = n; let c: i32 = 0; let i: i32 = 0;
    while (i < 32) { if ((u & (1 as u32)) != (0 as u32)) { c = c + 1; } u = u >> (1 as u32); i = i + 1; }
    return c;
}
function leading_zeros(n: u32): i32 {
    if (n == (0 as u32)) { return 32; }
    let u: u32 = n; let top: u32 = (1 as u32) << (31 as u32); let c: i32 = 0;
    while ((u & top) == (0 as u32)) { c = c + 1; u = u << (1 as u32); }
    return c;
}
function trailing_zeros(n: u32): i32 {
    if (n == (0 as u32)) { return 32; }
    let u: u32 = n; let c: i32 = 0;
    while ((u & (1 as u32)) == (0 as u32)) { c = c + 1; u = u >> (1 as u32); }
    return c;
}
function rotl(n: u32, bits: i32): u32 {
    let k: i32 = bits & 31; if (k == 0) { return n; }
    let left: u32 = n << (k as u32); let right: u32 = n >> ((32 - k) as u32);
    return left | right;
}
function main(): i32 {
    let r: i32 = count_ones(255 as u32);                            // 8
    if (leading_zeros(1 as u32) == 31) { r = r + 1; }             // 9
    if (trailing_zeros(8 as u32) == 3) { r = r + 1; }            // 10
    if (rotl(1 as u32, 1) == (2 as u32)) { r = r + 1; }         // 11
    if (rotl((1 as u32) << (31 as u32), 1) == (1 as u32)) { r = r + 1; }   // 12
    return r;
}`},
	{"int-byte-swap", `function u32_bswap(n: u32): u32 {
    let b0: u32 = n & (255 as u32);
    let b1: u32 = (n >> (8 as u32)) & (255 as u32);
    let b2: u32 = (n >> (16 as u32)) & (255 as u32);
    let b3: u32 = (n >> (24 as u32)) & (255 as u32);
    return (b0 << (24 as u32)) | (b1 << (16 as u32)) | (b2 << (8 as u32)) | b3;
}
function u64_bswap(n: u64): u64 {
    let r: u64 = 0 as u64; let i: i32 = 0;
    while (i < 8) { let b: u64 = (n >> ((i * 8) as u64)) & (255 as u64); r = r | (b << (((7 - i) * 8) as u64)); i = i + 1; }
    return r;
}
function main(): i32 {
    let r: i32 = 0;
    if (u32_bswap(16909060 as u32) == (67305985 as u32)) { r = r + 10; }
    if (u32_bswap(4278190080 as u32) == (255 as u32)) { r = r + 10; }
    if (u64_bswap(72623859790382856 as u64) == (578437695752307201 as u64)) { r = r + 10; }
    if (u64_bswap(u64_bswap(123456789 as u64)) == (123456789 as u64)) { r = r + 12; }
    return r;
}`},
	{"int-bit-accessors", `function i64_bit(n: i64, i: i32): boolean {
    if (i < 0 || i >= 64) { return false; }
    let mask: u64 = (1 as u64) << (i as u64);
    return ((n as u64) & mask) != (0 as u64);
}
function i64_set(n: i64, i: i32): i64 {
    if (i < 0 || i >= 64) { return n; }
    return ((n as u64) | ((1 as u64) << (i as u64))) as i64;
}
function i64_clear(n: i64, i: i32): i64 {
    if (i < 0 || i >= 64) { return n; }
    let inv: u64 = (((0 as i64) - (1 as i64)) as u64) ^ ((1 as u64) << (i as u64));
    return ((n as u64) & inv) as i64;
}
function main(): i32 {
    let r: i32 = 0;
    if (i64_bit(5 as i64, 0)) { r = r + 10; }
    if (!i64_bit(5 as i64, 1)) { r = r + 10; }
    if (i64_set(0 as i64, 3) == (8 as i64)) { r = r + 10; }
    if (i64_clear(15 as i64, 1) == (13 as i64)) { r = r + 12; }
    return r;
}`},
	{"int-checked-div", `function i64_cdiv(n: i64, other: i64): i64 {
    if (other == (0 as i64)) { return (0 as i64) - (1 as i64); }
    let min64: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    if (n == min64 && other == ((0 as i64) - (1 as i64))) { return (0 as i64) - (1 as i64); }
    return n / other;
}
function u32_cdiv(n: u32, other: u32): i32 {
    if (other == (0 as u32)) { return -1; }
    return (n / other) as i32;
}
function main(): i32 {
    let r: i32 = 0;
    if (i64_cdiv(10 as i64, 2 as i64) == (5 as i64)) { r = r + 10; }
    if (i64_cdiv(10 as i64, 0 as i64) == ((0 as i64) - (1 as i64))) { r = r + 10; }
    let min64: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    if (i64_cdiv(min64, (0 as i64) - (1 as i64)) == ((0 as i64) - (1 as i64))) { r = r + 10; }
    if (u32_cdiv(9 as u32, 3 as u32) == 3) { r = r + 12; }
    return r;
}`},
	{"int-log10-floor", `function i32_log10(n: i32): i32 {
    if (n <= 0) { return 0 - 1; }
    let r: i32 = 0; let m: i32 = n;
    while (m >= 10) { m = m / 10; r = r + 1; }
    return r;
}
function i64_log10(n: i64): i32 {
    if (n <= (0 as i64)) { return 0 - 1; }
    let r: i32 = 0; let m: i64 = n;
    while (m >= (10 as i64)) { m = m / (10 as i64); r = r + 1; }
    return r;
}
function u32_log10(n: u32): i32 {
    if (n == (0 as u32)) { return 0 - 1; }
    let r: i32 = 0; let m: u32 = n;
    while (m >= (10 as u32)) { m = m / (10 as u32); r = r + 1; }
    return r;
}
function main(): i32 {
    let r: i32 = 0;
    if (i32_log10(999) == 2) { r = r + 10; }
    if (i32_log10(1000) == 3) { r = r + 10; }
    if (i32_log10(0) == (0 - 1)) { r = r + 10; }
    if (i64_log10(1000000000000 as i64) == 12) { r = r + 6; }        // 10^12, 13 digits
    if (u32_log10(4000000000 as u32) == 9) { r = r + 6; }            // > 2^31, 10 digits
    return r;
}`},
	{"int-checked-mul", `function i32_cmul(n: i32, other: i32): i64 {
    let p: i64 = (n as i64) * (other as i64);
    let lo: i64 = (0 as i64) - (2147483647 as i64) - (1 as i64);
    let hi: i64 = 2147483647 as i64;
    if (p < lo || p > hi) { return (0 as i64) - 1; }
    return p;
}
function i64_cmul(n: i64, other: i64): i64 {
    if (n == (0 as i64) || other == (0 as i64)) { return 0 as i64; }
    let min64: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    let neg1: i64 = (0 as i64) - (1 as i64);
    if ((n == min64 && other == neg1) || (other == min64 && n == neg1)) { return (0 as i64) - 2; }
    let p: i64 = n * other;
    if (p / n != other) { return (0 as i64) - 2; }
    return p;
}
function main(): i32 {
    let r: i32 = 0;
    if (i32_cmul(6, 7) == (42 as i64)) { r = r + 10; }
    if (i32_cmul(100000, 100000) == ((0 as i64) - 1)) { r = r + 10; }        // 10^10 overflows i32
    if (i64_cmul(6 as i64, 7 as i64) == (42 as i64)) { r = r + 11; }
    if (i64_cmul(3037000500 as i64, 3037000500 as i64) == ((0 as i64) - 2)) { r = r + 11; }  // ~9.22e18 overflows i64
    return r;
}`},
	{"int-saturating-mul", `function i32_smul(n: i32, other: i32): i32 {
    let p: i64 = (n as i64) * (other as i64);
    if (p > (2147483647 as i64)) { return 2147483647; }
    if (p < ((0 as i64) - (2147483647 as i64) - (1 as i64))) { return 0 - 2147483647 - 1; }
    return p as i32;
}
function i64_smul(n: i64, other: i64): i64 {
    if (n == (0 as i64) || other == (0 as i64)) { return 0 as i64; }
    let max64: i64 = 9223372036854775807 as i64;
    let min64: i64 = (0 as i64) - max64 - (1 as i64);
    let neg1: i64 = (0 as i64) - (1 as i64);
    if ((n == min64 && other == neg1) || (other == min64 && n == neg1)) { return max64; }
    let p: i64 = n * other;
    if (p / n != other) {
        if ((n > (0 as i64)) == (other > (0 as i64))) { return max64; }
        return min64;
    }
    return p;
}
function main(): i32 {
    let max32: i32 = 2147483647;
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    if (i32_smul(6, 7) == 42) { r = r + 8; }
    if (i32_smul(100000, 100000) == max32) { r = r + 8; }               // +overflow -> MAX
    if (i32_smul(0 - 100000, 100000) == min32) { r = r + 8; }           // -overflow -> MIN
    if (i64_smul(3037000500 as i64, 3037000500 as i64) == (9223372036854775807 as i64)) { r = r + 9; }
    if (i64_smul((0 as i64) - 3037000500, 3037000500 as i64) == ((0 as i64) - 9223372036854775807 - 1)) { r = r + 9; }
    return r;
}`},
	{"int-reverse-bits", `function i32_rev(n: i32): i32 {
    let u: u32 = n as u32;
    let r: u32 = 0 as u32;
    let i: i32 = 0;
    while (i < 32) { r = (r << (1 as u32)) | (u & (1 as u32)); u = u >> (1 as u32); i = i + 1; }
    return r as i32;
}
function i64_rev(n: i64): i64 {
    let u: u64 = n as u64;
    let r: u64 = 0 as u64;
    let i: i32 = 0;
    while (i < 64) { r = (r << (1 as u64)) | (u & (1 as u64)); u = u >> (1 as u64); i = i + 1; }
    return r as i64;
}
function main(): i32 {
    let i32min: i32 = 0 - 2147483647 - 1;
    let i64min: i64 = (0 as i64) - 9223372036854775807 - 1;
    let r: i32 = 0;
    if (i32_rev(1) == i32min) { r = r + 8; }                          // bit 0 -> bit 31
    if (i32_rev(15) == (0 - 268435456)) { r = r + 8; }               // 0x0000000F -> 0xF0000000
    if (i32_rev(i32_rev(12345)) == 12345) { r = r + 8; }            // involution
    if (i64_rev(1 as i64) == i64min) { r = r + 9; }                 // bit 0 -> bit 63
    if (i64_rev(i64_rev(987654321 as i64)) == (987654321 as i64)) { r = r + 9; }
    return r;
}`},
	{"int-euclid", `function i32_de(n: i32, rhs: i32): i32 {
    let q: i32 = n / rhs;
    if (n % rhs < 0) { if (rhs > 0) { return q - 1; } return q + 1; }
    return q;
}
function i32_re(n: i32, rhs: i32): i32 {
    let r: i32 = n % rhs;
    if (r < 0) { if (rhs < 0) { return r - rhs; } return r + rhs; }
    return r;
}
function i64_re(n: i64, rhs: i64): i64 {
    let r: i64 = n % rhs;
    if (r < (0 as i64)) { if (rhs < (0 as i64)) { return r - rhs; } return r + rhs; }
    return r;
}
function main(): i32 {
    let r: i32 = 0;
    if (i32_de(0 - 7, 3) == (0 - 3)) { r = r + 8; }                  // rounds away from zero
    if (i32_re(0 - 7, 3) == 2) { r = r + 8; }                        // non-negative remainder
    if (i32_re(0 - 1, 3) == 2) { r = r + 8; }                        // wrap-around
    if (i64_re((0 as i64) - 7, 3 as i64) == (2 as i64)) { r = r + 9; }
    if (i64_re((0 as i64) - 1000000000001, 7 as i64) == (5 as i64)) { r = r + 9; }  // past i32 range
    return r;
}`},
	{"int-checked-pow", `function i32_cpow(n: i32, exp: i32): i64 {
    if (exp < 0) { return (0 as i64) - 1; }
    let result: i64 = 1 as i64;
    let base: i64 = n as i64;
    let e: i32 = exp;
    let lo: i64 = (0 as i64) - (2147483647 as i64) - (1 as i64);
    let hi: i64 = 2147483647 as i64;
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
    let r: i32 = 0;
    if (i32_cpow(2, 10) == (1024 as i64)) { r = r + 8; }
    if (i32_cpow(2, 30) == (1073741824 as i64)) { r = r + 8; }       // exactly fits
    if (i32_cpow(2, 31) == ((0 as i64) - 2)) { r = r + 8; }          // overflow sentinel
    if (i32_cpow(10, 9) == (1000000000 as i64)) { r = r + 9; }
    if (i32_cpow(0 - 3, 3) == ((0 as i64) - 27)) { r = r + 9; }      // negative base
    return r;
}`},
	{"int-overflowing", `function i32_oadd(n: i32, other: i32): (i32, boolean) {
    let r: i32 = n + other;
    return (r, (n > 0 && other > 0 && r < 0) || (n < 0 && other < 0 && r >= 0));
}
function i32_omul(n: i32, other: i32): (i32, boolean) {
    let p: i64 = (n as i64) * (other as i64);
    let lo: i64 = (0 as i64) - (2147483647 as i64) - (1 as i64);
    let hi: i64 = 2147483647 as i64;
    return (p as i32, p < lo || p > hi);
}
function main(): i32 {
    let max32: i32 = 2147483647;
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    let (a1, o1) = i32_oadd(2, 3);
    if (a1 == 5 && !o1) { r = r + 10; }
    let (a2, o2) = i32_oadd(max32, 1);
    if (a2 == min32 && o2) { r = r + 11; }                          // MAX+1 wraps to MIN
    let (a3, o3) = i32_omul(46340, 46340);
    if (a3 == 2147395600 && !o3) { r = r + 10; }
    let (a4, o4) = i32_omul(100000, 100000);
    if (o4) { r = r + 11; }                                        // 10^10 overflows
    return r;
}`},
	{"int-checked-neg", `function i32_oneg(n: i32): (i32, boolean) {
    if (n == (0 - 2147483647 - 1)) { return (n, true); }
    return (0 - n, false);
}
function i32_cabs(n: i32): i64 {
    if (n == (0 - 2147483647 - 1)) { return (0 as i64) - 2147483648; }   // MIN sentinel
    if (n < 0) { return (0 - n) as i64; }
    return n as i64;
}
function main(): i32 {
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    let (a1, o1) = i32_oneg(5);
    if (a1 == (0 - 5) && !o1) { r = r + 10; }
    let (a2, o2) = i32_oneg(min32);
    if (a2 == min32 && o2) { r = r + 11; }                          // MIN negates to itself
    if (i32_cabs(0 - 7) == (7 as i64)) { r = r + 10; }
    if (i32_cabs(min32) == ((0 as i64) - 2147483648)) { r = r + 11; }  // MIN sentinel
    return r;
}`},
	{"int-checked-shift", `function i32_cshl(n: i32, rhs: i32): i64 {
    if (rhs < 0 || rhs >= 32) { return (0 as i64) - 9999999999; }
    return (n << rhs) as i64;
}
function i32_cshr(n: i32, rhs: i32): i64 {
    if (rhs < 0 || rhs >= 32) { return (0 as i64) - 9999999999; }
    return (n >> rhs) as i64;
}
function main(): i32 {
    let r: i32 = 0;
    if (i32_cshl(1, 4) == (16 as i64)) { r = r + 8; }
    if (i32_cshl(1, 32) == ((0 as i64) - 9999999999)) { r = r + 8; }    // out-of-range sentinel
    if (i32_cshr(0 - 8, 2) == ((0 as i64) - 2)) { r = r + 8; }          // arithmetic shr
    if (i32_cshl(1, 31) == ((0 as i64) - 2147483648)) { r = r + 9; }    // 1<<31 = MIN
    if (i32_cshr(16, 2) == (4 as i64)) { r = r + 9; }
    return r;
}`},
	{"int-checked-rem", `function i32_crem(n: i32, other: i32): i64 {
    if (other == 0) { return (0 as i64) - 9999999999; }
    if (n == (0 - 2147483647 - 1) && other == (0 - 1)) { return (0 as i64) - 9999999999; }
    return (n % other) as i64;
}
function main(): i32 {
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    if (i32_crem(17, 5) == (2 as i64)) { r = r + 8; }
    if (i32_crem(0 - 17, 5) == ((0 as i64) - 2)) { r = r + 8; }        // sign of dividend
    if (i32_crem(5, 0) == ((0 as i64) - 9999999999)) { r = r + 8; }    // divide-by-zero sentinel
    if (i32_crem(min32, 0 - 1) == ((0 as i64) - 9999999999)) { r = r + 9; }  // MIN % -1 sentinel
    if (i32_crem(10, 3) == (1 as i64)) { r = r + 9; }
    return r;
}`},
	{"int-overflowing-div", `function i32_odiv(n: i32, other: i32): (i32, boolean) {
    let min32: i32 = 0 - 2147483647 - 1;
    if (n == min32 && other == (0 - 1)) { return (min32, true); }
    return (n / other, false);
}
function i32_orem(n: i32, other: i32): (i32, boolean) {
    let min32: i32 = 0 - 2147483647 - 1;
    if (n == min32 && other == (0 - 1)) { return (0, true); }
    return (n % other, false);
}
function main(): i32 {
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    let (q1, o1) = i32_odiv(17, 5);
    if (q1 == 3 && !o1) { r = r + 10; }
    let (q2, o2) = i32_odiv(min32, 0 - 1);
    if (q2 == min32 && o2) { r = r + 11; }                          // MIN / -1 wraps to MIN
    let (m1, p1) = i32_orem(min32, 0 - 1);
    if (m1 == 0 && p1) { r = r + 10; }                              // MIN % -1 is 0, flag set
    let (m2, p2) = i32_orem(0 - 17, 5);
    if (m2 == (0 - 2) && !p2) { r = r + 11; }                       // sign of dividend
    return r;
}`},
	{"int-saturating-div", `function i32_sdiv(n: i32, other: i32): i32 {
    if (n == (0 - 2147483647 - 1) && other == (0 - 1)) { return 2147483647; }
    return n / other;
}
function i32_sneg(n: i32): i32 {
    if (n == (0 - 2147483647 - 1)) { return 2147483647; }
    return 0 - n;
}
function i32_sabs(n: i32): i32 {
    if (n == (0 - 2147483647 - 1)) { return 2147483647; }
    if (n < 0) { return 0 - n; }
    return n;
}
function main(): i32 {
    let max32: i32 = 2147483647;
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    if (i32_sdiv(17, 5) == 3) { r = r + 8; }
    if (i32_sdiv(min32, 0 - 1) == max32) { r = r + 8; }            // MIN / -1 clamps to MAX
    if (i32_sneg(min32) == max32) { r = r + 8; }                   // -MIN clamps to MAX
    if (i32_sneg(0 - 7) == 7) { r = r + 9; }
    if (i32_sabs(min32) == max32) { r = r + 9; }                   // |MIN| clamps to MAX
    return r;
}`},
	{"int-unsigned-abs", `function i32_uabs(n: i32): u32 {
    if (n == (0 - 2147483647 - 1)) { return 2147483648 as u32; }
    if (n < 0) { return (0 - n) as u32; }
    return n as u32;
}
function main(): i32 {
    let min32: i32 = 0 - 2147483647 - 1;
    let r: i32 = 0;
    if (i32_uabs(0 - 5) == (5 as u32)) { r = r + 10; }
    if (i32_uabs(7) == (7 as u32)) { r = r + 10; }
    if (i32_uabs(0) == (0 as u32)) { r = r + 11; }
    if (i32_uabs(min32) == (2147483648 as u32)) { r = r + 11; }    // |MIN| fits u32
    return r;
}`},
}

func TestSelfHostNumericMethodsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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
