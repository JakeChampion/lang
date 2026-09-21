package e2eselfhost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A float truncated into a u8 saturates, it does not wrap.
//
// `-1.0 as u8` answered 255 on the self-host and 0 on native (#9912). Native
// picks the truncation's signedness from the DESTINATION — "Unsigned is chosen
// per the destination's signed-ness", `OpITruncF64` with `Unsigned:
// !dstInt.IsSigned()` — converts at 32 bits and then masks to the destination's
// width. The self-host picked it from the destination's WIDTH instead, so u8
// fell through to the signed conversion: the signed clamp out of a negative
// lands at INT32_MIN, and the mask reads that clamp's low byte. -1 → 255,
// -5 → 251, -300 → 212, each the low byte of the two's complement.
//
// u32 and u64 were already right (#4332 gave them the unsigned forms); u8 was
// the width that fix did not reach. u16, i8 and i16 are not castable from a
// float on either compiler (E064), so u8 is the whole of the gap.
//
// The program is SELF-CHECKING and answers a row number, because a value-per-
// exit-code probe cannot report 255 on wasm — WASI refuses anything at or above
// 126. Row 0 is agreement.
//
// Native runs the same rows here rather than the pins being taken on trust: if
// native's rule ever moves, its own leg fails and names the row, instead of the
// self-host being quietly held to a stale contract.
const floatToU8SaturateSrc = `
function u8_of(x: f64): i32 { return (x as u8) as i32; }
function u8_of32(x: f32): i32 { return (x as u8) as i32; }

function main(): i32 {
    var zero: f64 = 0.0;
    if (u8_of(zero - 1.0) != 0) { return 1; }
    if (u8_of(zero - 5.0) != 0) { return 2; }
    if (u8_of(zero - 300.0) != 0) { return 3; }
    if (u8_of(300.7) != 44) { return 4; }
    if (u8_of(255.9) != 255) { return 5; }
    if (u8_of(1.0e20) != 255) { return 6; }
    if (u8_of(zero - 1.0e20) != 0) { return 7; }
    if (u8_of(zero / zero) != 0) { return 8; }
    if (u8_of(0.0) != 0) { return 9; }
    if (u8_of(255.0) != 255) { return 10; }

    var z32: f32 = 0.0;
    if (u8_of32(z32 - 1.0) != 0) { return 11; }
    if (u8_of32(z32 - 300.0) != 0) { return 12; }
    if (u8_of32(300.7) != 44) { return 13; }
    if (u8_of32(255.9) != 255) { return 14; }

    if (((zero - 1.0) as u32) != 0) { return 15; }
    if (((zero - 1.0) as u64) != 0) { return 16; }
    if (((zero - 1.0) as i32) != (0 - 1)) { return 17; }
    return 0;
}
`

// floatToU8Rows names what each answer means, so a failure reads as the rule
// that broke rather than as a bare number.
var floatToU8Rows = map[int]string{
	1:  "-1.0 as u8 — the negative clamp, the shape #9912 reported (wrapping answers 255)",
	2:  "-5.0 as u8 (wrapping answers 251)",
	3:  "-300.0 as u8 (wrapping answers 212)",
	4:  "300.7 as u8 — above range, and the low byte of the saturated u32",
	5:  "255.9 as u8 — truncation toward zero at the top of the range",
	6:  "1e20 as u8 — the upper clamp, masked",
	7:  "-1e20 as u8 — the lower clamp",
	8:  "NaN as u8 — a saturating conversion answers 0",
	9:  "0.0 as u8",
	10: "255.0 as u8",
	11: "f32: -1.0 as u8 — the f32 source shares the conversion",
	12: "f32: -300.0 as u8",
	13: "f32: 300.7 as u8",
	14: "f32: 255.9 as u8",
	15: "-1.0 as u32 — already correct before #9912, must stay so",
	16: "-1.0 as u64 — already correct before #9912, must stay so",
	17: "-1.0 as i32 — the SIGNED destination still answers -1, not a clamp",
}

// floatToU8Explain turns semCompileRun's "<exit>|<stdout>" into the rule that
// broke. An answer it cannot read as a row is reported as-is rather than
// mapped to row 0, which would claim agreement.
func floatToU8Explain(t *testing.T, leg, got string) {
	t.Helper()
	row, err := strconv.Atoi(strings.TrimSuffix(got, "|"))
	if err != nil || row <= 0 {
		t.Fatalf("%s answered %q, want \"0|\"", leg, got)
	}
	why, named := floatToU8Rows[row]
	if !named {
		t.Fatalf("%s answered %q — row %d is not in floatToU8Rows; the program and the table have drifted apart", leg, got, row)
	}
	t.Fatalf("%s answered %q — row %d failed: %s", leg, got, row, why)
}

func TestSelfHostFloatToU8SaturatesLikeNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}

	// Native first: these are its rows, and it is the oracle the self-host is
	// being held to.
	t.Run("native", func(t *testing.T) {
		t.Run("x86-64-linux", func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, floatToU8SaturateSrc); code != 0 {
				t.Fatalf("native x86-64 answered %d — row %d failed: %s", code, code, floatToU8Rows[code])
			}
		})
		t.Run("arm64-linux", func(t *testing.T) {
			if _, code := compileAndRunArm64(t, floatToU8SaturateSrc); code != 0 {
				t.Fatalf("native arm64 answered %d — row %d failed: %s", code, code, floatToU8Rows[code])
			}
		})
		t.Run("wasm32-wasi", func(t *testing.T) {
			if code := compileAndRunWasmbinMain(t, floatToU8SaturateSrc); code != 0 {
				t.Fatalf("native wasm answered %d — row %d failed: %s", code, code, floatToU8Rows[code])
			}
		})
	})

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(floatToU8SaturateSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Both legs: the AST lowering (irlower) and the typed pipeline (ssarc)
	// pick the conversion separately, and both had the same hole.
	for _, leg := range []struct {
		name string
		sem  bool
	}{{"ast", false}, {"typed", true}} {
		t.Run(leg.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					got, _, _ := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, leg.sem, "", "")
					if got != "0|" {
						floatToU8Explain(t, leg.name+" "+target, got)
					}
				})
			}
		})
	}
}
