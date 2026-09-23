package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// undispatchableKeyPrograms are map keys the INTERPRETER compares by value and
// no compiled backend can hash: a tuple and an array. Each inserts a key and
// reads the same value back, so a compiled build that compares the POINTER
// instead answers the default rather than the value.
var undispatchableKeyPrograms = []struct{ name, key, src string }{
	{"tuple", "(i32, i32)", `import "core/map";
function main(): i32 {
    var m: Map[(i32, i32), i32] = map_new(8);
    m = m.insert((1, 2), 5);
    return m.get_or((1, 2), 0) + 9;
}
`},
	{"array", "i32[]", `import "core/map";
function main(): i32 {
    var m: Map[i32[], i32] = map_new(8);
    m = m.insert([1, 2], 5);
    return m.get_or([1, 2], 0) + 9;
}
`},
}

// TestMapUndispatchableKeyIsRefusedNotMiscompiled pins that a map key the
// runtime cannot hash is REFUSED at lowering rather than compiled into a
// silent wrong answer (#10020).
//
// mapKeyKindTag has branches for an integer, a string, a wide scalar and a
// struct/enum with derived Eq + Hash. A tuple or array key matched none and
// fell through to 0 — "i32-sized scalar" — so the emitted code compared the
// key POINTER. Two equal values built separately miss each other, and every
// lookup reads the default: these programs answered 9 compiled where the
// interpreter answers 14, with no diagnostic anywhere.
//
// The interpreter half is asserted too. `interp.valuesEqual` deep-compares
// composite keys and TestInterpMapCompositeKeys gates that, so refusing the
// COMPILED build must not be mistaken for the language dropping the feature —
// the refusal exists until the lowering does, which is what #10020 tracks.
func TestMapUndispatchableKeyIsRefusedNotMiscompiled(t *testing.T) {
	bin := buildLangBinForInterp(t)
	for _, tc := range undispatchableKeyPrograms {
		t.Run(tc.name, func(t *testing.T) {
			// The interpreter still answers it, and answers correctly.
			run := exec.Command(bin, "-interp", "-")
			run.Stdin = bytes.NewReader([]byte(tc.src))
			var out, errb bytes.Buffer
			run.Stdout, run.Stderr = &out, &errb
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != 14 {
				t.Fatalf("interpreter = %d, want 14 — the program this refusal is about no longer "+
					"means what the case says\nstderr: %s", code, errb.String())
			}

			// Compiling it names the key instead of emitting the wrong
			// answer. `-run` takes a path, not stdin, unlike `-interp`.
			entry := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(entry, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			cmp := exec.Command(bin, "-run", entry)
			var cout, cerr bytes.Buffer
			cmp.Stdout, cmp.Stderr = &cout, &cerr
			_ = cmp.Run()
			code := cmp.ProcessState.ExitCode()
			msg := cerr.String()
			if code == 9 {
				t.Fatalf("compiled build answered 9 where the interpreter answers 14 — the key is " +
					"being compared as a pointer again, which is the silent wrong answer this refuses")
			}
			if code == 14 {
				t.Skipf("a Map keyed by %s compiles and answers correctly now — the lowering landed, "+
					"so delete this case and let the key through (#10020)", tc.key)
			}
			if !strings.Contains(msg, "cannot be compiled") || !strings.Contains(msg, tc.key) {
				t.Fatalf("want a refusal naming %s, got exit %d with:\n%s", tc.key, code, msg)
			}
		})
	}
}
