package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func closureArgChurnSelfHostSrc(rounds int, indirect bool) string {
	body := `        t = t + len([1, 2, i]);`
	decl := ""
	if indirect {
		decl = "    let h: (i32[]) => i32 = (xs: i32[]) => xs.len();\n"
		body = `        t = t + h([1, 2, i]);`
	}
	return fmt.Sprintf(`import "std/i32";

function len(xs: i32[]): i32 { return xs.len(); }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
%s    while (i < %d) {
%s
        i = i + 1;
    }
    return t %% 7;
}`, decl, rounds, body)
}

func TestSelfHostClosureArgReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	liveOf := func(name string, rounds int, indirect bool) int64 {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, closureArgChurnSelfHostSrc(rounds, indirect), []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, _ := hevRun(t, runner, progBin)
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("parse %q: %v", summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s: allocated nothing — the probe is not exercising the path", name)
		}
		t.Logf("%s (rounds=%d, indirect=%v): %s", name, rounds, indirect, summary)
		return live
	}

	base100 := liveOf("cloarg_base100", 100, false)
	base200 := liveOf("cloarg_base200", 200, false)
	ind100 := liveOf("cloarg_ind100", 100, true)
	ind200 := liveOf("cloarg_ind200", 200, true)

	d100, d200 := ind100-base100, ind200-base200
	t.Logf("closure-call arg delta: 100 rounds = %d B, 200 rounds = %d B", d100, d200)
	if d200 != d100 {
		t.Errorf("a fresh argument to a closure call leaks per round on the self-host: delta "+
			"over the same call made directly is %d B at 100 rounds and %d B at 200 (control "+
			"%d / %d). The argument is dead at the call's return, so the delta must be flat (#6460)",
			d100, d200, base100, base200)
	}
}
