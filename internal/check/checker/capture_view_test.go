package checker

import (
	"strings"
	"testing"
)

// E082: a closure may not capture a value holding a borrowed view, whether
// the view is bare or reached through a record, tuple, enum payload, Option
// or array, and whether the closure is an arrow, a nested function or a `use`
// callback. A generic body instantiated at `str` is refused when
// monomorphisation re-checks the instance (gates).
func TestCaptureViewRejected(t *testing.T) {
	for name, src := range map[string]string{
		"str parameter":   `function f(sep: str): i32 { let g = (): i32 => sep.len(); return g(); }`,
		"local str view":  `function f(s: string): i32 { let v: str = slice_unchecked(s, 0, 1); let g = (): i32 => v.len(); return g(); }`,
		"byte view":       `function f(s: string): i32 { let b: [u8] = s.as_bytes(); let g = (): i32 => b.len(); return g(); }`,
		"array slice":     `function f(xs: i32[]): i32 { let w: [i32] = xs[0:2]; let g = (): i32 => w.len(); return g(); }`,
		"record field":    `struct P { a: str } function f(s: string): () => i32 { let p: P = P { a: slice_unchecked(s, 0, 1) }; return () => p.a.len(); }`,
		"tuple element":   `function f(s: string): i32 { let t: (str, i32) = (slice_unchecked(s, 0, 1), 1); let g = (): i32 => t.1; return g(); }`,
		"enum payload":    `enum Tok { Word(str), Num(i32) } function f(s: string): i32 { let t: Tok = Word(slice_unchecked(s, 0, 1)); let g = (): i32 => match (t) { Word(w) => w.len(), Num(n) => n }; return g(); }`,
		"option of view":  `function f(s: string): i32 { let o: Option[str] = Some(slice_unchecked(s, 0, 1)); let g = (): i32 => match (o) { Some(x) => x.len(), None => 0 }; return g(); }`,
		"nested function": `function f(s: string): i32 { let v: str = slice_unchecked(s, 0, 1); function inner(): i32 { return v.len(); } return inner(); }`,
		"use callback":    `function apply(x: i32, k: (i32) => i32): i32 { return k(x); } function f(s: str): i32 { use n <- apply(41); return n + s.len(); }`,
	} {
		err := checkSource(t, src)
		if err == nil || !hasCode(err, "E082") {
			t.Errorf("%s: want E082, got %v", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "which holds a borrowed view") {
			t.Errorf("%s: want the borrowed-view message, got %v", name, err)
		}
	}
}

// A closure over an owned copy, a view reached without a closure, and a
// lambda parameter that shadows a view all stay accepted.
func TestCaptureViewAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"owned copy":         `function f(sep: str): i32 { let owned: string = sep + ""; let g = (): i32 => owned.len(); return g(); }`,
		"owned array":        `function f(xs: i32[]): i32 { let g = (): i32 => xs.len(); return g(); }`,
		"if expression":      `function f(s: string, c: boolean): i32 { let v: str = slice_unchecked(s, 0, 1); let n: i32 = if (c) { v.len() } else { 0 }; return n; }`,
		"shadowing param":    `function f(s: string): i32 { let v: str = slice_unchecked(s, 0, 1); let g = (v: i32): i32 => v + 1; return g(v.len()); }`,
		"recursive viewless": `enum L { Cons(i32, L), Nil } function f(): i32 { let l: L = Cons(1, Nil); let g = (): i32 => match (l) { Cons(x, r) => x, Nil => 0 }; return g(); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}
