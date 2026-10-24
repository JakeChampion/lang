package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const unsignedNegationProg = `
function neg32(x: u32): u32 {
    return -x;
}

function main(): i32 {
    let b: u32 = 5;
    if (-b != 4294967291) { return 1; }
    if (neg32(1) != 4294967295) { return 2; }
    let z: u32 = 0;
    if (-z != 0) { return 3; }
    let c: u64 = 7;
    if (-c != 18446744073709551609) { return 4; }
    if (-c + c != 0) { return 5; }
    return 42;
}
`

const unsignedNegationU8UsizeProg = `
function neg8(x: u8): u8 {
    return -x;
}

function main(): i32 {
    let a: u8 = 1;
    if (-a != 255) { return 1; }
    if (neg8(200) != 56) { return 2; }
    let z: u8 = 0;
    if (-z != 0) { return 3; }
    let d: usize = 3;
    if (-d + d != 0) { return 4; }
    if (-d <= d) { return 5; }
    return 42;
}
`

var unsignedNegationCases = []struct{ name, src string }{
	{"u32-u64", unsignedNegationProg},
	{"u8-usize", unsignedNegationU8UsizeProg},
}

func TestUnsignedNegationInterp(t *testing.T) {
	for _, tc := range unsignedNegationCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpExit(t, tc.src); got != 42 {
				t.Fatalf("interp got %d, want 42", got)
			}
		})
	}
}

func TestUnsignedNegationSelfHostInterp(t *testing.T) {
	if _, got := runSelfHostInterp(t, unsignedNegationProg); got != 42 {
		t.Fatalf("self-host interp got %d, want 42", got)
	}
}

func TestUnsignedNegationX86_64(t *testing.T) {
	for _, tc := range unsignedNegationCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := compileAndRunX86_64(t, tc.src); got != 42 {
				t.Fatalf("x86-64 got %d, want 42", got)
			}
		})
	}
}

func TestUnsignedNegationArm64(t *testing.T) {
	for _, tc := range unsignedNegationCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := compileAndRunArm64(t, tc.src); got != 42 {
				t.Fatalf("arm64 got %d, want 42", got)
			}
		})
	}
}

func TestUnsignedNegationWasm(t *testing.T) {
	for _, tc := range unsignedNegationCases {
		t.Run(tc.name, func(t *testing.T) {
			e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, tc.src, nil)
			if got := compileAndRunWasmbinMain(t, tc.src); got != 42 {
				t.Fatalf("wasm got %d, want 42", got)
			}
		})
	}
}
