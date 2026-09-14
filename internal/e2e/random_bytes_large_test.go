package e2e

import "testing"

// randomBytesLargeProbe asks for a MEGABYTE in one call, which is what makes
// it different from the 16-byte cases beside it: `getrandom(2)` returns how
// many bytes it wrote, and past one page it may write fewer and return early
// when a signal arrives — or -EINTR having written none. A helper that calls
// it once and ignores the result leaves the tail of the buffer as the
// allocator left it, which is zeros, and nothing reports it (#9221).
//
// The assertion is a RUN of zeros rather than "not all zeros": a short fill
// leaves a zero tail behind good bytes, so the whole buffer is never zero.
// Sixty-four zeros in a row is 2^-512 as an accident and unmissable as a
// truncation.
const randomBytesLargeProbe = `function main(): i32 {
    var bs: u8[] = random_bytes(1048576);
    if (bs.len() != 1048576) { return 1; }
    var run: i32 = 0;
    var worst: i32 = 0;
    var i: i32 = 0;
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

// TestRandomBytesLargeFill runs the probe on every backend that answers
// random_bytes. The Darwin arm of the arm64 helper has always looped —
// getentropy caps a call at 256 bytes — so this is the property the two
// register backends and the interpreter now share with it.
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
