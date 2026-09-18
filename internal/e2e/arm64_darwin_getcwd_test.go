package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestArm64DarwinGetcwdValue asserts what `getcwd()` ANSWERS on
// arm64-darwin, against a directory the test chose.
//
// #9131: this backend issued Darwin 296 for getcwd, and 296 is
// vm_pressure_monitor — there is no SYS___getcwd in the macOS SDK's
// syscall.h at all. It SUCCEEDED, left the buffer untouched, and the
// strlen loop measured uninitialised stack, so `getcwd()` answered a
// short run of garbage and reported no error. Nothing caught it because
// no test asserted the VALUE on this target.
//
// It also took its third argument as a pointer to copy a page count out
// to, and the emitter set only x0/x1 — so the kernel wrote four bytes at
// whatever the caller had left in x2. That is the corruption in #9722
// (`ls --hyperlink` emitting pointer-shaped bytes where a path belongs)
// and in chroot's two relative-NEWROOT corpus cases, whose diagnostics
// quoted an operand with its first four bytes replaced by a page count:
//
//	fern: cannot change root directory to ''$'\000\000\000\000''oot': …
//	fern: cannot change root directory to '!'$'\002\000\000''oups': …
//
// So the operand check below is not decoration: it is the half of the bug
// that reached a program's output rather than just its own return value.
//
// macOS resolves the temp dir through a symlink (/var to /private/var),
// and F_GETPATH answers the resolved path, so the expectation is the
// symlink-evaluated form rather than what t.TempDir handed back.
func TestArm64DarwinGetcwdValue(t *testing.T) {
	const prog = `function main(): i32 {
  var a: string[] = args();
  if (a.len() < 2) { return 97; }
  var p: string = a[1];
  var cwd: string = getcwd();
  if (cwd.len() == 0) { return 98; }
  print(cwd);
  print(p);
  return 0;
}
`
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	const operand = "newroot"
	cmd := exec.Command(out, operand)
	cmd.Dir = dir
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v (97 = no argv[1], 98 = getcwd answered empty)", err)
	}
	if string(got) != want+"\n"+operand+"\n" {
		t.Errorf("getcwd() and the caller's operand did not both survive\n got: %q\nwant: %q",
			got, want+"\n"+operand+"\n")
	}
}
