package e2e

import (
	"strings"
	"testing"
)

func TestWASMAbortExportBridge(t *testing.T) {
	for _, param := range []string{"i32[]", "string"} {
		t.Run(param, func(t *testing.T) {
			src := `@export("local:test/bridge@0.1.0", "accept")
function accept(value: ` + param + `): i32 { return 0; }
function main(): i32 { return 0; }`
			artifact := buildCLIComponent(t, src)
			out, errout, code := runWasmArtifact(t, artifact, runOpts{})
			if code != 0 {
				t.Fatalf("export bridge: exit=%d stdout=%q stderr=%q", code, out, errout)
			}
		})
	}
}

func TestWASMMatrixAllocationOverflowCause(t *testing.T) {
	cases := []struct{ name, expr string }{
		{"inner", "__inner_mul_add_f64(a, a, 50000, 0, 50000, 0.0)"},
		{"outer", "__outer_mul_f64(filled(), filled())"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `@noinline function filled(): f64[] {
 let values: f64[] = [];
 let i: i32 = 0;
 while (i < 50000) { values = values.append(0.0); i = i + 1; }
 return values;
}
function main(): i32 { let a: f64[] = []; let result: f64[] = ` + tc.expr + `; return result.len(); }`
			artifact := buildCLIComponent(t, src)
			_, errout, code := runWasmArtifact(t, artifact, runOpts{})
			if code == 0 || !strings.Contains(errout, "fern: allocation size out of range") {
				t.Fatalf("allocation overflow: exit=%d stderr=%q", code, errout)
			}
		})
	}
}
