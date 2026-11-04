package e2ecompiler

import "testing"

// A program maps a private region, adopts it as its heap, and checks that
// the cursor and every later box lie inside the region. 97: the cursor did
// not move into the region. 98: an allocation after the adoption did not
// move the cursor. 96: a box allocated before the adoption was corrupted.
// 95: a box allocated after the adoption lies outside the region.
const heapAdoptProg = `function main(): i32 {
    unsafe {
        let keep: i32[] = [7, 8, 9];
        let size: i64 = 1048576i64;
        let nr: i32 = 9;
        if (target_arch() == "arm64") {
            nr = 222;
        }
        let region: i64 = __syscall6(nr, 0i64, size, 3i64, 34i64, 0i64 - 1i64, 0i64);
        if (region <= 0i64) { return 99; }
        __heap_adopt(region, region + size);
        let at: i64 = __heap_mark();
        if (at < region || at >= region + size) { return 97; }
        let fresh: i32[] = [keep[0] - 6, 2, 3];
        let after: i64 = __heap_mark();
        if (after <= at) { return 98; }
        if (after > region + size) { return 95; }
        if (keep[0] != 7 || keep[2] != 9) { return 96; }
        if (fresh[0] != 1) { return 94; }
        return 0;
    }
}
`

func TestSelfHostHeapAdoptIR(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, heapAdoptProg, target); code != 0 {
				t.Errorf("exited %d, want 0 (97 = cursor outside the region; 98 = no allocation after adopt; 96 = an earlier box corrupted; 95 = a later box outside the region)\n%s", code, stderr)
			}
		})
	}
}
