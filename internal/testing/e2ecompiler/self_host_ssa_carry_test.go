package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

const ssaCarryProg = `import "std/u64";

const ALL: u64 = 0xffffffffffffffffu64;
@noinline function addc(x: u64, y: u64): u64 {
    let s: u64 = x + y;
    return (x & y | (x | y) & (s ^ ALL)) >> 63u64;
}
@noinline function addc_in(x: u64, y: u64, c: u64): u64 {
    let k: u64 = c & 1u64;
    let s: u64 = x + y + k;
    return (x & y | (x | y) & (s ^ ALL)) >> 63u64;
}
@noinline function addc_swapped(x: u64, y: u64): u64 {
    let s: u64 = y + x;
    return ((ALL ^ s) & (y | x) | y & x) >> 63u64;
}
@noinline function subb(x: u64, y: u64): u64 {
    let d: u64 = x - y;
    return ((x ^ ALL) & y | ((x ^ ALL) | y) & d) >> 63u64;
}
@noinline function subb_in(x: u64, y: u64, b: u64): u64 {
    let k: u64 = b & 1u64;
    let d: u64 = x - y - k;
    return ((x ^ ALL) & y | ((x ^ ALL) | y) & d) >> 63u64;
}
@noinline function addc_top(x: u64, y: u64, c: u64): u64 {
    let k: u64 = c >> 63u64;
    let s: u64 = x + y + k;
    return (x & y | (x | y) & (s ^ ALL)) >> 63u64;
}
@noinline function wide_in(x: u64, y: u64, c: u64): u64 {
    let s: u64 = x + y + c;
    return (x & y | (x | y) & (s ^ ALL)) >> 63u64;
}
@noinline function add4(a0: u64, a1: u64, a2: u64, a3: u64, b0: u64, b1: u64, b2: u64, b3: u64): u64 {
    let s0: u64 = a0 + b0;
    let c0: u64 = (a0 & b0 | (a0 | b0) & (s0 ^ ALL)) >> 63u64;
    let s1: u64 = a1 + b1 + c0;
    let c1: u64 = (a1 & b1 | (a1 | b1) & (s1 ^ ALL)) >> 63u64;
    let s2: u64 = a2 + b2 + c1;
    let c2: u64 = (a2 & b2 | (a2 | b2) & (s2 ^ ALL)) >> 63u64;
    let s3: u64 = a3 + b3 + c2;
    let c3: u64 = (a3 & b3 | (a3 | b3) & (s3 ^ ALL)) >> 63u64;
    let p0: u64 = a0 * b3;
    let p1: u64 = a1 * b2;
    let p2: u64 = a2 * b1;
    let p3: u64 = a3 * b0;
    let q0: u64 = __mulhi_u64(a0, b0);
    let q1: u64 = __mulhi_u64(a3, b3);
    return s0 * 3u64 + s1 * 5u64 + s2 * 7u64 + s3 * 11u64 + c3 * 13u64 + (p0 ^ p1 ^ p2 ^ p3) + q0 * 17u64 + q1 * 19u64 + a0 + a1 + a2 + a3 + b0 + b1 + b2 + b3;
}
@noinline function add_carried(x: u64, y: u64, h: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return h + c ^ s;
}
@noinline function add_carried_left(x: u64, y: u64, h: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return c + h ^ s;
}
@noinline function sub_borrowed(x: u64, y: u64, h: u64): u64 {
    let d: u64 = x - y;
    let b: u64 = ((x ^ ALL) & y | ((x ^ ALL) | y) & d) >> 63u64;
    return h - b ^ d;
}
struct W3 { a: u64, b: u64, c: u64 }
@noinline function carried_far(x: u64, y: u64, h: u64, z: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    let m: u64 = z * s;
    let n: u64 = m ^ x;
    return (h + c) ^ n;
}
@noinline function carried_twice(x: u64, y: u64, h: u64, z: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return (h + c) ^ (z + c) ^ s;
}
@noinline function carried_late(x: u64, y: u64, h: u64, z: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    let m: u64 = z * h;
    let t: u64 = m + c;
    let k: u64 = (m & c | (m | c) & (t ^ ALL)) >> 63u64;
    return (h + k) ^ t ^ s;
}
@noinline function carried_masked(x: u64, y: u64, h: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return (h + c & 0xffffffffu64) ^ s;
}
@noinline function add3(x: W3, y: W3): W3 {
    let s0: u64 = x.a + y.a;
    let c0: u64 = (x.a & y.a | (x.a | y.a) & (s0 ^ ALL)) >> 63u64;
    let t1: u64 = x.b + y.b;
    let c1a: u64 = (x.b & y.b | (x.b | y.b) & (t1 ^ ALL)) >> 63u64;
    let s1: u64 = t1 + c0;
    let c1b: u64 = (t1 & c0 | (t1 | c0) & (s1 ^ ALL)) >> 63u64;
    let c1: u64 = c1a | c1b;
    let s2: u64 = x.c + y.c + c1;
    return W3 { a: s0, b: s1, c: s2 };
}
@noinline function add3_sum(x: W3, y: W3): W3 {
    let s0: u64 = x.a + y.a;
    let c0: u64 = (x.a & y.a | (x.a | y.a) & (s0 ^ ALL)) >> 63u64;
    let t1: u64 = x.b + y.b;
    let c1a: u64 = (x.b & y.b | (x.b | y.b) & (t1 ^ ALL)) >> 63u64;
    let s1: u64 = c0 + t1;
    let c1b: u64 = (t1 & c0 | (t1 | c0) & (s1 ^ ALL)) >> 63u64;
    let s2: u64 = x.c + y.c + (c1b + c1a);
    return W3 { a: s0, b: s1, c: s2 };
}
@noinline function sub3(x: W3, y: W3): W3 {
    let d0: u64 = x.a - y.a;
    let b0: u64 = ((x.a ^ ALL) & y.a | ((x.a ^ ALL) | y.a) & d0) >> 63u64;
    let t1: u64 = x.b - y.b;
    let b1a: u64 = ((x.b ^ ALL) & y.b | ((x.b ^ ALL) | y.b) & t1) >> 63u64;
    let d1: u64 = t1 - b0;
    let b1b: u64 = ((t1 ^ ALL) & b0 | ((t1 ^ ALL) | b0) & d1) >> 63u64;
    let b1: u64 = b1a | b1b;
    let d2: u64 = x.c - y.c - b1;
    return W3 { a: d0, b: d1, c: d2 };
}
@noinline function sub4(a0: u64, a1: u64, a2: u64, a3: u64, b0: u64, b1: u64, b2: u64, b3: u64): u64 {
    let d0: u64 = a0 - b0;
    let k0: u64 = ((a0 ^ ALL) & b0 | ((a0 ^ ALL) | b0) & d0) >> 63u64;
    let d1: u64 = a1 - b1 - k0;
    let k1: u64 = ((a1 ^ ALL) & b1 | ((a1 ^ ALL) | b1) & d1) >> 63u64;
    let d2: u64 = a2 - b2 - k1;
    let k2: u64 = ((a2 ^ ALL) & b2 | ((a2 ^ ALL) | b2) & d2) >> 63u64;
    let d3: u64 = a3 - b3 - k2;
    let k3: u64 = ((a3 ^ ALL) & b3 | ((a3 ^ ALL) | b3) & d3) >> 63u64;
    return d0 * 3u64 + d1 * 5u64 + d2 * 7u64 + d3 * 11u64 + k3 * 13u64 + k1 * 17u64;
}
@noinline function sub_p(a0: u64, a1: u64, a2: u64, a3: u64): u64 {
    let d0: u64 = a0 - 0xffffffffffffffffu64;
    let k0: u64 = ((a0 ^ ALL) & 0xffffffffffffffffu64 | ((a0 ^ ALL) | 0xffffffffffffffffu64) & d0) >> 63u64;
    let d1: u64 = a1 - 0x00000000ffffffffu64 - k0;
    let k1: u64 = ((a1 ^ ALL) & 0x00000000ffffffffu64 | ((a1 ^ ALL) | 0x00000000ffffffffu64) & d1) >> 63u64;
    let d2: u64 = a2 - 0x0000000000000000u64 - k1;
    let k2: u64 = ((a2 ^ ALL) & 0x0000000000000000u64 | ((a2 ^ ALL) | 0x0000000000000000u64) & d2) >> 63u64;
    let d3: u64 = a3 - 0xffffffff00000001u64 - k2;
    let k3: u64 = ((a3 ^ ALL) & 0xffffffff00000001u64 | ((a3 ^ ALL) | 0xffffffff00000001u64) & d3) >> 63u64;
    return d0 * 3u64 + d1 * 5u64 + d2 * 7u64 + d3 * 11u64 + k3 * 13u64;
}
@noinline function add_k(a0: u64, a1: u64): u64 {
    let s0: u64 = a0 + 0xffffffff00000001u64;
    let c0: u64 = (a0 & 0xffffffff00000001u64 | (a0 | 0xffffffff00000001u64) & (s0 ^ ALL)) >> 63u64;
    let s1: u64 = a1 + 0x00000000ffffffffu64 + c0;
    let c1: u64 = (a1 & 0x00000000ffffffffu64 | (a1 | 0x00000000ffffffffu64) & (s1 ^ ALL)) >> 63u64;
    let s2: u64 = a0 + 0u64 + c1;
    let c2: u64 = (a0 & 0u64 | (a0 | 0u64) & (s2 ^ ALL)) >> 63u64;
    return s0 * 3u64 + s1 * 5u64 + s2 * 7u64 + c2 * 11u64;
}
@noinline function add4_spilled(a0: u64, a1: u64, a2: u64, a3: u64, b0: u64, b1: u64, b2: u64, b3: u64): u64 {
    let m0: u64 = a0 * b1;
    let m1: u64 = a1 * b2;
    let m2: u64 = a2 * b3;
    let m3: u64 = a3 * b0;
    let m4: u64 = a0 ^ b2;
    let m5: u64 = a1 ^ b3;
    let m6: u64 = a2 ^ b0;
    let m7: u64 = a3 ^ b1;
    let s0: u64 = a0 + b0;
    let c0: u64 = (a0 & b0 | (a0 | b0) & (s0 ^ ALL)) >> 63u64;
    let s1: u64 = a1 + b1 + c0;
    let c1: u64 = (a1 & b1 | (a1 | b1) & (s1 ^ ALL)) >> 63u64;
    let s2: u64 = a2 + b2 + c1;
    let c2: u64 = (a2 & b2 | (a2 | b2) & (s2 ^ ALL)) >> 63u64;
    let s3: u64 = a3 + b3 + c2;
    let c3: u64 = (a3 & b3 | (a3 | b3) & (s3 ^ ALL)) >> 63u64;
    return s0 * m0 + s1 * m1 + s2 * m2 + s3 * m3 + c3 * 13u64 + c1 * m4 + (m5 ^ m6 ^ m7) + a0 + a1 + a2 + a3 + b0 + b1 + b2 + b3;
}
@noinline function carried_narrow(x: u64, y: u64, h: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return (((h + c) as u32) as u64) ^ s;
}
@noinline function carried_imm(x: u64, y: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return (7u64 + c) ^ s;
}
@noinline function borrowed_imm(x: u64, y: u64): u64 {
    let d: u64 = x - y;
    let b: u64 = ((x ^ ALL) & y | ((x ^ ALL) | y) & d) >> 63u64;
    return (d - b) ^ (9u64 - b);
}
@noinline function linked_imm(x: u64, y: u64, z: u64): u64 {
    let s: u64 = x + y;
    let c: u64 = (x & y | (x | y) & (s ^ ALL)) >> 63u64;
    return (5u64 + z + c) ^ s;
}
@noinline function wide_k(x: u64): u64 {
    let lo: u64 = x * 0xffffffff00000001u64;
    let hi: u64 = __mulhi_u64(x, 0xffffffff00000001u64);
    return lo ^ hi;
}
@noinline function wide_small(x: u64): u64 {
    let lo: u64 = x * 10u64;
    let hi: u64 = __mulhi_u64(10u64, x);
    return lo ^ hi;
}
@noinline function wide_xy(x: u64, y: u64): u64 {
    let lo: u64 = x * y;
    let hi: u64 = __mulhi_u64(y, x);
    return lo ^ hi;
}
function main(): i32 {
    let edges: u64[] = [0u64, 1u64, 2u64, 0x7fffffffffffffffu64, 0x8000000000000000u64, 0xfffffffffffffffeu64, 0xffffffffffffffffu64, 0x123456789abcdef0u64, 0xedcba98765432110u64];
    let acc: u64 = 0u64;
    for x in edges {
        for y in edges {
            let r: u64 = addc(x, y) + 2u64 * addc_in(x, y, 0u64) + 4u64 * addc_in(x, y, 7u64) + 8u64 * addc_swapped(x, y);
            r = r + 16u64 * subb(x, y) + 32u64 * subb_in(x, y, 0u64) + 64u64 * subb_in(x, y, ALL);
            r = r + 512u64 * addc_top(x, y, ALL) + 1024u64 * addc_top(x, y, 1u64);
            r = r + 128u64 * wide_in(x, y, 0x8000000000000001u64) + 256u64 * wide_in(x, y, 3u64);
            acc = acc * 1000003u64 + r;
            acc = acc ^ add4(x, y, x ^ y, y - x, y, x, x + 1u64, ALL - y);
            acc = acc * 7u64 + wide_k(x) + 3u64 * wide_small(y) + 5u64 * wide_xy(x, y);
            for h in [0u64, x ^ y, ALL] {
                acc = acc * 31u64 + add_carried(x, y, h) + 3u64 * add_carried_left(x, y, h) + 5u64 * sub_borrowed(x, y, h);
                acc = acc * 41u64 + carried_imm(x, y) + 3u64 * borrowed_imm(x, y) + 5u64 * linked_imm(x, y, h);
                acc = acc * 31u64 + carried_far(x, y, h, y + 3u64) + 3u64 * carried_twice(x, y, h, x) + 5u64 * carried_late(x, y, h, y) + 7u64 * carried_masked(x, y, h) + 11u64 * carried_narrow(x, y, h);
                let p = W3 { a: x, b: h, c: y };
                let q = W3 { a: y, b: ALL - h, c: x ^ 1u64 };
                let r = add3(p, q);
                let r2 = add3_sum(p, q);
                let r3 = sub3(p, q);
                acc = acc * 131u64 + r.a + 3u64 * r.b + 5u64 * r.c + 7u64 * r2.a + 11u64 * r2.b + 13u64 * r2.c + 17u64 * r3.a + 19u64 * r3.b + 23u64 * r3.c;
                acc = acc * 37u64 + sub_p(x, y, h, x ^ h) + 3u64 * add_k(y, h);
                acc = acc ^ sub4(x, y, h, y - x, y, h, x + 1u64, ALL - y) ^ add4_spilled(x, h, x ^ y, y - x, y, ALL - h, x + 1u64, ALL - y);
            }
        }
    }
    print(acc.to_string());
    return (acc % 251u64) as i32;
}
`

