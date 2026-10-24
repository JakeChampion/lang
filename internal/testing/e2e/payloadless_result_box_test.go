package e2e

import "testing"

const payloadlessResultBoxProg = `
function myenv(name: string): Option[string] {
    return env(name);
}
function main(): i32 {
    eprint("ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ");
    match (myenv("FERN_PAYLOADLESS_BOX_UNSET")) {
        Some(v) => { return 1; },
        None => { }
    }
    return 42;
}
`

func TestPayloadlessResultBoxInterp(t *testing.T) {
	if got := runInterpExit(t, payloadlessResultBoxProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestPayloadlessResultBoxX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, payloadlessResultBoxProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestPayloadlessResultBoxArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, payloadlessResultBoxProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestPayloadlessResultBoxWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, payloadlessResultBoxProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
