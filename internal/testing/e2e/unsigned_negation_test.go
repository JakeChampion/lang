package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Unary minus on an unsigned integer wraps at the operand's width: `-x` is
// `0 - x` (docs/INTEGER-SEMANTICS.md, #12657). Both checkers accepted it and
// the oracle wrapped, while lowering refused it as `operator contract: - u32`.
// Each program returns 42 iff every check holds.
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

// The byte and the address, apart: the self-host interpreter holds both in an
// i32, wrapping no u8 arithmetic (#12686) and comparing a usize signed
// (#12663), so it runs only the u32 and u64 program.
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

// The compile runs ahead of the wasmtime lookup, so the lowering is held on
// this target where no runtime is installed.
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