func TestSelfHostSSACarriesAgreeWithInterp(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carries.fern")
	if err := os.WriteFile(src, []byte(ssaCarryProg), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := exec.Command(h.native, "-interp", src)
	want, _ := oracle.Output()
	if oracle.ProcessState == nil || !oracle.ProcessState.Exited() {
		t.Fatalf("the interpreter did not exit normally: %v", oracle.ProcessState)
	}
	wantCode := oracle.ProcessState.ExitCode()
	for _, tg := range h.targets {
		bin := filepath.Join(dir, "carries-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if got, code := h.runProduced(t, tg, bin); got != string(want) || code != wantCode {
			t.Errorf("%s: printed %q and exited %d, the interpreter %q and %d", tg.target, got, code, want, wantCode)
		}
	}
}

func TestSelfHostSSACarriesReadTheFlags(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carries.fern")
	if err := os.WriteFile(src, []byte(ssaCarryProg), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		flags  *regexp.Regexp
		shift  *regexp.Regexp
		kept   *regexp.Regexp
		link   *regexp.Regexp
	}{
		{"x86-64-linux", regexp.MustCompile(`(?m)^\s+setb `), regexp.MustCompile(`(?m)^\s+shrq \$63, `), regexp.MustCompile(`(?m)^\s+(adcq|sbbq) \$0, `), regexp.MustCompile(`(?m)^\s+(adcq|sbbq) [^$]`)},
		{"arm64-linux", regexp.MustCompile(`(?m)^\s+cset x\d+, (hs|lo)$`), regexp.MustCompile(`(?m)^\s+lsr x\d+, x\d+, #63$`), regexp.MustCompile(`(?m)^\s+(adc|sbc)s? x\d+, x\d+, xzr$`), regexp.MustCompile(`(?m)^\s+(adc|sbc)s? x\d+, x\d+, x\d+$`)},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "carries-"+c.target+".s")
		if report, err := exec.Command(h.cli, "-target", c.target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"addc", "addc_in", "addc_swapped", "subb", "subb_in", "add4"} {
			fn := functionListing(string(asm), "__fn_"+name)
			if fn == "" {
				t.Fatalf("%s: no __fn_%s in the listing", c.target, name)
			}
			if !c.flags.MatchString(fn) || c.shift.MatchString(fn) {
				t.Errorf("%s: %s should read its carries off the flags, with no shift by 63:\n%s", c.target, name, fn)
			}
		}
		for _, name := range []string{"add_carried", "add_carried_left", "sub_borrowed", "carried_far", "carried_late", "carried_masked", "carried_narrow", "carried_imm"} {
			fn := functionListing(string(asm), "__fn_"+name)
			if !c.kept.MatchString(fn) || c.flags.MatchString(fn) || c.shift.MatchString(fn) {
				t.Errorf("%s: %s's carry should stay in the flags for the add or subtract that reads it:\n%s", c.target, name, fn)
			}
		}
		chains := []struct {
			name         string
			links, flags int
		}{{"add3", 2, 0}, {"add3_sum", 2, 0}, {"sub3", 2, 0}, {"add4", 3, 1}, {"sub4", 3, 2}, {"add4_spilled", 3, 2}, {"sub_p", 2, 1}, {"add_k", 1, 1}}
		for _, ch := range chains {
			fn := functionListing(string(asm), "__fn_"+ch.name)
			links := len(c.link.FindAllString(fn, -1))
			flags := len(c.flags.FindAllString(fn, -1))
			if links != ch.links || flags != ch.flags || c.shift.MatchString(fn) {
				t.Errorf("%s: %s has %d adds with carry and writes %d carries to a register, want %d and %d:\n%s", c.target, ch.name, links, flags, ch.links, ch.flags, fn)
			}
		}
		if fn := functionListing(string(asm), "__fn_carried_twice"); !c.kept.MatchString(fn) || len(c.flags.FindAllString(fn, -1)) != 1 {
			t.Errorf("%s: carried_twice should write its carry to a register one time, and its first reader should read the flags:\n%s", c.target, fn)
		}
		if fn := functionListing(string(asm), "__fn_addc_top"); !c.flags.MatchString(fn) {
			t.Errorf("%s: addc_top should read its carry off the flags:\n%s", c.target, fn)
		}
		if fn := functionListing(string(asm), "__fn_wide_in"); !c.shift.MatchString(fn) || c.flags.MatchString(fn) {
			t.Errorf("%s: wide_in's incoming carry may exceed 1, so it should keep the formula:\n%s", c.target, fn)
		}
	}
}

// A product and its high half with the same operands make one mulq, also when
// each names its own literal of the same value. The mulq reads its operand
// from the home of that operand, with no copy into %rcx.
func TestSelfHostSSAProductHalvesShareOneMultiply(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carries.fern")
	if err := os.WriteFile(src, []byte(ssaCarryProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "carries.s")
	if report, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, report)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mulq := regexp.MustCompile(`(?m)^\s+mulq `)
	imulq := regexp.MustCompile(`(?m)^\s+imulq `)
	rcx := regexp.MustCompile(`(?m)^\s+(movq [^\n]*, %rcx|mulq %rcx)$`)
	for _, name := range []string{"wide_k", "wide_small", "wide_xy"} {
		fn := functionListing(string(asm), "__fn_"+name)
		if fn == "" {
			t.Fatalf("no __fn_%s in the listing", name)
		}
		if n := len(mulq.FindAllString(fn, -1)); n != 1 || imulq.MatchString(fn) {
			t.Errorf("%s has %d mulq, want one mulq and no imulq:\n%s", name, n, fn)
		}
		if rcx.MatchString(fn) {
			t.Errorf("%s copies a factor into %%rcx, want the mulq to read it in place:\n%s", name, fn)
		}
	}
}
