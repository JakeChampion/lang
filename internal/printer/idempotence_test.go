package printer

// Idempotence guard for the formatter.
//
// `fern -fmt` is the canonical-form tool: users run it on save, CI
// runs it in `-d` mode to flag drift, and the LSP exposes it as a
// "format document" action. The formatter MUST be idempotent —
// `format(format(x)) == format(x)` — or else every save introduces
// new diffs and the `-d` gate becomes useless.
//
// Idempotence is a strictly stronger property than determinism
// (which the parser determinism guard in PR #1719 already covers
// for the parse → print chain): a deterministic but non-idempotent
// formatter would re-indent / re-parenthesise / re-fold lines
// stably across runs but never reach a fixed point. This test
// pins the fixed-point property directly by formatting twice and
// asserting bytewise equality.
//
// Each case rides a different surface that has historically been a
// formatter bug source: indentation across nested blocks,
// minimal-parens emission, comment preservation, multi-line decl
// layouts (struct fields, fn params), and the various expression
// kinds (match arms, f-strings, struct literals).

import (
	"testing"
)

// idempotenceMatrix favours surfaces where a formatting decision
// could re-fire on the already-formatted input — re-indenting a
// nested block, re-parenthesising a binary that already has
// minimum-needed parens, breaking a single-line struct literal
// across lines on the second pass, etc.
var idempotenceMatrix = map[string]string{
	"minimal": `function main(): i32 { return 0; }`,

	"nested_if_while": `
function classify(n: i32): i32 {
	if (n > 0) {
		while (n > 1) { n = n - 1; }
		return 1;
	} else if (n < 0) {
		return 0 - 1;
	}
	return 0;
}`,

	"match_arms": `
enum Shape { Circle(i32), Rect(i32, i32) }
function area(s: Shape): i32 {
	match (s) {
		Circle(r) => { return r * r; },
		Rect(w, h) => { return w * h; }
	}
	return 0;
}`,

	"struct_and_literals": `
struct Point { x: i32, y: i32 }
function main(): i32 {
	var p: Point = Point { x: 3, y: 4 };
	return p.x + p.y;
}`,

	"expression_precedence": `
function main(): i32 {
	var a: i32 = 1 + 2 * 3 - 4 / 5;
	var b: bool = a > 0 && a < 100 || a == 42;
	var c: i32 = (a & 0xFF) | (a >> 4);
	return c;
}`,

	"fstring": `
function main(): i32 {
	var name: string = "world";
	print(f"hello, {name}!");
	return 0;
}`,

	"comments_preserved": `
// top-level doc
function main(): i32 {
	// inside-fn comment
	var x: i32 = 0; // trailing
	return x;
}`,

	// Unsigned literals whose magnitude exceeds i64::MAX are stored by
	// the parser as a negative int64 bit pattern (via ParseUint). The
	// formatter used to render them with `-%d` and `-x.Value`, which
	// overflowed for math.MinInt64 (== 2^63) and emitted a spurious `--`
	// that grew a leading `-` on every pass. 2^63 and (2^32-1)^2 both live
	// in that (i64::MAX, u64::MAX] window.
	"unsigned_large_literals": `
function main(): i32 {
	var a: u64 = 9223372036854775808 as u64;
	var b: u64 = 18446744065119617025 as u64;
	var c: u64 = 18446744073709551615 as u64;
	return 0;
}`,
}

// TestFormatIdempotent asserts every program in the matrix is a
// fixed point of Format: formatting twice yields the same output
// as formatting once. A failure means Format produces output that
// doesn't round-trip stably through itself — every save would add
// a new diff and the `fern -fmt -d` gate would emit false
// positives.
func TestFormatIdempotent(t *testing.T) {
	for name, src := range idempotenceMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			once := formatSrc(t, src)
			twice := formatSrc(t, once)
			if once != twice {
				t.Fatalf("format not idempotent:\n--- first pass (%d bytes) ---\n%s\n--- second pass (%d bytes) ---\n%s",
					len(once), once, len(twice), twice)
			}
		})
	}
}
