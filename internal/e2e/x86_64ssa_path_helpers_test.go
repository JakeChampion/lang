package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// x86_64ssa had no emitter for any of the path builtins arm64ssa serves, so a
// program naming one was refused outright ("call target(s) the module never
// defines"). That refusal is why 58 of the 105 coreutils could not be built
// with -backend ssa on x86-64 at all, and why the backend cannot yet be the
// default on its own target (#9559, #9640).
//
// These two programs exercise every one of the helpers added with this test,
// in both directions where a refusal is meaningful: the answer and the error.
// They are asserted against the flat x86-64 emitter rather than against
// hand-written expectations, because the flat backend is the oracle for what
// each builtin means — the SSA backend is a second implementation of the same
// semantics, and the only interesting question is whether the two agree.
//
// Each program reports through its exit status so the comparison is one
// integer: 0 for agreement, and a distinct code per assertion otherwise, which
// names the helper that disagreed.

// The interrogating helpers: getcwd, access, lstat against stat over a
// symlink, and read_link. create_symlink is here too, since a symlink is what
// makes the other three distinguishable, and chmod because access has to be
// asked something whose answer depends on the mode it was handed.
const x86SSAPathQuerySrc = `function main(): i32 {
    var cwd: string = getcwd();
    if (cwd.len() < 1) { return 10; }
    if (cwd[0] != 47) { return 11; }

    match (access(cwd, 4)) { Ok(_) => {}, Err(e) => { return 20; } }
    match (access(cwd + "/no-such-entry-here", 4)) { Ok(_) => { return 21; }, Err(e) => {} }
    // The mode has to reach the kernel: a 0600 regular file is readable and
    // not executable, so R_OK and X_OK must answer differently over the same
    // name. A helper that ignored the mode would pass both.
    match (write_file(cwd + "/plain", "x")) { Ok(_) => {}, Err(e) => { return 22; } }
    match (chmod(cwd + "/plain", 384)) { Ok(_) => {}, Err(e) => { return 23; } }
    match (access(cwd + "/plain", 4)) { Ok(_) => {}, Err(e) => { return 24; } }
    match (access(cwd + "/plain", 1)) { Ok(_) => { return 25; }, Err(e) => {} }

    match (create_symlink(cwd, cwd + "/lnk")) { Ok(_) => {}, Err(e) => { return 30; } }
    match (lstat(cwd + "/lnk")) {
        Ok(s) => { if (s.is_dir) { return 31; } },
        Err(e) => { return 32; }
    }
    match (stat(cwd + "/lnk")) {
        Ok(s) => { if (!s.is_dir) { return 33; } },
        Err(e) => { return 34; }
    }

    match (read_link(cwd + "/lnk")) {
        Ok(t) => { if (t != cwd) { return 40; } },
        Err(e) => { return 41; }
    }
    match (read_link(cwd)) { Ok(t) => { return 42; }, Err(e) => {} }
    return 0;
}
`

// The mutating family: create_dir, remove_dir, chdir, truncate, chmod,
// create_link, rename, mknod and chown_at. mknod makes a FIFO, the one node
// type an unprivileged process may create, and chown_at names the ids the file
// already has, which any owner may set.
const x86SSAPathOpSrc = `function main(): i32 {
    var base: string = getcwd();

    match (create_dir(base + "/d", 493)) { Ok(_) => {}, Err(e) => { return 10; } }
    match (create_dir(base + "/d", 493)) { Ok(_) => { return 11; }, Err(e) => {} }
    match (remove_dir(base + "/d")) { Ok(_) => {}, Err(e) => { return 12; } }
    match (remove_dir(base + "/d")) { Ok(_) => { return 13; }, Err(e) => {} }

    match (create_dir(base + "/sub", 493)) { Ok(_) => {}, Err(e) => { return 20; } }
    match (chdir(base + "/sub")) { Ok(_) => {}, Err(e) => { return 21; } }
    if (getcwd() != base + "/sub") { return 22; }
    match (chdir(base)) { Ok(_) => {}, Err(e) => { return 23; } }
    match (chdir(base + "/no-such-dir")) { Ok(_) => { return 24; }, Err(e) => {} }

    match (write_file(base + "/f", "abcdefgh")) { Ok(_) => {}, Err(e) => { return 30; } }
    match (truncate(base + "/f", 3 as i64)) { Ok(_) => {}, Err(e) => { return 31; } }
    match (read_file(base + "/f")) { Ok(c) => { if (c != "abc") { return 32; } }, Err(e) => { return 33; } }
    match (truncate(base + "/f", 0 as i64 - 1 as i64)) { Ok(_) => { return 34; }, Err(e) => {} }

    match (chmod(base + "/f", 384)) { Ok(_) => {}, Err(e) => { return 40; } }
    match (stat(base + "/f")) { Ok(s) => { if (s.mode % 4096 != 384) { return 41; } }, Err(e) => { return 42; } }

    match (create_link(base + "/f", base + "/g")) { Ok(_) => {}, Err(e) => { return 50; } }
    match (read_file(base + "/g")) { Ok(c) => { if (c != "abc") { return 51; } }, Err(e) => { return 52; } }
    match (create_link(base + "/f", base + "/g")) { Ok(_) => { return 53; }, Err(e) => {} }

    match (rename(base + "/g", base + "/h")) { Ok(_) => {}, Err(e) => { return 60; } }
    match (read_file(base + "/h")) { Ok(c) => { if (c != "abc") { return 61; } }, Err(e) => { return 62; } }
    match (read_file(base + "/g")) { Ok(c) => { return 63; }, Err(e) => {} }

    match (mknod(base + "/p", 4096 + 384, 0, 0)) { Ok(_) => {}, Err(e) => { return 70; } }
    match (lstat(base + "/p")) { Ok(s) => { if (s.is_file || s.is_dir) { return 71; } }, Err(e) => { return 72; } }

    match (chown_at(base + "/f", 0 - 1, 0 - 1, true)) { Ok(_) => {}, Err(e) => { return 80; } }
    match (chown_at(base + "/no-such-file", 0 - 1, 0 - 1, true)) { Ok(_) => { return 81; }, Err(e) => {} }
    return 0;
}
`

