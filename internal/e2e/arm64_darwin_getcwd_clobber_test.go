package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestArm64DarwinGetcwdKeepsCallerStrings pins that `getcwd()` leaves the
// caller's own strings alone on arm64-darwin.
//
// The corpus found this through `chroot`: on Darwin the two cases whose
// NEWROOT is RELATIVE print a diagnostic whose operand has its first four
// bytes replaced by binary garbage while the tail survives —
//
//	gnu:  cannot change root directory to 'newroot': Operation not permitted
//	fern: cannot change root directory to ''$'\000\000\000\000''oot': …
//	gnu:  cannot change root directory to '--groups': …
//	fern: cannot change root directory to '!'$'\002\000\000''oups': …
//
// Every other chroot case in the catalogue passes an ABSOLUTE newroot, and
// all of them pass. A relative name is the one shape that reaches
// `getcwd()`, via `canon.canonicalize`, and `chroot` is the only caller
// that reads its operand again AFTERWARDS — which is why `pwd` and
// `realpath` stay green on Darwin while this does not.
//
// The same signature reaches `ls --hyperlink` (#9722): pointer-shaped bytes
// where an absolute path belongs, on Darwin only.
//
// Both lengths are covered because they are different string
// representations: 7 bytes is the inline (SSO) form, whose bytes live in a
// register pair, and 34 bytes is the heap form behind a data pointer. Which
// one corrupts says whether the damage is to a register or to memory.
//
// Clean on x86-64-linux and arm64-linux (under qemu), with the sanitizer's
// over-release and use-after-free detectors silent on both, and clean under
// FERN_HIGH_HEAP=1 — so it is not an rc bug and not pointer truncation in
// the above-4-GiB address regime.
func TestArm64DarwinGetcwdKeepsCallerStrings(t *testing.T) {
	const prog = `function main(): i32 {
  var a: string[] = args();
  if (a.len() < 2) { return 97; }
  var p: string = a[1];
  print(p);
  var cwd: string = getcwd();
  if (cwd.len() == 0) { return 98; }
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
	for _, operand := range []string{"newroot", "newroot-well-past-the-inline-limit"} {
		t.Run(operand, func(t *testing.T) {
			cmd := exec.Command(out, operand)
			cmd.Dir = dir // getcwd() answers THIS
			got, err := cmd.Output()
			if err != nil {
				t.Fatalf("run: %v (97 = no argv[1], 98 = getcwd answered empty)", err)
			}
			want := operand + "\n" + operand + "\n"
			if string(got) != want {
				t.Errorf("operand %q did not survive getcwd()\n got: %q\nwant: %q",
					operand, got, want)
			}
		})
	}
}
