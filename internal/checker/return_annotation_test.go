package checker

import (
	"strings"
	"testing"
)

// Every named function declares its return type. Omitting it is E070 — not an
// inference request. Replaces the return-inference suite: a signature is the
// one part of a function its callers read, so it is written, not derived.
func TestMissingReturnTypeRejected(t *testing.T) {
	for _, src := range []string{
		// value-returning bodies that inference used to accept
		`function add(a: i32, b: i32) { return a + b; } function main(): i32 { return add(40, 2); }`,
		`function greet() { return "hi"; } function main(): i32 { return greet().len(); }`,
		`function pos(n: i32) { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`,
		`function pick(b: boolean) { if (b) { return 10; } return 20; } function main(): i32 { return pick(true); }`,
		`function base() { return 21; } function dbl(): i32 { return base() * 2; } function main(): i32 { return dbl(); }`,
		`function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { return 0; }`,
		// a VOID body is rejected too — `: void` is written out
		`function log() { return; } function main(): i32 { log(); return 0; }`,
		`function noop() { } function main(): i32 { noop(); return 0; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected the missing-return-type error for %q", src)
			continue
		}
		if !strings.Contains(err.Error(), "missing return type") {
			t.Errorf("expected the missing-return-type (E070) error for %q, got: %v", src, err)
		}
	}
}

// The annotated forms of the same programs are accepted, including `: void`.
func TestExplicitReturnTypeAccepted(t *testing.T) {
	for _, src := range []string{
		`function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }`,
		`function greet(): string { return "hi"; } function main(): i32 { return greet().len(); }`,
		`function pos(n: i32): boolean { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`,
		`function find(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function main(): i32 { return 0; }`,
		`function log(): void { return; } function main(): i32 { log(); return 0; }`,
		`function noop(): void { } function main(): i32 { noop(); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("annotated program wrongly rejected %q: %v", src, err)
		}
	}
}

// An explicit `: void` still rejects a value return — the annotation is a
// declaration, not a hint.
func TestExplicitVoidRejectsValueReturn(t *testing.T) {
	if err := checkSource(t, `function f(): void { return 5; } function main(): i32 { return 0; }`); err == nil {
		t.Error("expected an error: explicit : void returning a value")
	}
}

// The analyses that used to run on an INFERRED return type still run on the
// declared one: a value-returning function that can fall off the end is a
// missing-return error, and returning a value on only some paths is E012.
func TestDeclaredReturnTypeKeepsFlowAnalyses(t *testing.T) {
	err := checkSource(t, `function f(b: boolean): i32 { if (b) { return 5; } } function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "missing return") {
		t.Errorf("expected a missing-return error, got: %v", err)
	}
	err = checkSource(t, `function f(b: boolean): i32 { if (b) { return 5; } return; } function main(): i32 { return 0; }`)
	if err == nil {
		t.Error("expected an error for a bare return in a value-returning function")
	}
}

// A missing return type reports ONCE, at the declaration — it does not cascade
// into the call sites.
//
// The function still defaults to void after E070, so without the error-recovery
// inference in checkFunction that void propagates: the return mismatches, and
// then every USE of the call result mismatches too, blaming callers for the
// callee's missing annotation. Measured at the time of writing: 1 error with
// the recovery, 3 without. This pins the property so the recovery is not later
// removed as "dead code" — it decides no accepted program's meaning, but it
// decides how a rejected one reads.
func TestUnannotatedFunctionReportsOnce(t *testing.T) {
	for _, src := range []string{
		`function add(a: i32, b: i32) { return a + b; } function main(): i32 { return add(40, 2); }`,
		`function greet() { return "hi"; } function main(): i32 { return greet().len(); }`,
		`function base() { return 21; } function dbl(): i32 { return base() * 2; } function main(): i32 { return dbl(); }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected an error for %q", src)
			continue
		}
		if n := strings.Count(err.Error(), "type error at"); n != 1 {
			t.Errorf("%q: reported %d errors, want exactly 1 (the E070 at the declaration)\n%v", src, n, err)
		}
		if !strings.Contains(err.Error(), "missing return type") {
			t.Errorf("%q: the single error should be the missing-return-type one, got: %v", src, err)
		}
	}
}
