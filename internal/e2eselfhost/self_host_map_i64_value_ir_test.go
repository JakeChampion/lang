package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapI64ValueIRX86_64 gates 64-bit Map VALUES (i64 / u64) on the
// self-host x86-64 IR path (#5253). The map stores its value column 8-byte
// (movq / stride 8) and __fern_map_get returns the value full-width, but the IR
// LOWERING previously handled the value as a single i32 word, so:
//
//  1. a wide i64/u64 value (`Map { 1: 5000000007 }`) lowered as op_const_i32 and
//     TRUNCATED to 32 bits before the 8-byte store (stayed on IR, silently wrong);
//  2. a CAST value (`x as u64`) forced the whole function to the AST fallback
//     (as_i64/as_u64 has no lower_expr arm), where a chained 64-bit unsigned op
//     then used a signed shift and diverged once bit 63 was set.
//
// The fix routes an i64/u64 map insert/set VALUE and get_or DEFAULT through
// lower_i64 (full-width, and lower_i64 handles the cast so the function stays on
// IR), marks a get_or on an i64/u64 map 64-bit-wide (infer_expr_width / lower_i64),
// and marks a u64-map get_or unsigned (expr_is_u64) so a chained `>>` selects
// shr_u. Every case asserts the modload -ir-probe verdict is "module: IR" AND the
// compiled binary matches the interpreter oracle. x86-64 only (the loader driver
// takes argv file paths, like the other modload tests); results are <= 126.
func TestSelfHostMapI64ValueIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name string
		main string
	}{
		// (1) wide i64 LITERAL value round-trips full-width. 5000000007 % 1000 == 7
		// (was 199: 5000000007 truncated to 705032711, %1000 == 711 → exit 199).
		{"i64-literal", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 }; var g: i64 = m.get_or(1, 0); return (g % 1000) as i32; }\n"},
		// (2) u64 CAST value + chained unsigned shift stays on IR (no AST fallback)
		// and shifts UNSIGNED. 18e18 >> 58 == 62 (was 254: AST path used sarq).
		{"u64-cast-shift", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }\n"},
		// (3) i64 value from a VARIABLE, inserted via .insert on a bound map.
		// 9000000000 % 1000 == 0.
		{"i64-var-insert", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = map_new(8); var v: i64 = 9000000000; m = m.insert(1, v); return (m.get_or(1, 0) % 1000) as i32; }\n"},
		// (4) u64 value inserted via .insert (cast), chained shift. Same 62.
		{"u64-insert-shift", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, u64] = map_new(8); m = m.insert(1, 18000000000000000000 as u64); return (m.get_or(1, 0 as u64) >> 58) as i32; }\n"},
		// (5) unannotated get_or binding width-tracks i64 (infer_expr_width), so a
		// later `% 1000` is 64-bit. 12000000005 % 1000 == 5.
		{"i64-unannotated-getor", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = Map { 2: 12000000005 }; var g = m.get_or(2, 0); return (g % 1000) as i32; }\n"},
		// (6) MISS path returns the (wide) default full-width. get_or on absent key
		// yields the default 7000000009; % 1000 == 9.
		{"i64-default-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = map_new(8); var g: i64 = m.get_or(99, 7000000009); return (g % 1000) as i32; }\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the whole program must reach the IR path — case (2)
			// specifically regressed here (cast value → AST) before the fix.
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("%s did not route module: IR\n%s", tc.name, probe)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
