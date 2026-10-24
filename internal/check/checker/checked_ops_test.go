package checker

import (
	"strings"
	"testing"
)

func TestCheckedOperatorTyping(t *testing.T) {
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
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; match (a +? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; match (a -? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function f(): Option[i32] { let a: i32 = 1; let b: i32 = 2; let c: i32 = (a *? b)?; return Some(c); }`)
	mustOK(`function main(): i32 { let a: u8 = 1; let b: u8 = 2; match (a +? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: i64 = 1; let b: i64 = 2; match (a *? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: u64 = 1; let b: u64 = 2; match (a -? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { match (40 +? 2) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: i32 = 84; let b: i32 = 2; match (a /? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: u32 = 85; let b: u32 = 43; match (a %? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 4; match (a <<? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { let a: u32 = 256; let b: u32 = 2; match (a >>? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)

	mustOK(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; let c: Option[i32] = a +? b; return match (c) { Some(v) => v, None => 0 }; }`)
	mustOK(`function f(): Option[i32] { let a: i32 = 1; let b: i32 = 2; return a +? b; }
function main(): i32 { return match (f()) { Some(v) => v, None => 0 }; }`)
	mustOK(`function g(o: Option[i32]): i32 { return match (o) { Some(v) => v, None => 0 }; }
function main(): i32 { let a: i32 = 1; let b: i32 = 2; return g(a +? b); }`)
	mustOK(`function main(): i32 { let a: i64 = 1; let b: i64 = 2; let c: Option[i64] = a *? b; return match (c) { Some(v) => v as i32, None => 0 }; }`)
	mustOK(`function f(): Option[u32] { let a: u32 = 1; let b: u32 = 2; return a -? b; }
function main(): i32 { return match (f()) { Some(v) => v as i32, None => 0 }; }`)
	mustOK(`function main(): i32 { let a: i32 = 8; let b: i32 = 2; let c: Option[i32] = a /? b; let d: Option[i32] = a %? b; let e: Option[i32] = a <<? b; let f: Option[i32] = a >>? b; return match (c) { Some(v) => v, None => 0 }; }`)

	mustErr(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; let c: Option[i64] = a +? b; return 0; }`,
		`Option`)
	mustErr(`function main(): i32 { let s: string = "a"; let t: string = "b"; let u: Option[string] = s +? t; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { let a: f64 = 1.0; let b: f64 = 2.0; let c: Option[f64] = a *? b; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { let a: i32 = 1; let b: u32 = 2; let c: Option[i32] = a +? b; return 0; }`,
		`share an integer type`)
	mustErr(`function main(): i32 { let a: usize = 1; let b: usize = 2; match (a +? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`,
		"not supported on `usize`")
	if err := checkSource(t, `function main(): i32 { let a: usize = 1; let b: usize = 2; match (a +? b) { Some(v) => { return 1; }, None => { return 0; } } }`); err == nil || strings.Contains(err.Error(), "non-enum") {
		t.Errorf("want the usize E009 alone, got: %v", err)
	}
	if err := checkSource(t, `function f(a: usize, n: usize): Option[usize] { return a <<? n; }`); err == nil || strings.Contains(err.Error(), "E002") || strings.Contains(err.Error(), "expected Option[usize]") {
		t.Errorf("want the usize E009 alone, got: %v", err)
	}
	mustErr(`function main(): i32 { let a: f64 = 1.0; let b: f64 = 2.0; let c: Option[f64] = a /? b; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { let a: usize = 1; let b: usize = 2; match (a %? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`,
		"not supported on `usize`")
}
