package e2e

import "testing"

// `async function` and `pub opaque struct` are contextual declaration
// modifiers with wasm-side / checker-side meaning (the component-model-async
// export surface; E021 opaque access). Neither changes what a NATIVE binary
// does: the declaration compiles as an ordinary function / struct. Nothing
// gated that on the register backends, so #6631 — which taught the self-host
// parser the same two forms — pins it here as well, on both native targets.
const declModifiersSrc = `async function compute(): i32 { return 7; }
pub async function other(): i32 { return 2; }
pub opaque struct E { a: i32 }
function main(): i32 {
    var e: E = E { a: 5 };
    var async: i32 = 0;
    var opaque: i32 = 0;
    return compute() + other() + e.a + async + opaque;
}
`

func TestDeclModifiersNativeX86_64(t *testing.T) {
	_, code := compileAndRunX86_64(t, declModifiersSrc)
	if code != 14 {
		t.Errorf("exit = %d, want 14", code)
	}
}

func TestDeclModifiersNativeArm64(t *testing.T) {
	_, code := compileAndRunArm64(t, declModifiersSrc)
	if code != 14 {
		t.Errorf("exit = %d, want 14", code)
	}
}
