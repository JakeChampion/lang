// `priority` / `set_priority` (#8375's primitive) end to end on every backend
// that provides them.
//
// The read cannot assert a fixed number — the nice value is whatever the
// runner was started with — so the probe SETS one and reads it back. That is
// the check with teeth AND it keeps the test off the ABI split: Linux's
// getpriority answers the value biased by 20 while BSD answers it directly,
// so an expected value computed in Go would be right on one and wrong on the
// other. Reading back a 19 the probe itself asked for catches a bias left in
// place (1), a correction applied twice (1 again), and the swapped syscall
// numbers, which make a read quietly renice and answer 0.
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
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// prioritySource returns 0 when the pair agrees with itself, and a code
// naming the disagreement otherwise.
func prioritySource() string {
	return `function main(): i32 {
    var orig: i32 = priority();

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
`
}

func TestX86_64Priority(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, prioritySource(), nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = the value set was not the value read back)\n%s", code, out)
	}
}

func TestArm64Priority(t *testing.T) {
	out, code := compileAndRunArm64(t, prioritySource())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = the value set was not the value read back)\n%s", code, out)
	}
}

func TestArm64SSAPriority(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, prioritySource(), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = the value set was not the value read back)\n%s", code, stderr)
	}
}

// The interpreter shares its process with the Go runtime, so the writes move
// `fern`'s own niceness. That is the same one-process scope a compiled
// program has, and it is why this probe puts the value back before it ends.
func TestInterpPriority(t *testing.T) {
	if code := runInterpExit(t, prioritySource()); code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = the value set was not the value read back)", code)
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
