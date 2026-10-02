package e2eselfhost

import "testing"

// `s = s + piece` on a local the frame owns grows its buffer in place while
// the block's size class has room, so 20,000 eight-byte appends allocate only
// at class boundaries: about 290 times, where a fresh concatenation per append
// allocated 20,000 times and copied the whole prefix each time (#9077). The
// program exits 42 when the result is whole and the loop allocated fewer than
// 1,000 times.
const stringAppendInPlaceSrc = `function main(): i32 {
    let s: string = "";
    let a1: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 20000) { s = s + "abcdefgh"; i = i + 1; }
    let a2: i64 = __heap_alloc_count();
    if (s.len() != 160000) { return 1; }
    if (s[159999] as i32 != 104 || s[80000] as i32 != 97) { return 2; }
    if (a2 - a1 >= 1000) { return 3; }
    return 42;
}`

func TestSelfHostStringAppendInPlace(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, stringAppendInPlaceSrc, target); code != 42 {
				t.Errorf("exited %d, want 42 (3 is an allocation per append)\n%s", code, stderr)
			}
		})
	}
}
