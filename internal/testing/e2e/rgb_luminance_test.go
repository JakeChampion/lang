package e2e

import "testing"

const rgbLuminanceProg = `
import "std/math" as math;
function main(): i32 {
    if (math.rgb_luminance(math.pack_rgb(255, 255, 255)) != 255) { return 1; }
    if (math.rgb_luminance(math.pack_rgb(0, 0, 0)) != 0) { return 2; }
    if (math.rgb_luminance(math.pack_rgb(0, 255, 0)) != 149) { return 3; }
    if (math.rgb_luminance(math.pack_rgb(0, 0, 255)) != 29) { return 4; }
    if (math.rgb_luminance(math.pack_rgb(255, 0, 0)) != 76) { return 5; }
    let green: i32 = math.rgb_luminance(math.pack_rgb(0, 255, 0));
    let red: i32 = math.rgb_luminance(math.pack_rgb(255, 0, 0));
    let blue: i32 = math.rgb_luminance(math.pack_rgb(0, 0, 255));
    if (!(green > red && red > blue)) { return 6; }
    if (!math.rgb_is_dark(math.pack_rgb(0, 0, 0))) { return 7; }
    if (!math.rgb_is_dark(math.pack_rgb(0, 0, 255))) { return 8; }
    if (math.rgb_is_dark(math.pack_rgb(255, 255, 255))) { return 9; }
    if (math.rgb_is_dark(math.pack_rgb(0, 255, 0))) { return 10; }
    if (math.rgb_luminance(math.pack_rgb(255, 136, 0)) != 156) { return 11; }
    if (math.rgb_is_dark(math.pack_rgb(255, 136, 0))) { return 12; }
    return 42;
}
`

func TestRgbLuminanceInterp(t *testing.T) {
	if got := runInterpExit(t, rgbLuminanceProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRgbLuminanceX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, rgbLuminanceProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRgbLuminanceWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, rgbLuminanceProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRgbLuminanceArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, rgbLuminanceProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
