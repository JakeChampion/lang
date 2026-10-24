package checker

import (
	"strings"
	"testing"
)

func TestSaturatingOperatorTyping(t *testing.T) {
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("unexpected error for %q: %v", src, err)
		}
	}
	mustErr := func(src, want string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected an error for %q, got none", src)
			return
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("for %q: want error containing %q, got: %v", src, want, err)
		}
	}
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; return a +| b; }`)
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; return a -| b; }`)
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; return a *| b; }`)
	mustOK(`function main(): i32 { let a: u8 = 1; let b: u8 = 2; return (a +| b) as i32; }`)
	mustOK(`function main(): i32 { let a: i64 = 1; let b: i64 = 2; return (a *| b) as i32; }`)
	mustOK(`function main(): i32 { let a: u64 = 1; let b: u64 = 2; return (a -| b) as i32; }`)
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; return a <<| b; }`)
	mustOK(`function main(): i32 { let a: u8 = 1; let b: u8 = 2; return (a <<| b) as i32; }`)
	mustOK(`function main(): i32 { let a: i64 = 1; let b: i64 = 2; return (a <<| b) as i32; }`)

	mustErr(`function main(): i32 { let s: string = "a"; let t: string = "b"; let u: string = s +| t; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { let a: f64 = 1.0; let b: f64 = 2.0; let c: f64 = a *| b; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { let a: i32 = 1; let b: u32 = 2; return a +| b; }`,
		`share an integer type`)
	mustErr(`function main(): i32 { let a: usize = 1; let b: usize = 2; return (a +| b) as i32; }`,
		"not supported on `usize`")
	mustErr(`function main(): i32 { let a: f64 = 1.0; let b: f64 = 2.0; let c: f64 = a <<| b; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { let a: i32 = 1; let b: u32 = 2; return a <<| b; }`,
		`share an integer type`)
	mustErr(`function main(): i32 { let a: usize = 1; let b: usize = 2; return (a <<| b) as i32; }`,
		"not supported on `usize`")

}
