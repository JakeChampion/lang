package e2ecompiler

import "testing"

func TestSelfHostFormatBytesIR(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
	}{
		{"bytes", `import "std/format";
function main(): i32 {
    let s: string = format.format_bytes(512);
    if (s.len() != 5) { return 100; }
    if (s[0] != 53 || s[1] != 49 || s[2] != 50 || s[3] != 32 || s[4] != 66) { return 101; }
    return 42;
}`},
		{"kib", `import "std/format";
function main(): i32 {
    let s: string = format.format_bytes(2048);
    if (s.len() != 5) { return 100; }
    if (s[0] != 50 || s[2] != 75 || s[3] != 105 || s[4] != 66) { return 101; }
    return 42;
}`},
		{"neg-mib", `import "std/format";
function main(): i32 {
    let s: string = format.format_bytes(0 - 3145728);
    if (s[0] != 45 || s[1] != 51 || s[2] != 32 || s[3] != 77) { return 100; }
    return 42;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); got != 42 {
				t.Errorf("self-host format_bytes %q: check = %d, want 42", tc.name, got)
			}
		})
	}
}
