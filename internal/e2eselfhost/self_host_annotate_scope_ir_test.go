package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateScopeCases widen the typed-IR annotation (#5531) from the flat
// statement scope to the scopes a nested BODY actually sees. The annotate pass
// must not rebuild a for-loop body or a match arm in the ENCLOSING scope: the
// loop variable / variant payload is unbound there, so any call whose type
// depends on one (a method on the binding, above all) resolved to TypeUnknown —
// tag "", the structural fallback. annotate_for_scope / annotate_arm_scope now
// bind them as check_stmt does, so those calls carry the checker's type.
//
// Verified decisive, not merely present: with the consumer arm in irlower's
// expr_is_f64 sabotaged to never match c.ty, the f64 cases below emit DIFFERENT
// asm — but only with these bindings in place; before them the same sabotage was
// inert, because nothing inside the nested body was annotated at all.
//
// Each case puts the typed call INSIDE the nested body, over the binding, and
// picks a result type whose misclassification is observable: f64 arithmetic, an
// unsigned shift, a string length, a tuple element, a struct field.
var annotateScopeCases = []struct {
	name string
	src  string
}{
	// f64-returning method on a for-loop variable: an integer add on the
	// double's bits would not sum to 3.75.
	{"for_f64_method", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 2.0; }
function main(): i32 {
    var vs: V[] = [V { x: 1.5 }, V { x: 0.375 }];
    var t: f64 = 0.0;
    for v in vs { t = t + v.scaled(); }
    return t as i32;
}`}, // 3.0 + 0.75 = 3.75 -> 3
	// u64-returning method on a for-loop variable: a SIGNED >> on a bit-63-set
	// value gives a different answer.
	{"for_u64_method", `struct W { v: u64 }
function (w: W) bits(): u64 { return w.v; }
function main(): i32 {
    var ws: W[] = [W { v: 18000000000000000000 as u64 }];
    var t: i32 = 0;
    for w in ws { t = t + ((w.bits() >> 40) as i32); }
    return t;
}`}, // 216
	// string-returning method on a for-loop variable.
	{"for_str_method", `struct B { n: i32 }
function (b: B) label(): string { return "abc"; }
function main(): i32 {
    var bs: B[] = [B { n: 7 }];
    var t: i32 = 0;
    for b in bs { t = t + b.label().len(); }
    return t;
}`}, // 3
	// tuple-returning method on a for-loop variable, read by element index.
	{"for_tuple_method", `struct P { a: i32, b: i32 }
function (p: P) pair(): (i32, i32) { return (p.a, p.b); }
function main(): i32 {
    var ps: P[] = [P { a: 3, b: 4 }];
    var t: i32 = 0;
    for p in ps { t = t + p.pair().0 * 10 + p.pair().1; }
    return t;
}`}, // 34
	// range-form loop variable: bound i32, so the call over it annotates.
	{"for_range_var", `function dbl(n: i32): i32 { return n * 2; }
function main(): i32 {
    var t: i32 = 0;
    for i in 0..4 { t = t + dbl(i); }
    return t;
}`}, // 2*(0+1+2+3) = 12
	// struct-returning method on a MATCH ARM's variant payload.
	{"match_payload_struct", `struct Q { n: i32 }
function (q: Q) twice(): Q { return Q { n: q.n * 2 }; }
enum E { A(Q), B(i32) }
function main(): i32 {
    var e: E = E.A(Q { n: 5 });
    var t: i32 = 0;
    match (e) {
        E.A(q) => { t = q.twice().n; },
        E.B(k) => { t = k; }
    }
    return t;
}`}, // 10
	// f64-returning method on a match arm's payload, summed into an f64 local:
	// an integer add on the double's bits would not reach 3.5.
	{"match_payload_f64", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 2.0; }
enum E { A(V), B(i32) }
function main(): i32 {
    var e: E = E.A(V { x: 1.5 });
    var t: f64 = 0.5;
    match (e) {
        E.A(v) => { t = t + v.scaled(); },
        E.B(k) => { t = 1.5; }
    }
    return t as i32;
}`}, // 0.5 + 3.0 = 3.5 -> 3
	// f64-returning method on a match arm's payload, under a GUARD that also
	// calls over the payload — the guard is annotated in the arm scope too.
	{"match_guard_f64", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 4.0; }
enum E { A(V), B(i32) }
function main(): i32 {
    var e: E = E.A(V { x: 1.25 });
    var t: f64 = 0.0;
    match (e) {
        E.A(v) when v.scaled() > 2.0 => { t = v.scaled(); },
        E.A(v) => { t = 0.5; },
        E.B(k) => { t = 1.5; }
    }
    return t as i32;
}`}, // 5.0 -> 5
}

// TestSelfHostAnnotateScopeIR_X86_64 pins the for-loop / match-arm bindings
// threaded through the annotate pass, feeding irlower's type predicates through
// the self-host x86-64 IR path (#5520 / #5531).
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
