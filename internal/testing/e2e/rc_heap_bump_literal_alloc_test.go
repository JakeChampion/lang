package e2e

import (
	"testing"
)

func literalAllocBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let b: u8[] = __alloc_u8(8);
        b = b.with(0, (i % 200) as u8);
        b = b.with(1, ((i + 1) % 200) as u8);
        acc = acc + (b[0] as i32) + (b[1] as i32);
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64LiteralAllocReclaim(t *testing.T) {
	if s, l := mustRunX86_64FreeOn(t, literalAllocBumpSrc("5000")), mustRunX86_64FreeOn(t, literalAllocBumpSrc("50000")); s != l {
		t.Errorf("literal-sized temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", s, l)
	}
}

func TestArm64LiteralAllocReclaim(t *testing.T) {
	if s, l := mustRunArm64FreeOn(t, literalAllocBumpSrc("5000")), mustRunArm64FreeOn(t, literalAllocBumpSrc("50000")); s != l {
		t.Errorf("literal-sized temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", s, l)
	}
}

func TestWASMLiteralAllocReclaim(t *testing.T) {
	s, l := runWasm(t, literalAllocBumpSrc("5000")), runWasm(t, literalAllocBumpSrc("50000"))
	if s != l {
		t.Errorf("literal-sized temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", s, l)
	}
	if s == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
}

const toRgbHexOverRelease = `import "std/i32";
function main(): i32 {
    if ((0).to_rgb_hex() != "#000000") { return 1; }
    if ((255).to_rgb_hex() != "#0000ff") { return 2; }
    if ((65280).to_rgb_hex() != "#00ff00") { return 3; }
    if ((16711680).to_rgb_hex() != "#ff0000") { return 4; }
    if ((16777215).to_rgb_hex() != "#ffffff") { return 5; }
    return 0;
}`

func TestToRgbHexNoOverRelease(t *testing.T) {
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, toRgbHexOverRelease); code != 0 {
			t.Errorf("x86_64: to_rgb_hex over-release/value error, code=%d", code)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, toRgbHexOverRelease); code != 0 {
			t.Errorf("arm64: to_rgb_hex over-release/value error, code=%d", code)
		}
	})
}
