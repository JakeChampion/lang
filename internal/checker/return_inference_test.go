package checker

import (
	"strings"
	"testing"
)

// An unannotated function infers its return type from its `return`
// expressions, so a value-returning body that previously errored
// ("returns void but returns a value") now type-checks.
func TestReturnInferenceAccepts(t *testing.T) {
	for _, src := range []string{
		`function add(a: i32, b: i32) { return a + b; } function main(): i32 { return add(40, 2); }`,
		`function greet() { return "hi"; } function main(): i32 { return greet().len(); }`,
		`function pos(n: i32) { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`,
		`function pick(b: boolean) { if (b) { return 10; } return 20; } function main(): i32 { return pick(true); }`,
		// inference flows through a call to another inferred function
		// (declaration order).
		`function base() { return 21; } function dbl() { return base() * 2; } function main(): i32 { return dbl(); }`,
		// bare None adopts Option[i32] from the Some arm.
		`function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(5)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`,
		// no value returns -> stays void (unchanged).
		`function log() { return; } function main(): i32 { log(); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

// An explicit `: void` is NOT inference — a value return must still be
// rejected.
func TestReturnInferenceRespectsExplicitVoid(t *testing.T) {
	if err := checkSource(t, `function f(): void { return 5; } function main(): i32 { return 0; }`); err == nil {
		t.Error("expected an error: explicit : void returning a value")
	}
}

// Conflicting return-expression types can't be inferred — E002 asks for
// an annotation.
func TestReturnInferenceConflict(t *testing.T) {
	err := checkSource(t, `function f(b: boolean) { if (b) { return 1; } return "x"; } function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if !strings.Contains(err.Error(), "conflicting return types") {
		t.Errorf("expected an inference-conflict error, got: %v", err)
	}
}

// Returning a value on some paths but not others is rejected (E012).
func TestReturnInferenceMixedValueVoid(t *testing.T) {
	err := checkSource(t, `function f(b: boolean) { if (b) { return 5; } return; } function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("expected a mixed value/void error")
	}
	if !strings.Contains(err.Error(), "some paths but not others") {
		t.Errorf("expected a mixed value/void error, got: %v", err)
	}
}

// An inferred non-void function that can fall off the end is still
// caught by the missing-return analysis (E052), now reported with the
// inferred type.
func TestReturnInferenceFallsOffEnd(t *testing.T) {
	err := checkSource(t, `function f(b: boolean) { if (b) { return 5; } } function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("expected a missing-return error")
	}
	if !strings.Contains(err.Error(), "missing return") {
		t.Errorf("expected a missing-return error, got: %v", err)
	}
}
