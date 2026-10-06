package e2ecompiler

import "testing"

// The large tier rounds a >= 512 KiB block up to native's 3-significant-bit
// capacity (#9077), so ten live 525,024-byte strings bump 640 KiB each, 6 MiB in
// all, where the earlier 512-KiB-multiple classes bumped 1 MiB each. The program
// exits with the MiB the ten concatenations bumped, after checking their total
// length.
const largeTierGeometricSrc = `function main(): i32 {
    let unit: string = "abcdefgh";
    let half: string = "";
    let k: i32 = 0;
    while (k < 32812) { half = half + unit; k = k + 1; }
    half = half + "abcd";
    let keep: string[] = [];
    k = 0;
    while (k < 10) { keep = keep.append(""); k = k + 1; }
    let b1: i64 = __heap_bump_bytes() as i64;
    k = 0;
    while (k < 10) { keep = keep.with(k, half + half); k = k + 1; }
    let b2: i64 = __heap_bump_bytes() as i64;
    let total: i32 = 0;
    for s in keep { total = total + s.len(); }
    if (total != 5250000) { return 97; }
    return ((b2 - b1) / 1048576) as i32;
}`

func TestSelfHostLargeTierGeometric(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, largeTierGeometricSrc, target); code != 6 {
				t.Errorf("bumped %d MiB for ten 525,024-byte strings, want 6 (10 is the linear 512-KiB classes)\n%s", code, stderr)
			}
		})
	}
}
