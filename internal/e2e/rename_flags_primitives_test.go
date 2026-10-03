// rename_noreplace and rename_exchange (#9784) end to end on every native
// backend: renameat2 with RENAME_NOREPLACE / RENAME_EXCHANGE on Linux,
// renameatx_np with RENAME_EXCL / RENAME_SWAP on Darwin, where the flag
// values differ. A wrong flag still produces a plausible call — an exchange
// that only renames, a no-replace that replaces — so the probe checks the
// RESULTING TREE as well as each return value.
//
// WASI has neither call; E066 refuses both there (capability `fsrename`),
// which TestWASMRenameFlagsRefused pins.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// renameFlagsSource is the probe, its paths under `dir`. Every failure
// returns its own exit code, so the number names the step.
func renameFlagsSource(dir string) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	return fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "A")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (write_file(%[2]q, "B")) { Ok(_) => {}, Err(_) => { return 2; } }
    // An occupied destination is EEXIST, and neither name changes.
    match (rename_noreplace(%[1]q, %[2]q)) { Ok(_) => { return 3; }, Err(AlreadyExists(_)) => {}, Err(_) => { return 4; } }
    // The exchange swaps what the two names point at.
    match (rename_exchange(%[1]q, %[2]q)) { Ok(_) => {}, Err(_) => { return 5; } }
    match (read_file(%[1]q)) { Ok(s) => { if (s != "B") { return 6; } }, Err(_) => { return 7; } }
    match (read_file(%[2]q)) { Ok(s) => { if (s != "A") { return 8; } }, Err(_) => { return 9; } }
    // A free destination is an ordinary rename.
    match (rename_noreplace(%[1]q, %[3]q)) { Ok(_) => {}, Err(_) => { return 10; } }
    match (read_file(%[1]q)) { Ok(_) => { return 11; }, Err(_) => {} }
    // An exchange needs both names to exist.
    match (rename_exchange(%[3]q, %[4]q)) { Ok(_) => { return 12; }, Err(NotFound(_)) => {}, Err(_) => { return 13; } }
    return 0;
}
`, p("a"), p("b"), p("c"), p("missing"))
}

// renameFlagsCheckTree asserts what the probe left on disk.
func renameFlagsCheckTree(t *testing.T, dir string) {
	t.Helper()
	for _, absent := range []string{"a", "missing"} {
		if _, err := os.Lstat(filepath.Join(dir, absent)); !os.IsNotExist(err) {
			t.Errorf("%s exists after the probe (lstat err = %v)", absent, err)
		}
	}
	for name, want := range map[string]string{"b": "A", "c": "B"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestX86_64RenameFlagsPrimitives(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, renameFlagsSource(dir), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see renameFlagsSource)", code)
	}
	renameFlagsCheckTree(t, dir)
}

func TestArm64RenameFlagsPrimitives(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, renameFlagsSource(dir))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see renameFlagsSource)\n%s", code, out)
	}
	renameFlagsCheckTree(t, dir)
}

func TestArm64SSARenameFlagsPrimitives(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, renameFlagsSource(dir), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see renameFlagsSource)\n%s", code, stderr)
	}
	renameFlagsCheckTree(t, dir)
}

func TestInterpRenameFlagsPrimitives(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, renameFlagsSource(dir)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see renameFlagsSource)", code)
	}
	renameFlagsCheckTree(t, dir)
}

// Darwin's flag values are not Linux's: RENAME_EXCL is 4 where
// RENAME_NOREPLACE is 1, so a Linux word on XNU would ask for something else.
func TestArm64DarwinRenameFlagsPrimitives(t *testing.T) {
	dir := t.TempDir()
	if buildAndRunDarwin(t, dir, renameFlagsSource(dir)) {
		renameFlagsCheckTree(t, dir)
	}
}

func TestWASMRenameFlagsRefused(t *testing.T) {
	bin := buildFernCLI(t)
	for _, name := range []string{"rename_noreplace", "rename_exchange"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
			prog := fmt.Sprintf("function main(): i32 {\n    match (%s(\"a\", \"b\")) { Ok(_) => { return 0; }, Err(_) => { return 1; } }\n}\n", name)
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			o, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "prog.wasm"), src).CombinedOutput()
			out := string(o)
			if err == nil {
				t.Fatalf("wasm32-wasi build of %s succeeded, want E066", name)
			}
			if !strings.Contains(out, "E066") || !strings.Contains(out, "fsrename") {
				t.Fatalf("want E066 naming fsrename, got:\n%s", out)
			}
		})
	}
}
