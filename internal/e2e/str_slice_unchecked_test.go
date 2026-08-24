// Differential coverage for `slice_unchecked(s, a, b): str` (#5634, D9,
// slice 3) — the byte slice s[a:b] under its honest name: half-open,
// byte-indexed, trapping (exit 134) on a < 0 || b > s.len() || a > b, and
// deliberately NOT checking UTF-8 char boundaries — and its total
// std/string sibling `s.slice_snap(a, b)`, which clamps both bounds to
// [0, len] then snaps them INWARD to char boundaries (a forward, b
// backward) and returns "" on an inverted or snapped-empty range.
//
// The multibyte fixture is "héllo" built from explicit bytes
// (string_from_bytes_unchecked, the utf8_test idiom) so the é's
// [195, 169] encoding is pinned rather than trusted to source encoding.
package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// strSliceUncheckedProgram exits 0 on success, a distinct code per failed
// step. Steps: (a) the mid-code-point cut stays byte-honest; boundary
// index forms; (b) the slice_snap snap/clamp/inverted/identity table;
// (d) an owned-temp source (a + b) sliced in a loop, exercising the IR's
// owned-temp stash — asserted by value, since a stash bug reads freed
// bytes or the wrong view.
const strSliceUncheckedProgram = `import "std/string";

// h(104) é(195,169) l l o — 6 bytes, 5 code points.
function mk(): string {
    var b: u8[] = [104 as u8, 195 as u8, 169 as u8, 108 as u8, 108 as u8, 111 as u8];
    return string_from_bytes_unchecked(b);
}

function main(): i32 {
    var s: string = mk();
    if (s.len() != 6) { return 1; }

    // (a) Byte-honest: the cut lands mid-é and keeps the lead byte.
    var cut: str = slice_unchecked(s, 0, 2);
    if (cut.len() != 2) { return 2; }
    if (cut[0] != 104) { return 3; }
    if (cut[1] != 195) { return 4; }

    // Boundary index forms: full range, empty range, suffix.
    if (slice_unchecked(s, 0, 6) != s) { return 5; }
    if (slice_unchecked(s, 3, 3).len() != 0) { return 6; }
    if (slice_unchecked(s, 3, 6) != "llo") { return 7; }

    // (b) slice_snap: b snaps backward off the continuation byte at 2...
    if (s.slice_snap(0, 2) != "h") { return 8; }
    // ...a snaps forward off it...
    if (s.slice_snap(2, 6) != "llo") { return 9; }
    // ...and boundary indices are identity with the raw slice.
    if (s.slice_snap(1, 3).len() != 2) { return 10; }
    if (s.slice_snap(1, 3) != slice_unchecked(s, 1, 3)) { return 11; }
    // Clamp both ends to [0, len].
    if (s.slice_snap(0 - 5, 99) != s) { return 12; }
    // Inverted and snapped-empty ranges are "".
    if (s.slice_snap(4, 2) != "") { return 13; }
    if (s.slice_snap(2, 2) != "") { return 14; }
    if ("".slice_snap(0, 5) != "") { return 15; }
    // ASCII: every index is a boundary, so slice_snap is plain slicing.
    if ("hello".slice_snap(1, 3) != "el") { return 16; }
    if ("hello".slice_snap(0, 5) != "hello") { return 17; }

    // (d) Owned-temp source in a loop: the concat result is a temporary
    // the view borrows, so the lowering must keep it alive (the stash
    // path) — and release it each iteration.
    var a: string = "ab";
    var c: string = "cd";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var t: str = slice_unchecked(a + c, 0, 2);
        if (t != "ab") { return 18; }
        if (t[1] != 98) { return 19; }
        acc = acc + t.len();
        i = i + 1;
    }
    if (acc != 100) { return 20; }
    return 0;
}
`

func TestStrSliceUncheckedInterp(t *testing.T) {
	if got := runInterpExit(t, strSliceUncheckedProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestStrSliceUncheckedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, strSliceUncheckedProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestStrSliceUncheckedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, strSliceUncheckedProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}

func TestStrSliceUncheckedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, strSliceUncheckedProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

// TestStrSliceUncheckedArm64SSA drives the same program through the CLI's
// `-backend ssa` path (the SSA-direct arm64 backend), which the in-process
// compileAndRunArm64 harness does not reach.
func TestStrSliceUncheckedArm64SSA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64 -backend ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(strSliceUncheckedProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog.bin")
	emit := exec.Command(bin, "-target", "arm64-linux", "-backend", "ssa", "-o", out, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target arm64-linux -backend ssa: %v\nstderr:\n%s", err, eb.String())
	}
	run := runArm64Bin(qemu, out)
	err := run.Run()
	got := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			got = ee.ExitCode()
		} else {
			t.Fatalf("run %s: %v", out, err)
		}
	}
	if got != 0 {
		t.Fatalf("arm64-ssa got %d, want 0", got)
	}
}

// The trap contract: each case violates one clause of
// a < 0 || b > s.len() || a > b. If the bounds check were missing, every
// program here returns a small slice length (or garbage) — never 134 —
// so the exit code discriminates a trap from a silent out-of-bounds view.
// Bounds are carried in vars so constfold cannot pre-judge them.
var strSliceUncheckedTrapCases = []struct{ name, src string }{
	{"high_past_end", `function main(): i32 {
    var s: string = "hello";
    var hi: i32 = 6;
    var t: str = slice_unchecked(s, 0, hi);
    return t.len();
}`},
	{"negative_low", `function main(): i32 {
    var s: string = "hello";
    var lo: i32 = 0 - 1;
    var t: str = slice_unchecked(s, lo, 2);
    return t.len();
}`},
	{"inverted", `function main(): i32 {
    var s: string = "hello";
    var lo: i32 = 3;
    var t: str = slice_unchecked(s, lo, 1);
    return t.len();
}`},
}

func TestStrSliceUncheckedTrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping trap e2e in -short mode")
	}
	for _, c := range strSliceUncheckedTrapCases {
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				// The interpreter reports a diagnostic and exits non-zero
				// rather than using the natives' 134 abort.
				if got := runInterpExit(t, c.src); got == 0 {
					t.Errorf("interp did not trap (exit 0)\nsrc:\n%s", c.src)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				out, got := compileAndRunX86_64(t, c.src)
				if got != 134 {
					t.Errorf("x86-64 exit = %d, want 134 (trap)\nout: %q\nsrc:\n%s", got, out, c.src)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				out, got := compileAndRunArm64(t, c.src)
				if got != 134 {
					t.Errorf("arm64 exit = %d, want 134 (trap)\nout: %q\nsrc:\n%s", got, out, c.src)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				// wasm's `unreachable` surfaces as wasmtime's own non-zero
				// exit, not 134 — assert the trap, not its spelling
				// (matching assertAborts).
				comp := buildNumComponent(t, c.src)
				_, _, code := runComponent(t, comp, runOpts{})
				if code == 0 {
					t.Errorf("wasm did not trap (exit 0)\nsrc:\n%s", c.src)
				}
			})
		})
	}
}

// TestRunnerStringSliceSnapExamplePasses gates the pure-Fern runner suite
// for slice_snap + slice_unchecked, wired like the other examples/tests
// gates in test_runner_test.go.
func TestRunnerStringSliceSnapExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_slice_snap_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string slice_snap + slice_unchecked", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}
