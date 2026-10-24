package e2ecompiler

import (
	"fmt"
	"testing"
)

var arm64MemForms = []struct {
	load, store string
	reg         string
	size        int
}{
	{"ldrb", "strb", "w1", 1},
	{"ldrh", "strh", "w1", 2},
	{"ldr", "str", "w1", 4},
	{"ldr", "str", "x1", 8},
	{"ldrsb", "ldrsb", "w1", 1},
	{"ldrsh", "ldrsh", "w1", 2},
	{"ldrsb", "ldrsb", "x1", 1},
	{"ldrsw", "ldrsw", "x1", 4},
}

func arm64MemOffsets(size int) []int {
	offs := []int{0, 1, size, size + 1, 8, 255, 256, 257, -1, -8, -255, -256}
	offs = append(offs, 4095*size, 4094*size)
	if size > 1 {
		offs = append(offs, 4095*size-1)
	}
	return offs
}

func arm64MemEncodable(off, size int) bool {
	if off >= -256 && off <= 255 {
		return true
	}
	return off >= 0 && off%size == 0 && off/size <= 4095
}

func arm64MemCases() []string {
	var out []string
	for _, f := range arm64MemForms {
		for _, base := range []string{"x2", "x28", "sp"} {
			for _, off := range arm64MemOffsets(f.size) {
				if !arm64MemEncodable(off, f.size) {
					continue
				}
				out = append(out,
					fmt.Sprintf("%s %s, [%s, #%d]", f.load, f.reg, base, off),
					fmt.Sprintf("%s %s, [%s, #%d]", f.store, f.reg, base, off),
				)
			}
		}
	}
	return out
}

func TestSelfHostArm64MemOffsetsMatchGas(t *testing.T) {
	cases := arm64MemCases()
	if len(cases) < 200 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of forms, bases and offsets", len(cases))
	}
	compareArm64Cases(t, cases)
}

func TestSelfHostArm64RefusesUnencodableOffsets(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkRefusedSelfHost(t, bin, runner, []string{
		"ldrb w1, [x2, #4096]",
		"strb w1, [x2, #4096]",
		"ldrh w1, [x2, #8192]",
		"ldr w1, [x2, #16384]",
		"ldr x1, [x2, #32768]",
		"ldrsw x1, [x2, #16384]",
		"ldrb w1, [x2, #-257]",
		"ldr x1, [x2, #-257]",
		"ldrh w1, [x2, #8191]",
		"ldr x1, [x2, #1004]",
	})
}

func TestSelfHostArm64AcceptsTheLastLegalOffset(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"ldrb w1, [x2, #4095]", 0x397ffc41},
		{"ldrh w1, [x2, #8190]", 0x797ffc41},
		{"ldr w1, [x2, #16380]", 0xb97ffc41},
		{"ldr x1, [x2, #32760]", 0xf97ffc41},
		{"ldrb w1, [x2, #-256]", 0x38500041},
		{"ldrh w1, [x2, #255]", 0x784ff041},
	})
}
