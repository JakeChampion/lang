package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func prioritySource() string {
	return `function main(): i32 {
    let orig: i32 = priority();

    // Raising is always permitted, whoever is running.
    match (set_priority(19)) {
        Ok(_) => {},
        Err(_) => { return 2; }
    }
    if (priority() != 19) { return 3; }

    // A value past the kernel's range is CLAMPED, not refused — to the
    // kernel's own ceiling, 19 on Linux and 20 on XNU.
    match (set_priority(1000)) {
        Ok(_) => {},
        Err(_) => { return 4; }
    }
    let top: i32 = priority();
    if (top < 19 || top > 20) { return 5; }

    // Going back down needs privilege. Both answers are right; what is not
    // right is a refusal that moved the value anyway.
    match (set_priority(orig)) {
        Ok(_) => {
            if (priority() != orig) { return 6; }
        },
        Err(_) => {
            if (priority() != top) { return 7; }
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

const priorityAloneSource = `function main(): i32 {
    let p: i32 = priority();
    if (p < 0 - 20 || p > 20) { return 1; }
    return 0;
}
`

func TestX86_64PriorityAlone(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, priorityAloneSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (1 = the nice value is out of range)\n%s", code, out)
	}
}

func TestArm64PriorityAlone(t *testing.T) {
	out, code := compileAndRunArm64(t, priorityAloneSource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (1 = the nice value is out of range)\n%s", code, out)
	}
}

func TestInterpPriority(t *testing.T) {
	if code := runInterpExit(t, prioritySource()); code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = the value set was not the value read back)", code)
	}
}

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
