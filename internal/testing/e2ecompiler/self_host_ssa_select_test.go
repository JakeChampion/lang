package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

const ssaSelectProg = `@noinline function mn(a: i32, b: i32): i32 { return if (a < b) { a } else { b }; }
@noinline function mx_u(a: u32, b: u32): u32 { return if (a > b) { a } else { b }; }
@noinline function clamp(x: i64, lo: i64, hi: i64): i64 {
    let r: i64 = x;
    if (r < lo) { r = lo; }
    if (r > hi) { r = hi; }
    return r;
}
@noinline function ab(a: i32): i32 {
    if (a < 0) { return 0 - a; }
    return a;
}
@noinline function order(a: i32, b: i32): i32 {
    let lo: i32 = a;
    let hi: i32 = b;
    if (b < a) { lo = b; hi = a; }
    return hi * 1000 + lo;
}
@noinline function both(a: i32, b: i32): boolean { return a > 0 && b > 0; }
@noinline function fpick(x: f64, a: i32, b: i32): i32 { return if (x < 0.5) { a } else { b }; }
@noinline function busy(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64): i64 {
    let p: i64 = a * b;
    let q: i64 = c * d;
    let r: i64 = e * f;
    let s: i64 = g * h;
    let u: i64 = a + h;
    let v: i64 = b + g;
    let w: i64 = c + f;
    let x: i64 = d + e;
    let m: i64 = if (p < q) { r } else { s };
    return p + q + r + s + u + v + w + x + m * 3i64;
}
@noinline function spill(a: i64, b: i64, c: i64, d: i64): i64 {
    let v1: i64 = a * 3i64; let v2: i64 = b * 5i64; let v3: i64 = c * 7i64; let v4: i64 = d * 11i64;
    let v5: i64 = a + b; let v6: i64 = c + d; let v7: i64 = a - c; let v8: i64 = b - d;
    let v9: i64 = a ^ d; let v10: i64 = b ^ c; let v11: i64 = a | c; let v12: i64 = b & d;
    let v13: i64 = v1 + v2; let v14: i64 = v3 + v4;
    let m: i64 = if (v13 < v14) { v1 } else { v12 };
    let n: i64 = if (v5 > v6) { v13 } else { v14 };
    return v1 + v2 + v3 + v4 + v5 + v6 + v7 + v8 + v9 + v10 + v11 + v12 + v13 + v14 + m * 17i64 + n * 19i64;
}
enum Probe { Has(i32, i32), Not }
function probe(i: i32): Probe {
    let k: i32 = 0;
    while (k < 2) {
        if (k == 1) { return Has(k, i); }
        k = k + 1;
    }
    return Not;
}
@noinline function tagged(r: i32, acc: i32): i32 {
    match (probe(r)) { Has(a, b) => { return acc + a + b; }, Not => { return acc; } }
}
@noinline function phi_tag(n: i32): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < n) {
        let got: Probe = probe(r);
        match (got) { Has(a, b) => { acc = acc + a + b; }, Not => {} }
        r = r + 1;
    }
    return acc % 91;
}
@noinline function safe_div(a: i32, b: i32): i32 { return if (b != 0) { a / b } else { 0 }; }
@noinline function at(xs: i32[], i: i32): i32 { return if (i < xs.len()) { xs[i] } else { 0 - 1 }; }
@noinline function fmn(a: f64, b: f64): f64 { return if (a < b) { a } else { b }; }
function main(): i32 {
    let xs: i32[] = [5, 9, 2];
    let acc: i64 = 0i64;
    let seed: i32 = 12345;
    let i: i32 = 0;
    while (i < 300) {
        seed = seed * 1103515245 + 12345;
        let a: i32 = ((seed >> 16) & 1023) - 512;
        seed = seed * 1103515245 + 12345;
        let b: i32 = ((seed >> 16) & 1023) - 512;
        acc = acc + (mn(a, b) as i64) + (mx_u(a as u32, b as u32) as i64);
        acc = acc + clamp(a as i64, 0i64 - 100i64, 100i64) + (ab(a) as i64) + (order(a, b) as i64);
        if (both(a, b)) { acc = acc + 17i64; }
        acc = acc + (fpick((a as f64) / 1024.0, a, b) as i64);
        acc = acc + busy(a as i64, b as i64, 3i64, (a - b) as i64, 7i64, (b + 1) as i64, (a * 2) as i64, 11i64);
        acc = acc + spill(a as i64, b as i64, (a & 15) as i64, (b - a) as i64);
        acc = acc + (tagged(a, b) as i64);
        acc = acc + (phi_tag(i % 61) as i64);
        acc = acc + (safe_div(a, b & 3) as i64) + (at(xs, b & 3) as i64);
        acc = acc + (fmn((a as f64) * 0.5, b as f64) as i64);
        acc = acc * 31i64 % 1000000007i64;
        i = i + 1;
    }
    return (acc % 251i64) as i32;
}
`

func TestSelfHostSSASelectsAgreeWithInterp(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "selects.fern")
	if err := os.WriteFile(src, []byte(ssaSelectProg), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := exec.Command(h.native, "-interp", src)
	_ = oracle.Run()
	if oracle.ProcessState == nil || !oracle.ProcessState.Exited() {
		t.Fatalf("the interpreter did not exit normally: %v", oracle.ProcessState)
	}
	want := oracle.ProcessState.ExitCode()
	for _, tg := range h.targets {
		bin := filepath.Join(dir, "selects-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != want {
			t.Errorf("%s: exit %d, the interpreter's is %d", tg.target, got, want)
		}
	}
}

func TestSelfHostSSASelectsAreConditionalMoves(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "selects.fern")
	if err := os.WriteFile(src, []byte(ssaSelectProg), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		move   *regexp.Regexp
		branch *regexp.Regexp
	}{
		{"x86-64-linux", regexp.MustCompile(`(?m)^\s+cmov[a-z]+q `), regexp.MustCompile(`(?m)^\s+j[a-ln-z]\w* `)},
		{"arm64-linux", regexp.MustCompile(`(?m)^\s+csel `), regexp.MustCompile(`(?m)^\s+(?:b\.\w+|cbn?z|tbn?z) `)},
	}
	selects := []string{"mn", "mx_u", "clamp", "ab", "order", "both", "fpick", "busy", "spill"}
	moves := []string{"tagged", "phi_tag"}
	branches := []string{"safe_div", "at", "fmn"}
	for _, c := range cases {
		out := filepath.Join(dir, "selects-"+c.target+".s")
		if report, err := exec.Command(h.cli, "-target", c.target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range selects {
			fn := functionListing(string(asm), "__fn_"+name)
			if fn == "" {
				t.Fatalf("%s: no __fn_%s in the listing", c.target, name)
			}
			if c.branch.MatchString(fn) || !c.move.MatchString(fn) {
				t.Errorf("%s: %s should be a conditional move with no conditional branch:\n%s", c.target, name, fn)
			}
		}
		for _, name := range moves {
			if fn := functionListing(string(asm), "__fn_"+name); !c.move.MatchString(fn) {
				t.Errorf("%s: %s should hold a conditional move:\n%s", c.target, name, fn)
			}
		}
		for _, name := range branches {
			fn := functionListing(string(asm), "__fn_"+name)
			if fn == "" {
				t.Fatalf("%s: no __fn_%s in the listing", c.target, name)
			}
			if !c.branch.MatchString(fn) {
				t.Errorf("%s: %s should keep its branch:\n%s", c.target, name, fn)
			}
		}
	}
}
