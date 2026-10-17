package e2ecompiler

import "testing"

func TestSelfHostArm64YieldOperandsRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	driver := buildAsmBenchDriver(t, gcc)
	for _, op := range []string{"yield x0", "yield #1"} {
		if refused := refusalsFor(t, driver, runner, ".text\n_start:\n"+op+"\n"); len(refused) == 0 {
			t.Errorf("accepted invalid operand in %q", op)
		}
	}
}
