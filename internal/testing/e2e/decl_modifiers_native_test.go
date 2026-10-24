package e2e

import "testing"

const declModifiersSrc = `async function compute(): i32 { return 7; }
pub async function other(): i32 { return 2; }
pub opaque struct E { a: i32 }
function main(): i32 {
    let e: E = E { a: 5 };
    let async: i32 = 0;
    let opaque: i32 = 0;
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
