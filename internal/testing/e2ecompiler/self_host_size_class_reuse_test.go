package e2ecompiler

import "testing"

const sizeClassReuseProgram = `@noinline function fill(n: i32): i32 {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs.len();
}
@noinline function sweep(): i32 {
    let t: i32 = 0;
    let n: i32 = 200;
    while (n < 1000) { t = t + fill(n); n = n + 37; }
    return t;
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    let t: i32 = sweep();
    let before: i64 = __heap_bump_bytes();
    t = t + sweep();
    print_int(t); print("");
    print_int((__heap_bump_bytes() - before) as i32); print("");
    return 0;
}
`

func TestSelfHostSizeClassReuse(t *testing.T) {
	want := "25894\n0\n"
	runSemanticProgram(t, "sizeclass", sizeClassReuseProgram, []string{"fill", "sweep"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": "25894\n", "wasm32-wasi": want})
}
