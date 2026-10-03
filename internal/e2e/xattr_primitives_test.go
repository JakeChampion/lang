// getxattr and lgetxattr (#9098) end to end on every backend that has them:
// the value's bytes verbatim (a NUL inside it included), a final symlink
// followed by one and not the other, and the Err an absent attribute or a
// missing path gives. The host sets the attribute through Go, so the probe
// reads back something it did not write itself.
//
// WASI has no extended attributes; E066 refuses both there (capability
// `xattr`), which TestWASMXattrRefused pins.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const xattrProbeValue = "hello\x00world"

// xattrFixture makes `f` carrying user.fern and a symlink `l` to it, and
// returns the probe source. Every failure returns its own exit code.
func xattrFixture(t *testing.T, dir string) string {
	t.Helper()
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setUserXattr(t, f, "user.fern", xattrProbeValue)
	if err := os.Symlink("f", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	p := func(name string) string { return filepath.Join(dir, name) }
	return fmt.Sprintf(`function main(): i32 {
    // The value verbatim, its NUL included.
    match (getxattr(%[1]q, "user.fern")) {
        Ok(v) => {
            if (v.len() != 11) { return 1; }
            if (slice_unchecked(v, 0, 5) != "hello") { return 2; }
            if (v[5] != 0 as u8) { return 3; }
            if (slice_unchecked(v, 6, 11) != "world") { return 4; }
        },
        Err(_) => { return 5; }
    }
    // getxattr follows the link to f; lgetxattr asks about the link.
    match (getxattr(%[2]q, "user.fern")) { Ok(v) => { if (v.len() != 11) { return 6; } }, Err(_) => { return 7; } }
    match (lgetxattr(%[2]q, "user.fern")) { Ok(_) => { return 8; }, Err(_) => {} }
    // An absent attribute carries the platform's own text.
    match (getxattr(%[1]q, "user.absent")) {
        Ok(_) => { return 9; },
        Err(Other(_, m)) => { if (m != %[4]q) { return 10; } },
        Err(_) => { return 11; }
    }
    match (lgetxattr(%[3]q, "user.fern")) { Ok(_) => { return 12; }, Err(NotFound(_)) => {}, Err(_) => { return 13; } }
    return 0;
}
`, p("f"), p("l"), p("missing"), xattrAbsentText)
}

func TestInterpXattrPrimitives(t *testing.T) {
	if code := runInterpExit(t, xattrFixture(t, t.TempDir())); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)", code)
	}
}

func TestX86_64XattrPrimitives(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, xattrFixture(t, t.TempDir()), nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)\n%s", code, out)
	}
}

func TestX86_64SSAXattrPrimitives(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := xattrFixture(t, dir)
	for _, backend := range []string{"flat", "ssa"} {
		if code := runPathProbe(t, bin, qemu, dir, "xattr", backend, src, ""); code != 0 {
			t.Errorf("-backend %s: exit = %d, want 0 — the code names the step (see xattrFixture)", backend, code)
		}
	}
}

func TestArm64XattrPrimitives(t *testing.T) {
	out, code := compileAndRunArm64(t, xattrFixture(t, t.TempDir()))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)\n%s", code, out)
	}
}

func TestArm64SSAXattrPrimitives(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, xattrFixture(t, dir), os.Environ())
	if code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ()); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)\n%s", code, stderr)
	}
}

// Darwin's getxattr is one call with an options word, and XATTR_NOFOLLOW is
// what makes it lgetxattr.
func TestArm64DarwinXattrPrimitives(t *testing.T) {
	dir := t.TempDir()
	buildAndRunDarwin(t, dir, xattrFixture(t, dir))
}

func TestWASMXattrRefused(t *testing.T) {
	bin := buildFernCLI(t)
	for _, name := range []string{"getxattr", "lgetxattr"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
			prog := fmt.Sprintf("function main(): i32 {\n    match (%s(\"a\", \"user.x\")) { Ok(_) => { return 0; }, Err(_) => { return 1; } }\n}\n", name)
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			o, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "prog.wasm"), src).CombinedOutput()
			out := string(o)
			if err == nil {
				t.Fatalf("wasm32-wasi build of %s succeeded, want E066", name)
			}
			if !strings.Contains(out, "E066") || !strings.Contains(out, "xattr") {
				t.Fatalf("want E066 naming xattr, got:\n%s", out)
			}
		})
	}
}
