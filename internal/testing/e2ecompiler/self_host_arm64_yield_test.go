package e2ecompiler

import "testing"

func TestSelfHostArm64FixedOperands(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	driver := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, driver, runner, []pinnedAsm{
		{"bti", 0xd503241f},
		{"bti c", 0xd503245f},
		{"bti j", 0xd503249f},
		{"bti jc", 0xd50324df},
		{"bti Jc", 0xd50324df},
		{"ret", 0xd65f03c0},
		{"ret x0", 0xd65f0000},
		{"ret fp", 0xd65f03a0},
		{"ret lr", 0xd65f03c0},
		{"ret xzr", 0xd65f03e0},
	})
	checkRefusedSelfHost(t, driver, runner, []string{
		"bti w0", "bti z", "bti c, j",
		"nop x0", "yield x0", "yield #1", "wfe x0", "wfi x0",
		"eret x0", "paciasp x0", "autiasp x0", "xpaclri x0",
		"ret sp", "ret w0", "ret d0", "ret x0, x1",
	})
}
