package e2ecompiler

import "testing"

// A shift keeps the type of its value when its count has another integer
// type: a signed count, a wider count and a narrower count.
const shiftMixedCountProgram = `function rot(x: u32, n: i32): u32 {
    return x << n | x >> (32 - n);
}

function narrow(x: u8, n: u32): u8 {
    return x << n;
}

function wide(x: i64, n: u8): i64 {
    return x >> n;
}

function main(): i32 {
    if (rot(0x80000001u32, 4) != 0x18u32) {
        return 1;
    }
    if (narrow(0x81u8, 1u32) != 2u8) {
        return 2;
    }
    if (wide(-256i64, 4u8) != -16i64) {
        return 3;
    }
    let count = 3;
    let bits = 1u32 << count;
    if (bits != 8u32) {
        return 4;
    }
    return 0;
}`

func TestSelfHostShiftMixedCount(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, shiftMixedCountProgram, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
