package e2eselfhost

import "testing"

// TestSelfHostSemScalarContracts compiles programs under FERN_SEM_IR_STRICT,
// so each is the typed lowering's own output, and pins their answers.
func TestSelfHostSemScalarContracts(t *testing.T) {
	c := newStrictCLI(t)
	arm64gcc, qemu := arm64Tooling(t)
	native := []struct {
		name string
		src  string
		want int
	}{
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
}
