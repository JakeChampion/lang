package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateScopeCases = []struct {
	name string
	src  string
}{
	{"for_f64_method", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 2.0; }
function main(): i32 {
    let vs: V[] = [V { x: 1.5 }, V { x: 0.375 }];
    let t: f64 = 0.0;
    for v in vs { t = t + v.scaled(); }
    return t as i32;
}`},
	{"for_u64_method", `struct W { v: u64 }
function (w: W) bits(): u64 { return w.v; }
function main(): i32 {
    let ws: W[] = [W { v: 18000000000000000000 as u64 }];
    let t: i32 = 0;
    for w in ws { t = t + ((w.bits() >> 40) as i32); }
    return t;
}`},
	{"for_str_method", `struct B { n: i32 }
function (b: B) label(): string { return "abc"; }
function main(): i32 {
    let bs: B[] = [B { n: 7 }];
    let t: i32 = 0;
    for b in bs { t = t + b.label().len(); }
    return t;
}`},
	{"for_tuple_method", `struct P { a: i32, b: i32 }
function (p: P) pair(): (i32, i32) { return (p.a, p.b); }
function main(): i32 {
    let ps: P[] = [P { a: 3, b: 4 }];
    let t: i32 = 0;
    for p in ps { t = t + p.pair().0 * 10 + p.pair().1; }
    return t;
}`},
	{"for_range_var", `function dbl(n: i32): i32 { return n * 2; }
function main(): i32 {
    let t: i32 = 0;
    for i in 0..4 { t = t + dbl(i); }
    return t;
}`},
	{"match_payload_struct", `struct Q { n: i32 }
function (q: Q) twice(): Q { return Q { n: q.n * 2 }; }
enum E { A(Q), B(i32) }
function main(): i32 {
    let e: E = E.A(Q { n: 5 });
    let t: i32 = 0;
    match (e) {
        E.A(q) => { t = q.twice().n; },
        E.B(k) => { t = k; }
    }
    return t;
}`},
	{"match_payload_f64", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 2.0; }
enum E { A(V), B(i32) }
function main(): i32 {
    let e: E = E.A(V { x: 1.5 });
    let t: f64 = 0.5;
    match (e) {
        E.A(v) => { t = t + v.scaled(); },
        E.B(k) => { t = 1.5; }
    }
    return t as i32;
}`},
	{"match_guard_f64", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 4.0; }
enum E { A(V), B(i32) }
function main(): i32 {
    let e: E = E.A(V { x: 1.25 });
    let t: f64 = 0.0;
    match (e) {
        E.A(v) when v.scaled() > 2.0 => { t = v.scaled(); },
        E.A(v) => { t = 0.5; },
        E.B(k) => { t = 1.5; }
    }
    return t as i32;
}`},
}

func TestSelfHostAnnotateScopeIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	for _, tc := range annotateScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annscope_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
