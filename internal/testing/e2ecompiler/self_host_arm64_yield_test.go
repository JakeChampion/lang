package e2ecompiler

import "testing"

func TestSelfHostArm64FixedOperands(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	driver := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, driver, runner, []pinnedAsm{
		{"svc #0", 0xd4000001},
		{"hvc #65535", 0xd41fffe2},
		{"smc 010", 0xd4000103},
		{"brk #0b101", 0xd42000a0},
		{"svc +4", 0xd4000081},
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
		"brk #65536", "brk #-1", "svc x0", "svc #0x", "smc #0, #2", "hvc",
		"svc #4294967296", "svc #09",
		"bti w0", "bti z", "bti c, j",
		"nop x0", "yield x0", "yield #1", "wfe x0", "wfi x0",
		"eret x0", "paciasp x0", "autiasp x0", "xpaclri x0",
		"ret sp", "ret w0", "ret d0", "ret x0, x1",
	})
}
