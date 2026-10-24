package printer

import (
	"testing"
)

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
	let p: Point = Point { x: 3, y: 4 };
	return p.x + p.y;
}`,

	"expression_precedence": `
function main(): i32 {
	let a: i32 = 1 + 2 * 3 - 4 / 5;
	let b: bool = a > 0 && a < 100 || a == 42;
	let c: i32 = (a & 0xFF) | (a >> 4);
	return c;
}`,

	"fstring": `
function main(): i32 {
	let name: string = "world";
	print(f"hello, {name}!");
	return 0;
}`,

	"comments_preserved": `
// top-level doc
function main(): i32 {
	// inside-fn comment
	let x: i32 = 0; // trailing
	return x;
}`,

	"unsigned_large_literals": `
function main(): i32 {
	let a: u64 = 9223372036854775808 as u64;
	let b: u64 = 18446744065119617025 as u64;
	let c: u64 = 18446744073709551615 as u64;
	return 0;
}`,
}

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
