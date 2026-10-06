package e2e

import "testing"

// #8843. `env` and `read_line` hand back an Option[string] BOX the caller
// now owns and releases (#8398 / #8405), but the payloadless `None` arm
// wrote only the tag and left the box's data / len words holding whatever
// the recycled block last contained. The IR's branchless enum drop reads a
// uniform enum's payload words WITHOUT testing the tag, so those stale
// bytes were released as a string pointer.
//
// The `eprint` ahead of the call is the poison: it puts a block carrying
// the message bytes back on the freelist, and the None box is allocated
// out of it — so the released "pointer" is a run of 'Z's. On wasm that is
// an out-of-bounds trap; on the natives the same read is an arbitrary rc
// write with nothing to catch it.
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
