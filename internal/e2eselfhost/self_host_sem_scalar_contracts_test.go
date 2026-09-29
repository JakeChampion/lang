package e2eselfhost

import "testing"

// TestSelfHostSemScalarContracts compiles the unsigned-operator and __c_call
// programs under FERN_SEM_IR_STRICT, so each is the typed lowering's own
// output, and pins the answers. On the register backends a usize is 64 bits,
// as it is in the interpreter, so every program the interpreter can run must
// answer what it does there; wasm's usize is 32 bits.
func TestSelfHostSemScalarContracts(t *testing.T) {
	c := newStrictCLI(t)
	arm64gcc, qemu := arm64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	native := []struct {
		name   string
		src    string
		want   int
		interp bool
	}{
		{"usize-operators", semUsizeOperatorsSource, 31, true},
		{"usize-operators-at-register-width", semUsizeWideOperatorsSource, 127, true},
		{"usize-wide-literal", semUsizeWideLiteralSource, 70, true},
		// The interpreter has no C ABI to call through.
		{"c-call-trampolines", semCCallSource, 0, false},
	}
	for _, tc := range native {
		t.Run(tc.name, func(t *testing.T) {
			if tc.interp {
				if got := interpExitStdin(t, interpBin, tc.src, ""); got != tc.want {
					t.Errorf("interpreter: exit %d, want %d", got, tc.want)
				}
			}
			if got, _ := c.runX86(t, c.emit(t, "x86-64-linux", tc.src)); got != tc.want {
				t.Errorf("x86-64: exit %d, want %d", got, tc.want)
			}
			if got, _ := runArm64(t, arm64gcc, qemu, c.emit(t, "arm64-linux", tc.src)); got != tc.want {
				t.Errorf("arm64: exit %d, want %d", got, tc.want)
			}
		})
	}
	wasm := []struct {
		name string
		src  string
		want int
	}{
		{"usize-operators", semUsizeOperatorsSource, 31},
		// 70000000000 wraps to the 32-bit address 1280523264 (#10743).
		{"usize-wide-literal", semUsizeWideLiteralSource, 1},
	}
	for _, tc := range wasm {
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			if got, _ := runWasm(t, c.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("typed lowering: exit %d, want %d", got, tc.want)
			}
			if got, _ := runWasm(t, c.emit(t, "wasm32-wasi", tc.src, "FERN_SEM_IR=")); got != tc.want {
				t.Errorf("AST lowering: exit %d, want %d", got, tc.want)
			}
		})
	}
}
