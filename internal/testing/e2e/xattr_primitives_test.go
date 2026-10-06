// getxattr and lgetxattr (#9098), setxattr and lsetxattr (#9154) end to end
// on every backend that has them: the value's bytes verbatim (a NUL inside it
// included), a final symlink followed by one call and not the other, and the
// Err an absent attribute or a missing path gives. The host sets the
// attribute the probe reads, and reads back the ones the probe writes, so
// neither half is checked only against itself.
//
// WASI has no extended attributes; E066 refuses all four there (capability
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
        Err(Other(_, m, _)) => { if (m != %[4]q) { return 10; } },
        Err(_) => { return 11; }
    }
    match (lgetxattr(%[3]q, "user.fern")) { Ok(_) => { return 12; }, Err(NotFound(_)) => {}, Err(_) => { return 13; } }
    // A written value, NUL included, reads back whole.
    match (setxattr(%[1]q, "user.set", "a" + "\x00" + "b")) { Ok(_) => {}, Err(_) => { return 14; } }
    match (getxattr(%[1]q, "user.set")) { Ok(v) => { if (v.len() != 3 || v[1] != 0 as u8) { return 15; } }, Err(_) => { return 16; } }
    // An empty value is still an attribute.
    match (setxattr(%[1]q, "user.empty", "")) { Ok(_) => {}, Err(_) => { return 17; } }
    match (getxattr(%[1]q, "user.empty")) { Ok(v) => { if (v.len() != 0) { return 18; } }, Err(_) => { return 19; } }
    // setxattr through the link lands on f; lsetxattr never does, whatever
    // the link's own filesystem says to a user attribute on a symlink.
    match (setxattr(%[2]q, "user.via", "v")) { Ok(_) => {}, Err(_) => { return 20; } }
    match (lsetxattr(%[2]q, "user.link", "v")) { Ok(_) => {}, Err(_) => {} }
    match (getxattr(%[1]q, "user.link")) { Ok(_) => { return 21; }, Err(_) => {} }
    match (setxattr(%[3]q, "user.set", "v")) { Ok(_) => { return 22; }, Err(NotFound(_)) => {}, Err(_) => { return 23; } }
    return 0;
}
`, p("f"), p("l"), p("missing"), xattrAbsentText)
}

// checkXattrWrites reads the probe's writes back through the host.
func checkXattrWrites(t *testing.T, dir string) {
	t.Helper()
	f := filepath.Join(dir, "f")
	for name, want := range map[string]string{"user.set": "a\x00b", "user.empty": "", "user.via": "v"} {
		if got, ok := hostUserXattr(t, f, name); !ok || got != want {
			t.Errorf("host reads %s = %q (present %v), want %q", name, got, ok, want)
		}
	}
	if _, ok := hostUserXattr(t, f, "user.link"); ok {
		t.Errorf("lsetxattr on the link set user.link on its target")
	}
}

func TestInterpXattrPrimitives(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, xattrFixture(t, dir)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)", code)
	}
	checkXattrWrites(t, dir)
}

func TestX86_64XattrPrimitives(t *testing.T) {
	dir := t.TempDir()
	if code, out := compileRunX86_64WithSetup(t, xattrFixture(t, dir), nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)\n%s", code, out)
	}
	checkXattrWrites(t, dir)
}

func TestArm64XattrPrimitives(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, xattrFixture(t, dir))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see xattrFixture)\n%s", code, out)
	}
	checkXattrWrites(t, dir)
}

// Darwin's getxattr and setxattr are one call each with an options word,
// and XATTR_NOFOLLOW is what makes them the `l` forms.
func TestArm64DarwinXattrPrimitives(t *testing.T) {
	dir := t.TempDir()
	buildAndRunDarwin(t, dir, xattrFixture(t, dir))
	checkXattrWrites(t, dir)
}

func TestWASMXattrRefused(t *testing.T) {
	bin := buildFernCLI(t)
	for _, call := range []string{
		`getxattr("a", "user.x")`, `lgetxattr("a", "user.x")`,
		`setxattr("a", "user.x", "v")`, `lsetxattr("a", "user.x", "v")`,
		`getxattr_bytes("a", "user.x")`, `lgetxattr_bytes("a", "user.x")`,
		`setxattr_bytes("a", "user.x", [255 as u8])`, `lsetxattr_bytes("a", "user.x", [])`,
	} {
		name := call[:strings.IndexByte(call, '(')]
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
			prog := fmt.Sprintf("function main(): i32 {\n    match (%s) { Ok(_) => { return 0; }, Err(_) => { return 1; } }\n}\n", call)
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
