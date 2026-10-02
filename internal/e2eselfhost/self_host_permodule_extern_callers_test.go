package e2eselfhost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A whole-module unit of the per-module driver leaves out what has no body
// on its native target: an `@import` extern and every function that calls
// one, a call made from inside a lambda body included. A function that only
// spells an extern's name, as a local or a field, is no caller and stays;
// the merged emit of the same program builds and runs.
func TestSelfHostPerModuleUnitDropsExternCallers(t *testing.T) {
	gcc, runner, bin := buildModloadDriverX86(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	stage := t.TempDir()
	host := `@import("wasi:http/types@0.2.0", "[method]incoming-request.method")
function ext(): i32;

pub struct Named { ext: i32 }

pub function via_lambda(): i32 {
    let f: () => i32 = (): i32 => { return ext(); };
    return f();
}

pub function keeps(): i32 {
    let n: Named = Named { ext: 40 };
    let ext: i32 = n.ext + 1;
    return ext + 1;
}
`
	entrySrc := `import "./host";

function main(): i32 {
    if (target_os() == "wasi-http") {
        return host.via_lambda();
    }
    return host.keeps() - 42;
}
`
	if err := os.WriteFile(filepath.Join(stage, "host.fern"), []byte(host), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(stage, "main.fern")
	if err := os.WriteFile(entry, []byte(entrySrc), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(runDriverFile(t, runner, bin, entry, "-per-module-count"))))
	if err != nil || count != 2 {
		t.Fatalf("-per-module-count = %q, want 2", err)
	}
	var units strings.Builder
	for i := 0; i < count; i++ {
		units.Write(runDriverFile(t, runner, bin, entry, "-per-module-emit", strconv.Itoa(i)))
	}
	asm := units.String()
	if !strings.Contains(asm, "__fn_host__keeps") {
		t.Error("the unit dropped keeps, which calls no extern")
	}
	for _, gone := range []string{"__fn_host__via_lambda", "__fn_host__ext"} {
		if strings.Contains(asm, gone) {
			t.Errorf("the unit carries %s, which has no body on x86-64", gone)
		}
	}

	merged := string(runDriverFile(t, runner, bin, entry))
	prog := buildBin(t, gcc, stage, "externs", merged)
	rc := runX86_64Bin(runner, prog)
	if out, err := rc.CombinedOutput(); err != nil {
		t.Fatalf("merged emit: %v\n%s", err, out)
	}
}
