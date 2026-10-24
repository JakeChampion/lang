package checker

import (
	"strings"
	"testing"
)

func TestStringOrderingAccepted(t *testing.T) {
	for _, src := range []string{
		`function f(): boolean { return "abc" < "abd"; }`,
		`function f(): boolean { return "abc" <= "abd"; }`,
		`function f(): boolean { return "abc" > "abd"; }`,
		`function f(): boolean { return "abc" >= "abd"; }`,
		`function f(s: string): boolean { return s < "abd"; }`,
		`function f(v: str): boolean { return v < "abd"; }`,
		`function f(v: str, s: string): boolean { return v < s; }`,
		`function f(v: str, s: string): boolean { return s < v; }`,
		`function f(a: str, b: str): boolean { return a >= b; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error: %v", src, err)
		}
	}
}

func TestStringOrderingMixedOperandsRejected(t *testing.T) {
	for _, src := range []string{
		`function f(): boolean { return "abc" < 1; }`,
		`function f(): boolean { return 1 < "abc"; }`,
		`function f(): boolean { return "abc" < 1.5; }`,
		`function f(): boolean { return "abc" < true; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected an error, got nil", src)
			continue
		}
		if !strings.Contains(err.Error(), `operator "<" requires`) {
			t.Errorf("%q: error %q is not the operand diagnostic", src, err.Error())
		}
	}
}

func TestStringNonOrderingOperatorsRejected(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { return "a" - "b"; }`,
		`function f(): i32 { return "a" * "b"; }`,
		`function f(): i32 { return "a" & "b"; }`,
		`function f(): i32 { return "a" << "b"; }`,
	} {
		if err := checkSource(t, src); err == nil {
			t.Errorf("%q: expected an error, got nil", src)
		}
	}
}
