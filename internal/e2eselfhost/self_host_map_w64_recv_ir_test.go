package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapW64RecvIRCases extend the #5253 slice-1 coverage (#5289,
// self_host_map_i64_value_ir_test.go — annotated map-typed LOCALS) to the
// receiver / inference shapes that were still miscompiled ON the IR path:
//
//   - a Map[K, u64] STRUCT FIELD receiver (`c.m.get_or(...) >> 58`) and an
//     UNANNOTATED map binding (`var m = Map { 1: big as u64 }`): the read-side
//     predicates resolved the receiver via expr_map_type_tag only (a local's
//     annotation or a map-returning call), so these shapes width-tracked 32 /
//     signed even though the lowering site stored the column full-width —
//     a silent wrong answer (113, want 62), NOT an AST fallback. Fixed by
//     get_or_recv_map_type (struct-field / tuple-element / array-element
//     receiver resolution) + the expr_map_type_tag 64-bit value-tag inference.
//
// Plus shapes distinct from the #5289 suite worth pinning: an unsigned
// COMPARE on the get_or result, a string-keyed fresh-key overwrite (the
// kconsume flag next to the valwide flag), a NEGATIVE i64 value (sign-extend,
// not zero-extend, through lower_i64's int_extend), and i32/string-valued
// regression guards. Routing-pinned to "ir", interp-oracle-checked.
var mapW64RecvIRCases = []struct {
	name string
	main string
}{
	// Struct-field receiver: 62 (was 113 — silent on-IR miscompile).
	{"structfield-u64-getor-shr", `import "core/map";
struct C { m: Map[i32, u64] }
function main(): i32 { var c: C = C { m: Map { 1: 18000000000000000000 as u64 } }; return (c.m.get_or(1, 0 as u64) >> 58) as i32; }`},
	// Unannotated map binding: 62 (was 113).
	{"unannot-u64-getor-shr", `import "core/map";
function main(): i32 { var m = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }`},
	// Unsigned compare on the get_or result: 7 (signed saw negative → 9).
	{"u64-getor-cmp", `import "core/map";
function main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; if (m.get_or(1, 0 as u64) > (100 as u64)) { return 7; } return 9; }`},
	// String-keyed u64 map with a computed (fresh) key overwrite — the
	// kconsume flag decode next to the valwide flag. 62.
	{"strkey-u64-overwrite", `import "core/map";
function main(): i32 { var m: Map[string, u64] = Map { "a": 1 as u64 }; m = m.insert("k" + "ey", 18000000000000000000 as u64); m = m.insert("k" + "ey", 18000000000000000000 as u64); return (m.get_or("key", 0 as u64) >> 58) as i32; }`},
	// NEGATIVE i64 value: lower_i64's int_extend sign-extends the i32 leaf
	// (a zero-extended store would read back 4294967293). 5.
	{"i64-negative-value", `import "core/map";
function main(): i32 { var m: Map[i32, i64] = Map { 1: 0 }; m = m.insert(1, 0 - 3); var g: i64 = m.get_or(1, 0); if (g == (0 - 3)) { return 5; } return 6; }`},
	// i32/i32 map regression: set/get_or/keys/values + the owncols flag. 21.
	{"i32-regress", `import "core/map";
function main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20 }; m = m.insert(3, 30); var ks = m.keys(); var vs = m.values(); return m.get_or(1, 0) + m.get_or(9, 5) + ks.len() + vs.len(); }`},
	// String-valued map regression (get_or on string values unchanged). 7.
	{"strval-regress", `import "core/map";
function main(): i32 { var m: Map[string, string] = Map { "a": "hello" }; return m.get_or("a", "x").len() + m.get_or("z", "yy").len(); }`},
	// f64-VALUED map get_or chained in a float op: expr_is_f64's get_or arm
	// (the f64 sibling of the u64 arm) — without it `* 2.0` lowered as an
	// integer op on the double's bits (255, want 5). #5253.
	{"f64-getor-mul", `import "core/map";
function main(): i32 { var m: Map[i32, f64] = Map { 1: 2.5 }; return (m.get_or(1, 0.0) * 2.0) as i32; }`},
	// Unannotated f64 map binding: the structural/binding vtag inference now
	// records "f64" so the read side float-tracks it. 5.
	{"f64-unannot-getor", `import "core/map";
function main(): i32 { var m = Map { 1: 2.5 }; return (m.get_or(1, 0.0) * 2.0) as i32; }`},
	// f64 map STRUCT FIELD receiver. 5.
	{"f64-structfield-getor", `import "core/map";
struct C { m: Map[i32, f64] }
function main(): i32 { var c: C = C { m: Map { 1: 2.5 } }; return (c.m.get_or(1, 0.0) * 2.0) as i32; }`},
	// f64 DEFAULT on the miss path. 6.
	{"f64-default-miss", `import "core/map";
function main(): i32 { var m: Map[i32, f64] = Map { 9: 1.0 }; return (m.get_or(1, 3.25) * 2.0) as i32; }`},
}

func TestSelfHostMapW64RecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")
	for _, tc := range mapW64RecvIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
