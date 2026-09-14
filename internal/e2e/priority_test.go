// `priority` / `set_priority` (#8375's primitive) end to end on every backend
// that provides them.
//
// The read cannot assert a fixed number — the nice value is whatever the
// runner was started with — so it asserts the one the test process ITSELF
// reads through Go. A child inherits its parent's niceness, so the two are
// the same fact, and that is the check with teeth: Linux returns the value
// BIASED by 20, so a helper that forwards the syscall's answer unchanged says
// 20 where the truth is 0, and one that negates the correction says -20. Both
// are plausible-looking numbers.
//
// The write is then exercised against two measured kernel behaviours. A value
// outside -20..19 is CLAMPED rather than refused, so `set_priority(1000)`
// succeeds and leaves 19 behind; and LOWERING the value needs privilege, so
// the last leg accepts either outcome — Ok with the original read back, or
// Err with nothing changed. Which one it is depends on whether the suite runs
// as root, and both are correct.
//
// wasm has no leg: neither preview has a scheduler knob, so the `sched`
// capability withholds both builtins and the program is refused at check
// time.
package e2e

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// selfNice is the test process's own nice value, with Linux's bias undone —
// Go's syscall.Getpriority is the raw syscall and does NOT undo it, unlike
// every libc wrapper.
func selfNice(t *testing.T) int {
	t.Helper()
	n, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatalf("getpriority: %v", err)
	}
	return 20 - n
}

// prioritySource returns 0 when both builtins agree with what the harness
// measured, and a code naming the disagreement otherwise.
func prioritySource(want int) string {
	return fmt.Sprintf(`function main(): i32 {
    var orig: i32 = priority();
    // The bias left in place reads as 20 - want; the correction applied
    // twice reads as want - 20. Neither is want.
    if (orig != %d) { return 1; }

    // Raising is always permitted, whoever is running.
    match (set_priority(19)) {
        Ok(_) => {},
        Err(_) => { return 2; }
    }
    if (priority() != 19) { return 3; }

    // A value past the kernel's range is CLAMPED, not refused.
    match (set_priority(1000)) {
        Ok(_) => {},
        Err(_) => { return 4; }
    }
    if (priority() != 19) { return 5; }

    // Going back down needs privilege. Both answers are right; what is not
    // right is a refusal that moved the value anyway.
    match (set_priority(orig)) {
        Ok(_) => {
            if (priority() != orig) { return 6; }
        },
        Err(_) => {
            if (priority() != 19) { return 7; }
        }
    }
    return 0;
}
`, want)
}

func TestX86_64Priority(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, prioritySource(selfNice(t)), nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (1 = disagreed with the harness's own getpriority)\n%s", code, out)
	}
}

func TestArm64Priority(t *testing.T) {
	out, code := compileAndRunArm64(t, prioritySource(selfNice(t)))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (1 = disagreed with the harness's own getpriority)\n%s", code, out)
	}
}

func TestArm64SSAPriority(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, prioritySource(selfNice(t)), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (1 = disagreed with the harness's own getpriority)\n%s", code, stderr)
	}
}

// The interpreter shares its process with the Go runtime, so the writes move
// `fern`'s own niceness. That is the same one-process scope a compiled
// program has, and it is why this probe puts the value back before it ends.
func TestInterpPriority(t *testing.T) {
	if code := runInterpExit(t, prioritySource(selfNice(t))); code != 0 {
		t.Fatalf("exit = %d, want 0 (1 = disagreed with the harness's own getpriority)", code)
	}
}

// Both wasm worlds refuse both halves, and the refusal has to come from the
// capability scan rather than from the emitter: "this target has no scheduler
// knob" is a different statement from "the backend forgot one".
func TestWASMPriorityRefused(t *testing.T) {
	for _, tc := range []struct{ builtin, src string }{
		{"priority", "function main(): i32 { return priority(); }\n"},
		{"set_priority", "function main(): i32 { match (set_priority(5)) { Ok(_) => { return 0; }, Err(_) => { return 1; } } }\n"},
	} {
		prog, err := parser.Parse(tc.src)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.builtin, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("check %s: %v", tc.builtin, err)
		}
		for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
			vs := platforms.Enforce(prog, target)
			if len(vs) == 0 {
				t.Errorf("%s accepted %s; it has no scheduler knob", target, tc.builtin)
				continue
			}
			if vs[0].Builtin != tc.builtin || vs[0].Capability != "sched" {
				t.Errorf("%s refused %q on %q, want %s on sched", target, vs[0].Builtin, vs[0].Capability, tc.builtin)
			}
		}
	}
}
