package e2e

import (
	"os/exec"
	"testing"
)

// Both the Result box and the fresh words array belong to the caller.
// Repeated reads must reuse their allocations after a warm-up, and keeping
// the payload alive through termios_set must not release it prematurely.
// Use successful calls for heap balance: IoError payloads are immortal and
// their allocations cannot be reclaimed by releasing the owned Result box.
const termiosOwnershipSource = `function round(): i32 {
    match (termios_get(0)) {
        Ok(words) => {
            if (words.len() != 24) { return 11; }
            match (termios_set(0, 0, words)) {
                Ok(_) => {}, Err(_) => { return 12; }
            }
            if (words[5] != (3 as i64)) { return 13; }
        },
        Err(_) => { return 10; }
    }
    return 0;
}
function main(): i32 {
    if (round() != 0) { return 90; }
    var before: i64 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 100) {
        if (round() != 0) { return 91; }
        i = i + 1;
    }
    if (__heap_bump_bytes() != before) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

func TestX86_64TermiosOwnership(t *testing.T) {
	bin, runner := compileX86_64FreeOn(t, termiosOwnershipSource)
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	if code := termiosOnPty(t, cmd); code != 0 {
		t.Fatalf("termios ownership exit %d (98: leaked allocation, 99: over-release)", code)
	}
}

func TestArm64TermiosOwnership(t *testing.T) {
	bin, qemu := compileArm64FreeOn(t, termiosOwnershipSource)
	if code := termiosOnPty(t, runArm64Bin(qemu, bin)); code != 0 {
		t.Fatalf("termios ownership exit %d (98: leaked allocation, 99: over-release)", code)
	}
}
