package e2eselfhost

import "testing"

// TestSelfHostForInStringIR covers `for b in STR` (iterate a string's bytes)
// through the self-hosted x86-64 compiler. The self-host
// foreach assumed an array layout (length @0, elements @ base+idx*8+8), but a
// string is { data_ptr @0, len @8 } with byte elements, so it read the data
// pointer as the length and 8-byte-indexed the header (#2822 — the reproducer
// `for b in "AB"` returned 2 instead of 131). irlower now desugars a string
// foreach to a counted loop over STR.len() with a byte-index bind, reusing the
// range-for + string-index paths.
//
// Covered: string literals, string locals, string-returning calls and methods,
// slices, byte sum / count, and break / continue.
func TestSelfHostForInStringIR(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"literal-byte-sum", `function main(): i32 { var s: i32 = 0; for b in "AB" { s = s + (b as i32); } return s; }`, 131}, // 'A'+'B' = 65+66
		{"literal-count", `function main(): i32 { var n: i32 = 0; for c in "hello" { n = n + 1; } return n; }`, 5},
		{"local", `function main(): i32 { var t: string = "AB"; var s: i32 = 0; for b in t { s = s + (b as i32); } return s; }`, 131},
		{"continue", `function main(): i32 { var s: i32 = 0; for b in "ABC" { if (b == 66) { continue; } s = s + (b as i32); } return s; }`, 132}, // skip 'B'
		{"break", `function main(): i32 { var s: i32 = 0; for b in "ABC" { if (b == 66) { break; } s = s + (b as i32); } return s; }`, 65},
		{"empty", `function main(): i32 { var s: i32 = 7; for b in "" { s = s + (b as i32); } return s; }`, 7},
		// A string-returning CALL / METHOD / SLICE as the iterable (#2822
		// follow-up): these route through the byte-foreach path now that the
		// eligibility probe threads str_ret_fns (so the iterable types as a
		// string instead of falling to the array-snapshot path and bailing).
		{"call-returning-string", `function greet(): string { return "AB"; } function main(): i32 { var s: i32 = 0; for b in greet() { s = s + (b as i32); } return s; }`, 131},
		{"method-returning-string", `struct B { tag: i32 } function (b: B) name(): string { return "AB"; } function main(): i32 { var x: B = B { tag: 1 }; var s: i32 = 0; for c in x.name() { s = s + (c as i32); } return s; }`, 131},
		{"slice", `function main(): i32 { var s: i32 = 0; for b in slice_unchecked("ABCD", 1, 3) { s = s + (b as i32); } return s; }`, 133}, // 'B'+'C' = 66+67
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
