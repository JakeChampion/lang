package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2649 — IR-path runtime helpers written in Fern.
//
// __fern_str_to_i32 is the first runtime helper hosted on the self-hosted IR
// path as a Fern function (asmcore.rt_src_str_to_i32, lowered through the IR
// pipeline by asm_ir.emit_ir_runtime_fern_fn) rather than the hand-written
// stack-arg wrapper that used to live in emit_ir_runtime. It links as the
// ordinary user-function symbol __fn___fern_str_to_i32, which the IR call site
// (op_call_direct("__fern_str_to_i32") → ir_helper_symbol) already targets.
//
// TestSelfHostAsmIRPath/str2i32-* already prove the IR-compiled helper computes
// correctly (incl. the roundtrip case, which feeds a freshly-allocated string
// in — exercising the borrowed-param path with no use-after-free). This test
// locks in the *migration*: the IR driver's emitted asm must define the
// Fern-compiled __fn___fern_str_to_i32 and must NOT contain the old hand-asm
// wrapper's local labels (.Lirs2i_*), so a silent revert to the wrapper fails.
func TestSelfHostRuntimeHelperStrToI32IsFernIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun_rt")

	cases := []struct {
		name string
		prog string
		sym  string   // Fern-compiled symbol the IR asm must define + call
		gone []string // hand-asm labels of the old IR body/wrapper that must be gone
	}{
		{
			"str_to_i32",
			`function main(): i32 { return str_to_i32("42"); }`,
			"__fn___fern_str_to_i32",
			[]string{".Lirs2i_"},
		},
		{
			"str_cmp",
			`function main(): i32 { if ("abc" < "abd") { return 1; } return 0; }`,
			"__fn___fern_str_cmp",
			[]string{"\n__fern_str_cmp:", ".Lstrcmp_loop"},
		},
		{
			"str_search",
			`function main(): i32 { if ("hello".starts_with("he")) { return 1; } return 0; }`,
			"__fn___fern_str_starts_with",
			[]string{"\n__fern_str_starts_with:", ".Lir_sw_loop", ".Lir_idx_outer"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.prog))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v", err)
			}
			got := string(out)
			if !strings.Contains(got, tc.sym+":") {
				t.Errorf("IR asm missing %s: definition — helper no longer compiled from Fern on the IR path?", tc.sym)
			}
			if !strings.Contains(got, "call "+tc.sym) {
				t.Errorf("IR asm missing call %s — call site not resolving to the Fern helper", tc.sym)
			}
			for _, bad := range tc.gone {
				if strings.Contains(got, bad) {
					t.Errorf("IR asm still contains hand-written form %q — IR migration regressed", bad)
				}
			}
		})
	}
}
