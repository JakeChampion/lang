package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// bufBuilderProbe exercises every builtin of the capacity-carrying string
// builder (#8773) on one program: the reserve, a whole-string push, a byte, a
// heap-form push past the inline cap, a byte range out of a heap string and
// one out of an inline (SSO) string, the zero-copy take, the re-arm a take
// leaves behind, an empty take, growth past the reserved capacity, and the
// release. Each check has its own exit code so a failure names the step.
//
// The byte assertions matter as much as the lengths: a take that handed back
// the wrong pointer, or an arm64 two-word return whose second word was
// dropped, still reports a plausible length.
const bufBuilderProbe = `function main(): i32 {
    var b: usize = buf_new(8);
    if (buf_len(b) != 0) { return 1; }
    buf_push(b, "hello");
    if (buf_len(b) != 5) { return 2; }
    buf_push_byte(b, 44);
    buf_push(b, "abcdefghijklmnopqrstuvwxyz");
    if (buf_len(b) != 32) { return 3; }
    buf_push_range(b, "0123456789", 2, 5);
    if (buf_len(b) != 35) { return 4; }
    buf_push_range(b, "xyz", 0, 2);
    if (buf_len(b) != 37) { return 5; }
    var s: string = buf_take(b);
    if (s.len() != 37) { return 6; }
    if (buf_len(b) != 0) { return 7; }
    if (s[0] != 104) { return 8; }
    if (s[5] != 44) { return 9; }
    if (s[31] != 122) { return 10; }
    if (s[32] != 50) { return 11; }
    if (s[34] != 52) { return 12; }
    if (s[35] != 120) { return 13; }
    if (s[36] != 121) { return 14; }
    buf_push(b, "tail");
    if (buf_take(b) != "tail") { return 15; }
    if (buf_take(b).len() != 0) { return 16; }
    var i: i32 = 0;
    while (i < 1000) { buf_push(b, "0123456789"); i = i + 1; }
    if (buf_len(b) != 10000) { return 17; }
    var big: string = buf_take(b);
    if (big.len() != 10000) { return 18; }
    if (big[0] != 48) { return 19; }
    if (big[9999] != 57) { return 20; }
    buf_free(b);
    return 0;
}
`

// bufTwoBuildersProbe is the property the singleton strbuf cannot have: two
// builders accumulating at once, interleaved, each keeping its own bytes.
const bufTwoBuildersProbe = `function main(): i32 {
    var a: usize = buf_new(16);
    var c: usize = buf_new(16);
    buf_push(a, "AAA");
    buf_push(c, "BB");
    buf_push(a, "A");
    buf_push_byte(c, 66);
    if (buf_len(a) != 4) { return 1; }
    if (buf_len(c) != 3) { return 2; }
    if (buf_take(a) != "AAAA") { return 3; }
    if (buf_take(c) != "BBB") { return 4; }
    buf_free(a);
    buf_free(c);
    return 0;
}
`

// bufNestedProbe builds an inner string through one builder while an outer one
// is mid-accumulation, then pushes the result into the outer. Nesting is the
// second thing the singleton forbids, and it is the shape every stdlib writer
// built on the builder will take.
const bufNestedProbe = `function join(parts: string[], sep: string): string {
    var inner: usize = buf_new(32);
    var i: i32 = 0;
    while (i < parts.len()) {
        if (i > 0) { buf_push(inner, sep); }
        buf_push(inner, parts[i]);
        i = i + 1;
    }
    var out: string = buf_take(inner);
    buf_free(inner);
    return out;
}

function main(): i32 {
    var outer: usize = buf_new(16);
    buf_push(outer, "[");
    buf_push(outer, join(["a", "bb", "ccc"], ", "));
    buf_push(outer, "]");
    var s: string = buf_take(outer);
    buf_free(outer);
    if (s != "[a, bb, ccc]") { return 1; }
    return 0;
}
`

// TestBufBuilder runs the builder probes on every backend the builtins reach:
// the interpreter (the reference oracle), x86-64, arm64 and wasm. A builder is
// a usize handle rather than a value of its own type, so the legs differ in
// what that handle addresses -- a 32-byte control block on the natives, a
// 16-byte one on wasm32, a table key in the interp -- and agreeing on all four
// is what says the layouts are consistent.
func TestBufBuilder(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	probes := []struct {
		name string
		src  string
	}{
		{"all-builtins", bufBuilderProbe},
		{"two-builders", bufTwoBuildersProbe},
		{"nested", bufNestedProbe},
	}
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := interpExit(t, interpBin, p.src); code != 0 {
					t.Errorf("interp exit %d, want 0", code)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, p.src); code != 0 {
					t.Errorf("x86-64 exit %d, want 0", code)
				}
			})
			t.Run("arm64", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, p.src); code != 0 {
					t.Errorf("arm64 exit %d, want 0", code)
				}
			})
			t.Run("wasm", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, p.src); code != 0 {
					t.Errorf("wasm exit %d, want 0", code)
				}
			})
			// The arm64 assembly runs natively here through the Mach-O
			// backend, so Apple Silicon covers it without qemu.
			t.Run("arm64_darwin", func(t *testing.T) {
				if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
					t.Skip("arm64-darwin execution only runs on Apple Silicon")
				}
				if code := runArm64Darwin(t, p.src); code != 0 {
					t.Errorf("arm64-darwin exit %d, want 0", code)
				}
			})
		})
	}
}

// runArm64Darwin builds src for arm64-darwin through the in-process Mach-O
// backend and runs it, returning the exit code.
func runArm64Darwin(t *testing.T, src string) int {
	t.Helper()
	bin := buildFernCLI(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("arm64-darwin build failed: %v\n%s", err, o)
	}
	cmd := exec.Command(out)
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("arm64-darwin binary did not run to a normal exit (state=%v)", ps)
	}
	return ps.ExitCode()
}
