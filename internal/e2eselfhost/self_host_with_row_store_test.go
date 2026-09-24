package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every `.with` form the AST lowering emits stores its element through
// lower_with_elem, which retains a bare array local stored as a row of a
// nested array, as the append arm does (#4702). `units` stores a local `chain`
// that its next rebind releases; `through` then binds each row to a local and
// clones the table row by row. Without the retain the rebind freed the stored
// row and `through` touched it (#10219). Built through the AST lowering, under
// the sanitizer, which quarantines a freed block.
const withRowStoreProg = `@noinline
function through(deps: i32[][]): i32[][] {
    var out: i32[][] = deps;
    var w: i32 = 0;
    while (w < deps.len()) {
        var chain: i32[] = [];
        var r: i32[] = deps[w];
        var k: i32 = 0;
        while (k < r.len()) { chain = chain.append(r[k]); k = k + 1; }
        out = out.with(w, chain);
        w = w + 1;
    }
    return out;
}

@noinline
function units(dependencies: i32[][], payloads: boolean[]): i32[][] {
    var deps: i32[][] = dependencies;
    var i: i32 = 0;
    while (i < payloads.len()) {
        if (payloads[i]) {
            var chain: i32[] = [];
            deps = deps.with(i, chain);
        }
        i = i + 1;
    }
    return through(deps);
}

function main(): i32 {
    var rows: i32[][] = [[1], [2], [3], [4], [5], [6]];
    var payloads: boolean[] = [false, true, false, true, false, false];
    var deps: i32[][] = units(rows, payloads);
    var sum: i32 = 0;
    for row in deps { for v in row { sum = sum + v; } }
    return sum;
}
`

func TestSelfHostWithRowStoreRetained(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "rows.fern")
	if err := os.WriteFile(src, []byte(withRowStoreProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		build.Env = append(os.Environ(), "FERN_SEM_IR=", "FERN_SANITIZE=1")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 15 || strings.Contains(stderr.String(), "use-after-free") {
			t.Errorf("%s: exit %d, want 15 (the interpreter's) with no freed row touched\n%s", tg.target, got, stderr.String())
		}
	}
}
