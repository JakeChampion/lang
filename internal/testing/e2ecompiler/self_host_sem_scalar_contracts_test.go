package e2ecompiler

import "testing"

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
		{"usize-operators", semUsizeOperatorsSource, 63, true},
		{"usize-operators-at-register-width", semUsizeWideOperatorsSource, 255, true},
		{"usize-wide-literal", semUsizeWideLiteralSource, 70, true},
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
		{"usize-operators", semUsizeOperatorsSource, 63},
		{"usize-wide-literal", semUsizeWideLiteralSource, 1},
		{"usize-wide-product", semUsizeWideProductSource, 112},
	}
	for _, tc := range wasm {
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			if got, _ := runWasm(t, c.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
	}
}
