package arm64_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// The scaled immediate offset's ceiling.
//
// LDR/STR's unsigned imm12 holds the offset DIVIDED by the access size, so
// the largest it reaches is 4095*size. One step past that does not overflow
// out of the instruction — it wraps within the field:
//
//	ldr x1, [x2, #32768]     encoded as   f9400041   =   ldr x1, [x2]
//
// A valid instruction addressing offset zero, silently, where the source
// asked for 32768. Every plain load and store had this hole, and the fuzz
// lane could not find it: its generator draws displacements from a pool
// that stops well short of the ceiling, so the wrapping offsets were never
// generated.
//
// gas refuses all of these with "immediate offset out of range".
func TestScaledOffsetCeilingIsRefused(t *testing.T) {
	for _, src := range []string{
		// One scale unit past the ceiling, each access size, both directions.
		"ldrb w1, [x2, #4096]", "strb w1, [x2, #4096]",
		"ldrh w1, [x2, #8192]", "strh w1, [x2, #8192]",
		"ldr w1, [x2, #16384]", "str w1, [x2, #16384]",
		"ldr x1, [x2, #32768]", "str x1, [x2, #32768]",
		// And far past it, where the wrap lands on some other offset
		// entirely rather than zero.
		"ldr x1, [x2, #40960]", "ldrb w1, [x2, #5000]",
	} {
		got, _, err := arm64.AssembleProgram(src+"\n", 0x400000)
		if err == nil {
			t.Errorf("%q was accepted as % x — gas refuses it, and this encodes a different offset", src, got)
		}
	}
}

// TestScaledOffsetCeilingIsReachable is the other half: the refusal must
// not have eaten the last legal offset of each size.
func TestScaledOffsetCeilingIsReachable(t *testing.T) {
	for _, c := range []struct {
		src  string
		want uint32
	}{
		{"ldrb w1, [x2, #4095]", 0x397ffc41},
		{"ldrh w1, [x2, #8190]", 0x797ffc41},
		{"ldr w1, [x2, #16380]", 0xb97ffc41},
		{"ldr x1, [x2, #32760]", 0xf97ffc41},
	} {
		b, _, err := arm64.AssembleProgram(c.src+"\n", 0x400000)
		if err != nil {
			t.Errorf("%q REFUSED, but it is the last offset the scaled form reaches: %v", c.src, err)
			continue
		}
		got := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		if got != c.want {
			t.Errorf("%q assembles to %08x, gas emits %08x", c.src, got, c.want)
		}
	}
}
