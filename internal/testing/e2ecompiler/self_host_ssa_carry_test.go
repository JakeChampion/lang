package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// The branch-free carry out of a 64-bit add and borrow out of a subtract are
// read off the flags (ssa.fuse_carries, #12558): with and without an
// incoming carry known to be 0 or 1, with the formula's operands commuted,
// chained through four limbs as multi-word addition does, and in a body busy
// enough to spill. An incoming carry that may exceed 1 keeps the formula, as
// the flags would answer differently. Every input pair runs from 0 to all
// ones through the top bit's edges.
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
	}{
		{"x86-64-linux", regexp.MustCompile(`(?m)^\s+setb `), regexp.MustCompile(`(?m)^\s+shrq \$63, `)},
		{"arm64-linux", regexp.MustCompile(`(?m)^\s+cset x\d+, (hs|lo)$`), regexp.MustCompile(`(?m)^\s+lsr x\d+, x\d+, #63$`)},
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
		// addc_top's incoming carry is itself a shift by 63.
		if fn := functionListing(string(asm), "__fn_addc_top"); !c.flags.MatchString(fn) {
			t.Errorf("%s: addc_top should read its carry off the flags:\n%s", c.target, fn)
		}
		if fn := functionListing(string(asm), "__fn_wide_in"); !c.shift.MatchString(fn) || c.flags.MatchString(fn) {
			t.Errorf("%s: wide_in's incoming carry may exceed 1, so it should keep the formula:\n%s", c.target, fn)
		}
	}
}
