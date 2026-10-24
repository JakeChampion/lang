package e2e

import "testing"

const randomBytesLargeProbe = `function main(): i32 {
    let bs: u8[] = random_bytes(1048576);
    if (bs.len() != 1048576) { return 1; }
    let run: i32 = 0;
    let worst: i32 = 0;
    let i: i32 = 0;
    while (i < bs.len()) {
        if (bs[i] == 0 as u8) { run = run + 1; } else { run = 0; }
        if (run > worst) { worst = run; }
        i = i + 1;
    }
    if (worst >= 64) { return 2; }
    // The edges of the contract, in the same program: nothing asked for is
    // the shared empty, and one byte is one byte.
    if (random_bytes(0).len() != 0) { return 3; }
    if (random_bytes(1).len() != 1) { return 4; }
    return 0;
}
`

func TestRandomBytesLargeFill(t *testing.T) {
	t.Run("interp", func(t *testing.T) {
		if code := interpExit(t, buildLangBinForInterp(t), randomBytesLargeProbe); code != 0 {
			t.Errorf("interp exit %d, want 0 (1=length, 2=a 64-byte zero run, 3/4=the edges)", code)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, randomBytesLargeProbe); code != 0 {
			t.Errorf("x86-64 exit %d, want 0", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, randomBytesLargeProbe); code != 0 {
			t.Errorf("arm64 exit %d, want 0", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, randomBytesLargeProbe); code != 0 {
			t.Errorf("wasm exit %d, want 0", code)
		}
	})
}
