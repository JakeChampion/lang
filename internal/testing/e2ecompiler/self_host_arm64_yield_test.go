package e2ecompiler

import "testing"

func TestSelfHostArm64FixedOperands(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	driver := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, driver, runner, []pinnedAsm{
		{"ret", 0xd65f03c0},
		{"ret x0", 0xd65f0000},
		{"ret fp", 0xd65f03a0},
		{"ret lr", 0xd65f03c0},
		{"ret xzr", 0xd65f03e0},
	})
	checkRefusedSelfHost(t, driver, runner, []string{
		"nop x0", "yield x0", "yield #1", "wfe x0", "wfi x0",
		"eret x0", "paciasp x0", "autiasp x0", "xpaclri x0",
		"ret sp", "ret w0", "ret d0", "ret x0, x1",
	})
}
