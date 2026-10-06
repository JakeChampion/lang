package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh temporary lent to a borrowing callee is released by the caller
// (refs #9450): a variant construction written at the call site, whose
// payloads are fresh, and a string[] literal of fresh or literal elements at a
// borrowable position of a callee that returns no pointer. `first` hands an
// element back, so its literal must not be freed before `f` is done with it;
// the sanitizer is what would see it if it were.
const lentTempReleaseSrc = `import "std/i32";
enum E { A(i32), B(i32) }
enum S { Word(string), Nothing }
function pick(e: E): i32 { match (e) { A(n) => { return n; }, B(n) => { return n + 1; } } }
function wlen(s: S): i32 { match (s) { Word(w) => { return w.len(); }, Nothing => { return 0; } } }
function cnt(ws: string[], min: i32): i32 { let n: i32 = 0; for w in ws { if (w.len() >= min) { n = n + 1; } } return n; }
function first(ws: string[]): string { return ws[0]; }
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 20) {
        t = t + pick(A(i)) + pick(B(1)) + wlen(Word("w" + i.to_string()));
        t = t + cnt(["ab", "cdef", "g" + i.to_string()], 2);
        i = i + 1;
    }
    let f: string = first(["kept" + "", "x"]);
    return (t + f.len()) % 251;
}
`

const lentTempReleaseWant = 93

func TestSelfHostLentTempRelease(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "lent_temp_release.fern")
	if err := os.WriteFile(src, []byte(lentTempReleaseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != lentTempReleaseWant {
		t.Fatalf("exit=%d, want %d (stderr %q)", exit, lentTempReleaseWant, stderr)
	}
	allocs, frees, _ := leakSummaryOf(t, "lent_temp_release", stderr)
	if allocs != frees {
		t.Fatalf("allocs=%d frees=%d, want every block freed (stderr %q)", allocs, frees, stderr)
	}
}