// runPathProbe builds src for x86-64 with the named backend and runs it in a
// directory of its own, since these programs create names beside the working
// directory. `stdin` is fed to the program on a pipe, which is what a probe
// reading standard input needs and what an empty string skips. The program's
// exit status is the result.
func runPathProbe(t *testing.T, bin, qemu, dir, name, backend, src, stdin string) int {
	t.Helper()
	srcPath := filepath.Join(dir, name+"_"+backend+".fern")
	binPath := filepath.Join(dir, name+"_"+backend+".bin")
	runDir := filepath.Join(dir, name+"_"+backend+".run")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", runDir, err)
	}
	compile := exec.Command(bin, "-target", "x86-64-linux", "-backend", backend, "-o", binPath, srcPath)
	compile.Env = e2eharness.ChildEnv()
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("x86-64 -backend %s build of %s failed: %v\n%s", backend, name, err, out)
	}
	run := runX86Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv()
	run.Dir = runDir
	if stdin != "" {
		run.Stdin = strings.NewReader(stdin)
	}
	var errBuf strings.Builder
	run.Stderr = &errBuf
	err := run.Run()
	ee, ok := err.(*exec.ExitError)
	if err != nil && !ok {
		t.Fatalf("%s/%s: %v\n%s", name, backend, err, errBuf.String())
	}
	if ee != nil {
		if ee.ExitCode() < 0 {
			t.Fatalf("%s/%s died on a signal: %v\n%s", name, backend, ee, errBuf.String())
		}
		return ee.ExitCode()
	}
	return 0
}

func TestX86_64SSAPathHelpersMatchTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	for _, probe := range []struct{ name, src string }{
		{"path_query", x86SSAPathQuerySrc},
		{"path_op", x86SSAPathOpSrc},
	} {
		t.Run(probe.name, func(t *testing.T) {
			flat := runPathProbe(t, bin, qemu, dir, probe.name, "flat", probe.src, "")
			if flat != 0 {
				t.Fatalf("the flat emitter itself reports %d — the probe is wrong, not the SSA backend", flat)
			}
			if ssa := runPathProbe(t, bin, qemu, dir, probe.name, "ssa", probe.src, ""); ssa != flat {
				t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
					"Each code names one assertion in the probe source above; the two "+
					"backends are two implementations of the same builtins and must agree.", ssa, flat)
			}
		})
	}
}

// compileX86_64SSA and runX86_64SSABin are the x86-64 twins of the arm64 SSA
// pair in arm64_ssa_read_chunk_test.go, for the probes that live in their own
// files rather than here: mknod's dev_t packing and chown_at's follow flag are
// each written out once and run on every backend that provides the builtin, so
// this backend gets the same probe rather than being taken on trust.
func compileX86_64SSA(t *testing.T, fern, src string, env []string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "main.bin")
	cmd := exec.Command(fern, "-target", "x86-64-linux", "-backend", "ssa", "-o", out, srcPath)
	cmd.Env = env
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, o)
	}
	return out
}

func runX86_64SSABin(t *testing.T, qemu, bin, dir string, env []string) (int, string) {
	t.Helper()
	cmd := runX86Bin(qemu, bin)
	cmd.Dir = dir
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("run: %v", err)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}
