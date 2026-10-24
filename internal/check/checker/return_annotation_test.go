package checker

import (
	"strings"
	"testing"
)

func TestMissingReturnTypeRejected(t *testing.T) {
	for _, src := range []string{
		`function add(a: i32, b: i32) { return a + b; } function main(): i32 { return add(40, 2); }`,
		`function greet() { return "hi"; } function main(): i32 { return greet().len(); }`,
		`function pos(n: i32) { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`,
		`function pick(b: boolean) { if (b) { return 10; } return 20; } function main(): i32 { return pick(true); }`,
		`function base() { return 21; } function dbl(): i32 { return base() * 2; } function main(): i32 { return dbl(); }`,
		`function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { return 0; }`,
		`function log() { return; } function main(): i32 { log(); return 0; }`,
		`function noop() { } function main(): i32 { noop(); return 0; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected the missing-return-type error for %q", src)
			continue
		}
		if !strings.Contains(err.Error(), "missing return type") {
			t.Errorf("expected the missing-return-type (E081) error for %q, got: %v", src, err)
		}
		if code := firstErrCode(err); code != "E081" {
			t.Errorf("%q: missing return type is %s, want E081", src, code)
		}
	}
}

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

func TestExplicitVoidRejectsValueReturn(t *testing.T) {
	if err := checkSource(t, `function f(): void { return 5; } function main(): i32 { return 0; }`); err == nil {
		t.Error("expected an error: explicit : void returning a value")
	}
}

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
			t.Errorf("%q: reported %d errors, want exactly 1 (the E081 at the declaration)\n%v", src, n, err)
		}
		if !strings.Contains(err.Error(), "missing return type") {
			t.Errorf("%q: the single error should be the missing-return-type one, got: %v", src, err)
		}
	}
}

func TestSelfReturningInferenceIsRefused(t *testing.T) {
	for _, src := range []string{
		`function A() { return A; }`,
		`function A() { { return A; } }`,
		`function A() { return [A]; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected an error for %q", src)
			continue
		}
		if !strings.Contains(err.Error(), "returns itself") {
			t.Errorf("%q: want the self-returning inference error, got: %v", src, err)
		}
	}
}
