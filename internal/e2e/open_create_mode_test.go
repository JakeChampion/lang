package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `open_writer` and `open_appender` create a file the way `open(2)` is
// conventionally called — mode 0666, filtered by the process umask, which is
// what Go's `os.Create`, Rust's `File::create` and C's `fopen` all ask for. A
// fixed 0644 would ignore the mask's group and other write policy with no way
// to ask for it back, which is exactly what `truncate -s 5 new` under `umask
// 002` shows: GNU leaves 0664 and a 0644 creation leaves 0644.
//
// `open_exclusive` is deliberately NOT 0666 — its callers are creating a file
// only they should hold — so it is pinned here too, at 0600, to keep the two
// rules from drifting into each other.
//
// The mask is per-PROCESS state a child inherits, so each case sets it in a
// shell it then exec's THROUGH rather than in this process: a `syscall.Umask`
// here would be global to the whole suite.
const openCreateModeSrc = `function main(): i32 {
    match (open_writer("w.out")) {
        Ok(w) => { w.close(); },
        Err(e) => { return 2; }
    }
    match (open_appender("a.out")) {
        Ok(w) => { w.close(); },
        Err(e) => { return 3; }
    }
    match (open_exclusive("x.out")) {
        Ok(w) => { w.close(); },
        Err(e) => { return 4; }
    }
    return 0;
}
`

func TestOpenHelpersCreateThroughTheUmask(t *testing.T) {
	bin := buildFernCLI(t)
	x86Qemu, haveX86 := x86Runner()
	arm64Qemu, haveArm64 := arm64Runner()
	for _, tc := range []struct {
		name   string
		target []string
		arm64  bool
	}{
		{"x86-64", []string{"-target", "x86-64-linux"}, false},
		{"arm64", []string{"-target", "arm64-linux"}, true},
		{"arm64-ssa", []string{"-target", "arm64-linux", "-backend", "ssa"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, have := x86Qemu, haveX86
			if tc.arm64 {
				runner, have = arm64Qemu, haveArm64
			}
			if !have {
				t.Skipf("no way to run a %s binary here", tc.name)
			}
			build := t.TempDir()
			srcPath := filepath.Join(build, "opens.fern")
			if err := os.WriteFile(srcPath, []byte(openCreateModeSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			prog := filepath.Join(build, "opens")
			args := append(append([]string{}, tc.target...), "-o", prog, srcPath)
			if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, o)
			}

			// 002 and 111 are the masks a 0644 creation gets wrong: the
			// first because it leaves the group write bit alone, the
			// second because it takes away a class of BIT rather than a
			// class of user.
			for _, m := range []struct {
				umask int
				write os.FileMode
				excl  os.FileMode
			}{
				{0o000, 0o666, 0o600},
				{0o002, 0o664, 0o600},
				{0o022, 0o644, 0o600},
				{0o077, 0o600, 0o600},
				{0o111, 0o666, 0o600},
				{0o222, 0o444, 0o400},
			} {
				t.Run(fmt.Sprintf("umask %03o", m.umask), func(t *testing.T) {
					dir := t.TempDir()
					argv := []string{prog}
					if runner != "" {
						argv = []string{runner, prog}
					}
					shell := append([]string{"-c",
						fmt.Sprintf("umask %03o; exec \"$0\" \"$@\"", m.umask)}, argv...)
					cmd := exec.Command("sh", shell...)
					cmd.Dir = dir
					if o, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("run: %v\n%s", err, o)
					}
					for _, f := range []struct {
						name string
						want os.FileMode
					}{
						{"w.out", m.write},
						{"a.out", m.write},
						{"x.out", m.excl},
					} {
						st, err := os.Stat(filepath.Join(dir, f.name))
						if err != nil {
							t.Fatalf("stat %s: %v", f.name, err)
						}
						if got := st.Mode().Perm(); got != f.want {
							t.Errorf("%s under umask %03o is %04o, want %04o", f.name, m.umask, got, f.want)
						}
					}
				})
			}
		})
	}
}
