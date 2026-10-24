package e2ecompiler

import "testing"

func TestSelfHostForInStringIR(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"literal-byte-sum", `function main(): i32 { let s: i32 = 0; for b in "AB" { s = s + (b as i32); } return s; }`, 131},
		{"literal-count", `function main(): i32 { let n: i32 = 0; for c in "hello" { n = n + 1; } return n; }`, 5},
		{"local", `function main(): i32 { let t: string = "AB"; let s: i32 = 0; for b in t { s = s + (b as i32); } return s; }`, 131},
		{"continue", `function main(): i32 { let s: i32 = 0; for b in "ABC" { if (b == 66) { continue; } s = s + (b as i32); } return s; }`, 132},
		{"break", `function main(): i32 { let s: i32 = 0; for b in "ABC" { if (b == 66) { break; } s = s + (b as i32); } return s; }`, 65},
		{"empty", `function main(): i32 { let s: i32 = 7; for b in "" { s = s + (b as i32); } return s; }`, 7},
		{"call-returning-string", `function greet(): string { return "AB"; } function main(): i32 { let s: i32 = 0; for b in greet() { s = s + (b as i32); } return s; }`, 131},
		{"method-returning-string", `struct B { tag: i32 } function (b: B) name(): string { return "AB"; } function main(): i32 { let x: B = B { tag: 1 }; let s: i32 = 0; for c in x.name() { s = s + (c as i32); } return s; }`, 131},
		{"slice", `function main(): i32 { let s: i32 = 0; for b in slice_unchecked("ABCD", 1, 3) { s = s + (b as i32); } return s; }`, 133},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
