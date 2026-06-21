package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// Predicate-based iterator adapters — `any` / `all` / `find` — that take a
// `(T) => boolean` closure through a function-typed parameter. These work on
// every NATIVE backend (interp / x86-64 / wasm / arm64). They are NOT yet in
// the shipped `core/iter` module because they trip a precisely-characterised
// self-host IR bug:
//
//	a closure whose RETURN type is `boolean`, called INDIRECTLY through a
//	function-typed parameter, miscompiles on the self-host IR path — it routes
//	`ir` and emits, then crashes at runtime (exit -1).
//
// The minimal repro is `function apply(x: i32, f: (i32) => boolean): boolean {
// return f(x); }`. A type sweep over the closure's return type pins it as
// boolean-specific: `(i32) => i32`, `(i32) => f64`, and `(i32) => string`
// indirect returns all lower and run correctly on the self-host IR path, and
// `(i32) => i64` routes to the AST emitter (correct via fallback); only
// `boolean` stays on the IR path and crashes. This is the same root cause as
// the `fold` A≠T crash recorded for #3618 — there the closure was `(boolean,
// i32) => boolean`, i.e. a boolean-return indirect call — so "A≠T" was the
// symptom, not the cause. Fixing the boolean indirect-return codegen on the
// self-host IR path (then moving these adapters into core/iter) is a focused
// follow-up.
//
// These tests are the behavioural spec the fix must satisfy; they guard the
// native semantics in the meantime.
var predicateAdapterPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function any[T, I: Iterator[T]](it: I, pred: (T) => boolean): boolean { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return true; } cur = t.1; }, None => { go = false; }, } } return false; }
function all[T, I: Iterator[T]](it: I, pred: (T) => boolean): boolean { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (!pred(t.0)) { return false; } cur = t.1; }, None => { go = false; }, } } return true; }
function find[T, I: Iterator[T]](it: I, pred: (T) => boolean): Option[T] { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return Some(t.0); } cur = t.1; }, None => { go = false; }, } } return None; }
`

var predicateAdapterCases = []struct {
	name string
	main string
	want int
}{
	// any: 0..5 contains 3 → true → 5.
	{"any-hit", `function main(): i32 { if (any(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x == 3; })) { return 5; } return 0; }`, 5},
	// any: 0..5 has no value > 9 → false → 9.
	{"any-miss", `function main(): i32 { if (any(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x > 9; })) { return 0; } return 9; }`, 9},
	// all: every value in 0..5 is < 10 → true → 6.
	{"all-true", `function main(): i32 { if (all(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x < 10; })) { return 6; } return 0; }`, 6},
	// all: not every value is even → false → 8.
	{"all-false", `function main(): i32 { if (all(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x % 2 == 0; })) { return 0; } return 8; }`, 8},
	// find: first even ≥ 2 in 0..9 → Some(2).
	{"find-some", `function main(): i32 { match (find(RangeIter { cur: 0, end: 9 }, function (x: i32): boolean { return x >= 2 && x % 2 == 0; })) { Some(v) => { return v; }, None => { return 99; } } }`, 2},
	// find: no match → None → 7.
	{"find-none", `function main(): i32 { match (find(RangeIter { cur: 0, end: 3 }, function (x: i32): boolean { return x > 100; })) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
}

// TestNativeGenericPredicateAdapters pins any/all/find on the native interp /
// x86-64 / wasm backends. See predicateAdapterPrelude for the self-host caveat.
func TestNativeGenericPredicateAdapters(t *testing.T) {
	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := predicateAdapterPrelude + tc.main + "\n"
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(prog), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, prog); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeGenericPredicateAdaptersArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeGenericPredicateAdaptersArm64(t *testing.T) {
	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := predicateAdapterPrelude + tc.main + "\n"
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(prog), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
