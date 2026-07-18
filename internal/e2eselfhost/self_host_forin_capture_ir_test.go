package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostForinCaptureIRX86_64 pins the for-in binder capture resolution
// (cap_type_in_stmts' StmtFor arm): a lambda capturing a for-in loop binder
// used to resolve cap_type "" (the binder has no `var` declaration), so the
// closure lift declined and the whole module fell to the AST path — where a
// capturing lambda in a struct fn FIELD miscompiles (silent wrong values:
// the struct-field shape returned 3 for native's 15, the nested two-binder
// shape 27 for 81, the map-keys shape 1 for 16). The binder now resolves
// from the iterated expression — an ident iterating a declared T[] binds a
// T, `m.keys()` / `m.values()` bind the receiver Map's key / value type —
// so these shapes lower via the IR path (asserted via the .Lir_ label
// witness) and compute the native values.
func TestSelfHostForinCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
		// irWitness: the emitted asm must carry IR-path labels (the shape
		// must not fall back to the AST emitter, where the struct-fn-field
		// capture miscompiles). "" skips the check.
		irWitness string
	}{
		{"forin-binder-fn-field",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var acc: i32 = 0; var xs: i32[] = [1, 2, 3]; for x in xs { var h: H = H { f: function (q: i32): i32 { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; }`,
			15, ".Lir_main"},
		{"forin-nested-two-binders",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var acc: i32 = 0; var xs: i32[] = [1, 2, 3]; for x in xs { for y in xs { var h: H = H { f: function (q: i32): i32 { return q + x + y; }, id: x * y }; acc = acc + h.f(1) + h.id; } } return acc; }`,
			81, ".Lir_main"},
		{"forin-map-keys-binder",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var acc: i32 = 0; for k in m.keys() { var h: H = H { f: function (x: i32): i32 { return x + k; }, id: k }; acc = acc + h.f(5) + h.id; } return acc; }`,
			16, ".Lir_main"},
		{"forin-map-values-binder",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var acc: i32 = 0; for v in m.values() { var h: H = H { f: function (x: i32): i32 { return x + v; }, id: v }; acc = acc + h.f(1) + h.id; } return acc; }`,
			62, ".Lir_main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if tc.irWitness != "" && !strings.Contains(string(asm), tc.irWitness) {
				t.Fatalf("%s: emitted asm missing %q — the for-in capture shape fell back to the AST path", tc.name, tc.irWitness)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
