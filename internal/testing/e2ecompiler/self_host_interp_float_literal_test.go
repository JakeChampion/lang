package e2ecompiler

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// The parser stores a float literal's bits once. An integer literal that the
// parser or the checker settles to a float must get its bits at that point.
var interpFloatLiteralCases = []struct {
	name string
	src  string
}{
	{"decimal", "function main(): i32 {\n  let x: f64 = 0.1 + 0.2;\n  if (x == 0.3 || x < 0.3) { return 1; }\n  if (1.6666666666666602e-01 * 3.0 < 0.49) { return 2; }\n  return 7;\n}\n"},
	{"integer-settled-to-f64", "function half(x: f64): f64 { return x / 2.0; }\nfunction main(): i32 {\n  let x: f64 = 3;\n  if (x != 3.0 || half(5) != 2.5) { return 1; }\n  return 7;\n}\n"},
	{"integer-settled-to-f32", "function main(): i32 {\n  let x: f32 = 2;\n  let y: f32 = 0.1;\n  if (x + y < 2.09 || x + y > 2.11) { return 1; }\n  return 7;\n}\n"},
	{"negative-and-exponent", "function main(): i32 {\n  let x: f64 = -2.5e-3;\n  if (x * 1000.0 != -2.5) { return 1; }\n  return 7;\n}\n"},
}

func TestSelfHostInterpFloatLiteral(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
	}
	interpBin := buildLangBinForInterp(t)

	for _, tc := range interpFloatLiteralCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 7 {
				t.Fatalf("native interp oracle exited %d, want 7 — the case itself is wrong, not the self-host engine", want)
			}
			if got := runDriverExit(t, runner, driverBin, []byte(tc.src)); got != want {
				t.Errorf("self-host interp exited %d, want %d (native interp oracle)", got, want)
			}
		})
	}

	// A 17-digit literal takes the parser's exact slow path. Evaluated once
	// per iteration, it must cost about as much as an integer literal.
	loop := func(lit string) time.Duration {
		src := fmt.Sprintf("function main(): i32 {\n  let s: f64 = 0.0;\n  let i = 0;\n  while (i < 20000) { s = s + %s; i = i + 1; }\n  if (s <= 0.0) { return 1; }\n  return 7;\n}\n", lit)
		start := time.Now()
		if got := runDriverExit(t, runner, driverBin, []byte(src)); got != 7 {
			t.Fatalf("loop over %s exited %d, want 7", lit, got)
		}
		return time.Since(start)
	}
	short, long := loop("1.0"), loop("1.6666666666666602e-01")
	if long > 3*short {
		t.Errorf("20000 evaluations of a 17-digit literal took %v and of 1.0 took %v: the literal is parsed on each evaluation", long, short)
	}
}
