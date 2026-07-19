package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapI64ValueIRCases exercise `import "core/map"` programs with a 64-bit VALUE
// column — `Map[K, i64]` / `Map[K, u64]`. The self-host IR map ops store/load an
// 8-byte value word (movq on x86), but the value was lowered as i32 and the get
// result was never width/sign-tracked, so a wide value truncated to i32 and a
// cast value (`x as i64` / `x as u64`) bailed the whole module to the legacy AST
// emitter — where a chained 64-bit UNSIGNED op then used a signed shift and
// diverged once bit 63 was set (#5253). The fix threads the value width + sign:
//   - the `.set()`/`.insert()` value and the `.get_or()` default lower via
//     lower_i64 for an i64/u64 column (a map-literal chain keys off the value
//     when its `map_new_i32(n)` base carries no V);
//   - `.get()` / `.get_or()` classify i64 (width) + u64 (unsigned) so the result
//     round-trips full-width and unsigned ops select shr_u / gt_u.
//
// Each case asserts the modload -ir-probe verdict is "module: IR" AND the
// compiled binary matches the interpreter oracle. x86-64 only (the loader driver
// takes argv file paths, like the other modload tests). Results are <= 126.
var mapI64ValueIRCases = []struct {
	name string
	main string
}{
	// Map-literal i64 value, wide bare literal: 5000000007 % 1000 = 7 (was
	// truncated to i32 on the IR path).
	{"lit-i64-wide", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 }; return (m.get_or(1, 0) % 1000) as i32; }\n"},
	// Map-literal i64 value, cast value (`as i64`): the cast used to bail the
	// module to AST. 5000000007 % 1000 = 7.
	{"lit-i64-cast", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 as i64 }; return (m.get_or(1, 0) % 1000) as i32; }\n"},
	// Map-literal u64 value with bit 63 set, chained UNSIGNED shift: the AST
	// fallback shifted arithmetically (sar) and diverged. 18e18 >> 58 = 62.
	{"lit-u64-shift", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }\n"},
	// insert() form, wide i64 value: 5000000007 % 1000 = 7.
	{"insert-i64-wide", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = map_new(8); m = m.insert(1, 5000000007); return (m.get_or(1, 0) % 1000) as i32; }\n"},
	// insert() form, u64 value chained in an unsigned shift: 18e18 >> 58 = 62.
	{"insert-u64-shift", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, u64] = map_new(8); m = m.insert(1, 18000000000000000000 as u64); return (m.get_or(1, 0 as u64) >> 58) as i32; }\n"},
	// String-keyed i64 value (the value-width path is independent of key kind):
	// 5000000007 % 1000 = 7.
	{"strkey-i64", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i64] = map_new(8); m = m.insert(\"a\", 5000000007); return (m.get_or(\"a\", 0) % 1000) as i32; }\n"},
	// Two wide i64 values summed full-width: (5000000007 + 9000000000) % 1000 = 7.
	{"i64-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i64] = map_new(8); m = m.insert(1, 5000000007); m = m.insert(2, 9000000000); return ((m.get_or(1, 0) + m.get_or(2, 0)) % 1000) as i32; }\n"},
	// u64 value compared with an UNSIGNED `>` (bit 63 set): 18e18 > 9e18 → 5.
	{"u64-cmp", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, u64] = map_new(8); m = m.insert(1, 18000000000000000000 as u64); if (m.get_or(1, 0 as u64) > 9000000000000000000 as u64) { return 5; } return 6; }\n"},
}

func TestSelfHostMapI64ValueIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range mapI64ValueIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the whole program must reach the IR path.
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("%s did not route module: IR\n%s", tc.name, probe)
			}
			// Compile + run, oracle-checked.
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
