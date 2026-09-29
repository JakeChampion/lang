package e2eselfhost

import "testing"

// TestSelfHostSemScalarContracts compiles the unsigned-operator and __c_call
// programs under FERN_SEM_IR_STRICT, so each is the typed lowering's own
// output, and pins the answers: the interpreter's on the 32-bit-safe program,
// and the 64-bit answers the register backends owe past 32 bits.
func TestSelfHostSemScalarContracts(t *testing.T) {
	c := newStrictCLI(t)
	arm64gcc, qemu := arm64Tooling(t)
	issue := `function main(): i32 {
    var a: usize = 70000000000 as usize;
    var b: usize = 1000000000 as usize;
    return (a / b) as i32;
}
`
	native := []struct {
		name string
		src  string
		want int
	}{
		{"usize-operators", semUsizeOperatorsSource, 31},
		{"usize-operators-at-register-width", semUsizeWideOperatorsSource, 127},
		{"usize-divide-literal", issue, 70},
		{"c-call-trampolines", semCCallSource, 0},
	}
	for _, tc := range native {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := c.runX86(t, c.emit(t, "x86-64-linux", tc.src)); got != tc.want {
				t.Errorf("x86-64: exit %d, want %d", got, tc.want)
			}
			if got, _ := runArm64(t, arm64gcc, qemu, c.emit(t, "arm64-linux", tc.src)); got != tc.want {
				t.Errorf("arm64: exit %d, want %d", got, tc.want)
			}
		})
	}
	t.Run("usize-operators/wasm", func(t *testing.T) {
		if got, _ := runWasm(t, c.emit(t, "wasm32-wasi", semUsizeOperatorsSource)); got != 31 {
			t.Errorf("wasm: exit %d, want 31", got)
		}
	})
}
