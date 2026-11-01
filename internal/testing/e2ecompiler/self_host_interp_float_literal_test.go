package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	copySelfHostDriver(t, dir, "drivers/interp_run.fern", "drivers/interp_float_cache_run.fern")

	var driverBin string
	var cacheBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
		cacheBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_float_cache_run.fern", "interp_float_cache_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
		cacheBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_float_cache_run.fern", "interp_float_cache_run")
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

	// Different text must not replace the bits that the parser cached.
	// A control interpreter that parses the text again returns zero here.
	for _, literal := range []string{"1.0", "1.6666666666666602e-01"} {
		t.Run("cached-bits/"+literal, func(t *testing.T) {
			if got := runDriverExit(t, runner, cacheBin, []byte("return "+literal+";\n")); got != 7 {
				t.Fatalf("cached-bit probe exited %d, want 7", got)
			}
		})
	}

	// Keep the repeated-evaluation workload without a wall-clock gate.
	loop := func(lit string) {
		src := fmt.Sprintf("function main(): i32 {\n  let s: f64 = 0.0;\n  let i = 0;\n  while (i < 20000) { s = s + %s; i = i + 1; }\n  if (s <= 0.0) { return 1; }\n  return 7;\n}\n", lit)
		if got := runDriverExit(t, runner, driverBin, []byte(src)); got != 7 {
			t.Fatalf("loop over %s exited %d, want 7", lit, got)
		}
	}
	loop("1.0")
	loop("1.6666666666666602e-01")

	t.Run("reparse-control", func(t *testing.T) {
		// Change only the temporary interpreter copy. The same probe must fail.
		path := filepath.Join(dir, "interp.fern")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		const cached = "v_float(f64_from_bits(n.bits))"
		if strings.Count(string(src), cached) != 1 {
			t.Fatal("expected one cached float evaluation in the interpreter")
		}
		control := strings.Replace(string(src), cached, "v_float(f64_from_bits(util.parse_f64_bits(n.text)))", 1)
		if err := os.WriteFile(path, []byte(control), 0o644); err != nil {
			t.Fatal(err)
		}
		var controlBin string
		if native {
			controlBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_float_cache_run.fern", "interp_float_reparse_control")
		} else {
			gcc, _ := x86_64Tooling(t)
			controlBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_float_cache_run.fern", "interp_float_reparse_control")
		}
		for _, literal := range []string{"1.0", "1.6666666666666602e-01"} {
			if got := runDriverExit(t, runner, controlBin, []byte("return "+literal+";\n")); got != 3 {
				t.Fatalf("reparse control for %s exited %d, want cached-bit mismatch 3", literal, got)
			}
		}
	})
}
